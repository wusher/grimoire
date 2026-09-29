//go:build !windows

package grimoire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func emptyCatalogPlan(t *testing.T, root string) *bindingCatalogPlan {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return &bindingCatalogPlan{root: root, active: true}
}

func TestBindingMutationCommitFailureStagesRollback(t *testing.T) {
	t.Run("journal", func(t *testing.T) {
		paths := testPaths(t)
		blocked := filepath.Join(paths.Home, "config-file")
		if err := os.WriteFile(blocked, []byte("blocked"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths.ConfigHome = blocked
		mutation := &bindingMutation{paths: paths, journal: bindingMutationJournal{Version: bindingMutationVersion}, catalog: emptyCatalogPlan(t, filepath.Join(paths.Home, "catalog"))}
		if err := mutation.Commit(); err == nil || !strings.Contains(err.Error(), "save binding mutation journal") {
			t.Fatalf("journal failure = %v", err)
		}
	})

	t.Run("installed links", func(t *testing.T) {
		paths := testPaths(t)
		plan := &symlinkPlan{actions: []symlinkAction{{kind: symlinkCreate, path: filepath.Join(paths.Home, "missing-parent", "link"), target: "/target", directory: true}}}
		mutation := &bindingMutation{paths: paths, journal: bindingMutationJournal{Version: bindingMutationVersion}, catalog: emptyCatalogPlan(t, filepath.Join(paths.Home, "catalog")), links: plan}
		if err := mutation.Commit(); err == nil || !strings.Contains(err.Error(), "update installed links") {
			t.Fatalf("link failure = %v", err)
		}
	})

	t.Run("catalog apply", func(t *testing.T) {
		paths := testPaths(t)
		catalog := emptyCatalogPlan(t, filepath.Join(paths.Home, "catalog"))
		catalog.rootPlan.actions = []symlinkAction{{kind: "corrupt"}}
		mutation := &bindingMutation{paths: paths, journal: bindingMutationJournal{Version: bindingMutationVersion}, catalog: catalog}
		if err := mutation.Commit(); err == nil || !strings.Contains(err.Error(), "update catalog links") {
			t.Fatalf("catalog apply failure = %v", err)
		}
	})

	t.Run("catalog verify", func(t *testing.T) {
		paths := testPaths(t)
		mutation := &bindingMutation{paths: paths, journal: bindingMutationJournal{Version: bindingMutationVersion}, catalog: &bindingCatalogPlan{root: filepath.Join(paths.Home, "missing-catalog"), active: true}}
		if err := mutation.Commit(); err == nil || !strings.Contains(err.Error(), "verify catalog links") {
			t.Fatalf("catalog verify failure = %v", err)
		}
	})

	t.Run("catalog content verify", func(t *testing.T) {
		paths := testPaths(t)
		repository := filepath.Join(paths.Home, "repository")
		after := []BoundRepository{{Path: repository, Skills: []string{"alpha"}}}
		mutation := &bindingMutation{paths: paths, journal: bindingMutationJournal{Version: bindingMutationVersion, After: after}, catalog: emptyCatalogPlan(t, paths.Binding())}
		if err := mutation.Commit(); err == nil || !strings.Contains(err.Error(), "verify catalog:") {
			t.Fatalf("catalog content failure = %v", err)
		}
	})

	t.Run("bindings write", func(t *testing.T) {
		paths := testPaths(t)
		catalog := emptyCatalogPlan(t, paths.Binding())
		catalog.directories = []string{paths.BindingsFile()}
		mutation := &bindingMutation{paths: paths, journal: bindingMutationJournal{Version: bindingMutationVersion}, catalog: catalog}
		if err := mutation.Commit(); err == nil || !strings.Contains(err.Error(), "save bindings") {
			t.Fatalf("bindings write failure = %v", err)
		}
	})

	t.Run("ownership write", func(t *testing.T) {
		paths := testPaths(t)
		repository := filepath.Join(paths.Home, "repository")
		source := makeSkillIn(t, repository, "", "alpha", "Alpha")
		bindings := []BoundRepository{{Path: repository, Skills: []string{"alpha"}}}
		catalog, err := planBindingCatalog(paths, nil, bindings, false, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		installed := filepath.Join(paths.SkillsHomes()[0], "alpha")
		if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
			t.Fatal(err)
		}
		links := &symlinkPlan{}
		if err := links.Create(installed, source, true); err != nil {
			t.Fatal(err)
		}
		before := ownershipState{Version: ownershipVersion}
		after := before.clone()
		after.set(installed, source)
		if err := os.MkdirAll(paths.OwnershipFile(), 0o755); err != nil {
			t.Fatal(err)
		}
		mutation := &bindingMutation{paths: paths, journal: bindingMutationJournal{Version: bindingMutationVersion, After: bindings}, catalog: catalog}
		mutation.SetLinkAndOwnershipUpdate(links, before, after)
		if err := mutation.Commit(); err == nil || !strings.Contains(err.Error(), "save ownership") {
			t.Fatalf("ownership write failure = %v", err)
		}
	})
}

func TestBindingMutationRollbackReportsEveryProtectedFailure(t *testing.T) {
	t.Run("ownership", func(t *testing.T) {
		paths := testPaths(t)
		blocked := filepath.Join(paths.Home, "config-file")
		if err := os.WriteFile(blocked, []byte("blocked"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths.ConfigHome = blocked
		before := ownershipState{Version: ownershipVersion}
		mutation := &bindingMutation{paths: paths, catalog: &bindingCatalogPlan{}, beforeOwnership: &before, ownershipWritten: true}
		if err := mutation.rollback(); err == nil || !strings.Contains(err.Error(), "rollback ownership") {
			t.Fatalf("rollback = %v", err)
		}
	})

	t.Run("bindings", func(t *testing.T) {
		paths := testPaths(t)
		blocked := filepath.Join(paths.Home, "config-file")
		if err := os.WriteFile(blocked, []byte("blocked"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths.ConfigHome = blocked
		mutation := &bindingMutation{paths: paths, catalog: &bindingCatalogPlan{}, journal: bindingMutationJournal{BeforeExists: true}, bindingsWritten: true}
		if err := mutation.rollback(); err == nil || !strings.Contains(err.Error(), "rollback bindings") {
			t.Fatalf("rollback = %v", err)
		}
	})

	t.Run("catalog directory", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "created")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "foreign"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		mutation := &bindingMutation{catalog: &bindingCatalogPlan{createdDirectories: []string{dir, ""}}}
		if err := mutation.rollback(); err == nil || !strings.Contains(err.Error(), "remove created catalog directory") {
			t.Fatalf("rollback = %v", err)
		}
	})

	t.Run("installed link", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "missing")
		links := &symlinkPlan{actions: []symlinkAction{{kind: symlinkCreate, path: path, target: "/target", directory: true, created: true}}}
		mutation := &bindingMutation{catalog: &bindingCatalogPlan{}, links: links}
		if err := mutation.rollback(); err == nil || !strings.Contains(err.Error(), "rollback installed links") {
			t.Fatalf("rollback = %v", err)
		}
	})

	t.Run("journal cleanup", func(t *testing.T) {
		paths := testPaths(t)
		journal := paths.BindingMutationFile()
		if err := os.MkdirAll(filepath.Join(journal, "child"), 0o755); err != nil {
			t.Fatal(err)
		}
		mutation := &bindingMutation{paths: paths, catalog: &bindingCatalogPlan{}, journalWritten: true}
		if err := mutation.rollback(); err == nil || !strings.Contains(err.Error(), "clear rolled-back binding mutation journal") {
			t.Fatalf("rollback = %v", err)
		}
	})
}

func TestBindingMutationSuccessfulOwnershipCommit(t *testing.T) {
	paths := testPaths(t)
	repository := filepath.Join(paths.Home, "repository")
	source := makeSkillIn(t, repository, "", "alpha", "Alpha")
	bindings := []BoundRepository{{Path: repository, Skills: []string{"alpha"}}}
	catalog, err := planBindingCatalog(paths, nil, bindings, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(paths.SkillsHomes()[0], "alpha")
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	links := &symlinkPlan{}
	if err := links.Create(installed, source, true); err != nil {
		t.Fatal(err)
	}
	before := ownershipState{Version: ownershipVersion}
	after := before.clone()
	after.set(installed, source)
	mutation := &bindingMutation{paths: paths, journal: bindingMutationJournal{Version: bindingMutationVersion, After: bindings}, catalog: catalog}
	mutation.SetLinkAndOwnershipUpdate(links, before, after)
	if mutation.CatalogChangeCount() == 0 {
		t.Fatal("expected catalog changes")
	}
	if err := mutation.Commit(); err != nil {
		t.Fatal(err)
	}
	if mutation.journalWritten || !mutation.bindingsWritten || !mutation.ownershipWritten {
		t.Fatalf("mutation flags = %#v", mutation)
	}
}

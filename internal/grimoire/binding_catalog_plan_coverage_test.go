//go:build !windows

package grimoire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanBindingCatalogRejectsUnsafeRootsAndSelections(t *testing.T) {
	paths := testPaths(t)
	first := filepath.Join(paths.Home, "first")
	second := filepath.Join(paths.Home, "second")
	duplicate := []BoundRepository{{Path: first, Skills: []string{"alpha"}}, {Path: second, Skills: []string{"alpha"}}}
	if _, err := planBindingCatalog(paths, duplicate, nil, false, false, nil); err == nil || !strings.Contains(err.Error(), "selected from both") {
		t.Fatalf("duplicate old selection = %v", err)
	}
	if _, err := planBindingCatalog(paths, nil, duplicate, false, false, nil); err == nil || !strings.Contains(err.Error(), "selected from both") {
		t.Fatalf("duplicate new selection = %v", err)
	}

	foreignRoot := paths.Binding()
	if err := os.MkdirAll(filepath.Dir(foreignRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createSymlink(first, foreignRoot, true); err != nil {
		t.Fatal(err)
	}
	if _, err := planBindingCatalog(paths, nil, nil, false, false, nil); err == nil || !strings.Contains(err.Error(), "foreign symlink") {
		t.Fatalf("foreign root = %v", err)
	}
	if _, err := planBindingCatalog(paths, nil, nil, true, false, nil); err == nil || !strings.Contains(err.Error(), "explicit approval") {
		t.Fatalf("unapproved legacy root = %v", err)
	}

	paths = testPaths(t)
	if err := os.MkdirAll(filepath.Dir(paths.Binding()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Binding(), []byte("foreign"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := planBindingCatalog(paths, nil, nil, false, false, nil); err == nil || !strings.Contains(err.Error(), "real file") {
		t.Fatalf("real catalog root = %v", err)
	}
}

func TestPlanBindingCatalogHandlesEveryExistingEntryState(t *testing.T) {
	root := t.TempDir()
	repositories := filepath.Join(root, "repositories")
	paths := testPaths(t)
	paths.ConfigHome = filepath.Join(root, "config")
	oldAlpha := filepath.Join(repositories, "old", "alpha")
	newAlpha := filepath.Join(repositories, "new", "alpha")
	for _, dir := range []string{oldAlpha, newAlpha} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	before := []BoundRepository{{Path: filepath.Dir(oldAlpha), Skills: []string{"alpha"}}}
	after := []BoundRepository{{Path: filepath.Dir(newAlpha), Skills: []string{"alpha"}}}

	t.Run("replace managed link", func(t *testing.T) {
		paths := paths
		paths.ConfigHome = filepath.Join(root, "replace")
		if err := os.MkdirAll(paths.Binding(), 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(paths.Binding(), "alpha")
		if err := createSymlink(oldAlpha, link, true); err != nil {
			t.Fatal(err)
		}
		plan, err := planBindingCatalog(paths, before, after, false, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := plan.Apply(); err != nil {
			t.Fatal(err)
		}
		if err := plan.Verify(); err != nil {
			t.Fatal(err)
		}
		assertBindingLinkTarget(t, link, newAlpha)
	})

	t.Run("foreign link", func(t *testing.T) {
		paths := paths
		paths.ConfigHome = filepath.Join(root, "foreign")
		if err := os.MkdirAll(paths.Binding(), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := createSymlink(filepath.Join(root, "other"), filepath.Join(paths.Binding(), "alpha"), true); err != nil {
			t.Fatal(err)
		}
		if _, err := planBindingCatalog(paths, before, after, false, false, nil); err == nil || !strings.Contains(err.Error(), "foreign link") {
			t.Fatalf("foreign link = %v", err)
		}
	})

	t.Run("real entry", func(t *testing.T) {
		paths := paths
		paths.ConfigHome = filepath.Join(root, "real")
		if err := os.MkdirAll(paths.Binding(), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(paths.Binding(), "alpha"), []byte("foreign"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := planBindingCatalog(paths, nil, after, false, false, nil); err == nil || !strings.Contains(err.Error(), "real file or directory") {
			t.Fatalf("real entry = %v", err)
		}
		if _, err := planBindingCatalog(paths, before, nil, false, false, nil); err != nil {
			t.Fatalf("removed real entry should be preserved: %v", err)
		}
	})

	t.Run("create and remove", func(t *testing.T) {
		paths := paths
		paths.ConfigHome = filepath.Join(root, "transitions")
		if err := os.MkdirAll(paths.Binding(), 0o755); err != nil {
			t.Fatal(err)
		}
		createPlan, err := planBindingCatalog(paths, nil, after, false, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := createPlan.Apply(); err != nil {
			t.Fatal(err)
		}
		removePlan, err := planBindingCatalog(paths, after, nil, false, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := removePlan.Apply(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(paths.Binding(), "alpha")); !os.IsNotExist(err) {
			t.Fatalf("removed link remains: %v", err)
		}
	})
}

func TestBindingCatalogDirectoryPlanningAndVerificationFailures(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := missingDirectories(filepath.Join(blocked, "child")); err == nil {
		t.Fatalf("blocked directory chain = %v", err)
	}
	if _, err := missingDirectories(blocked); err == nil || !strings.Contains(err.Error(), "real file") {
		t.Fatalf("file directory root = %v", err)
	}

	planned := filepath.Join(root, "one", "two")
	directories, err := missingDirectories(planned)
	if err != nil || len(directories) != 2 {
		t.Fatalf("directories = %#v, %v", directories, err)
	}
	plan := &bindingCatalogPlan{root: planned, directories: directories}
	if err := os.MkdirAll(directories[0], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if err := plan.Verify(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(planned); err != nil {
		t.Fatal(err)
	}
	if err := plan.Verify(); err == nil || !strings.Contains(err.Error(), "changed after validation") {
		t.Fatalf("verify missing root = %v", err)
	}
}

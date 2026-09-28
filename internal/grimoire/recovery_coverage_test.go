package grimoire

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBindingRecoveryCompletesInstalledLinksCatalogAndOwnership(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repository")
	source := filepath.Join(repository, "alpha")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(paths.SkillsHomes()[0], "alpha")
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	after := []BoundRepository{{Path: repository, Skills: []string{"alpha"}}}
	ownership := ownershipState{Version: ownershipVersion}
	ownership.set(installed, source)
	journal := bindingMutationJournal{Version: bindingMutationVersion, After: after,
		Links: []journalSymlinkAction{{Kind: symlinkCreate, Path: installed, Target: source, Directory: true}}, Ownership: &ownership}
	if err := writeBindingMutationJournal(paths, journal); err != nil {
		t.Fatal(err)
	}
	if err := recoverBindingMutation(paths); err != nil {
		t.Fatal(err)
	}
	assertBindingLinkTarget(t, filepath.Join(paths.Binding(), "alpha"), source)
	assertBindingLinkTarget(t, installed, source)
	gotBindings, err := readConfiguredBindings(paths)
	if err != nil || !equalBindings(gotBindings, after) {
		t.Fatalf("recovered bindings = %#v, error = %v", gotBindings, err)
	}
	gotOwnership, err := configuredOwnership(paths)
	if err != nil || !ownershipProves(gotOwnership, installed) {
		t.Fatalf("recovered ownership = %#v, error = %v", gotOwnership, err)
	}
	if _, err := os.Lstat(paths.BindingMutationFile()); !os.IsNotExist(err) {
		t.Fatalf("recovery journal remains: %v", err)
	}
}

func TestBindingRecoveryPreservesForeignInterruptedInstalledLink(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	link, foreign := filepath.Join(paths.Home, "installed", "alpha"), filepath.Join(paths.Home, "foreign", "alpha")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createSymlink(foreign, link, true); err != nil {
		t.Fatal(err)
	}
	journal := bindingMutationJournal{Version: bindingMutationVersion,
		Links: []journalSymlinkAction{{Kind: symlinkCreate, Path: link, Target: filepath.Join(paths.Home, "wanted", "alpha"), Directory: true}}}
	if err := writeBindingMutationJournal(paths, journal); err != nil {
		t.Fatal(err)
	}
	err := recoverBindingMutation(paths)
	if err == nil || !strings.Contains(err.Error(), "does not match interrupted mutation") {
		t.Fatalf("recovery error = %v", err)
	}
	assertBindingLinkTarget(t, link, foreign)
	if _, err := os.Lstat(paths.BindingMutationFile()); err != nil {
		t.Fatalf("recovery journal was unexpectedly removed: %v", err)
	}
}

func TestRecoverSymlinkPlanCompletesInterruptedMoveStates(t *testing.T) {
	for _, state := range []struct {
		name     string
		old, new bool
	}{
		{name: "move not started", old: true}, {name: "new link was created", old: true, new: true}, {name: "old link was removed"},
	} {
		t.Run(state.name, func(t *testing.T) {
			root := t.TempDir()
			oldLink, newLink := filepath.Join(root, "old"), filepath.Join(root, "new")
			target, beforeTarget := filepath.Join(root, "wanted"), filepath.Join(root, "before")
			if state.old {
				if err := createSymlink(beforeTarget, oldLink, true); err != nil {
					t.Fatal(err)
				}
			}
			if state.new {
				if err := createSymlink(target, newLink, true); err != nil {
					t.Fatal(err)
				}
			}
			record := journalSymlinkAction{Kind: symlinkMove, Path: oldLink, NewPath: newLink, Target: target, Directory: true, BeforeTarget: beforeTarget, BeforeDirectory: true}
			plan, err := recoverSymlinkPlan([]journalSymlinkAction{record})
			if err != nil {
				t.Fatal(err)
			}
			if err := plan.Apply(); err != nil {
				t.Fatal(err)
			}
			if err := verifyJournalSymlinkActions([]journalSymlinkAction{record}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(oldLink); !os.IsNotExist(err) {
				t.Fatalf("old link remains: %v", err)
			}
			assertBindingLinkTarget(t, newLink, target)
		})
	}
}

func TestRecoveryRejectsUnknownSymlinkJournalAction(t *testing.T) {
	_, err := recoverSymlinkPlan([]journalSymlinkAction{{Kind: symlinkActionKind("corrupt"), Path: filepath.Join(t.TempDir(), "link")}})
	if err == nil || !strings.Contains(err.Error(), "unknown journal symlink action") {
		t.Fatalf("recovery error = %v", err)
	}
}

func TestConfiguredOwnershipRejectsUnsafePersistedState(t *testing.T) {
	for _, test := range []struct{ name, body, want string }{
		{name: "invalid JSON", body: "{", want: "read ownership"},
		{name: "unsupported version", body: `{"version":2,"links":[]}`, want: "unsupported ownership version 2"},
		{name: "relative path", body: `{"version":1,"links":[{"path":"relative","target":"/target"}]}`, want: "ownership paths must be absolute"},
		{name: "duplicate cleaned path", body: `{"version":1,"links":[{"path":"/links/alpha","target":"/one"},{"path":"/links/./alpha","target":"/two"}]}`, want: "duplicate ownership path"},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := testPaths(t)
			if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths.OwnershipFile(), []byte(test.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := configuredOwnership(paths)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ownership error = %v", err)
			}
		})
	}
}

func TestBindingCatalogRejectsDuplicateSkillNamesBeforeMutation(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	first, second := filepath.Join(paths.Home, "first"), filepath.Join(paths.Home, "second")
	_, err := planBindingCatalog(paths, nil, []BoundRepository{{Path: first, Skills: []string{"alpha"}}, {Path: second, Skills: []string{"alpha"}}}, false, false, nil)
	if err == nil || !strings.Contains(err.Error(), "selected from both") {
		t.Fatalf("collision error = %v", err)
	}
	if _, statErr := os.Lstat(paths.Binding()); !os.IsNotExist(statErr) {
		t.Fatalf("collision created catalog state: %v", statErr)
	}
}

func TestCatalogFindRequiresRepositoryPathForClashingNames(t *testing.T) {
	catalog := Catalog{Skills: []Skill{{Name: "alpha", Group: "one", Dir: "/one/alpha", Repository: "/one"}, {Name: "alpha", Group: "two", Dir: "/two/alpha", Repository: "/two"}}, Clashes: map[string][]Skill{}}
	catalog.finish()
	if _, err := catalog.Find("alpha"); err == nil || !strings.Contains(err.Error(), "Rename one") {
		t.Fatalf("ambiguous lookup error = %v", err)
	}
	skill, err := catalog.Find(filepath.Join("two", "alpha"))
	if err != nil || skill == nil || skill.Dir != "/two/alpha" {
		t.Fatalf("repository-qualified lookup = %#v, error = %v", skill, err)
	}
}

func TestCLIReportsSetupAndSelectionErrorsWithoutPicker(t *testing.T) {
	paths := testPaths(t)
	var out, errOut bytes.Buffer
	cli := &CLI{Out: &out, Err: &errOut, Paths: paths, Config: Config{Boring: true}, setupErr: errors.New("paths unavailable")}
	if code := cli.Run(context.Background(), []string{"cast", "alpha"}); code != 1 || !strings.Contains(errOut.String(), "paths unavailable") {
		t.Fatalf("setup failure code = %d, stderr = %q", code, errOut.String())
	}
	errOut.Reset()
	cli.setupErr = nil
	if code := cli.Run(context.Background(), []string{"unknown"}); code != 1 || !strings.Contains(errOut.String(), "no command called unknown") {
		t.Fatalf("unknown command code = %d, stderr = %q", code, errOut.String())
	}
	catalog := Catalog{Skills: []Skill{{Name: "alpha", Dir: "/skills/alpha"}}}
	if _, err := cli.choose([]string{"alpha", "beta"}, catalog, catalog.Skills, "install"); err == nil || !strings.Contains(err.Error(), "at most one") {
		t.Fatalf("multiple selection error = %v", err)
	}
	if _, err := cli.choose(nil, catalog, catalog.Skills, "install"); err == nil || !strings.Contains(err.Error(), "boring mode has no picker") {
		t.Fatalf("boring picker error = %v", err)
	}
}

package grimoire

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBindingTransactionValidatesAllDestinationsBeforeMutation(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	oldRoot := filepath.Join(paths.Home, "old")
	newRoot := filepath.Join(paths.Home, "new")
	foreignRoot := filepath.Join(paths.Home, "foreign")
	for _, root := range []string{oldRoot, newRoot, foreignRoot} {
		for _, name := range []string{"alpha", "beta"} {
			if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.MkdirAll(paths.Binding(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createSymlink(filepath.Join(oldRoot, "alpha"), filepath.Join(paths.Binding(), "alpha"), true); err != nil {
		t.Fatal(err)
	}
	if err := createSymlink(filepath.Join(foreignRoot, "beta"), filepath.Join(paths.Binding(), "beta"), true); err != nil {
		t.Fatal(err)
	}

	before := []BoundRepository{{Path: oldRoot, Skills: []string{"alpha", "beta"}}}
	after := []BoundRepository{{Path: newRoot, Skills: []string{"alpha", "beta"}}}
	err := syncSkillBindings(paths, before, after, false)
	if err == nil || !strings.Contains(err.Error(), "foreign link") {
		t.Fatalf("validation error = %v", err)
	}
	assertBindingLinkTarget(t, filepath.Join(paths.Binding(), "alpha"), filepath.Join(oldRoot, "alpha"))
}

func TestBindingTransactionReturnsRollbackErrorsAndContinues(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	oldRoot := filepath.Join(paths.Home, "old")
	newRoot := filepath.Join(paths.Home, "new")
	names := []string{"alpha", "beta", "gamma"}
	for _, root := range []string{oldRoot, newRoot} {
		for _, name := range names {
			if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.MkdirAll(paths.Binding(), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := createSymlink(filepath.Join(oldRoot, name), filepath.Join(paths.Binding(), name), true); err != nil {
			t.Fatal(err)
		}
	}

	realCreateSymlink := createSymlink
	t.Cleanup(func() { createSymlink = realCreateSymlink })
	applyFailure := errors.New("injected apply failure")
	rollbackFailure := errors.New("injected rollback failure")
	createCalls := 0
	createSymlink = func(target, link string, directory bool) error {
		createCalls++
		switch createCalls {
		case 3:
			return applyFailure
		case 5:
			return rollbackFailure
		default:
			return realCreateSymlink(target, link, directory)
		}
	}

	before := []BoundRepository{{Path: oldRoot, Skills: names}}
	after := []BoundRepository{{Path: newRoot, Skills: names}}
	err := syncSkillBindings(paths, before, after, false)
	if !errors.Is(err, applyFailure) || !errors.Is(err, rollbackFailure) {
		t.Fatalf("joined transaction error = %v", err)
	}
	if createCalls != 7 {
		t.Fatalf("create calls = %d, want 7", createCalls)
	}
	assertBindingLinkTarget(t, filepath.Join(paths.Binding(), "alpha"), filepath.Join(oldRoot, "alpha"))
	assertBindingLinkTarget(t, filepath.Join(paths.Binding(), "beta"), filepath.Join(oldRoot, "beta"))
	assertBindingLinkTarget(t, filepath.Join(paths.Binding(), "gamma"), filepath.Join(oldRoot, "gamma"))
}

func TestSymlinkPlanRechecksBeforeDestructiveChanges(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "link")
	original := filepath.Join(root, "original")
	foreign := filepath.Join(root, "foreign")
	if err := createSymlink(original, link, true); err != nil {
		t.Fatal(err)
	}
	snapshot, err := snapshotSymlink(link)
	if err != nil {
		t.Fatal(err)
	}
	plan := &symlinkPlan{}
	if err := plan.Remove(link, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := createSymlink(foreign, link, true); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err == nil || !strings.Contains(err.Error(), "changed after validation") {
		t.Fatalf("apply error = %v", err)
	}
	assertBindingLinkTarget(t, link, foreign)
}

func TestSymlinkPlanRollbackLeavesAChangedLinkInPlace(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "link")
	wanted := filepath.Join(root, "wanted")
	foreign := filepath.Join(root, "foreign")
	plan := &symlinkPlan{}
	if err := plan.Create(link, wanted, true); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := createSymlink(foreign, link, true); err != nil {
		t.Fatal(err)
	}
	if err := plan.Rollback(); !errors.Is(err, errSymlinkRollbackConflict) || !strings.Contains(err.Error(), "leave changed link") {
		t.Fatalf("rollback error = %v", err)
	}
	assertBindingLinkTarget(t, link, foreign)
}

func TestSymlinkPlanRollbackLeavesAMissingTransactionLinkMissing(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "link")
	original := filepath.Join(root, "original")
	if err := createSymlink(original, link, true); err != nil {
		t.Fatal(err)
	}
	snapshot, err := snapshotSymlink(link)
	if err != nil {
		t.Fatal(err)
	}
	plan := &symlinkPlan{}
	if err := plan.Replace(link, snapshot, filepath.Join(root, "wanted"), true); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}

	if err := plan.Rollback(); !errors.Is(err, errSymlinkRollbackConflict) {
		t.Fatalf("rollback error = %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("changed link was restored: %v", err)
	}
}

func TestBindingTransactionRequiresLegacyRootApproval(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	legacyRoot := filepath.Join(paths.Home, "legacy")
	newRoot := filepath.Join(paths.Home, "new")
	alpha := makeSkillIn(t, legacyRoot, "", "alpha", "Legacy")
	beta := makeSkillIn(t, newRoot, "", "beta", "New")
	if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createSymlink(legacyRoot, paths.Binding(), true); err != nil {
		t.Fatal(err)
	}

	chosen := []Skill{ReadSkill(beta, newRoot, paths.SkillsHomes())}
	result := BindSkills(paths, newRoot, chosen)
	if result.Status != Blocked || !strings.Contains(result.Message, "requires explicit approval") {
		t.Fatalf("ordinary bind = %#v", result)
	}
	assertBindingLinkTarget(t, paths.Binding(), legacyRoot)
	if _, err := os.Lstat(paths.BindingsFile()); !os.IsNotExist(err) {
		t.Fatalf("bindings file changed without approval: %v", err)
	}

	result = BindSkillsWithOptions(paths, newRoot, chosen, BindingOptions{ReplaceLegacyCatalogRoot: true})
	if result.Status != Bound {
		t.Fatalf("approved bind = %#v", result)
	}
	info, err := os.Lstat(paths.Binding())
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("approved catalog root = %#v, error = %v", info, err)
	}
	assertBindingLinkTarget(t, filepath.Join(paths.Binding(), "alpha"), alpha)
	assertBindingLinkTarget(t, filepath.Join(paths.Binding(), "beta"), beta)
}

func TestLegacyCatalogRollbackIsIdempotent(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	legacyRoot := filepath.Join(paths.Home, "legacy")
	alpha := filepath.Join(legacyRoot, "alpha")
	if err := os.MkdirAll(alpha, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createSymlink(legacyRoot, paths.Binding(), true); err != nil {
		t.Fatal(err)
	}
	bindings := []BoundRepository{{Path: legacyRoot, Skills: []string{"alpha"}}}
	plan, err := planBindingCatalog(paths, bindings, bindings, true, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if err := plan.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := plan.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertBindingLinkTarget(t, paths.Binding(), legacyRoot)
}

func TestBindingRecoveryRejectsAChangedApprovedLegacyRoot(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	legacyRoot := filepath.Join(paths.Home, "legacy")
	foreignRoot := filepath.Join(paths.Home, "foreign")
	alpha := filepath.Join(legacyRoot, "alpha")
	if err := os.MkdirAll(alpha, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(foreignRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createSymlink(legacyRoot, paths.Binding(), true); err != nil {
		t.Fatal(err)
	}
	bindings := []BoundRepository{{Path: legacyRoot, Skills: []string{"alpha"}}}
	mutation, err := prepareBindingMutation(paths, bindings, bindings, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if mutation.journal.LegacyRootSnapshot == nil {
		t.Fatal("legacy root snapshot was not journaled")
	}
	if err := writeBindingMutationJournal(paths, mutation.journal); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.Binding()); err != nil {
		t.Fatal(err)
	}
	if err := createSymlink(foreignRoot, paths.Binding(), true); err != nil {
		t.Fatal(err)
	}

	err = recoverBindingMutation(paths)
	if err == nil || !strings.Contains(err.Error(), "changed after validation") {
		t.Fatalf("recovery error = %v", err)
	}
	assertBindingLinkTarget(t, paths.Binding(), foreignRoot)
}

func TestBindingMutationRecoversAfterCatalogLinksChanged(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	oldRoot := filepath.Join(paths.Home, "old")
	newRoot := filepath.Join(paths.Home, "new")
	for _, root := range []string{oldRoot, newRoot} {
		if err := os.MkdirAll(filepath.Join(root, "alpha"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	before := []BoundRepository{{Path: oldRoot, Skills: []string{"alpha"}}}
	after := []BoundRepository{{Path: newRoot, Skills: []string{"alpha"}}}
	if err := writeBindings(paths, before); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Binding(), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(paths.Binding(), "alpha")
	if err := createSymlink(filepath.Join(newRoot, "alpha"), link, true); err != nil {
		t.Fatal(err)
	}
	journal := bindingMutationJournal{
		Version: bindingMutationVersion, Before: before, After: after, BeforeExists: true,
	}
	if err := writeBindingMutationJournal(paths, journal); err != nil {
		t.Fatal(err)
	}

	recovered, err := BoundRepositories(paths)
	if err != nil || !equalBindings(recovered, after) {
		t.Fatalf("recovery run bindings = %#v, error = %v", recovered, err)
	}
	bindings, err := readConfiguredBindings(paths)
	if err != nil || !equalBindings(bindings, after) {
		t.Fatalf("recovered bindings = %#v, error = %v", bindings, err)
	}
	assertBindingLinkTarget(t, link, filepath.Join(newRoot, "alpha"))
	if _, err := os.Lstat(paths.BindingMutationFile()); !os.IsNotExist(err) {
		t.Fatalf("binding journal remains: %v", err)
	}
}

func TestBindingMutationRecoversARealCatalogAfterLegacyReplacement(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	legacyRoot := filepath.Join(paths.Home, "legacy")
	newRoot := filepath.Join(paths.Home, "new")
	alpha := filepath.Join(legacyRoot, "alpha")
	beta := filepath.Join(newRoot, "beta")
	for _, skill := range []string{alpha, beta} {
		if err := os.MkdirAll(skill, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
		t.Fatal(err)
	}
	before := []BoundRepository{{Path: legacyRoot, Skills: []string{"alpha"}}}
	after := []BoundRepository{
		{Path: legacyRoot, Skills: []string{"alpha"}},
		{Path: newRoot, Skills: []string{"beta"}},
	}
	journal := bindingMutationJournal{
		Version: bindingMutationVersion, Before: before, After: after,
		LegacyRoot: true, ReplaceLegacyRoot: true,
	}
	if err := writeBindingMutationJournal(paths, journal); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths.Binding(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createSymlink(alpha, filepath.Join(paths.Binding(), "alpha"), true); err != nil {
		t.Fatal(err)
	}
	bindings, err := configuredBindings(paths)
	if err != nil || !equalBindings(bindings, after) {
		t.Fatalf("bindings during recovery = %#v, error = %v", bindings, err)
	}

	if err := recoverBindingMutation(paths); err != nil {
		t.Fatal(err)
	}
	assertBindingLinkTarget(t, filepath.Join(paths.Binding(), "alpha"), alpha)
	assertBindingLinkTarget(t, filepath.Join(paths.Binding(), "beta"), beta)
	if _, err := os.Lstat(paths.BindingMutationFile()); !os.IsNotExist(err) {
		t.Fatalf("binding journal remains: %v", err)
	}
}

func assertBindingLinkTarget(t *testing.T, link, want string) {
	t.Helper()
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("read link %s: %v", link, err)
	}
	if got := resolveLink(link, target); !samePath(got, want) {
		t.Fatalf("link %s target = %s, want %s", link, got, want)
	}
}

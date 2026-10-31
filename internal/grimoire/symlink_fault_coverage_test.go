//go:build !windows

package grimoire

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSymlinkPlansRejectChangedAndReservedDestinations(t *testing.T) {
	root := t.TempDir()
	occupied := filepath.Join(root, "occupied")
	if err := os.WriteFile(occupied, []byte("foreign"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := &symlinkPlan{}
	if err := plan.Create(occupied, "/target", true); err == nil || !strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("create occupied error = %v", err)
	}
	if err := plan.Move(filepath.Join(root, "missing"), occupied, symlinkSnapshot{}, "/target", true); err == nil {
		t.Fatal("move accepted a missing source and occupied destination")
	}

	link := filepath.Join(root, "link")
	if err := createSymlink("/before", link, true); err != nil {
		t.Fatal(err)
	}
	wrong := symlinkSnapshot{target: "/wrong", directory: true}
	if err := plan.Replace(link, wrong, "/after", true); err == nil {
		t.Fatal("replace accepted a changed link")
	}
	if err := plan.Remove(link, wrong); err == nil {
		t.Fatal("remove accepted a changed link")
	}
	if err := plan.createMissing(filepath.Join(root, "reserved"), "/one", true); err != nil {
		t.Fatal(err)
	}
	if err := plan.createMissing(filepath.Join(root, "reserved"), "/two", true); err == nil || !strings.Contains(err.Error(), "planned more than once") {
		t.Fatalf("duplicate reservation error = %v", err)
	}
	if plan.Count() != 1 || len(plan.journalActions()) != 1 {
		t.Fatalf("plan accounting = %d, %#v", plan.Count(), plan.journalActions())
	}
}

func TestSymlinkApplyAndVerifyFailurePaths(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	if err := (&symlinkPlan{actions: []symlinkAction{{kind: symlinkCreate, path: missing, target: "/target", directory: true}}}).Verify(); err == nil {
		t.Fatal("verify accepted a missing created link")
	}
	existing := filepath.Join(root, "existing")
	if err := createSymlink("/target", existing, true); err != nil {
		t.Fatal(err)
	}
	if err := (&symlinkPlan{actions: []symlinkAction{{kind: symlinkRemove, path: existing}}}).Verify(); err == nil {
		t.Fatal("verify accepted a link that should be removed")
	}
	if err := (&symlinkPlan{actions: []symlinkAction{{kind: symlinkMove, path: existing, newPath: missing, target: "/target", directory: true}}}).Verify(); err == nil {
		t.Fatal("verify accepted an incomplete move")
	}
	unknown := &symlinkAction{kind: "unknown"}
	if err := (&symlinkPlan{}).applyAction(unknown); err == nil || !strings.Contains(err.Error(), "unknown symlink action") {
		t.Fatalf("unknown action error = %v", err)
	}

	originalCreate := createSymlink
	t.Cleanup(func() { createSymlink = originalCreate })
	createSymlink = func(_, _ string, _ bool) error { return errors.New("injected create failure") }
	plan := &symlinkPlan{actions: []symlinkAction{{kind: symlinkCreate, path: missing, target: "/target", directory: true}}}
	if err := plan.Apply(); err == nil || !strings.Contains(err.Error(), "injected create failure") {
		t.Fatalf("create failure = %v", err)
	}
}

func TestSymlinkRollbackProtectsForeignChanges(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	create := symlinkAction{kind: symlinkCreate, path: missing, target: "/made", directory: true, created: true}
	if err := rollbackSymlinkAction(&create); err == nil || !errors.Is(err, errSymlinkRollbackConflict) {
		t.Fatalf("missing created-link rollback = %v", err)
	}
	if err := createSymlink("/foreign", missing, true); err != nil {
		t.Fatal(err)
	}
	if err := rollbackSymlinkAction(&create); err == nil || !errors.Is(err, errSymlinkRollbackConflict) {
		t.Fatalf("changed created-link rollback = %v", err)
	}

	restored := filepath.Join(root, "restored")
	before := symlinkSnapshot{target: "/before", directory: true}
	remove := symlinkAction{kind: symlinkRemove, path: restored, before: before, removed: true}
	if err := rollbackSymlinkAction(&remove); err != nil {
		t.Fatal(err)
	}
	if err := rollbackSymlinkAction(&remove); err != nil {
		t.Fatal(err)
	}

	replacement := filepath.Join(root, "replacement")
	if err := createSymlink("/after", replacement, true); err != nil {
		t.Fatal(err)
	}
	replace := symlinkAction{kind: symlinkReplace, path: replacement, target: "/after", directory: true, before: before, created: true, removed: true}
	if err := rollbackSymlinkAction(&replace); err != nil {
		t.Fatal(err)
	}

	oldPath, newPath := filepath.Join(root, "old"), filepath.Join(root, "new")
	if err := createSymlink("/after", newPath, true); err != nil {
		t.Fatal(err)
	}
	move := symlinkAction{kind: symlinkMove, path: oldPath, newPath: newPath, target: "/after", directory: true, before: before, created: true, removed: true}
	if err := rollbackSymlinkAction(&move); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverSymlinkPlanCoversInterruptedReplaceRemoveAndMove(t *testing.T) {
	for _, test := range []struct {
		name    string
		kind    symlinkActionKind
		old     string
		new     string
		apply   bool
		wantErr bool
	}{
		{name: "replace already complete", kind: symlinkReplace, old: "/wanted"},
		{name: "replace after removal", kind: symlinkReplace, apply: true},
		{name: "replace before removal", kind: symlinkReplace, old: "/before", apply: true},
		{name: "replace conflict", kind: symlinkReplace, old: "/foreign", wantErr: true},
		{name: "remove complete", kind: symlinkRemove},
		{name: "remove pending", kind: symlinkRemove, old: "/before", apply: true},
		{name: "remove conflict", kind: symlinkRemove, old: "/foreign", wantErr: true},
		{name: "move complete", kind: symlinkMove, new: "/wanted"},
		{name: "move both links", kind: symlinkMove, old: "/before", new: "/wanted", apply: true},
		{name: "move new conflict", kind: symlinkMove, old: "/before", new: "/foreign", wantErr: true},
		{name: "move old conflict", kind: symlinkMove, old: "/foreign", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			oldPath, newPath := filepath.Join(root, "old"), filepath.Join(root, "new")
			if test.old != "" {
				if err := createSymlink(test.old, oldPath, true); err != nil {
					t.Fatal(err)
				}
			}
			if test.new != "" {
				if err := createSymlink(test.new, newPath, true); err != nil {
					t.Fatal(err)
				}
			}
			record := journalSymlinkAction{Kind: test.kind, Path: oldPath, NewPath: newPath, Target: "/wanted", Directory: true, BeforeTarget: "/before", BeforeDirectory: true}
			plan, err := recoverSymlinkPlan([]journalSymlinkAction{record})
			if test.wantErr {
				if err == nil {
					t.Fatal("conflicting recovery was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.apply {
				if err := plan.Apply(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestSymlinkInspectionAndRestoreErrorPaths(t *testing.T) {
	root := t.TempDir()
	regular := filepath.Join(root, "regular")
	if err := os.WriteFile(regular, []byte("foreign"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := inspectSymlink(regular); err == nil {
		t.Fatal("regular file inspected as link")
	}
	if err := restoreMissingSymlink(regular, symlinkSnapshot{target: "/before"}); err == nil || !errors.Is(err, errSymlinkRollbackConflict) {
		t.Fatalf("restore over regular file = %v", err)
	}
	link := filepath.Join(root, "link")
	if err := createSymlink("/before", link, true); err != nil {
		t.Fatal(err)
	}
	before := symlinkSnapshot{target: "/before", directory: true}
	if err := restoreMissingSymlink(link, before); err != nil {
		t.Fatal(err)
	}
	if err := restoreMissingSymlink(link, symlinkSnapshot{target: "/other", directory: true}); err == nil {
		t.Fatal("restore accepted an occupied link")
	}

	originalCreate, originalRemove := createSymlink, removeSymlink
	t.Cleanup(func() { createSymlink, removeSymlink = originalCreate, originalRemove })
	createSymlink = func(_, _ string, _ bool) error { return errors.New("restore failed") }
	if err := restoreMissingSymlink(filepath.Join(root, "new"), before); err == nil || !strings.Contains(err.Error(), "restore link") {
		t.Fatalf("restore create failure = %v", err)
	}
	createSymlink = originalCreate
	removeSymlink = func(string) error { return errors.New("remove failed") }
	if err := removeMadeSymlink(link, before); err == nil || !strings.Contains(err.Error(), "remove transaction link") {
		t.Fatalf("remove failure = %v", err)
	}
}

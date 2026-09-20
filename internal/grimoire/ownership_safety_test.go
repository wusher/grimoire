package grimoire

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHoneLeavesAnUnownedLinkAlone(t *testing.T) {
	paths := testPaths(t)
	makeSkill(t, paths, "", "alpha", "First")
	home := paths.SkillsHomes()[0]
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "alpha")
	foreign := filepath.Join(paths.Home, "foreign", "alpha")
	if err := os.Symlink(foreign, link); err != nil {
		t.Fatal(err)
	}

	changes, err := Hone(paths, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("changes = %#v", changes)
	}
	assertLinkTarget(t, link, foreign)
}

func TestHoneAdoptsAValidExistingLink(t *testing.T) {
	paths := testPaths(t)
	source := makeSkill(t, paths, "", "alpha", "First")
	home := paths.SkillsHomes()[0]
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "alpha")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}

	changes, err := Hone(paths, false)
	if err != nil || len(changes) != 1 || changes[0].Action != "adopted" {
		t.Fatalf("changes = %#v, error = %v", changes, err)
	}
	ownership, err := configuredOwnership(paths)
	if err != nil || !ownershipProves(ownership, link) {
		t.Fatalf("ownership was not recorded: %#v, %v", ownership, err)
	}
}

func TestHoneDryRunUpdatesItsSimulatedOwnership(t *testing.T) {
	paths := testPaths(t)
	source := makeSkill(t, paths, "", "alpha", "First")
	home := paths.SkillsHomes()[0]
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "alpha")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	ownership := ownershipState{Version: ownershipVersion}
	ownership.set(link, filepath.Join(paths.Home, "old", "alpha"))
	if err := writeOwnership(paths, ownership); err != nil {
		t.Fatal(err)
	}

	dryChanges, err := Hone(paths, true)
	if err != nil || len(dryChanges) != 1 || dryChanges[0].Action != "adopted" {
		t.Fatalf("dry-run changes = %#v, error = %v", dryChanges, err)
	}
	realChanges, err := Hone(paths, false)
	if err != nil || len(realChanges) != 1 || realChanges[0].Action != "adopted" {
		t.Fatalf("real changes = %#v, error = %v", realChanges, err)
	}
}

func TestHoneReturnsUnexpectedReadlinkErrors(t *testing.T) {
	paths := testPaths(t)
	makeSkill(t, paths, "", "alpha", "First")
	link := filepath.Join(paths.SkillsHomes()[0], "alpha")
	ownership := ownershipState{Version: ownershipVersion}
	ownership.set(link, filepath.Join(paths.Home, "old", "alpha"))
	if err := writeOwnership(paths, ownership); err != nil {
		t.Fatal(err)
	}
	realReadSymlink := readSymlink
	t.Cleanup(func() { readSymlink = realReadSymlink })
	wantedErr := errors.New("readlink denied")
	readSymlink = func(path string) (string, error) {
		if path == link {
			return "", wantedErr
		}
		return realReadSymlink(path)
	}

	changes, err := Hone(paths, false)
	if !errors.Is(err, wantedErr) || len(changes) != 0 {
		t.Fatalf("changes = %#v, error = %v", changes, err)
	}
	updated, readErr := configuredOwnership(paths)
	if readErr != nil || len(updated.Links) != 1 {
		t.Fatalf("ownership changed = %#v, error = %v", updated, readErr)
	}
}

func TestUninstallRequiresProofAndInstallCanAdopt(t *testing.T) {
	paths := testPaths(t)
	source := makeSkill(t, paths, "", "alpha", "First")
	skill := ReadSkill(source, filepath.Join(paths.Repo, "skills"), paths.SkillsHomes())
	link := skill.LinkPaths()[0]
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}

	if result := Uninstall(paths, skill); result.Status != InstallBlocked {
		t.Fatalf("uninstall without ownership = %#v", result)
	}
	assertLinkTarget(t, link, source)
	if result := Install(paths, skill); result.Status != AlreadyStatus || !strings.Contains(result.Message, "ownership recorded") {
		t.Fatalf("adopt existing link = %#v", result)
	}
	if result := Uninstall(paths, skill); result.Status != Removed {
		t.Fatalf("uninstall adopted link = %#v", result)
	}
}

func TestOwnershipTransactionReportsRollbackFailures(t *testing.T) {
	root := t.TempDir()
	configFile := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(configFile, []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := Paths{ConfigHome: configFile}
	before := ownershipState{Version: ownershipVersion}
	after := before.clone()
	after.set(filepath.Join(root, "link"), filepath.Join(root, "target"))

	_, err := commitOwnershipUpdate(paths, before, after, func() error {
		return errors.New("link restore failed")
	})
	if err == nil {
		t.Fatal("ownership update succeeded")
	}
	for _, message := range []string{"save ownership", "rollback link or binding changes", "link restore failed"} {
		if !strings.Contains(err.Error(), message) {
			t.Errorf("error does not contain %q: %v", message, err)
		}
	}
}

func TestHoneRollsBackAllChangesAfterAnApplyError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory modes do not block link removal on Windows")
	}
	paths := testPaths(t)
	makeSkill(t, paths, "", "alpha", "First")
	firstHome := filepath.Join(paths.ClaudeHome, "skills")
	blockedHome := filepath.Join(paths.OpenCodeHome, "skills")
	for _, home := range []string{firstHome, blockedHome} {
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	firstLink := filepath.Join(firstHome, "alpha")
	firstTarget := filepath.Join(paths.Home, "old", "alpha")
	blockedLink := filepath.Join(blockedHome, "orphan")
	blockedTarget := filepath.Join(paths.Home, "missing", "orphan")
	if err := os.Symlink(firstTarget, firstLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(blockedTarget, blockedLink); err != nil {
		t.Fatal(err)
	}
	ownership := ownershipState{Version: ownershipVersion}
	ownership.set(firstLink, firstTarget)
	ownership.set(blockedLink, blockedTarget)
	if err := writeOwnership(paths, ownership); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blockedHome, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blockedHome, 0o755) })

	changes, err := Hone(paths, false)
	if err == nil {
		t.Fatal("hone succeeded")
	}
	if len(changes) != 0 {
		t.Fatalf("changes after rollback = %#v", changes)
	}
	assertLinkTarget(t, firstLink, firstTarget)
	assertLinkTarget(t, blockedLink, blockedTarget)
	updated, readErr := configuredOwnership(paths)
	if readErr != nil || !ownershipProves(updated, firstLink) || !ownershipProves(updated, blockedLink) {
		t.Fatalf("ownership after rollback = %#v, error = %v", updated, readErr)
	}
}

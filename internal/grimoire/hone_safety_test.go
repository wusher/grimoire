package grimoire

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHoneDryRunMatchesRealRunAfterSimulatedAdoption(t *testing.T) {
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
	oldTarget := filepath.Join(paths.Home, "old", "alpha")
	ownership := ownershipState{Version: ownershipVersion}
	ownership.set(link, oldTarget)
	if err := writeOwnership(paths, ownership); err != nil {
		t.Fatal(err)
	}

	dryChanges, err := Hone(paths, true)
	if err != nil {
		t.Fatal(err)
	}
	afterDryRun, err := configuredOwnership(paths)
	if err != nil {
		t.Fatal(err)
	}
	if target, _ := afterDryRun.target(link); target != oldTarget {
		t.Fatalf("dry-run ownership target = %q, want %q", target, oldTarget)
	}

	realChanges, err := Hone(paths, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dryChanges, realChanges) {
		t.Fatalf("dry-run changes = %#v, real changes = %#v", dryChanges, realChanges)
	}
	updated, err := configuredOwnership(paths)
	if err != nil || !ownershipProves(updated, link) {
		t.Fatalf("ownership was not proved after real run: %#v, %v", updated, err)
	}
}

func TestHoneReturnsPartialChangesAndKeepsOwnershipOnReadlinkError(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "permission", err: os.ErrPermission},
		{name: "temporary", err: errors.New("temporary readlink failure")},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := testPaths(t)
			source := makeSkill(t, paths, "", "alpha", "First")
			home := paths.SkillsHomes()[0]
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			adoptedLink := filepath.Join(home, "alpha")
			if err := os.Symlink(source, adoptedLink); err != nil {
				t.Fatal(err)
			}
			ownedLink := filepath.Join(home, "orphan")
			ownedTarget := filepath.Join(paths.Home, "orphan-source")
			if err := os.Symlink(ownedTarget, ownedLink); err != nil {
				t.Fatal(err)
			}
			ownership := ownershipState{Version: ownershipVersion}
			ownership.set(ownedLink, ownedTarget)
			if err := writeOwnership(paths, ownership); err != nil {
				t.Fatal(err)
			}

			realReadSymlink := readSymlink
			readSymlink = func(path string) (string, error) {
				if path == ownedLink {
					return "", test.err
				}
				return realReadSymlink(path)
			}
			t.Cleanup(func() { readSymlink = realReadSymlink })

			changes, err := Hone(paths, false)
			if !errors.Is(err, test.err) {
				t.Fatalf("error = %v, want %v", err, test.err)
			}
			if len(changes) != 1 || changes[0].Action != "adopted" {
				t.Fatalf("partial changes = %#v", changes)
			}

			readSymlink = realReadSymlink
			updated, err := configuredOwnership(paths)
			if err != nil {
				t.Fatal(err)
			}
			if target, tracked := updated.target(ownedLink); !tracked || target != ownedTarget {
				t.Fatalf("owned link record = %q, %t; want %q, true", target, tracked, ownedTarget)
			}
			if _, tracked := updated.target(adoptedLink); tracked {
				t.Fatal("partial adoption was saved after the inspection error")
			}
		})
	}
}

func TestInstallPreservesExactOwnershipProofForEquivalentTarget(t *testing.T) {
	paths := testPaths(t)
	source := makeSkill(t, paths, "", "alpha", "First")
	alias := filepath.Join(paths.Home, "alpha-alias")
	if err := os.Symlink(source, alias); err != nil {
		t.Fatal(err)
	}
	skill := ReadSkill(alias, filepath.Dir(alias), paths.SkillsHomes())
	link := skill.LinkPaths()[0]
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}

	result := Install(paths, skill)
	if result.Status != AlreadyStatus || !strings.Contains(result.Message, "ownership recorded") {
		t.Fatalf("install result = %#v", result)
	}
	ownership, err := configuredOwnership(paths)
	if err != nil || !ownershipProves(ownership, link) {
		t.Fatalf("ownership was not proved: %#v, %v", ownership, err)
	}
	if target, _ := ownership.target(link); target != source {
		t.Fatalf("ownership target = %q, want exact link target %q", target, source)
	}
}

func TestUninstallKeepsOwnershipOnReadlinkError(t *testing.T) {
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
	ownership := ownershipState{Version: ownershipVersion}
	ownership.set(link, source)
	if err := writeOwnership(paths, ownership); err != nil {
		t.Fatal(err)
	}

	realReadSymlink := readSymlink
	wantedErr := errors.New("temporary readlink failure")
	readSymlink = func(path string) (string, error) {
		if path == link {
			return "", wantedErr
		}
		return realReadSymlink(path)
	}
	t.Cleanup(func() { readSymlink = realReadSymlink })

	result := Uninstall(paths, skill)
	if result.Status != InstallBlocked || !strings.Contains(result.Message, wantedErr.Error()) {
		t.Fatalf("uninstall result = %#v", result)
	}
	readSymlink = realReadSymlink
	updated, err := configuredOwnership(paths)
	if err != nil || !ownershipProves(updated, link) {
		t.Fatalf("ownership was not preserved: %#v, %v", updated, err)
	}
}

//go:build !windows

package grimoire

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHoneCLIReportsAndAppliesEveryRepairClass(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repoOne := filepath.Join(paths.Home, "repo-one")
	repoTwo := filepath.Join(paths.Home, "repo-two")
	fixed := makeSkillIn(t, repoOne, "", "fixed", "Fixed")
	adopted := makeSkillIn(t, repoOne, "", "adopted", "Adopted")
	clashOne := makeSkillIn(t, repoOne, "", "clash", "Clash one")
	clashTwo := makeSkillIn(t, repoTwo, "", "clash", "Clash two")
	bindings := []BoundRepository{
		{Path: repoOne, Skills: []string{"fixed", "adopted", "clash"}},
		{Path: repoTwo, Skills: []string{"clash"}},
	}
	if err := writeBindings(paths, bindings); err != nil {
		t.Fatal(err)
	}

	home := filepath.Join(paths.ClaudeHome, "skills")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	oldTarget := filepath.Join(paths.Home, "old-fixed")
	orphanTarget := filepath.Join(paths.Home, "orphan-target")
	for _, dir := range []string{oldTarget, orphanTarget} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	fixedLink := filepath.Join(home, "fixed")
	adoptedLink := filepath.Join(home, "adopted")
	removedLink := filepath.Join(home, "removed")
	orphanLink := filepath.Join(home, "orphan")
	clashLink := filepath.Join(home, "clash")
	for link, target := range map[string]string{
		fixedLink: oldTarget, adoptedLink: adopted, removedLink: filepath.Join(paths.Home, "gone"),
		orphanLink: orphanTarget, clashLink: clashOne,
	} {
		if err := createSymlink(target, link, true); err != nil {
			t.Fatal(err)
		}
	}
	ownership := ownershipState{Version: ownershipVersion}
	ownership.set(fixedLink, oldTarget)
	ownership.set(removedLink, filepath.Join(paths.Home, "gone"))
	ownership.set(filepath.Join(home, "released"), filepath.Join(paths.Home, "released-target"))
	ownership.set(orphanLink, orphanTarget)
	ownership.set(clashLink, clashOne)
	if err := writeOwnership(paths, ownership); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	cli := &CLI{Out: &output, Err: &output, Paths: paths}
	if err := cli.hone([]string{"--dry-run"}); err != nil {
		t.Fatal(err)
	}
	dry := output.String()
	for _, want := range []string{"would fix", "would remove", "would adopt", "would release", "to repair", "ownership recorded", "ownership released"} {
		if !strings.Contains(dry, want) {
			t.Errorf("dry-run output missing %q: %q", want, dry)
		}
	}
	assertBindingLinkTarget(t, fixedLink, oldTarget)
	if _, err := os.Lstat(removedLink); err != nil {
		t.Fatalf("dry run removed link: %v", err)
	}

	output.Reset()
	if err := cli.hone(nil); err != nil {
		t.Fatal(err)
	}
	actual := output.String()
	for _, want := range []string{"fixed", "removed", "adopted", "released", "repaired", "ownership recorded"} {
		if !strings.Contains(actual, want) {
			t.Errorf("hone output missing %q: %q", want, actual)
		}
	}
	assertBindingLinkTarget(t, fixedLink, fixed)
	assertBindingLinkTarget(t, adoptedLink, adopted)
	if _, err := os.Lstat(removedLink); !os.IsNotExist(err) {
		t.Fatalf("stale link remains: %v", err)
	}
	if _, err := os.Lstat(orphanLink); err != nil {
		t.Fatalf("live orphan was removed: %v", err)
	}
	assertBindingLinkTarget(t, clashLink, clashOne)
	if samePath(clashOne, clashTwo) {
		t.Fatal("clash fixture collapsed")
	}
}

func TestHoneBoringDryRunFormatsEveryAction(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repository")
	wanted := makeSkillIn(t, repository, "", "fixed", "Fixed")
	if err := writeBindings(paths, []BoundRepository{{Path: repository, Skills: []string{"fixed"}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(paths.ClaudeHome, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(paths.Home, "old")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(paths.ClaudeHome, "skills", "fixed")
	if err := createSymlink(old, link, true); err != nil {
		t.Fatal(err)
	}
	ownership := ownershipState{Version: ownershipVersion}
	ownership.set(link, old)
	if err := writeOwnership(paths, ownership); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	cli := &CLI{Out: &output, Err: &output, Paths: paths, Config: Config{Boring: true}}
	if err := cli.hone([]string{"--dry-run"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "would fix") || !strings.Contains(output.String(), "dry run") {
		t.Fatalf("boring dry-run output = %q", output.String())
	}
	assertBindingLinkTarget(t, link, old)
	if wanted == "" {
		t.Fatal("missing wanted source")
	}
}

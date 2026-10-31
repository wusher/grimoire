//go:build !windows

package grimoire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func installFakeGit(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  *"status --porcelain=v1"*)
    [ "$SCENARIO" = "status_fail" ] && exit 1
    [ "$SCENARIO" = "dirty" ] && { echo " M file"; exit 0; }
    [ -f "$STATE_DIR/fetched" ] && [ "$SCENARIO" = "status_after_fail" ] && exit 1
    [ -f "$STATE_DIR/fetched" ] && [ "$SCENARIO" = "dirty_after" ] && { echo "?? changed"; exit 0; }
    exit 0
    ;;
  *"rev-parse --abbrev-ref"*)
    [ "$SCENARIO" = "no_upstream" ] && exit 1
    echo origin/main
    ;;
  *"rev-parse HEAD"*)
    [ "$SCENARIO" = "head_fail" ] && exit 1
    if [ "$SCENARIO" = "head_changed" ] && [ -f "$STATE_DIR/fetched" ]; then
      echo bbbbbbbbbbbbbbbb
    elif [ "$SCENARIO" = "pulled" ] && [ -f "$STATE_DIR/merged" ]; then
      echo bbbbbbbbbbbbbbbb
    else
      echo aaaaaaaaaaaaaaaa
    fi
    ;;
  *"rev-parse @{u}^{commit}"*)
    if [ "$SCENARIO" = "current" ]; then
      echo aaaaaaaaaaaaaaaa
    else
      echo bbbbbbbbbbbbbbbb
    fi
    ;;
  *"fetch"*)
    [ "$SCENARIO" = "fetch_fail" ] && exit 1
    : > "$STATE_DIR/fetched"
    ;;
  *"merge --ff-only"*)
    [ "$SCENARIO" = "merge_fail" ] && exit 1
    : > "$STATE_DIR/merged"
    ;;
  *) exit 1 ;;
esac
`
	git := filepath.Join(bin, "git")
	if err := os.WriteFile(git, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func TestPullLatestReportsEverySafetyStop(t *testing.T) {
	installFakeGit(t)
	repository := t.TempDir()
	tests := []struct {
		scenario string
		status   RefreshStatus
		message  string
	}{
		{"status_fail", RefreshFailed, "not a Git repository"},
		{"dirty", RefreshSkipped, "uncommitted changes"},
		{"no_upstream", RefreshSkipped, "no upstream"},
		{"head_fail", RefreshFailed, "cannot read HEAD"},
		{"fetch_fail", RefreshFailed, "fetch failed"},
		{"status_after_fail", RefreshFailed, "cannot recheck worktree"},
		{"dirty_after", RefreshSkipped, "worktree changed during fetch"},
		{"head_changed", RefreshSkipped, "HEAD changed during fetch"},
		{"merge_fail", RefreshFailed, "not fast-forward"},
		{"current", CurrentBranch, "already up to date"},
		{"pulled", Pulled, "updated aaaaaaa to bbbbbbb"},
	}
	for _, test := range tests {
		t.Run(test.scenario, func(t *testing.T) {
			state := t.TempDir()
			t.Setenv("SCENARIO", test.scenario)
			t.Setenv("STATE_DIR", state)
			result := PullLatest(repository)
			if result.Status != test.status || !strings.Contains(result.Message, test.message) {
				t.Fatalf("result = %#v; want %s containing %q", result, test.status, test.message)
			}
		})
	}
}

func TestRepositoryIssueAndIndexHelpersCoverMissingAndBrokenLinks(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repository")
	valid := makeSkillIn(t, repository, "", "valid", "Valid")
	broken := makeSkillIn(t, repository, "", "broken", "Broken")
	bindings := BoundRepository{Path: repository, Skills: []string{"valid", "broken", "missing"}}
	if err := os.MkdirAll(paths.Binding(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createSymlink(valid, filepath.Join(paths.Binding(), "valid"), true); err != nil {
		t.Fatal(err)
	}
	if err := createSymlink(filepath.Join(paths.Home, "wrong"), filepath.Join(paths.Binding(), "broken"), true); err != nil {
		t.Fatal(err)
	}
	missing, brokenCount, err := repositoryIssues(paths, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if missing != 1 || brokenCount != 1 {
		t.Fatalf("issues = %d missing, %d broken", missing, brokenCount)
	}
	validExists, err := resourceFileExists(valid, SkillKind)
	if err != nil {
		t.Fatal(err)
	}
	brokenExists, err := resourceFileExists(broken, SkillKind)
	if err != nil {
		t.Fatal(err)
	}
	missingExists, err := resourceFileExists(filepath.Join(repository, "missing"), SkillKind)
	if err != nil {
		t.Fatal(err)
	}
	if !validExists || !brokenExists || missingExists {
		t.Fatal("skill existence mismatch")
	}

	cloned := cloneBindings([]BoundRepository{bindings})
	cloned[0].Skills[0] = "changed"
	if equalBindings([]BoundRepository{bindings}, cloned) || bindings.Skills[0] == "changed" {
		t.Fatal("binding clone was not independent")
	}
	if shortRevision("abcdef") != "abcdef" || shortRevision("abcdefghijkl") != "abcdefg" {
		t.Fatal("short revision")
	}
}

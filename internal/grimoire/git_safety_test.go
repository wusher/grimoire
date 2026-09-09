package grimoire

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreeChangesOverridesUntrackedFileConfiguration(t *testing.T) {
	paths := testPaths(t)
	repository := filepath.Join(paths.Home, "repository")
	initGit(t, repository)
	makeSkillIn(t, repository, "", "alpha", "First")
	if err := os.WriteFile(filepath.Join(repository, ".gitignore"), []byte("ignored/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, repository, "initial")
	if output, err := exec.Command("git", "-C", repository, "config", "status.showUntrackedFiles", "no").CombinedOutput(); err != nil {
		t.Fatalf("configure Git status: %v: %s", err, output)
	}

	untracked := filepath.Join(repository, "untracked", "deep", "draft.md")
	ignored := filepath.Join(repository, "ignored", "deep", "cache.bin")
	for _, path := range []string{untracked, ignored} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("local work"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	status, err := worktreeChanges(repository)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"?? " + filepath.ToSlash("untracked/deep/draft.md"),
		"!! " + filepath.ToSlash("ignored/deep/cache.bin"),
	} {
		if !strings.Contains(status, want) {
			t.Errorf("Git status does not contain %q:\n%s", want, status)
		}
	}
}

func TestRepositoryMoveRevisionMustBelongToCandidateHistory(t *testing.T) {
	paths := testPaths(t)
	candidate := filepath.Join(paths.Home, "candidate")
	foreign := filepath.Join(paths.Home, "foreign")
	initGit(t, candidate)
	makeSkillIn(t, candidate, "", "alpha", "Candidate")
	gitCommitAll(t, candidate, "candidate")
	candidateRevision, err := gitOutput(candidate, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	initGit(t, foreign)
	makeSkillIn(t, foreign, "", "beta", "Foreign")
	gitCommitAll(t, foreign, "foreign")
	foreignRevision, err := gitOutput(foreign, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", candidate, "fetch", "-q", foreign, foreignRevision).CombinedOutput(); err != nil {
		t.Fatalf("place foreign commit in candidate object store: %v: %s", err, output)
	}
	if _, err := gitOutput(candidate, "cat-file", "-e", foreignRevision+"^{commit}"); err != nil {
		t.Fatalf("foreign commit is not in candidate object store: %v", err)
	}

	record := BoundRepository{Identity: repositoryIdentity(candidate), Revision: foreignRevision}
	if repositoryMatchesMoveEvidence(record, candidate) {
		t.Fatal("candidate accepted a recorded revision outside its history")
	}
	record.Revision = candidateRevision
	if !repositoryMatchesMoveEvidence(record, candidate) {
		t.Fatal("candidate rejected a recorded revision in its history")
	}
}

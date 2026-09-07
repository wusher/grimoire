package grimoire

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// BoundRepository is one folder recorded in the binding manifest, with the
// repository-relative skill paths selected from it.
type BoundRepository struct {
	Path   string
	Skills []string
}

// BoundRepositories reads every folder the user bound. GRIMOIRE_REPO narrows
// automation to one folder, the same override LoadCatalog respects.
func BoundRepositories(paths Paths) ([]BoundRepository, error) {
	if paths.Repo != "" {
		root, err := absolute(paths.Repo)
		if err != nil {
			return nil, err
		}
		return []BoundRepository{{Path: root}}, nil
	}
	bindings, err := configuredBindings(paths)
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, noLibraryBoundError()
	}
	found := make([]BoundRepository, 0, len(bindings))
	for _, binding := range bindings {
		found = append(found, BoundRepository{Path: binding.Path, Skills: binding.Skills})
	}
	return found, nil
}

type RefreshStatus string

const (
	Pulled         RefreshStatus = "pulled"
	CurrentBranch  RefreshStatus = "current"
	RefreshSkipped RefreshStatus = "skipped"
	RefreshFailed  RefreshStatus = "failed"
)

type RefreshResult struct {
	Repo    string
	Status  RefreshStatus
	Message string
}

// PullLatest fetches and fast-forwards one repository. Safety rules, in
// order: a missing folder or non-repository fails, a dirty worktree is left
// completely alone, a branch without an upstream is skipped, and the pull
// uses --ff-only so a diverged branch fails instead of merging or rewriting.
// Local work is never touched.
func PullLatest(repository string) RefreshResult {
	info, statErr := os.Stat(repository)
	if statErr != nil || !info.IsDir() {
		return RefreshResult{Repo: repository, Status: RefreshFailed, Message: "folder is missing"}
	}
	dirty, err := gitOutput(repository, "status", "--porcelain")
	if err != nil {
		return RefreshResult{Repo: repository, Status: RefreshFailed, Message: "not a Git repository"}
	}
	if dirty != "" {
		return RefreshResult{Repo: repository, Status: RefreshSkipped, Message: "uncommitted changes; left alone"}
	}
	if _, err := gitOutput(repository, "rev-parse", "--abbrev-ref", "@{u}"); err != nil {
		return RefreshResult{Repo: repository, Status: RefreshSkipped, Message: "no upstream branch"}
	}
	before, err := gitOutput(repository, "rev-parse", "HEAD")
	if err != nil {
		return RefreshResult{Repo: repository, Status: RefreshFailed, Message: "cannot read HEAD"}
	}
	if _, err := gitOutput(repository, "fetch"); err != nil {
		return RefreshResult{Repo: repository, Status: RefreshFailed, Message: "fetch failed"}
	}
	if _, err := gitOutput(repository, "merge", "--ff-only", "@{u}"); err != nil {
		return RefreshResult{Repo: repository, Status: RefreshFailed, Message: "not fast-forward; left alone"}
	}
	after, _ := gitOutput(repository, "rev-parse", "HEAD")
	if before == after {
		return RefreshResult{Repo: repository, Status: CurrentBranch, Message: "already up to date"}
	}
	return RefreshResult{Repo: repository, Status: Pulled, Message: fmt.Sprintf("updated %s to %s", shortRevision(before), shortRevision(after))}
}

func gitOutput(repository string, args ...string) (string, error) {
	full := append([]string{"-C", repository}, args...)
	command := exec.Command("git", full...)
	body, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

func shortRevision(revision string) string {
	if len(revision) > 7 {
		return revision[:7]
	}
	return revision
}

package grimoire

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// BoundRepository is one folder recorded in the binding manifest, with the
// repository-relative skill paths selected from it.
type BoundRepository struct {
	Path     string   `json:"path"`
	Skills   []string `json:"skills"`
	Identity string   `json:"identity,omitempty"`
	Revision string   `json:"revision,omitempty"`
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
	unlock, err := lockBindings(paths)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := recoverBindingMutation(paths); err != nil {
		return nil, err
	}
	bindings, err := configuredBindings(paths)
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, noLibraryBoundError()
	}
	return bindings, nil
}

type RefreshStatus string

const (
	Pulled         RefreshStatus = "pulled"
	CurrentBranch  RefreshStatus = "current"
	RefreshSkipped RefreshStatus = "skipped"
	RefreshFailed  RefreshStatus = "failed"
)

type RefreshResult struct {
	Repo     string
	Status   RefreshStatus
	Message  string
	Changes  []RefreshChange
	before   string
	after    string
	repaired int
}

type RefreshChange struct {
	Action  string
	Name    string
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
	dirty, err := worktreeChanges(repository)
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
	dirty, err = worktreeChanges(repository)
	if err != nil {
		return RefreshResult{Repo: repository, Status: RefreshFailed, Message: "cannot recheck worktree after fetch", before: before}
	}
	if dirty != "" {
		return RefreshResult{Repo: repository, Status: RefreshSkipped, Message: "worktree changed during fetch; left alone", before: before}
	}
	current, err := gitOutput(repository, "rev-parse", "HEAD")
	if err != nil || current != before {
		return RefreshResult{Repo: repository, Status: RefreshSkipped, Message: "HEAD changed during fetch; left alone", before: before}
	}
	if _, err := gitOutput(repository, "merge", "--ff-only", "@{u}"); err != nil {
		return RefreshResult{Repo: repository, Status: RefreshFailed, Message: "not fast-forward; left alone"}
	}
	after, _ := gitOutput(repository, "rev-parse", "HEAD")
	if before == after {
		return RefreshResult{Repo: repository, Status: CurrentBranch, Message: "already up to date", before: before, after: after}
	}
	return RefreshResult{Repo: repository, Status: Pulled, Message: fmt.Sprintf("updated %s to %s", shortRevision(before), shortRevision(after)), before: before, after: after}
}

func worktreeChanges(repository string) (string, error) {
	return gitOutput(repository, "status", "--porcelain=v1", "--untracked-files=all", "--ignored")
}

// RefreshRepository finds a locally renamed repository, fast-forwards it, and
// reconciles managed links with path renames in the pulled commits.
func RefreshRepository(paths Paths, repository BoundRepository) RefreshResult {
	original := repository.Path
	changes := []RefreshChange{}
	repaired := 0
	info, statErr := os.Stat(repository.Path)
	pathExists := statErr == nil && info.IsDir()
	identityMismatch := pathExists && repository.Identity != "" && repositoryIdentity(repository.Path) != repository.Identity
	if !pathExists || identityMismatch {
		moved := findMovedRepository(repository)
		if moved != "" {
			linkChanges, linkRepairs, err := moveRepositoryBinding(paths, repository, moved)
			if err != nil {
				return RefreshResult{Repo: original, Status: RefreshFailed, Message: "found renamed folder, but could not update binding: " + err.Error()}
			}
			repository.Path = moved
			changes = append(changes, RefreshChange{Action: "moved", Message: fmt.Sprintf("binding and catalog links from %s", shortPath(original, paths.Home))})
			changes = append(changes, linkChanges...)
			repaired += linkRepairs
		} else if identityMismatch {
			return RefreshResult{Repo: original, Status: RefreshFailed, Message: "configured folder has a different repository identity; left alone"}
		}
	}
	adopted, err := adoptRepositoryLinks(paths, repository)
	if err != nil {
		return RefreshResult{
			Repo: repository.Path, Status: RefreshFailed, Message: "could not record existing installed links: " + err.Error(),
			Changes: changes, repaired: repaired,
		}
	}
	changes = append(changes, adopted...)

	result := PullLatest(repository.Path)
	result.Changes = changes
	result.repaired = repaired
	if result.Status == RefreshFailed {
		return result
	}

	from := repository.Revision
	if from == "" {
		from = result.before
	}
	renamed := map[string]string{}
	if from != "" && result.after != "" && from != result.after {
		if _, err := gitOutput(repository.Path, "merge-base", "--is-ancestor", from, result.after); err == nil {
			renamed = skillRenames(repository.Path, from, result.after)
		}
	}
	repairs, repaired, err := repairRepositoryBinding(paths, repository.Path, renamed, result.after)
	result.Changes = append(result.Changes, repairs...)
	result.repaired += repaired
	if err != nil {
		result.Status = RefreshFailed
		result.Message += "; link repair failed: " + err.Error()
	}
	return result
}

// findMovedRepository searches only siblings of the old folder. Skill paths
// can be stale after a pull that was not followed by a Grimoire refresh.
func findMovedRepository(repository BoundRepository) string {
	if repository.Identity == "" || repository.Revision == "" {
		return ""
	}
	parent := filepath.Dir(repository.Path)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return ""
	}
	var matches []string
	for _, entry := range entries {
		path := filepath.Join(parent, entry.Name())
		if filepath.Clean(path) == filepath.Clean(repository.Path) {
			continue
		}
		info, statErr := os.Stat(path)
		if statErr != nil || !info.IsDir() || !repositoryMatchesMoveEvidence(repository, path) {
			continue
		}
		root, rootErr := gitOutput(path, "rev-parse", "--show-toplevel")
		if rootErr != nil || !samePath(root, path) {
			continue
		}
		matches = append(matches, path)
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

func repositoryMatchesMoveEvidence(repository BoundRepository, candidate string) bool {
	if repository.Identity == "" || repository.Revision == "" || repositoryIdentity(candidate) != repository.Identity {
		return false
	}
	_, err := gitOutput(candidate, "merge-base", "--is-ancestor", repository.Revision+"^{commit}", "HEAD")
	return err == nil
}

func adoptRepositoryLinks(paths Paths, repository BoundRepository) ([]RefreshChange, error) {
	if paths.Repo != "" {
		return nil, nil
	}
	unlock, err := lockBindings(paths)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := recoverBindingMutation(paths); err != nil {
		return nil, err
	}
	ownership, err := configuredOwnership(paths)
	if err != nil {
		return nil, err
	}
	updated := ownership.clone()
	var changes []RefreshChange
	var adopted []ownedLink
	for _, rel := range repository.Skills {
		source := filepath.Join(repository.Path, rel)
		if !skillFileExists(source) {
			continue
		}
		for _, home := range paths.KnownSkillsHomes() {
			link := filepath.Join(home, filepath.Base(rel))
			actual, linkErr := linkTarget(link)
			if os.IsNotExist(linkErr) {
				continue
			}
			if linkErr != nil {
				return nil, fmt.Errorf("inspect installed link %s: %w", link, linkErr)
			}
			if !samePath(actual, source) || ownershipProves(ownership, link) {
				continue
			}
			updated.set(link, actual)
			adopted = append(adopted, ownedLink{Path: link, Target: actual})
			changes = append(changes, RefreshChange{
				Action: "recorded", Name: filepath.Base(rel),
				Message: "ownership of existing installed link in " + shortPath(home, paths.Home),
			})
		}
	}
	if len(changes) == 0 {
		return nil, nil
	}
	for _, record := range adopted {
		actual, err := linkTarget(record.Path)
		if err != nil || actual != record.Target {
			return nil, fmt.Errorf("installed link %s changed before ownership could be saved", record.Path)
		}
	}
	if _, err := commitOwnershipUpdate(paths, ownership, updated, nil); err != nil {
		return nil, err
	}
	return changes, nil
}

func repositoryIssues(paths Paths, repository BoundRepository) (int, int) {
	missing, broken := 0, 0
	for _, rel := range repository.Skills {
		source := filepath.Join(repository.Path, rel)
		if !skillFileExists(source) {
			missing++
			continue
		}
		link := filepath.Join(paths.Binding(), filepath.Base(rel))
		target, err := os.Readlink(link)
		if err != nil || !samePath(resolveLink(link, target), source) {
			broken++
		}
	}
	return missing, broken
}

func moveRepositoryBinding(paths Paths, repository BoundRepository, newPath string) ([]RefreshChange, int, error) {
	if paths.Repo != "" {
		return nil, 0, nil
	}
	if !repositoryMatchesMoveEvidence(repository, newPath) {
		return nil, 0, fmt.Errorf("renamed folder identity and revision are no longer proven")
	}
	unlock, err := lockBindings(paths)
	if err != nil {
		return nil, 0, err
	}
	defer unlock()
	if err := recoverBindingMutation(paths); err != nil {
		return nil, 0, err
	}
	current, err := configuredBindings(paths)
	if err != nil {
		return nil, 0, err
	}
	updated := append([]BoundRepository(nil), current...)
	found := -1
	for index := range current {
		if filepath.Clean(current[index].Path) == filepath.Clean(repository.Path) {
			if current[index].Identity != repository.Identity || current[index].Revision != repository.Revision {
				return nil, 0, fmt.Errorf("repository binding identity changed")
			}
			found = index
			break
		}
	}
	if found < 0 {
		return nil, 0, fmt.Errorf("old binding is no longer configured")
	}
	if !repositoryMatchesMoveEvidence(current[found], newPath) {
		return nil, 0, fmt.Errorf("renamed folder identity and revision changed while waiting for the binding lock")
	}
	updated[found].Path = filepath.Clean(newPath)
	ownership, err := configuredOwnership(paths)
	if err != nil {
		return nil, 0, err
	}
	updatedOwnership := ownership.clone()
	var installed []installedRename
	for _, rel := range repository.Skills {
		renamed, err := installedRenames(paths, ownership, filepath.Join(repository.Path, rel), filepath.Join(newPath, rel))
		if err != nil {
			return nil, 0, err
		}
		installed = append(installed, renamed...)
	}
	applyOwnershipRenames(&updatedOwnership, installed)
	installedPlan, err := planInstalledRenames(installed)
	if err != nil {
		return nil, 0, err
	}
	mutation, err := prepareBindingMutation(paths, current, updated, false, false)
	if err != nil {
		return nil, 0, err
	}
	mutation.SetLinkAndOwnershipUpdate(installedPlan, ownership, updatedOwnership)
	if err := mutation.Commit(); err != nil {
		return nil, 0, err
	}
	changes := installedRenameChanges(installed, paths.Home)
	return changes, mutation.CatalogChangeCount() + len(changes), nil
}

func repairRepositoryBinding(paths Paths, repository string, renames map[string]string, revision string) ([]RefreshChange, int, error) {
	if paths.Repo != "" {
		return nil, 0, nil
	}
	unlock, err := lockBindings(paths)
	if err != nil {
		return nil, 0, err
	}
	defer unlock()
	if err := recoverBindingMutation(paths); err != nil {
		return nil, 0, err
	}
	if legacyBindingRoot(paths) {
		return nil, 0, nil
	}
	current, err := configuredBindings(paths)
	if err != nil {
		return nil, 0, err
	}
	updated := cloneBindings(current)
	found := -1
	for index := range updated {
		if samePath(updated[index].Path, repository) {
			found = index
			break
		}
	}
	if found < 0 {
		return nil, 0, fmt.Errorf("repository binding is no longer configured")
	}
	identity := repositoryIdentity(repository)
	if updated[found].Identity != "" && identity != updated[found].Identity {
		return nil, 0, fmt.Errorf("repository identity changed; left links and binding alone")
	}
	ownership, err := configuredOwnership(paths)
	if err != nil {
		return nil, 0, err
	}
	updatedOwnership := ownership.clone()

	var changes []RefreshChange
	var installed []installedRename
	recordedIdentity := false
	if updated[found].Identity == "" {
		updated[found].Identity = identity
		recordedIdentity = recordedIdentity || updated[found].Identity != ""
	}
	if recordedIdentity {
		changes = append(changes, RefreshChange{Action: "recorded", Message: "repository identity for safe folder rename detection"})
	}
	if revision != "" {
		updated[found].Revision = revision
	}
	for index, oldRel := range updated[found].Skills {
		newRel, renamed := renames[filepath.Clean(oldRel)]
		if !renamed || skillFileExists(filepath.Join(repository, oldRel)) || !skillFileExists(filepath.Join(repository, newRel)) {
			continue
		}
		oldSource := filepath.Join(repository, oldRel)
		newSource := filepath.Join(repository, newRel)
		renamedInstalled, err := installedRenames(paths, ownership, oldSource, newSource)
		if err != nil {
			return nil, 0, err
		}
		installed = append(installed, renamedInstalled...)
		updated[found].Skills[index] = newRel
		oldName, newName := filepath.Base(oldRel), filepath.Base(newRel)
		if oldName == newName {
			changes = append(changes, RefreshChange{Action: "moved", Name: oldName, Message: fmt.Sprintf("from %s to %s", oldRel, newRel)})
		} else {
			changes = append(changes, RefreshChange{Action: "renamed", Name: oldName, Message: "to " + newName})
		}
	}
	applyOwnershipRenames(&updatedOwnership, installed)
	sort.Strings(updated[found].Skills)
	installedPlan, err := planInstalledRenames(installed)
	if err != nil {
		return nil, 0, err
	}
	mutation, err := prepareBindingMutation(paths, current, updated, false, false)
	if err != nil {
		return nil, 0, err
	}
	mutation.SetLinkAndOwnershipUpdate(installedPlan, ownership, updatedOwnership)
	repaired := mutation.CatalogChangeCount()
	if !equalBindings(current, updated) || repaired > 0 || installedPlan.Count() > 0 || !ownershipEqual(ownership, updatedOwnership) {
		if err := mutation.Commit(); err != nil {
			return nil, 0, err
		}
	}
	installedChanges := installedRenameChanges(installed, paths.Home)
	changes = append(changes, installedChanges...)
	if repaired > 0 {
		changes = append(changes, RefreshChange{Action: "repaired", Message: fmt.Sprintf("%d catalog link%s", repaired, plural(repaired))})
	}
	return changes, repaired + len(installedChanges), nil
}

func skillRenames(repository, before, after string) map[string]string {
	output, err := gitOutput(repository, "-c", "core.quotepath=false", "diff", "--name-status", "-z", "--find-renames", before, after, "--")
	if err != nil || output == "" {
		return map[string]string{}
	}
	parts := strings.Split(strings.TrimRight(output, "\x00"), "\x00")
	found := map[string]string{}
	for index := 0; index+2 < len(parts); {
		status := parts[index]
		index++
		if !strings.HasPrefix(status, "R") {
			index++
			continue
		}
		oldName, newName := parts[index], parts[index+1]
		index += 2
		if filepath.Base(oldName) != "SKILL.md" || filepath.Base(newName) != "SKILL.md" {
			continue
		}
		if !sameSkillDefinition(repository, before, after, oldName, newName) {
			continue
		}
		oldRel := filepath.Clean(filepath.FromSlash(filepath.Dir(oldName)))
		newRel := filepath.Clean(filepath.FromSlash(filepath.Dir(newName)))
		if safeRelative(oldRel) && safeRelative(newRel) {
			found[oldRel] = newRel
		}
	}
	return found
}

func sameSkillDefinition(repository, before, after, oldName, newName string) bool {
	oldBody, oldErr := gitOutput(repository, "show", before+":"+oldName)
	newBody, newErr := gitOutput(repository, "show", after+":"+newName)
	if oldErr != nil || newErr != nil {
		return false
	}
	return normalizeSkillDefinition(oldBody) == normalizeSkillDefinition(newBody)
}

func normalizeSkillDefinition(body string) string {
	lines := strings.Split(body, "\n")
	inFrontmatter := len(lines) > 0 && strings.TrimSpace(lines[0]) == "---"
	for index := 1; inFrontmatter && index < len(lines); index++ {
		line := lines[index]
		if strings.TrimSpace(line) == "---" {
			break
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && strings.HasPrefix(line, "name:") {
			lines[index] = ""
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func skillFileExists(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil && !info.IsDir()
}

type installedRename struct {
	home      string
	oldLink   string
	newLink   string
	newSource string
	snapshot  symlinkSnapshot
}

func installedRenames(paths Paths, ownership ownershipState, oldSource, newSource string) ([]installedRename, error) {
	var found []installedRename
	for _, home := range paths.KnownSkillsHomes() {
		oldLink := filepath.Join(home, filepath.Base(oldSource))
		target, tracked := ownership.target(oldLink)
		if !tracked || !samePath(target, oldSource) || !ownershipProves(ownership, oldLink) {
			continue
		}
		snapshot, err := snapshotSymlink(oldLink)
		if err != nil {
			return nil, fmt.Errorf("snapshot owned link %s: %w", oldLink, err)
		}
		found = append(found, installedRename{
			home: home, oldLink: oldLink, newLink: filepath.Join(home, filepath.Base(newSource)),
			newSource: newSource, snapshot: snapshot,
		})
	}
	return found, nil
}

func applyOwnershipRenames(ownership *ownershipState, renames []installedRename) {
	for _, rename := range renames {
		ownership.remove(rename.oldLink)
		ownership.set(rename.newLink, rename.newSource)
	}
}

func planInstalledRenames(renames []installedRename) (*symlinkPlan, error) {
	plan := &symlinkPlan{}
	for _, rename := range renames {
		if rename.oldLink == rename.newLink {
			if err := plan.Replace(rename.oldLink, rename.snapshot, rename.newSource, true); err != nil {
				return nil, fmt.Errorf("plan installed link repair: %w", err)
			}
		} else {
			if err := plan.Move(rename.oldLink, rename.newLink, rename.snapshot, rename.newSource, true); err != nil {
				return nil, fmt.Errorf("plan installed link rename: %w", err)
			}
		}
	}
	return plan, nil
}

func installedRenameChanges(renames []installedRename, home string) []RefreshChange {
	changes := make([]RefreshChange, 0, len(renames))
	for _, rename := range renames {
		changes = append(changes, RefreshChange{
			Action: "repaired", Name: filepath.Base(rename.newLink),
			Message: "installed link in " + shortPath(rename.home, home),
		})
	}
	return changes
}

func cloneBindings(bindings []BoundRepository) []BoundRepository {
	cloned := make([]BoundRepository, len(bindings))
	for index, binding := range bindings {
		cloned[index] = binding
		cloned[index].Skills = append([]string(nil), binding.Skills...)
	}
	return cloned
}

func equalBindings(left, right []BoundRepository) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Path != right[index].Path || left[index].Identity != right[index].Identity || left[index].Revision != right[index].Revision || !equalStrings(left[index].Skills, right[index].Skills) {
			return false
		}
	}
	return true
}

func repositoryRevision(repository string) string {
	revision, _ := gitOutput(repository, "rev-parse", "HEAD")
	return revision
}

func (c *CLI) confirmIndexRefresh() (bool, error) {
	input, output, ok := terminalFiles(c.In, c.Out)
	if !ok {
		return false, nil
	}
	theme := NewTheme(output)
	fmt.Fprint(output, theme.Tag("spark", Violet)+theme.Paint("refresh repositories now?", Violet)+theme.Paint(" [y/N] ", Grey))
	scanner := bufio.NewScanner(input)
	if !scanner.Scan() {
		return false, scanner.Err()
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes", nil
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

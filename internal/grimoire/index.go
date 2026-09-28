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
	Hooks    []string `json:"hooks,omitempty"`
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
	return pullLatest(repository, nil)
}

type preMergeCheck func(before, target string) error

func pullLatest(repository string, preMerge preMergeCheck) RefreshResult {
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
	target, err := gitOutput(repository, "rev-parse", "@{u}^{commit}")
	if err != nil {
		return RefreshResult{Repo: repository, Status: RefreshFailed, Message: "cannot read upstream after fetch; left alone", before: before}
	}
	if preMerge != nil {
		if err := preMerge(before, target); err != nil {
			return RefreshResult{Repo: repository, Status: RefreshFailed, Message: "unsafe hook update; left alone: " + err.Error(), before: before}
		}
	}
	if _, err := gitOutput(repository, "merge", "--ff-only", target); err != nil {
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

	result := pullLatest(repository.Path, func(before, target string) error {
		return preflightHookUpdate(repository, before, target)
	})
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

func preflightHookUpdate(repository BoundRepository, before, target string) error {
	if len(repository.Hooks) == 0 {
		return nil
	}
	from := repository.Revision
	if from == "" {
		from = before
	}
	if _, err := gitOutput(repository.Path, "merge-base", "--is-ancestor", from+"^{commit}", target); err != nil {
		from = before
	}
	renamed := skillRenames(repository.Path, from, target)
	for _, selected := range repository.Hooks {
		rel, err := validateHookRelative(selected)
		if err != nil {
			return err
		}
		if replacement, ok := renamed["hook:"+rel]; ok {
			rel = replacement
		}
		if err := validateHookGitTree(repository.Path, target, rel); err != nil {
			return err
		}
	}
	return nil
}

func validateHookGitTree(repository, revision, rel string) error {
	clean, err := validateHookRelative(rel)
	if err != nil {
		return err
	}
	parts := strings.Split(filepath.ToSlash(clean), "/")
	for index := range parts {
		component := strings.Join(parts[:index+1], "/")
		mode, objectType, exists, err := gitTreeEntry(repository, revision, component)
		if err != nil {
			return fmt.Errorf("inspect upstream hook path %s: %w", component, err)
		}
		if !exists {
			return nil
		}
		if mode != "040000" || objectType != "tree" {
			return fmt.Errorf("upstream hook path %s is not a directory tree", component)
		}
	}
	marker := filepath.ToSlash(filepath.Join(clean, HookKind.Marker()))
	mode, objectType, exists, err := gitTreeEntry(repository, revision, marker)
	if err != nil {
		return fmt.Errorf("inspect upstream hook marker %s: %w", marker, err)
	}
	if !exists {
		return nil
	}
	if objectType != "blob" || (mode != "100644" && mode != "100755") {
		return fmt.Errorf("upstream hook %s must contain a regular %s", clean, HookKind.Marker())
	}
	return nil
}

func gitTreeEntry(repository, revision, path string) (string, string, bool, error) {
	command := exec.Command("git", "-C", repository, "ls-tree", "-z", revision, "--", ":(literal)"+filepath.ToSlash(path))
	body, err := command.Output()
	if err != nil {
		return "", "", false, err
	}
	if len(body) == 0 {
		return "", "", false, nil
	}
	record := strings.TrimSuffix(string(body), "\x00")
	if strings.Contains(record, "\x00") {
		return "", "", false, fmt.Errorf("path matched multiple tree entries")
	}
	header, _, found := strings.Cut(record, "\t")
	fields := strings.Fields(header)
	if !found || len(fields) != 3 {
		return "", "", false, fmt.Errorf("unexpected git ls-tree output")
	}
	return fields[0], fields[1], true, nil
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
		if samePath(path, repository.Path) {
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
	for _, resource := range repositoryResources(repository) {
		rel, kind := resource.path, resource.kind
		source := filepath.Join(repository.Path, rel)
		exists, markerErr := resourceFileExists(source, kind)
		if markerErr != nil {
			return nil, fmt.Errorf("inspect %s %s: %w", kind.Name(), source, markerErr)
		}
		if !exists {
			continue
		}
		if kind == HookKind {
			if err := validateHookSelection(repository.Path, rel, true); err != nil {
				return nil, err
			}
		}
		for _, home := range paths.KnownResourceHomes(kind) {
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
			name := filepath.Base(rel)
			if kind == HookKind {
				name = "hook:" + name
			}
			changes = append(changes, RefreshChange{
				Action: "recorded", Name: name,
				Message: "ownership of existing installed link in " + shortPath(home, paths.Home),
			})
		}
	}
	if len(changes) == 0 {
		return nil, nil
	}
	for _, record := range adopted {
		actual, err := linkTarget(record.Path)
		if err != nil || !samePath(actual, record.Target) {
			return nil, fmt.Errorf("installed link %s changed before ownership could be saved", record.Path)
		}
	}
	if _, err := commitOwnershipUpdate(paths, ownership, updated, nil); err != nil {
		return nil, err
	}
	return changes, nil
}

func repositoryIssues(paths Paths, repository BoundRepository) (int, int, error) {
	missing, broken := 0, 0
	for _, resource := range repositoryResources(repository) {
		rel, kind := resource.path, resource.kind
		source := filepath.Join(repository.Path, rel)
		exists, markerErr := resourceFileExists(source, kind)
		if markerErr != nil {
			return missing, broken, markerErr
		}
		if !exists {
			missing++
			continue
		}
		if kind == HookKind {
			if err := validateHookSelection(repository.Path, rel, true); err != nil {
				return missing, broken, err
			}
		}
		link := filepath.Join(paths.CatalogRoot(kind), filepath.Base(rel))
		target, err := os.Readlink(link)
		if err != nil || !samePath(resolveLink(link, target), source) {
			broken++
		}
	}
	return missing, broken, nil
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
		if samePath(current[index].Path, repository.Path) {
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
	for _, resource := range repositoryResources(repository) {
		rel, kind := resource.path, resource.kind
		if kind == HookKind {
			if err := validateHookSelection(newPath, rel, true); err != nil {
				return nil, 0, err
			}
		}
		renamed, err := installedResourceRenames(paths, ownership, filepath.Join(repository.Path, rel), filepath.Join(newPath, rel), kind)
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
	for _, selection := range []struct {
		kind  ResourceKind
		paths *[]string
	}{{SkillKind, &updated[found].Skills}, {HookKind, &updated[found].Hooks}} {
		for index, oldRel := range *selection.paths {
			key := filepath.Clean(oldRel)
			if selection.kind == HookKind {
				if _, err := validateHookRelative(oldRel); err != nil {
					return nil, 0, err
				}
				key = "hook:" + key
			}
			newRel, renamed := renames[key]
			if !renamed {
				continue
			}
			if selection.kind == HookKind {
				if _, err := validateHookRelative(newRel); err != nil {
					return nil, 0, err
				}
				if err := validateHookSelection(repository, oldRel, false); err != nil {
					return nil, 0, err
				}
				if err := validateHookSelection(repository, newRel, true); err != nil {
					return nil, 0, err
				}
			}
			oldExists, markerErr := resourceFileExists(filepath.Join(repository, oldRel), selection.kind)
			if markerErr != nil {
				return nil, 0, markerErr
			}
			newExists, markerErr := resourceFileExists(filepath.Join(repository, newRel), selection.kind)
			if markerErr != nil {
				return nil, 0, markerErr
			}
			if oldExists || !newExists {
				continue
			}
			oldSource, newSource := filepath.Join(repository, oldRel), filepath.Join(repository, newRel)
			renamedInstalled, err := installedResourceRenames(paths, ownership, oldSource, newSource, selection.kind)
			if err != nil {
				return nil, 0, err
			}
			installed = append(installed, renamedInstalled...)
			(*selection.paths)[index] = newRel
			oldName, newName := filepath.Base(oldRel), filepath.Base(newRel)
			if oldName == newName {
				changes = append(changes, RefreshChange{Action: "moved", Name: oldName, Message: fmt.Sprintf("%s from %s to %s", selection.kind.Name(), oldRel, newRel)})
			} else {
				message := "to " + newName
				if selection.kind == HookKind {
					message = "hook to " + newName
				}
				changes = append(changes, RefreshChange{Action: "renamed", Name: oldName, Message: message})
			}
		}
	}
	applyOwnershipRenames(&updatedOwnership, installed)
	sort.Strings(updated[found].Skills)
	sort.Strings(updated[found].Hooks)
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
		marker := filepath.Base(oldName)
		if marker != filepath.Base(newName) || (marker != SkillKind.Marker() && marker != HookKind.Marker()) {
			continue
		}
		if !sameSkillDefinition(repository, before, after, oldName, newName) {
			continue
		}
		oldRel := filepath.Clean(filepath.FromSlash(filepath.Dir(oldName)))
		newRel := filepath.Clean(filepath.FromSlash(filepath.Dir(newName)))
		valid := safeRelative(oldRel) && safeRelative(newRel)
		if marker == HookKind.Marker() {
			_, oldErr := validateHookRelative(oldRel)
			_, newErr := validateHookRelative(newRel)
			valid = oldErr == nil && newErr == nil
		}
		if valid {
			key := oldRel
			if marker == HookKind.Marker() {
				key = "hook:" + oldRel
			}
			found[key] = newRel
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

func resourceFileExists(dir string, kind ResourceKind) (bool, error) {
	return resourceMarker(filepath.Join(dir, kind.Marker()), kind)
}

type repositoryResource struct {
	kind ResourceKind
	path string
}

func repositoryResources(repository BoundRepository) []repositoryResource {
	found := make([]repositoryResource, 0, len(repository.Skills)+len(repository.Hooks))
	for _, rel := range repository.Skills {
		found = append(found, repositoryResource{SkillKind, rel})
	}
	for _, rel := range repository.Hooks {
		found = append(found, repositoryResource{HookKind, rel})
	}
	return found
}

type installedRename struct {
	home      string
	oldLink   string
	newLink   string
	newSource string
	snapshot  symlinkSnapshot
}

func installedResourceRenames(paths Paths, ownership ownershipState, oldSource, newSource string, kind ResourceKind) ([]installedRename, error) {
	var found []installedRename
	for _, home := range paths.KnownResourceHomes(kind) {
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
		cloned[index].Hooks = append([]string(nil), binding.Hooks...)
	}
	return cloned
}

func equalBindings(left, right []BoundRepository) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !samePath(left[index].Path, right[index].Path) || left[index].Identity != right[index].Identity || left[index].Revision != right[index].Revision || !equalStrings(left[index].Skills, right[index].Skills) || !equalStrings(left[index].Hooks, right[index].Hooks) {
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
	c.writeResponsive(output, "", Grey, "refresh repositories now? [y/N]", Violet)
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

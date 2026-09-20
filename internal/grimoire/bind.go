package grimoire

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type BindStatus string

const (
	Bound    BindStatus = "bound"
	Rebound  BindStatus = "rebound"
	Already  BindStatus = "already"
	Unbound  BindStatus = "unbound"
	NotBound BindStatus = "not-bound"
	Blocked  BindStatus = "blocked"
)

type BindResult struct {
	Status  BindStatus
	Path    string
	Target  string
	Message string
	Changes []PathChange
}

// BindingOptions controls changes that need explicit caller approval.
type BindingOptions struct {
	ReplaceLegacyCatalogRoot bool
}

func (r BindResult) OK() bool { return r.Status != Blocked }

// DiscoverRepositorySkills finds every skill in the Git repository containing
// cwd. A skill is any directory below the repository root that contains a
// SKILL.md file.
func DiscoverRepositorySkills(cwd string, homes []string) (string, []Skill, error) {
	root, err := gitRoot(cwd)
	if err != nil {
		return "", nil, err
	}
	seen := map[string]bool{}
	var skills []Skill
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.IsDir() || entry.Name() != "SKILL.md" {
			return nil
		}
		dir := filepath.Dir(path)
		if !samePath(dir, root) && !seen[dir] {
			seen[dir] = true
			skills = append(skills, ReadSkill(dir, root, homes))
		}
		return nil
	})
	if err != nil {
		return "", nil, fmt.Errorf("scan Git repository: %w", err)
	}
	sort.SliceStable(skills, func(i, j int) bool {
		return skills[i].RepoPath() < skills[j].RepoPath()
	})
	return root, skills, nil
}

func gitRoot(cwd string) (string, error) {
	root, err := absolute(cwd)
	if err != nil {
		return "", err
	}
	command := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel")
	body, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("bind must be run inside a Git repository")
	}
	found := strings.TrimSpace(string(body))
	if found == "" {
		return "", fmt.Errorf("bind must be run inside a Git repository")
	}
	return absolute(found)
}

// BindLibrary is the non-interactive API: it discovers and binds every skill
// in the repository containing cwd. The CLI calls BindSkills after its picker
// has narrowed this list.
func BindLibrary(paths Paths, cwd string) BindResult {
	return BindLibraryWithOptions(paths, cwd, BindingOptions{})
}

// BindLibraryWithOptions binds all repository skills with caller-supplied options.
func BindLibraryWithOptions(paths Paths, cwd string, options BindingOptions) BindResult {
	root, skills, err := DiscoverRepositorySkills(cwd, paths.SkillsHomes())
	if err != nil {
		return bindFailure(paths.Binding(), cwd, err.Error())
	}
	if len(skills) == 0 {
		return bindFailure(paths.Binding(), root, "no */SKILL.md folders found in this Git repository")
	}
	return bindSkills(paths, root, skills, options)
}

// BindSkills replaces this repository's previous selection and connects the
// chosen source directories below GRIMOIRE_HOME/skills.
func BindSkills(paths Paths, repository string, chosen []Skill) BindResult {
	return BindSkillsWithOptions(paths, repository, chosen, BindingOptions{})
}

// BindSkillsWithOptions binds selected skills with caller-supplied options.
func BindSkillsWithOptions(paths Paths, repository string, chosen []Skill, options BindingOptions) BindResult {
	return bindSkills(paths, repository, chosen, options)
}

func bindSkills(paths Paths, repository string, chosen []Skill, options BindingOptions) BindResult {
	root, err := absolute(repository)
	if err != nil {
		return bindFailure(paths.Binding(), repository, err.Error())
	}
	selected, err := selectedPaths(root, chosen)
	if err != nil {
		return bindFailure(paths.Binding(), root, err.Error())
	}
	if len(selected) == 0 {
		return bindFailure(paths.Binding(), root, "no skills selected")
	}
	unlock, err := lockBindings(paths)
	if err != nil {
		return bindFailure(paths.Binding(), root, err.Error())
	}
	defer unlock()
	if err := recoverBindingMutation(paths); err != nil {
		return bindFailure(paths.Binding(), root, err.Error())
	}

	legacyRoot := legacyBindingRoot(paths)
	current, err := configuredBindings(paths)
	if err != nil {
		return bindFailure(paths.Binding(), root, err.Error())
	}
	updated := append([]BoundRepository(nil), current...)
	status := Bound
	found := -1
	for index, binding := range updated {
		if samePath(binding.Path, root) {
			found = index
			break
		}
	}
	entry := BoundRepository{
		Path: root, Skills: selected, Identity: repositoryIdentity(root), Revision: repositoryRevision(root),
	}
	metadataOnly := false
	previous := []string{}
	if found >= 0 {
		previous = append(previous, updated[found].Skills...)
		if equalStrings(updated[found].Skills, selected) {
			if updated[found].Identity == entry.Identity && updated[found].Revision == entry.Revision {
				return BindResult{Status: Already, Path: paths.Binding(), Target: root, Message: fmt.Sprintf("%d skill%s already bound", len(selected), plural(len(selected)))}
			}
			metadataOnly = true
		}
		updated[found] = entry
		status = Rebound
	} else {
		updated = append(updated, entry)
	}

	if err := updateBindings(paths, current, updated, legacyRoot, options.ReplaceLegacyCatalogRoot); err != nil {
		return bindFailure(paths.Binding(), root, err.Error())
	}
	word := "bound"
	if metadataOnly {
		word = "binding refreshed"
	} else if status == Rebound {
		word = "selection updated"
	}
	changes := bindingSelectionChanges(paths, root, previous, selected)
	changes = append(changes, PathChange{Action: "updated binding", Path: paths.BindingsFile()})
	return BindResult{Status: status, Path: paths.Binding(), Target: root, Message: fmt.Sprintf("%d skill%s %s", len(selected), plural(len(selected)), word), Changes: changes}
}

func bindingSelectionChanges(paths Paths, root string, before, after []string) []PathChange {
	old := map[string]bool{}
	for _, rel := range before {
		old[rel] = true
	}
	wanted := map[string]bool{}
	changes := []PathChange{}
	for _, rel := range after {
		wanted[rel] = true
		if !old[rel] {
			changes = append(changes, PathChange{Action: "created catalog link", Path: filepath.Join(paths.Binding(), filepath.Base(rel)), Target: filepath.Join(root, rel)})
		}
	}
	for _, rel := range before {
		if !wanted[rel] {
			changes = append(changes, PathChange{Action: "removed catalog link", Path: filepath.Join(paths.Binding(), filepath.Base(rel)), Target: filepath.Join(root, rel)})
		}
	}
	return changes
}

func selectedPaths(root string, chosen []Skill) ([]string, error) {
	selected := make([]string, 0, len(chosen))
	seen := map[string]bool{}
	for _, skill := range chosen {
		dir, err := absolute(skill.Dir)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("skill %s is outside repository %s", dir, root)
		}
		info, err := os.Stat(filepath.Join(dir, "SKILL.md"))
		if err != nil || info.IsDir() {
			return nil, fmt.Errorf("%s does not contain SKILL.md", dir)
		}
		rel = filepath.Clean(rel)
		if !seen[rel] {
			seen[rel] = true
			selected = append(selected, rel)
		}
	}
	sort.Strings(selected)
	return selected, nil
}

// UnbindLibrary disconnects the repository identified by cwd. It never
// removes source skills or links installed into agent homes.
func UnbindLibrary(paths Paths, cwd string) BindResult {
	return UnbindLibraryWithOptions(paths, cwd, BindingOptions{})
}

// UnbindLibraryWithOptions unbinds a repository with caller-supplied options.
func UnbindLibraryWithOptions(paths Paths, cwd string, options BindingOptions) BindResult {
	root, err := absolute(cwd)
	if err != nil {
		return bindFailure(paths.Binding(), cwd, err.Error())
	}
	if discovered, gitErr := gitRoot(root); gitErr == nil {
		root = discovered
	}
	unlock, err := lockBindings(paths)
	if err != nil {
		return bindFailure(paths.Binding(), root, err.Error())
	}
	defer unlock()
	if err := recoverBindingMutation(paths); err != nil {
		return bindFailure(paths.Binding(), root, err.Error())
	}
	legacyRoot := legacyBindingRoot(paths)
	current, err := configuredBindings(paths)
	if err != nil {
		return bindFailure(paths.Binding(), root, err.Error())
	}
	index := -1
	for at, binding := range current {
		if samePath(binding.Path, root) {
			index = at
			break
		}
	}
	if index < 0 {
		return BindResult{Status: NotBound, Path: paths.Binding(), Target: root, Message: "not bound"}
	}
	updated := append([]BoundRepository(nil), current[:index]...)
	updated = append(updated, current[index+1:]...)
	if err := updateBindings(paths, current, updated, legacyRoot, options.ReplaceLegacyCatalogRoot); err != nil {
		return bindFailure(paths.Binding(), root, err.Error())
	}
	changes := bindingSelectionChanges(paths, root, current[index].Skills, nil)
	changes = append(changes, PathChange{Action: "updated binding", Path: paths.BindingsFile()})
	return BindResult{Status: Unbound, Path: paths.Binding(), Target: root, Message: "unbound", Changes: changes}
}

// BoundSkills returns the manifest selection without requiring source files to
// exist. This lets unbind remove stale entries after a source folder moves.
func BoundSkills(paths Paths) ([]Skill, error) {
	bindings, err := configuredBindings(paths)
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, binding := range bindings {
		for _, rel := range binding.Skills {
			skill := ReadSkill(filepath.Join(binding.Path, rel), binding.Path, paths.SkillsHomes())
			if len(bindings) > 1 {
				skill.repositoryLabel = binding.Path
			}
			skills = append(skills, skill)
		}
	}
	return skills, nil
}

// UnbindSkills removes selected skills from the manifest and catalog links. It
// does not remove source folders or links that were installed into an agent.
func UnbindSkills(paths Paths, chosen []Skill) BindResult {
	return UnbindSkillsWithOptions(paths, chosen, BindingOptions{})
}

// UnbindSkillsWithOptions unbinds selected skills with caller-supplied options.
func UnbindSkillsWithOptions(paths Paths, chosen []Skill, options BindingOptions) BindResult {
	if len(chosen) == 0 {
		return bindFailure(paths.Binding(), "", "no skills selected")
	}
	unlock, err := lockBindings(paths)
	if err != nil {
		return bindFailure(paths.Binding(), "", err.Error())
	}
	defer unlock()
	if err := recoverBindingMutation(paths); err != nil {
		return bindFailure(paths.Binding(), "", err.Error())
	}
	legacyRoot := legacyBindingRoot(paths)
	current, err := configuredBindings(paths)
	if err != nil {
		return bindFailure(paths.Binding(), "", err.Error())
	}

	wanted := map[string]bool{}
	for _, skill := range chosen {
		wanted[filepath.Clean(skill.Dir)] = true
	}
	known := map[string]bool{}
	for _, binding := range current {
		for _, rel := range binding.Skills {
			known[filepath.Clean(filepath.Join(binding.Path, rel))] = true
		}
	}
	for dir := range wanted {
		if !known[dir] {
			return bindFailure(paths.Binding(), dir, fmt.Sprintf("%s is not bound", filepath.Base(dir)))
		}
	}

	updated := make([]BoundRepository, 0, len(current))
	removed := 0
	for _, binding := range current {
		kept := make([]string, 0, len(binding.Skills))
		for _, rel := range binding.Skills {
			if wanted[filepath.Clean(filepath.Join(binding.Path, rel))] {
				removed++
				continue
			}
			kept = append(kept, rel)
		}
		if len(kept) > 0 {
			binding.Skills = kept
			updated = append(updated, binding)
		}
	}
	if err := updateBindings(paths, current, updated, legacyRoot, options.ReplaceLegacyCatalogRoot); err != nil {
		return bindFailure(paths.Binding(), "", err.Error())
	}
	changes := []PathChange{}
	for dir := range wanted {
		changes = append(changes, PathChange{Action: "removed catalog link", Path: filepath.Join(paths.Binding(), filepath.Base(dir)), Target: dir})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	changes = append(changes, PathChange{Action: "updated binding", Path: paths.BindingsFile()})
	return BindResult{Status: Unbound, Path: paths.Binding(), Message: fmt.Sprintf("%d skill%s unbound", removed, plural(removed)), Changes: changes}
}

func configuredBindings(paths Paths) ([]BoundRepository, error) {
	journal, exists, err := readBindingMutationJournal(paths)
	if err != nil {
		return nil, err
	}
	if exists {
		return journal.After, nil
	}
	return readConfiguredBindings(paths)
}

func readConfiguredBindings(paths Paths) ([]BoundRepository, error) {
	body, err := os.ReadFile(paths.BindingsFile())
	if err == nil {
		var raw []struct {
			Path     string          `json:"path"`
			Skills   json.RawMessage `json:"skills"`
			Identity string          `json:"identity"`
			Revision string          `json:"revision"`
		}
		if decodeErr := json.Unmarshal(body, &raw); decodeErr == nil {
			bindings := make([]BoundRepository, 0, len(raw))
			for _, record := range raw {
				binding, err := decodeBinding(record.Path, record.Skills, record.Identity, record.Revision)
				if err != nil {
					return nil, fmt.Errorf("read repository bindings: %w", err)
				}
				bindings = append(bindings, binding)
			}
			return bindings, nil
		}

		// A short-lived multi-library format stored a JSON string array.
		var legacy []string
		if legacyErr := json.Unmarshal(body, &legacy); legacyErr != nil {
			return nil, fmt.Errorf("read repository bindings: %w", legacyErr)
		}
		bindings := make([]BoundRepository, 0, len(legacy))
		for _, root := range legacy {
			binding, err := legacyBinding(root, filepath.Join(root, "skills"))
			if err != nil {
				return nil, err
			}
			bindings = append(bindings, binding)
		}
		return bindings, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read repository bindings: %w", err)
	}

	// Migrate the original single-library symlink on the next write.
	rawTarget, err := os.Readlink(paths.Binding())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("a real file or directory already holds %s", paths.Binding())
	}
	target := resolveLink(paths.Binding(), rawTarget)
	root := target
	if filepath.Base(target) == "skills" {
		root = filepath.Dir(target)
	}
	return oneLegacyBinding(root, target)
}

func decodeBinding(path string, raw json.RawMessage, identity, revision string) (BoundRepository, error) {
	if path == "" {
		return BoundRepository{}, fmt.Errorf("a binding is missing its repository path")
	}
	var selected []string
	if err := json.Unmarshal(raw, &selected); err == nil {
		for _, rel := range selected {
			if !safeRelative(rel) {
				return BoundRepository{}, fmt.Errorf("unsafe skill path %q", rel)
			}
		}
		sort.Strings(selected)
		return BoundRepository{Path: filepath.Clean(path), Skills: selected, Identity: identity, Revision: revision}, nil
	}
	var oldRoot string
	if err := json.Unmarshal(raw, &oldRoot); err != nil {
		return BoundRepository{}, fmt.Errorf("invalid skills for %s", path)
	}
	return legacyBinding(path, oldRoot)
}

func oneLegacyBinding(root, skillsRoot string) ([]BoundRepository, error) {
	binding, err := legacyBinding(root, skillsRoot)
	if err != nil {
		return nil, err
	}
	return []BoundRepository{binding}, nil
}

func legacyBinding(root, skillsRoot string) (BoundRepository, error) {
	root, err := absolute(root)
	if err != nil {
		return BoundRepository{}, err
	}
	skillsRoot, err = absolute(skillsRoot)
	if err != nil {
		return BoundRepository{}, err
	}
	dirs, err := findSkillDirs(skillsRoot)
	if err != nil {
		return BoundRepository{}, err
	}
	selected := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		rel, err := filepath.Rel(root, dir)
		if err != nil || !safeRelative(rel) {
			return BoundRepository{}, fmt.Errorf("legacy skill %s is outside %s", dir, root)
		}
		selected = append(selected, filepath.Clean(rel))
	}
	sort.Strings(selected)
	return BoundRepository{Path: root, Skills: selected}, nil
}

func findSkillDirs(root string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if !entry.IsDir() && entry.Name() == "SKILL.md" && !samePath(filepath.Dir(path), root) {
			found = append(found, filepath.Dir(path))
		}
		return nil
	})
	return found, err
}

func safeRelative(path string) bool {
	clean := filepath.Clean(path)
	return path != "" && !filepath.IsAbs(path) && clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func bindingSkillMap(bindings []BoundRepository) (map[string]string, error) {
	found := map[string]string{}
	for _, binding := range bindings {
		for _, rel := range binding.Skills {
			dir := filepath.Join(binding.Path, rel)
			name := filepath.Base(dir)
			if previous, exists := found[name]; exists && !samePath(previous, dir) {
				return nil, fmt.Errorf("%s is selected from both %s and %s", name, previous, dir)
			}
			found[name] = dir
		}
	}
	return found, nil
}

func legacyBindingRoot(paths Paths) bool {
	if _, err := os.Lstat(paths.BindingsFile()); err == nil || !os.IsNotExist(err) {
		return false
	}
	info, err := os.Lstat(paths.Binding())
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

const bindingMutationVersion = 1

type bindingMutationJournal struct {
	Version            int                     `json:"version"`
	Before             []BoundRepository       `json:"before"`
	After              []BoundRepository       `json:"after"`
	BeforeExists       bool                    `json:"before_exists"`
	LegacyRoot         bool                    `json:"legacy_root,omitempty"`
	ReplaceLegacyRoot  bool                    `json:"replace_legacy_root,omitempty"`
	LegacyRootSnapshot *journalSymlinkSnapshot `json:"legacy_root_snapshot,omitempty"`
	Links              []journalSymlinkAction  `json:"links,omitempty"`
	Ownership          *ownershipState         `json:"ownership,omitempty"`
}

type bindingCatalogPlan struct {
	root               string
	replaceRoot        bool
	rootPlan           symlinkPlan
	links              symlinkPlan
	directories        []string
	createdDirectories []string
}

type bindingMutation struct {
	paths            Paths
	journal          bindingMutationJournal
	catalog          *bindingCatalogPlan
	links            *symlinkPlan
	beforeOwnership  *ownershipState
	afterOwnership   *ownershipState
	bindingsWritten  bool
	ownershipWritten bool
	journalWritten   bool
}

func updateBindings(paths Paths, before, after []BoundRepository, legacyRoot, replaceLegacyRoot bool) error {
	mutation, err := prepareBindingMutation(paths, before, after, legacyRoot, replaceLegacyRoot)
	if err != nil {
		return err
	}
	return mutation.Commit()
}

func prepareBindingMutation(paths Paths, before, after []BoundRepository, legacyRoot, replaceLegacyRoot bool) (*bindingMutation, error) {
	catalog, err := planBindingCatalog(paths, before, after, legacyRoot, replaceLegacyRoot, nil)
	if err != nil {
		return nil, err
	}
	_, statErr := os.Lstat(paths.BindingsFile())
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("inspect bindings: %w", statErr)
	}
	journal := bindingMutationJournal{
		Version: bindingMutationVersion, Before: before, After: after,
		BeforeExists: statErr == nil, LegacyRoot: legacyRoot, ReplaceLegacyRoot: replaceLegacyRoot,
	}
	if catalog.replaceRoot {
		beforeRoot := catalog.rootPlan.actions[0].before
		journal.LegacyRootSnapshot = &journalSymlinkSnapshot{Target: beforeRoot.target, Directory: beforeRoot.directory}
	}
	return &bindingMutation{
		paths:   paths,
		journal: journal,
		catalog: catalog,
	}, nil
}

func (mutation *bindingMutation) SetLinkAndOwnershipUpdate(plan *symlinkPlan, before, after ownershipState) {
	mutation.links = plan
	mutation.beforeOwnership = &before
	mutation.afterOwnership = &after
	mutation.journal.Links = plan.journalActions()
	wanted := after.clone()
	mutation.journal.Ownership = &wanted
}

func (mutation *bindingMutation) CatalogChangeCount() int {
	return mutation.catalog.links.Count()
}

func (mutation *bindingMutation) Commit() error {
	if err := writeBindingMutationJournal(mutation.paths, mutation.journal); err != nil {
		return fmt.Errorf("save binding mutation journal: %w", err)
	}
	mutation.journalWritten = true
	if mutation.links != nil {
		if err := mutation.links.Apply(); err != nil {
			return errors.Join(fmt.Errorf("update installed links: %w", err), mutation.rollback())
		}
	}
	if err := mutation.catalog.Apply(); err != nil {
		return errors.Join(fmt.Errorf("update catalog links: %w", err), mutation.rollback())
	}
	if err := mutation.catalog.Verify(); err != nil {
		return errors.Join(fmt.Errorf("verify catalog links: %w", err), mutation.rollback())
	}
	if err := verifyBindingCatalog(mutation.paths, mutation.journal.After); err != nil {
		return errors.Join(fmt.Errorf("verify catalog: %w", err), mutation.rollback())
	}
	if err := writeBindings(mutation.paths, mutation.journal.After); err != nil {
		return errors.Join(fmt.Errorf("save bindings: %w", err), mutation.rollback())
	}
	mutation.bindingsWritten = true
	if mutation.afterOwnership != nil && !ownershipEqual(*mutation.beforeOwnership, *mutation.afterOwnership) {
		if err := mutation.links.Verify(); err != nil {
			return errors.Join(fmt.Errorf("verify installed links before saving ownership: %w", err), mutation.rollback())
		}
		if err := writeOwnership(mutation.paths, *mutation.afterOwnership); err != nil {
			return errors.Join(fmt.Errorf("save ownership: %w", err), mutation.rollback())
		}
		mutation.ownershipWritten = true
	}
	if err := removeIfExists(mutation.paths.BindingMutationFile()); err != nil {
		return fmt.Errorf("clear binding mutation journal: %w", err)
	}
	mutation.journalWritten = false
	return nil
}

func (mutation *bindingMutation) rollback() error {
	var failures []error
	if mutation.ownershipWritten {
		if err := restoreOwnership(mutation.paths, *mutation.beforeOwnership); err != nil {
			failures = append(failures, fmt.Errorf("rollback ownership: %w", err))
		} else {
			mutation.ownershipWritten = false
		}
	}
	if mutation.bindingsWritten {
		var err error
		if mutation.journal.BeforeExists {
			err = writeBindings(mutation.paths, mutation.journal.Before)
		} else {
			err = removeIfExists(mutation.paths.BindingsFile())
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("rollback bindings: %w", err))
		} else {
			mutation.bindingsWritten = false
		}
	}
	if err := mutation.catalog.Rollback(); err != nil {
		failures = append(failures, fmt.Errorf("rollback catalog links: %w", err))
	}
	if mutation.links != nil {
		if err := mutation.links.Rollback(); err != nil {
			failures = append(failures, fmt.Errorf("rollback installed links: %w", err))
		}
	}
	rollbackErr := errors.Join(failures...)
	if rollbackErr == nil && mutation.journalWritten {
		if err := removeIfExists(mutation.paths.BindingMutationFile()); err != nil {
			return fmt.Errorf("clear rolled-back binding mutation journal: %w", err)
		}
		mutation.journalWritten = false
	}
	return rollbackErr
}

func recoverBindingMutation(paths Paths) error {
	journal, exists, err := readBindingMutationJournal(paths)
	if err != nil || !exists {
		return err
	}
	links, err := recoverSymlinkPlan(journal.Links)
	if err != nil {
		return fmt.Errorf("recover installed links: %w", err)
	}
	var legacyRootSnapshot *symlinkSnapshot
	if journal.LegacyRootSnapshot != nil {
		legacyRootSnapshot = &symlinkSnapshot{
			target: journal.LegacyRootSnapshot.Target, directory: journal.LegacyRootSnapshot.Directory,
		}
	} else if journal.LegacyRoot && journal.ReplaceLegacyRoot {
		if info, statErr := os.Lstat(paths.Binding()); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("recover catalog links: journal has no approved legacy catalog-root snapshot")
		}
	}
	catalog, err := planBindingCatalog(paths, journal.Before, journal.After, journal.LegacyRoot, journal.ReplaceLegacyRoot, legacyRootSnapshot)
	if err != nil {
		return fmt.Errorf("recover catalog links: %w", err)
	}
	if err := links.Apply(); err != nil {
		return fmt.Errorf("recover installed links: %w", err)
	}
	if err := catalog.Apply(); err != nil {
		return fmt.Errorf("recover catalog links: %w", err)
	}
	if err := verifyJournalSymlinkActions(journal.Links); err != nil {
		return fmt.Errorf("verify recovered installed links: %w", err)
	}
	if err := catalog.Verify(); err != nil {
		return fmt.Errorf("verify recovered catalog links: %w", err)
	}
	if err := verifyBindingCatalog(paths, journal.After); err != nil {
		return fmt.Errorf("verify recovered catalog: %w", err)
	}
	if err := writeBindings(paths, journal.After); err != nil {
		return fmt.Errorf("recover bindings: %w", err)
	}
	if journal.Ownership != nil {
		if err := writeOwnership(paths, *journal.Ownership); err != nil {
			return fmt.Errorf("recover ownership: %w", err)
		}
	}
	if err := removeIfExists(paths.BindingMutationFile()); err != nil {
		return fmt.Errorf("finish binding mutation recovery: %w", err)
	}
	return nil
}

func readBindingMutationJournal(paths Paths) (bindingMutationJournal, bool, error) {
	body, err := os.ReadFile(paths.BindingMutationFile())
	if os.IsNotExist(err) {
		return bindingMutationJournal{}, false, nil
	}
	if err != nil {
		return bindingMutationJournal{}, false, fmt.Errorf("read binding mutation journal: %w", err)
	}
	var journal bindingMutationJournal
	if err := json.Unmarshal(body, &journal); err != nil {
		return bindingMutationJournal{}, false, fmt.Errorf("read binding mutation journal: %w", err)
	}
	if journal.Version != bindingMutationVersion {
		return bindingMutationJournal{}, false, fmt.Errorf("unsupported binding mutation journal version %d", journal.Version)
	}
	return journal, true, nil
}

func writeBindingMutationJournal(paths Paths, journal bindingMutationJournal) error {
	return writeJSONFile(paths.BindingMutationFile(), journal)
}

func planBindingCatalog(paths Paths, before, after []BoundRepository, legacyRoot, replaceLegacyRoot bool, expectedLegacyRoot *symlinkSnapshot) (*bindingCatalogPlan, error) {
	oldSkills, err := bindingSkillMap(before)
	if err != nil {
		return nil, err
	}
	newSkills, err := bindingSkillMap(after)
	if err != nil {
		return nil, err
	}

	root := paths.Binding()
	plan := &bindingCatalogPlan{root: root}
	info, statErr := os.Lstat(root)
	if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		if !legacyRoot {
			return nil, fmt.Errorf("a foreign symlink already holds %s", root)
		}
		if !replaceLegacyRoot {
			return nil, fmt.Errorf("replacing the legacy catalog-root symlink at %s requires explicit approval", root)
		}
		snapshot := expectedLegacyRoot
		if snapshot == nil {
			current, err := snapshotSymlink(root)
			if err != nil {
				return nil, fmt.Errorf("inspect catalog root: %w", err)
			}
			snapshot = &current
		}
		if err := plan.rootPlan.Remove(root, *snapshot); err != nil {
			return nil, fmt.Errorf("plan legacy catalog root: %w", err)
		}
		plan.replaceRoot = true
		for _, name := range sortedSkillNames(nil, newSkills) {
			if err := plan.links.createMissing(filepath.Join(root, name), newSkills[name], true); err != nil {
				return nil, fmt.Errorf("plan catalog link %s: %w", name, err)
			}
		}
		return plan, nil
	}
	if statErr == nil && !info.IsDir() {
		return nil, fmt.Errorf("a real file already holds %s", root)
	}
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if os.IsNotExist(statErr) {
		directories, err := missingDirectories(root)
		if err != nil {
			return nil, err
		}
		plan.directories = directories
		for _, name := range sortedSkillNames(nil, newSkills) {
			if err := plan.links.createMissing(filepath.Join(root, name), newSkills[name], true); err != nil {
				return nil, fmt.Errorf("plan catalog link %s: %w", name, err)
			}
		}
		return plan, nil
	}

	for _, name := range sortedSkillNames(oldSkills, newSkills) {
		oldSource, wasBound := oldSkills[name]
		newSource, remains := newSkills[name]
		link := filepath.Join(root, name)
		info, err := os.Lstat(link)
		if os.IsNotExist(err) {
			if remains {
				if err := plan.links.Create(link, newSource, true); err != nil {
					return nil, fmt.Errorf("plan catalog link %s: %w", name, err)
				}
			}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect skill name %s: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			if remains {
				return nil, fmt.Errorf("a real file or directory already holds skill name %s", name)
			}
			continue
		}
		snapshot, err := snapshotSymlink(link)
		if err != nil {
			return nil, fmt.Errorf("inspect skill name %s: %w", name, err)
		}
		current := resolveLink(link, snapshot.target)
		if remains {
			if samePath(current, newSource) {
				continue
			}
			if !wasBound || !samePath(current, oldSource) {
				return nil, fmt.Errorf("a foreign link already holds skill name %s", name)
			}
			if err := plan.links.Replace(link, snapshot, newSource, true); err != nil {
				return nil, fmt.Errorf("plan catalog link %s: %w", name, err)
			}
			continue
		}
		if wasBound && samePath(current, oldSource) {
			if err := plan.links.Remove(link, snapshot); err != nil {
				return nil, fmt.Errorf("plan catalog link %s: %w", name, err)
			}
		}
	}
	return plan, nil
}

func sortedSkillNames(first, second map[string]string) []string {
	names := make([]string, 0, len(first)+len(second))
	seen := map[string]bool{}
	for name := range first {
		seen[name] = true
		names = append(names, name)
	}
	for name := range second {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func missingDirectories(path string) ([]string, error) {
	var reversed []string
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("a real file already holds %s", current)
			}
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		reversed = append(reversed, current)
		parent := filepath.Dir(current)
		if parent == current {
			return nil, fmt.Errorf("no existing parent directory for %s", path)
		}
	}
	directories := make([]string, len(reversed))
	for index := range reversed {
		directories[len(reversed)-1-index] = reversed[index]
	}
	return directories, nil
}

func (plan *bindingCatalogPlan) Apply() error {
	if err := plan.rootPlan.Apply(); err != nil {
		return fmt.Errorf("replace legacy catalog root: %w", err)
	}
	if plan.replaceRoot {
		if err := os.Mkdir(plan.root, 0o755); err != nil {
			return errors.Join(fmt.Errorf("create catalog root: %w", err), plan.Rollback())
		}
		plan.createdDirectories = append(plan.createdDirectories, plan.root)
	}
	for _, directory := range plan.directories {
		if info, err := os.Stat(directory); err == nil && info.IsDir() {
			continue
		}
		if err := os.Mkdir(directory, 0o755); err != nil {
			return errors.Join(fmt.Errorf("create catalog directory %s: %w", directory, err), plan.Rollback())
		}
		plan.createdDirectories = append(plan.createdDirectories, directory)
	}
	if err := plan.links.Apply(); err != nil {
		return errors.Join(err, plan.Rollback())
	}
	return nil
}

func (plan *bindingCatalogPlan) Rollback() error {
	var failures []error
	if err := plan.links.Rollback(); err != nil {
		failures = append(failures, err)
	}
	for index := len(plan.createdDirectories) - 1; index >= 0; index-- {
		directory := plan.createdDirectories[index]
		if directory == "" {
			continue
		}
		err := os.Remove(directory)
		if err != nil && !os.IsNotExist(err) {
			failures = append(failures, fmt.Errorf("remove created catalog directory %s: %w", directory, err))
		} else {
			plan.createdDirectories[index] = ""
		}
	}
	if err := plan.rootPlan.Rollback(); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (plan *bindingCatalogPlan) Verify() error {
	info, err := os.Lstat(plan.root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("catalog root %s changed after validation", plan.root)
	}
	return plan.links.Verify()
}

func verifyBindingCatalog(paths Paths, bindings []BoundRepository) error {
	skills, err := bindingSkillMap(bindings)
	if err != nil {
		return err
	}
	for name, source := range skills {
		link := filepath.Join(paths.Binding(), name)
		actual, err := linkTarget(link)
		if err != nil || !samePath(actual, source) {
			return fmt.Errorf("catalog link %s changed before bindings could be saved", name)
		}
	}
	return nil
}

func syncSkillBindings(paths Paths, before, after []BoundRepository, replaceLegacyRoot bool) error {
	plan, err := planBindingCatalog(paths, before, after, replaceLegacyRoot, replaceLegacyRoot, nil)
	if err != nil {
		return err
	}
	return plan.Apply()
}

func writeBindings(paths Paths, libraries []BoundRepository) error {
	return writeJSONFile(paths.BindingsFile(), libraries)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func bindFailure(path, target, message string) BindResult {
	return BindResult{Status: Blocked, Path: path, Target: target, Message: message}
}

func resolveLink(link, target string) string {
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return filepath.Clean(target)
	}
	return filepath.Clean(abs)
}

func samePath(left, right string) bool {
	a, errA := filepath.EvalSymlinks(left)
	b, errB := filepath.EvalSymlinks(right)
	if errA == nil && errB == nil {
		return a == b
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

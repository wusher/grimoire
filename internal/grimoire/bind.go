package grimoire

import (
	"encoding/json"
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
}

// libraryBinding records an explicit selection of skill directories, relative
// to a Git repository root. Relative paths keep the file readable and prevent
// one repository record from claiming files in another repository.
type libraryBinding struct {
	Path   string   `json:"path"`
	Skills []string `json:"skills"`
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
	root, skills, err := DiscoverRepositorySkills(cwd, paths.SkillsHomes())
	if err != nil {
		return bindFailure(paths.Binding(), cwd, err.Error())
	}
	if len(skills) == 0 {
		return bindFailure(paths.Binding(), root, "no */SKILL.md folders found in this Git repository")
	}
	return BindSkills(paths, root, skills)
}

// BindSkills replaces this repository's previous selection and connects the
// chosen source directories below GRIMOIRE_HOME/skills.
func BindSkills(paths Paths, repository string, chosen []Skill) BindResult {
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

	current, err := configuredBindings(paths)
	if err != nil {
		return bindFailure(paths.Binding(), root, err.Error())
	}
	updated := append([]libraryBinding(nil), current...)
	status := Bound
	found := -1
	for index, binding := range updated {
		if samePath(binding.Path, root) {
			found = index
			break
		}
	}
	entry := libraryBinding{Path: root, Skills: selected}
	if found >= 0 {
		if equalStrings(updated[found].Skills, selected) {
			return BindResult{Status: Already, Path: paths.Binding(), Target: root, Message: fmt.Sprintf("%d skill%s already bound", len(selected), plural(len(selected)))}
		}
		updated[found] = entry
		status = Rebound
	} else {
		updated = append(updated, entry)
	}

	if err := writeBindings(paths, updated); err != nil {
		return bindFailure(paths.Binding(), root, fmt.Sprintf("cannot save bindings: %v", err))
	}
	if err := syncSkillBindings(paths, current, updated); err != nil {
		_ = writeBindings(paths, current)
		return bindFailure(paths.Binding(), root, err.Error())
	}
	word := "bound"
	if status == Rebound {
		word = "selection updated"
	}
	return BindResult{Status: status, Path: paths.Binding(), Target: root, Message: fmt.Sprintf("%d skill%s %s", len(selected), plural(len(selected)), word)}
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
	updated := append([]libraryBinding(nil), current[:index]...)
	updated = append(updated, current[index+1:]...)
	if err := writeBindings(paths, updated); err != nil {
		return bindFailure(paths.Binding(), root, fmt.Sprintf("cannot save bindings: %v", err))
	}
	if err := syncSkillBindings(paths, current, updated); err != nil {
		_ = writeBindings(paths, current)
		return bindFailure(paths.Binding(), root, err.Error())
	}
	return BindResult{Status: Unbound, Path: paths.Binding(), Target: root, Message: "unbound"}
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
	if len(chosen) == 0 {
		return bindFailure(paths.Binding(), "", "no skills selected")
	}
	unlock, err := lockBindings(paths)
	if err != nil {
		return bindFailure(paths.Binding(), "", err.Error())
	}
	defer unlock()
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

	updated := make([]libraryBinding, 0, len(current))
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
			updated = append(updated, libraryBinding{Path: binding.Path, Skills: kept})
		}
	}
	if err := writeBindings(paths, updated); err != nil {
		return bindFailure(paths.Binding(), "", fmt.Sprintf("cannot save bindings: %v", err))
	}
	if err := syncSkillBindings(paths, current, updated); err != nil {
		_ = writeBindings(paths, current)
		return bindFailure(paths.Binding(), "", err.Error())
	}
	return BindResult{Status: Unbound, Path: paths.Binding(), Message: fmt.Sprintf("%d skill%s unbound", removed, plural(removed))}
}

func configuredBindings(paths Paths) ([]libraryBinding, error) {
	body, err := os.ReadFile(paths.BindingsFile())
	if err == nil {
		var raw []struct {
			Path   string          `json:"path"`
			Skills json.RawMessage `json:"skills"`
		}
		if decodeErr := json.Unmarshal(body, &raw); decodeErr == nil {
			bindings := make([]libraryBinding, 0, len(raw))
			for _, record := range raw {
				binding, err := decodeBinding(record.Path, record.Skills)
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
		bindings := make([]libraryBinding, 0, len(legacy))
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

func decodeBinding(path string, raw json.RawMessage) (libraryBinding, error) {
	if path == "" {
		return libraryBinding{}, fmt.Errorf("a binding is missing its repository path")
	}
	var selected []string
	if err := json.Unmarshal(raw, &selected); err == nil {
		for _, rel := range selected {
			if !safeRelative(rel) {
				return libraryBinding{}, fmt.Errorf("unsafe skill path %q", rel)
			}
		}
		sort.Strings(selected)
		return libraryBinding{Path: filepath.Clean(path), Skills: selected}, nil
	}
	var oldRoot string
	if err := json.Unmarshal(raw, &oldRoot); err != nil {
		return libraryBinding{}, fmt.Errorf("invalid skills for %s", path)
	}
	return legacyBinding(path, oldRoot)
}

func oneLegacyBinding(root, skillsRoot string) ([]libraryBinding, error) {
	binding, err := legacyBinding(root, skillsRoot)
	if err != nil {
		return nil, err
	}
	return []libraryBinding{binding}, nil
}

func legacyBinding(root, skillsRoot string) (libraryBinding, error) {
	root, err := absolute(root)
	if err != nil {
		return libraryBinding{}, err
	}
	skillsRoot, err = absolute(skillsRoot)
	if err != nil {
		return libraryBinding{}, err
	}
	dirs, err := findSkillDirs(skillsRoot)
	if err != nil {
		return libraryBinding{}, err
	}
	selected := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		rel, err := filepath.Rel(root, dir)
		if err != nil || !safeRelative(rel) {
			return libraryBinding{}, fmt.Errorf("legacy skill %s is outside %s", dir, root)
		}
		selected = append(selected, filepath.Clean(rel))
	}
	sort.Strings(selected)
	return libraryBinding{Path: root, Skills: selected}, nil
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

func bindingSkillMap(bindings []libraryBinding) (map[string]string, error) {
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

func syncSkillBindings(paths Paths, before, after []libraryBinding) error {
	oldSkills, err := bindingSkillMap(before)
	if err != nil {
		return err
	}
	newSkills, err := bindingSkillMap(after)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
		return fmt.Errorf("create Grimoire home: %w", err)
	}

	root := paths.Binding()
	legacyTarget := ""
	if info, statErr := os.Lstat(root); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		raw, readErr := os.Readlink(root)
		if readErr != nil {
			return readErr
		}
		legacyTarget = resolveLink(root, raw)
		owned := len(oldSkills) > 0 && filepath.Base(legacyTarget) == "skills"
		for _, source := range oldSkills {
			owned = owned && (samePath(source, legacyTarget) || pathInside(source, legacyTarget))
		}
		if !owned {
			return fmt.Errorf("a foreign symlink already holds %s", root)
		}
		if err := os.Remove(root); err != nil {
			return err
		}
	} else if statErr == nil && !info.IsDir() {
		return fmt.Errorf("a real file already holds %s", root)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		if legacyTarget != "" {
			_ = os.Symlink(legacyTarget, root)
		}
		return err
	}

	// Validate every destination before changing any link.
	for name, source := range newSkills {
		link := filepath.Join(root, name)
		raw, readErr := os.Readlink(link)
		if readErr == nil {
			current := resolveLink(link, raw)
			if samePath(current, source) || (oldSkills[name] != "" && samePath(current, oldSkills[name])) {
				continue
			}
			return fmt.Errorf("a foreign link already holds skill name %s", name)
		}
		if !os.IsNotExist(readErr) {
			return fmt.Errorf("a real file or directory already holds skill name %s", name)
		}
	}

	changed := map[string]string{}
	created := []string{}
	rollback := func() {
		for _, link := range created {
			_ = os.Remove(link)
		}
		for link, target := range changed {
			_ = os.Remove(link)
			_ = os.Symlink(target, link)
		}
		if legacyTarget != "" {
			_ = os.Remove(root)
			_ = os.Symlink(legacyTarget, root)
		}
	}
	for name, oldSource := range oldSkills {
		newSource, remains := newSkills[name]
		if remains && samePath(oldSource, newSource) {
			continue
		}
		link := filepath.Join(root, name)
		raw, readErr := os.Readlink(link)
		if readErr == nil && samePath(resolveLink(link, raw), oldSource) {
			changed[link] = raw
			if err := os.Remove(link); err != nil {
				rollback()
				return err
			}
		}
	}
	for name, source := range newSkills {
		link := filepath.Join(root, name)
		if raw, readErr := os.Readlink(link); readErr == nil && samePath(resolveLink(link, raw), source) {
			continue
		}
		if err := os.Symlink(source, link); err != nil {
			rollback()
			return fmt.Errorf("connect %s: %w", name, err)
		}
		created = append(created, link)
	}
	return nil
}

func writeBindings(paths Paths, libraries []libraryBinding) error {
	if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(libraries, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	temporary, err := os.CreateTemp(paths.ConfigHome, ".bindings-*.json")
	if err != nil {
		return err
	}
	name := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(name)
	}
	if _, err := temporary.Write(body); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(name, paths.BindingsFile()); err != nil {
		cleanup()
		return err
	}
	return nil
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

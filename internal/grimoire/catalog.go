package grimoire

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Catalog struct {
	Root    string
	Roots   []string
	Homes   []string
	Skills  []Skill
	TooDeep []string
	Clashes map[string][]Skill
}

func LoadCatalog(paths Paths) (Catalog, error) {
	if paths.Repo != "" {
		roots, err := paths.SkillsRoots()
		if err != nil {
			return Catalog{}, err
		}
		catalog := Catalog{Root: roots[0], Roots: roots, Homes: paths.SkillsHomes(), Clashes: map[string][]Skill{}}
		for _, root := range roots {
			if _, err := os.Stat(root); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return Catalog{}, fmt.Errorf("read skills directory: %w", err)
			}
			if err := catalog.walk(root, root, 0, SkillKind, paths.SkillsHomes()); err != nil {
				return Catalog{}, err
			}
		}
		repository, err := absolute(paths.Repo)
		if err != nil {
			return Catalog{}, err
		}
		hooksRoot := filepath.Join(repository, "hooks")
		if info, statErr := os.Lstat(hooksRoot); statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			if err := catalog.walkAll(hooksRoot, HookKind, paths.HooksHomes()); err != nil {
				return Catalog{}, err
			}
		} else if statErr == nil {
			return Catalog{}, fmt.Errorf("hooks root %s must be a real directory", hooksRoot)
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return Catalog{}, fmt.Errorf("read hooks directory: %w", statErr)
		}
		catalog.finish()
		return catalog, nil
	}

	bindings, err := configuredBindings(paths)
	if err != nil {
		return Catalog{}, err
	}
	if len(bindings) == 0 {
		return Catalog{}, noLibraryBoundError()
	}
	root := filepath.Join(bindings[0].Path, "skills")
	catalog := Catalog{Root: root, Homes: paths.SkillsHomes(), Clashes: map[string][]Skill{}}
	for _, binding := range bindings {
		catalog.Roots = append(catalog.Roots, binding.Path)
		for _, selection := range []struct {
			kind  ResourceKind
			paths []string
			homes []string
		}{{SkillKind, binding.Skills, paths.SkillsHomes()}, {HookKind, binding.Hooks, paths.HooksHomes()}} {
			for _, rel := range selection.paths {
				dir := filepath.Join(binding.Path, rel)
				exists, markerErr := resourceMarker(filepath.Join(dir, selection.kind.Marker()), selection.kind)
				if markerErr != nil {
					return Catalog{}, fmt.Errorf("read %s %s: %w", selection.kind.Name(), dir, markerErr)
				}
				if !exists {
					continue
				}
				if selection.kind == HookKind {
					if err := validateHookSelection(binding.Path, rel, true); err != nil {
						return Catalog{}, fmt.Errorf("read hook %s: %w", dir, err)
					}
				}
				catalog.Skills = append(catalog.Skills, readResource(dir, binding.Path, selection.homes, selection.kind))
			}
		}
	}
	catalog.finish()
	return catalog, nil
}

func (c *Catalog) walkAll(root string, kind ResourceKind, homes []string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.Name() != kind.Marker() {
			return nil
		}
		exists, markerErr := resourceMarker(path, kind)
		if markerErr != nil {
			return markerErr
		}
		if !exists {
			return nil
		}
		dir := filepath.Dir(path)
		if !samePath(dir, root) {
			if kind == HookKind {
				rel, relErr := filepath.Rel(filepath.Dir(root), dir)
				if relErr != nil {
					return relErr
				}
				if err := validateHookSelection(filepath.Dir(root), rel, true); err != nil {
					return err
				}
			}
			c.Skills = append(c.Skills, readResource(dir, filepath.Dir(root), homes, kind))
		}
		return nil
	})
}

func (c *Catalog) finish() {
	sort.SliceStable(c.Skills, func(i, j int) bool {
		if c.Skills[i].Name == c.Skills[j].Name {
			if c.Skills[i].Kind != c.Skills[j].Kind {
				return c.Skills[i].Kind.Name() < c.Skills[j].Kind.Name()
			}
			return c.Skills[i].RepoPath() < c.Skills[j].RepoPath()
		}
		return c.Skills[i].Name < c.Skills[j].Name
	})
	sort.Strings(c.TooDeep)
	byName := map[string][]Skill{}
	for _, skill := range c.Skills {
		key := skill.Kind.Name() + ":" + skill.Name
		byName[key] = append(byName[key], skill)
	}
	for name, skills := range byName {
		if len(skills) > 1 {
			key := name
			if skills[0].Kind == SkillKind {
				key = skills[0].Name
			}
			c.Clashes[key] = skills
		}
	}
}

func (c Catalog) Owns(path string) bool {
	for _, skill := range c.Skills {
		if samePath(path, skill.Dir) {
			return true
		}
	}
	return false
}

func (c *Catalog) walk(root, dir string, depth int, kind ResourceKind, homes []string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("scan %s: %w", dir, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		marker := filepath.Join(path, kind.Marker())
		exists, markerErr := resourceMarker(marker, kind)
		if markerErr != nil {
			return fmt.Errorf("inspect %s marker %s: %w", kind.Name(), marker, markerErr)
		}
		if exists {
			if depth > 1 {
				c.TooDeep = append(c.TooDeep, relative(root, path))
			} else {
				c.Skills = append(c.Skills, readResource(path, root, homes, kind))
			}
			continue
		}
		if depth < 1 {
			if err := c.walk(root, path, depth+1, kind, homes); err != nil {
				return err
			}
			continue
		}
		err := filepath.WalkDir(path, func(found string, item fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if item.Name() == kind.Marker() {
				exists, markerErr := resourceMarker(found, kind)
				if markerErr != nil {
					return markerErr
				}
				if exists {
					c.TooDeep = append(c.TooDeep, relative(root, filepath.Dir(found)))
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func relative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

func (c Catalog) Find(name string) (*Skill, error) {
	kind, selector, qualified := parseResourceSelector(name)
	if qualified {
		matches := c.selectorMatches(selector, &kind)
		if len(matches) > 0 {
			return c.resolveSelector(name, matches)
		}
	}
	return c.resolveSelector(name, c.selectorMatches(name, nil))
}

func (c Catalog) selectorMatches(selector string, kind *ResourceKind) []int {
	var paths []int
	for index := range c.Skills {
		resource := c.Skills[index]
		if (kind == nil || resource.Kind == *kind) && resource.RepoPath() == normalizeResourceSelector(resource.Kind, selector) {
			paths = append(paths, index)
		}
	}
	if len(paths) > 0 {
		return paths
	}
	var names []int
	for index := range c.Skills {
		if (kind == nil || c.Skills[index].Kind == *kind) && c.Skills[index].Name == selector {
			names = append(names, index)
		}
	}
	return names
}

func (c Catalog) resolveSelector(raw string, matches []int) (*Skill, error) {
	if len(matches) == 0 {
		return nil, nil
	}
	if len(matches) > 1 {
		paths := make([]string, len(matches))
		for i, index := range matches {
			paths[i] = c.Skills[index].Selector()
		}
		return nil, fmt.Errorf("%s is ambiguous; use %s", raw, strings.Join(paths, " or "))
	}
	return &c.Skills[matches[0]], nil
}

func parseResourceSelector(raw string) (ResourceKind, string, bool) {
	if prefix, value, ok := strings.Cut(raw, ":"); ok {
		switch prefix {
		case "skill":
			return SkillKind, normalizeResourceSelector(SkillKind, value), true
		case "hook":
			return HookKind, normalizeResourceSelector(HookKind, value), true
		}
	}
	return SkillKind, normalizeResourceSelector(SkillKind, raw), false
}

func normalizeResourceSelector(kind ResourceKind, value string) string {
	if kind == HookKind {
		portable := strings.ReplaceAll(value, "\\", "/")
		return filepath.ToSlash(filepath.Clean(filepath.FromSlash(portable)))
	}
	return filepath.Clean(filepath.FromSlash(value))
}

func (c Catalog) Installed() []Skill {
	var found []Skill
	for _, skill := range c.Skills {
		if skill.Installed() {
			found = append(found, skill)
		}
	}
	return found
}

func (c Catalog) Available() []Skill {
	var found []Skill
	for _, skill := range c.Skills {
		if !skill.Installed() {
			found = append(found, skill)
		}
	}
	return found
}

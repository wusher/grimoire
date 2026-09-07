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
			if err := catalog.walk(root, root, 0); err != nil {
				return Catalog{}, err
			}
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
		for _, rel := range binding.Skills {
			dir := filepath.Join(binding.Path, rel)
			info, statErr := os.Stat(filepath.Join(dir, "SKILL.md"))
			if os.IsNotExist(statErr) {
				continue
			}
			if statErr != nil || info.IsDir() {
				return Catalog{}, fmt.Errorf("read skill %s: %v", dir, statErr)
			}
			catalog.Skills = append(catalog.Skills, ReadSkill(dir, binding.Path, catalog.Homes))
		}
	}
	catalog.finish()
	return catalog, nil
}

func (c *Catalog) finish() {
	sort.SliceStable(c.Skills, func(i, j int) bool {
		if c.Skills[i].Name == c.Skills[j].Name {
			return c.Skills[i].RepoPath() < c.Skills[j].RepoPath()
		}
		return c.Skills[i].Name < c.Skills[j].Name
	})
	sort.Strings(c.TooDeep)
	byName := map[string][]Skill{}
	for _, skill := range c.Skills {
		byName[skill.Name] = append(byName[skill.Name], skill)
	}
	for name, skills := range byName {
		if len(skills) > 1 {
			c.Clashes[name] = skills
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

func (c *Catalog) walk(root, dir string, depth int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("scan %s: %w", dir, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		_, skillErr := os.Stat(filepath.Join(path, "SKILL.md"))
		if skillErr == nil {
			if depth > 1 {
				c.TooDeep = append(c.TooDeep, relative(root, path))
			} else {
				c.Skills = append(c.Skills, ReadSkill(path, root, c.Homes))
			}
			continue
		}
		if !os.IsNotExist(skillErr) {
			return skillErr
		}
		if depth < 1 {
			if err := c.walk(root, path, depth+1); err != nil {
				return err
			}
			continue
		}
		err := filepath.WalkDir(path, func(found string, item fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !item.IsDir() && item.Name() == "SKILL.md" {
				c.TooDeep = append(c.TooDeep, relative(root, filepath.Dir(found)))
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
	for index := range c.Skills {
		if c.Skills[index].RepoPath() == name {
			return &c.Skills[index], nil
		}
	}
	var found []Skill
	for _, skill := range c.Skills {
		if skill.Name == name {
			found = append(found, skill)
		}
	}
	if len(found) == 0 {
		return nil, nil
	}
	if len(found) > 1 {
		paths := make([]string, len(found))
		for i, skill := range found {
			paths[i] = skill.RepoPath()
		}
		return nil, fmt.Errorf("%s is used by %s. Rename one", name, strings.Join(paths, " and "))
	}
	return &found[0], nil
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

package grimoire

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

type Skill struct {
	Dir             string
	Name            string
	Group           string
	Repository      string
	DeclaredName    string
	Description     string
	repositoryLabel string
	homes           []string
}

func ReadSkill(dir, skillsRoot string, homes []string) Skill {
	abs, _ := absolute(dir)
	root, _ := absolute(skillsRoot)
	skill := Skill{Dir: abs, Name: filepath.Base(abs), Repository: root, homes: append([]string(nil), homes...)}
	if parent := filepath.Dir(abs); filepath.Clean(parent) != filepath.Clean(skillsRoot) {
		if group, err := filepath.Rel(skillsRoot, parent); err == nil && group != "." && safeRelative(group) {
			skill.Group = group
		} else {
			skill.Group = filepath.Base(parent)
		}
	}
	front := readFrontmatter(filepath.Join(abs, "SKILL.md"))
	skill.DeclaredName = front["name"]
	skill.Description = front["description"]
	return skill
}

func (s Skill) RepoPath() string {
	if s.Group == "" {
		return s.Name
	}
	return filepath.Join(s.Group, s.Name)
}

// DisplayGroup hides the conventional skills/ container while preserving any
// meaningful path below it.
func (s Skill) DisplayGroup() string {
	if s.Group == "" {
		return ""
	}
	group := filepath.Clean(s.Group)
	if group == "skills" {
		return ""
	}
	prefix := "skills" + string(filepath.Separator)
	return strings.TrimPrefix(group, prefix)
}

func skillGroupKey(skill Skill, multipleRepositories bool) string {
	group := skill.DisplayGroup()
	if !multipleRepositories {
		return group
	}
	repository := filepath.Base(skill.Repository)
	if skill.repositoryLabel != "" {
		repository = skill.repositoryLabel
	}
	if group == "" {
		return repository
	}
	return filepath.Join(repository, group)
}

func multipleSkillRepositories(skills []Skill) bool {
	repositories := map[string]bool{}
	for _, skill := range skills {
		repositories[filepath.Clean(skill.Repository)] = true
	}
	return len(repositories) > 1
}

func (s Skill) SkillMD() string { return filepath.Join(s.Dir, "SKILL.md") }
func (s Skill) Mismatch() bool  { return s.DeclaredName != "" && s.DeclaredName != s.Name }

func (s Skill) LinkPaths() []string {
	links := make([]string, 0, len(s.homes))
	for _, home := range s.homes {
		links = append(links, filepath.Join(home, s.Name))
	}
	return links
}

func (s Skill) Installed() bool {
	links := s.LinkPaths()
	if len(links) == 0 {
		return false
	}
	for _, link := range links {
		if !s.InstalledAt(link) {
			return false
		}
	}
	return true
}

func (s Skill) InstalledAt(link string) bool {
	target, err := os.Readlink(link)
	if err != nil {
		return false
	}
	return samePath(resolveLink(link, target), s.Dir)
}

// readFrontmatter deliberately reads only top-level YAML scalars. A skill may
// contain nested hook configuration, but name and description are the only
// values the catalog needs.
func readFrontmatter(path string) map[string]string {
	file, err := os.Open(path)
	if err != nil {
		return map[string]string{}
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return map[string]string{}
	}
	found := map[string]string{}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "-") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}
		found[strings.TrimSpace(key)] = value
	}
	return found
}

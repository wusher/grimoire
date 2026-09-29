package grimoire

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

type Skill struct {
	// Kind identifies the resource marker and destination. The zero value is a
	// skill so programs constructing Skill values continue to work unchanged.
	Kind            ResourceKind
	Dir             string
	Name            string
	Group           string
	Repository      string
	DeclaredName    string
	Description     string
	repositoryLabel string
	homes           []string
}

type ResourceKind string

const (
	SkillKind ResourceKind = ""
	HookKind  ResourceKind = "hook"
)

func (kind ResourceKind) Name() string {
	if kind == HookKind {
		return "hook"
	}
	return "skill"
}

func (kind ResourceKind) Plural() string { return kind.Name() + "s" }

func (kind ResourceKind) Marker() string {
	if kind == HookKind {
		return "HOOK.md"
	}
	return "SKILL.md"
}

func ReadSkill(dir, skillsRoot string, homes []string) Skill {
	return readResource(dir, skillsRoot, homes, SkillKind)
}

func ReadHook(dir, repositoryRoot string, homes []string) Skill {
	return readResource(dir, repositoryRoot, homes, HookKind)
}

func readResource(dir, resourceRoot string, homes []string, kind ResourceKind) Skill {
	abs, _ := absolute(dir)
	root, _ := absolute(resourceRoot)
	skill := Skill{Kind: kind, Dir: abs, Name: filepath.Base(abs), Repository: root, homes: append([]string(nil), homes...)}
	if parent := filepath.Dir(abs); filepath.Clean(parent) != filepath.Clean(resourceRoot) {
		if group, err := filepath.Rel(resourceRoot, parent); err == nil && group != "." && safeRelative(group) {
			skill.Group = group
		} else {
			skill.Group = filepath.Base(parent)
		}
	}
	front := readFrontmatter(filepath.Join(abs, kind.Marker()))
	skill.DeclaredName = front["name"]
	skill.Description = front["description"]
	return skill
}

func (s Skill) RepoPath() string {
	if s.Group == "" {
		return s.Name
	}
	path := filepath.Join(s.Group, s.Name)
	if s.Kind == HookKind {
		return filepath.ToSlash(path)
	}
	return path
}

// DisplayGroup hides the conventional skills/ or hooks/ container while preserving any
// meaningful path below it.
func (s Skill) DisplayGroup() string {
	if s.Group == "" {
		return ""
	}
	group := filepath.Clean(s.Group)
	container := s.Kind.Plural()
	if group == container {
		return ""
	}
	prefix := container + string(filepath.Separator)
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

func (s Skill) SkillMD() string    { return filepath.Join(s.Dir, "SKILL.md") }
func (s Skill) MarkerPath() string { return filepath.Join(s.Dir, s.Kind.Marker()) }
func (s Skill) Mismatch() bool     { return s.DeclaredName != "" && s.DeclaredName != s.Name }

func (s Skill) Selector() string { return s.Kind.Name() + ":" + s.RepoPath() }

func (s Skill) identity() string { return s.Kind.Name() + "\x00" + filepath.Clean(s.Dir) }

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

// readFrontmatter deliberately reads only top-level YAML scalars. Name and
// description are the only values the catalog needs for either resource kind.
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

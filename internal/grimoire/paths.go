package grimoire

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths owns every filesystem location used by the command. Environment
// overrides keep tests and automation away from a user's real homes.
type Paths struct {
	Home         string
	ConfigHome   string
	ClaudeHome   string
	OpenCodeHome string
	CodexHome    string
	Repo         string
	Output       string
	Familiar     string
}

func PathsFromEnv() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("find home directory: %w", err)
	}
	config, err := os.UserConfigDir()
	if err != nil {
		config = filepath.Join(home, ".config")
	}

	p := Paths{
		Home:         home,
		ConfigHome:   envOr("GRIMOIRE_HOME", filepath.Join(config, "grimoire")),
		ClaudeHome:   envOr("GRIMOIRE_CLAUDE_HOME", filepath.Join(home, ".claude")),
		OpenCodeHome: envOr("GRIMOIRE_OPENCODE_HOME", filepath.Join(config, "opencode")),
		CodexHome:    envOr("GRIMOIRE_CODEX_HOME", filepath.Join(home, ".codex")),
		Repo:         os.Getenv("GRIMOIRE_REPO"),
		Output:       os.Getenv("GRIMOIRE_OUTPUT"),
	}
	return p, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func (p Paths) Binding() string { return filepath.Join(p.ConfigHome, "skills") }

// BindingsFile stores repositories and their explicitly selected skills.
// Binding is the directory of per-skill symlinks managed from that manifest.
func (p Paths) BindingsFile() string { return filepath.Join(p.ConfigHome, "bindings.json") }

func (p Paths) FamiliarFile() string { return filepath.Join(p.ConfigHome, "familiar.json") }

// Libraries returns every bound library, with the primary (write target)
// first. A legacy installation with only the skills symlink is read without
// requiring a migration command.
func (p Paths) Libraries() ([]string, error) {
	if p.Repo != "" {
		root, err := absolute(p.Repo)
		if err != nil {
			return nil, err
		}
		return []string{root}, nil
	}

	bindings, err := configuredBindings(p)
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, noLibraryBoundError()
	}
	roots := make([]string, len(bindings))
	for index, binding := range bindings {
		roots[index] = binding.Path
	}
	return roots, nil
}

func noLibraryBoundError() error {
	return fmt.Errorf("no skills are bound; go to a Git repository containing */SKILL.md and run grimoire bind")
}

// Library returns the primary bound folder. GRIMOIRE_REPO remains useful for
// automation.
func (p Paths) Library() (string, error) {
	roots, err := p.Libraries()
	if err != nil {
		return "", err
	}
	return roots[0], nil
}

func (p Paths) RepoSkills() (string, error) {
	roots, err := p.SkillsRoots()
	if err != nil {
		return "", err
	}
	return roots[0], nil
}

func (p Paths) SkillsRoots() ([]string, error) {
	if p.Repo != "" {
		root, err := absolute(p.Repo)
		if err != nil {
			return nil, err
		}
		if info, err := os.Stat(filepath.Join(root, "skills")); err == nil && info.IsDir() {
			return []string{filepath.Join(root, "skills")}, nil
		}
		return []string{root}, nil
	}
	bindings, err := configuredBindings(p)
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, noLibraryBoundError()
	}
	roots := make([]string, len(bindings))
	for index, binding := range bindings {
		root := filepath.Join(binding.Path, "skills")
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			root = binding.Path
		}
		roots[index] = root
	}
	return roots, nil
}

func (p Paths) OutputDir() (string, error) {
	if p.Output != "" {
		return p.Output, nil
	}
	root, err := p.Library()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "output"), nil
}

func (p Paths) SkillsHomes() []string {
	switch p.Familiar {
	case "claude":
		return []string{filepath.Join(p.ClaudeHome, "skills")}
	case "opencode":
		return []string{filepath.Join(p.OpenCodeHome, "skills")}
	case "codex":
		return []string{filepath.Join(p.CodexHome, "skills")}
	}
	return nil
}

func absolute(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	return filepath.Clean(abs), nil
}

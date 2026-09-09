package grimoire

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFamiliarMetadataDrivesFamiliarBehavior(t *testing.T) {
	paths := Paths{
		ClaudeHome:   filepath.Join("homes", "claude"),
		OpenCodeHome: filepath.Join("homes", "opencode"),
		CodexHome:    filepath.Join("homes", "codex"),
	}
	want := []agentFamiliar{
		{Name: "claude", Label: "Claude Code", Home: filepath.Join(paths.ClaudeHome, "skills")},
		{Name: "opencode", Label: "OpenCode", Home: filepath.Join(paths.OpenCodeHome, "skills")},
		{Name: "codex", Label: "Codex", Home: filepath.Join(paths.CodexHome, "skills")},
	}

	options := availableFamiliars(paths)
	if !reflect.DeepEqual(options, want) {
		t.Fatalf("familiar options = %#v, want %#v", options, want)
	}

	for _, option := range options {
		name, err := normalizeFamiliar("  " + strings.ToUpper(option.Name) + "  ")
		if err != nil || name != option.Name {
			t.Errorf("normalize familiar %q = %q, %v", option.Name, name, err)
		}
		paths.Familiar = option.Name
		if homes := paths.SkillsHomes(); !reflect.DeepEqual(homes, []string{option.Home}) {
			t.Errorf("selected homes for %q = %#v, want %#v", option.Name, homes, []string{option.Home})
		}
	}

	wantHomes := []string{want[0].Home, want[1].Home, want[2].Home}
	if homes := paths.KnownSkillsHomes(); !reflect.DeepEqual(homes, wantHomes) {
		t.Errorf("known homes = %#v, want %#v", homes, wantHomes)
	}
}

func TestKnownSkillsHomesSkipsEmptyAndDuplicateHomes(t *testing.T) {
	paths := Paths{ClaudeHome: "shared", CodexHome: "shared"}
	want := []string{filepath.Join("shared", "skills")}
	if homes := paths.KnownSkillsHomes(); !reflect.DeepEqual(homes, want) {
		t.Errorf("known homes = %#v, want %#v", homes, want)
	}
}

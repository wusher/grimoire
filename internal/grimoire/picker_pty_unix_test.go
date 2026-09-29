//go:build linux

package grimoire

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestTerminalPickerStatePaths(t *testing.T) {
	if scenario := os.Getenv("GRIMOIRE_PTY_SCENARIO"); scenario != "" {
		runPickerPTYChild(t, scenario)
		return
	}
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script utility is required for PTY coverage")
	}
	tests := []struct {
		name string
		keys string
	}{
		{name: "picker-state", keys: "\x1b[A\x1b[B\x1b[C\x1b[D λ\x7f\r"},
		{name: "picker-escape", keys: string([]byte{4})},
		{name: "picker-empty", keys: "\r"},
		{name: "familiar-select", keys: "\x1b[B "},
		{name: "familiar-escape", keys: string([]byte{4})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := fmt.Sprintf("%q -test.run=^TestTerminalPickerStatePaths$", os.Args[0])
			cmd := exec.CommandContext(ctx, script, "-qefc", command, "/dev/null")
			cmd.Env = append(os.Environ(), "GRIMOIRE_PTY_SCENARIO="+test.name)
			cmd.Stdin = strings.NewReader(test.keys)
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("PTY child exceeded strict timeout: %v\n%s", ctx.Err(), output)
			}
			if err != nil {
				t.Fatalf("PTY child failed: %v\n%s", err, output)
			}
		})
	}
}

func runPickerPTYChild(t *testing.T, scenario string) {
	t.Helper()
	skills := []Skill{
		{Name: "alpha", Dir: "/skills/one/alpha", Group: "one", Description: "First"},
		{Name: "beta", Dir: "/skills/one/beta", Group: "one", Description: "Second"},
		{Name: "gamma", Dir: "/skills/gamma", Description: "Loose"},
	}
	switch scenario {
	case "picker-state":
		chosen, err := (Picker{In: os.Stdin, Out: os.Stdout, Prompt: "choose"}).Pick(skills)
		if err != nil || len(chosen) == 0 {
			t.Fatalf("stateful picker = %#v, %v", chosen, err)
		}
	case "picker-escape":
		chosen, err := (Picker{In: os.Stdin, Out: os.Stdout, Prompt: "choose"}).Pick(skills)
		if err != nil || chosen != nil {
			t.Fatalf("escaped picker = %#v, %v", chosen, err)
		}
	case "picker-empty":
		chosen, err := (Picker{In: os.Stdin, Out: os.Stdout, Prompt: "choose"}).Pick(nil)
		if err != nil || chosen != nil {
			t.Fatalf("empty picker = %#v, %v", chosen, err)
		}
	case "familiar-select":
		familiars := []agentFamiliar{{Name: "global", Label: "Global"}, {Name: "codex", Label: "Codex"}}
		selected, saved, err := (FamiliarPicker{In: os.Stdin, Out: os.Stdout}).Pick(familiars, "global")
		if err != nil || !saved || selected != "codex" {
			t.Fatalf("familiar selection = %q, %v, %v", selected, saved, err)
		}
	case "familiar-escape":
		familiars := []agentFamiliar{{Name: "global", Label: "Global"}}
		selected, saved, err := (FamiliarPicker{In: os.Stdin, Out: os.Stdout}).Pick(familiars, "")
		if err != nil || saved || selected != "" {
			t.Fatalf("familiar escape = %q, %v, %v", selected, saved, err)
		}
	default:
		t.Fatalf("unknown PTY scenario %q", scenario)
	}
}

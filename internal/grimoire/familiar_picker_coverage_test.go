package grimoire

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguredFamiliarReadFailures(t *testing.T) {
	paths := Paths{ConfigHome: t.TempDir()}
	if err := os.Mkdir(paths.FamiliarFile(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := configuredFamiliar(paths); err == nil || !strings.Contains(err.Error(), "read familiar") {
		t.Fatalf("directory familiar error = %v", err)
	}
	if err := os.Remove(paths.FamiliarFile()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.FamiliarFile(), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := configuredFamiliar(paths); err == nil || !strings.Contains(err.Error(), "read familiar") {
		t.Fatalf("invalid familiar error = %v", err)
	}
}

func TestConfigureFamiliarNonTerminalStates(t *testing.T) {
	paths := testPaths(t)
	var output bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &output, Err: &output, Paths: paths, Config: Config{Boring: false}}
	if code, err := cli.configureFamiliar(nil); code != 0 || err != nil || !strings.Contains(output.String(), "Claude Code") {
		t.Fatalf("show current = %d, %v, %q", code, err, output.String())
	}
	output.Reset()
	if code, err := cli.configureFamiliar([]string{"claude"}); code != 0 || err != nil || !strings.Contains(output.String(), "already bound") {
		t.Fatalf("same familiar = %d, %v, %q", code, err, output.String())
	}
	output.Reset()
	if code, err := cli.configureFamiliar([]string{"codex"}); code != 0 || err != nil || cli.Paths.Familiar != "codex" {
		t.Fatalf("change familiar = %d, %v, %#v", code, err, cli.Paths)
	}
	for _, want := range []string{"updated familiar", "existing links remain"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("change output missing %q: %q", want, output.String())
		}
	}

	missing := *cli
	missing.Paths.Familiar = ""
	missing.Out = &output
	missing.Err = &output
	output.Reset()
	if code, err := missing.configureFamiliar(nil); code != 1 || err == nil || !strings.Contains(err.Error(), "no familiar chosen") {
		t.Fatalf("missing familiar = %d, %v", code, err)
	}

	brokenPaths := testPaths(t)
	configFile := filepath.Join(brokenPaths.Home, "config-file")
	if err := os.WriteFile(configFile, []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	brokenPaths.ConfigHome = configFile
	broken := &CLI{In: strings.NewReader(""), Out: &output, Err: &output, Paths: brokenPaths}
	if code, err := broken.configureFamiliar([]string{"codex"}); code != 1 || err == nil || !strings.Contains(err.Error(), "save familiar") {
		t.Fatalf("save familiar failure = %d, %v", code, err)
	}
}

func TestEnsureFamiliarNonTerminalAndBoringStates(t *testing.T) {
	paths := testPaths(t)
	cli := &CLI{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, Paths: paths}
	if ready, err := cli.ensureFamiliar(); !ready || err != nil {
		t.Fatalf("selected familiar = %v, %v", ready, err)
	}
	cli.Paths.Familiar = ""
	if ready, err := cli.ensureFamiliar(); ready || err == nil || !strings.Contains(err.Error(), "no familiar chosen") {
		t.Fatalf("non-terminal familiar = %v, %v", ready, err)
	}
	cli.Config.Boring = true
	if ready, err := cli.ensureFamiliar(); ready || err == nil || !strings.Contains(err.Error(), "no familiar chosen") {
		t.Fatalf("boring familiar = %v, %v", ready, err)
	}
}

func TestPickerRejectsNonTerminalsAndExercisesStateHelpers(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	output, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = output.Close() }()
	if _, err := (Picker{In: input, Out: output}).Pick(nil); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("picker non-terminal error = %v", err)
	}
	if _, _, err := (FamiliarPicker{In: input, Out: output}).Pick(nil, ""); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("familiar picker non-terminal error = %v", err)
	}

	skill := Skill{Name: "alpha", Dir: "/alpha"}
	row := PickerRow{Name: "alpha", Skills: []Skill{skill}}
	marked := map[string]Skill{}
	if markedAll(row, marked) || markedSome(row, marked) {
		t.Fatal("empty marked state reported a selection")
	}
	marked[skill.identity()] = skill
	if !markedAll(row, marked) || !markedSome(row, marked) {
		t.Fatal("marked state did not report a selection")
	}
	if markedAll(PickerRow{}, marked) {
		t.Fatal("empty row reported all marked")
	}

	picker := Picker{Prompt: "pick", Theme: Theme{columns: 40}}
	rows := []PickerRow{{Name: "group", Group: true, Open: true, Skills: []Skill{skill}, Description: "description"}}
	frame := picker.frame(rows, strings.Repeat("query", 20), 0, marked, viewport{columns: 20, rows: 4})
	assertFrameFits(t, frame, viewport{columns: 20, rows: 4})
	frame = picker.frame(nil, "", 0, nil, viewport{columns: 40, rows: 6})
	assertFrameFits(t, frame, viewport{columns: 40, rows: 6})
}

func TestReadKeyRecognizesControlNavigationAndUTF8(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"enter", "\r", "enter"},
		{"space", " ", "space"},
		{"interrupt", string([]byte{3}), "interrupt"},
		{"eof control", string([]byte{4}), "escape"},
		{"backspace", string([]byte{127}), "backspace"},
		{"up", "\x1b[A", "up"},
		{"down", "\x1b[B", "down"},
		{"right", "\x1b[C", "right"},
		{"left", "\x1b[D", "left"},
		{"unknown escape", "\x1b[Z", "escape"},
		{"ascii", "x", "x"},
		{"utf8", "λ", "λ"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := write.WriteString(test.input); err != nil {
				t.Fatal(err)
			}
			if err := write.Close(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = read.Close() }()
			got, err := readKey(bufio.NewReader(read), read)
			if err != nil || got != test.want {
				t.Fatalf("readKey(%q) = %q, %v; want %q", test.input, got, err, test.want)
			}
		})
	}
}

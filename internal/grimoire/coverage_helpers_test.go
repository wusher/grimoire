package grimoire

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func capturePipe(t *testing.T, draw func(*os.File)) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	draw(write)
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	if err := read.Close(); err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestCoverageCackleAndFireworkHelpers(t *testing.T) {
	if runCackle(nil, nil, nil) {
		t.Fatal("nil descriptors must not animate")
	}
	rows := make([]string, cackleMouth+2)
	for index := range rows {
		rows[index] = "abcdefghijklmno"
	}
	if got := cackleArt(rows, 0.9, 4.1); len(got) != len(rows) || !strings.Contains(strings.Join(got, "\n"), "ha") {
		t.Fatalf("cackle = %#v", got)
	}
	if shiftRunes("abc", 1) != string([]rune{cackleBlank, 'a', 'b'}) || shiftRunes("abc", -1) != string([]rune{'b', 'c', cackleBlank}) || shiftRunes("", 1) != "" {
		t.Fatal("unexpected rune shift")
	}
	stampRunes(rows, "xx", -1, 0)
	stampRunes(rows, "xx", 0, -2)
	stampRunes(rows, "xx", 0, 99)
	if len(animationFrame([]string{"wide", "x"}, viewport{columns: 9, rows: 5})) != 5 {
		t.Fatal("animation frame")
	}
	if got := capturePipe(t, func(out *os.File) { drawAnimationFrame(out, []string{"x"}, viewport{columns: 4, rows: 2}) }); !strings.Contains(got, "\x1b[1;1H") {
		t.Fatalf("animation output = %q", got)
	}

	runFireworks(nil, nil, Theme{}, nil)
	shell := newShell(rand.New(rand.NewSource(1)), 0, 40, 20, []string{"alpha"})
	if shell.label != "alpha" || shell.burstAt < 4 {
		t.Fatalf("shell = %#v", shell)
	}
	grid := make([][]fireworkCell, 20)
	for row := range grid {
		grid[row] = make([]fireworkCell, 120)
	}
	for _, age := range []float64{-1, fireworkRise / 2, fireworkRise + fireworkBloom/2, fireworkRise + fireworkBloom + 1} {
		placeShell(grid, shell, age)
	}
	stampRing(grid, shell, 3, "*", Green)
	setFireworkCell(grid, -1, -1, "x", Red)
	stampBanner(grid, 0.5)
	stampBanner(make([][]fireworkCell, 2), 0)
	if got := capturePipe(t, func(out *os.File) {
		drawFireworkFrame(out, grid[:2], Theme{color: true}, viewport{columns: 124, rows: 4})
	}); !strings.Contains(got, "\x1b[1;1H") {
		t.Fatalf("firework output = %q", got)
	}
}

func TestCoveragePickerAndBrowserHelpers(t *testing.T) {
	skills := []Skill{{Name: "alpha", Description: "first", Dir: "/a"}, {Name: "beta", Description: "second", Dir: "/b", Group: "tools"}}
	picker := Picker{Prompt: "choose", Theme: Theme{color: true}, SelectAllLabel: "all"}
	if _, err := picker.Pick(skills); err == nil {
		t.Fatal("non-terminal picker must fail")
	}
	rows := []PickerRow{{Name: "tools", Group: true, Open: true, Skills: skills}, {Name: "alpha", Description: "first", Skills: skills[:1]}}
	marked := map[string]Skill{skills[0].identity(): skills[0]}
	if len(picker.frame(rows, "long query", 1, marked, viewport{columns: 40, rows: 5})) != 5 || len(picker.frame(nil, "", 0, nil, viewport{columns: 10, rows: 2})) != 2 {
		t.Fatal("picker frame dimensions")
	}
	if !markedSome(rows[0], marked) || markedAll(rows[0], marked) || markedAll(PickerRow{}, marked) {
		t.Fatal("picker marking")
	}
	for input, want := range map[string]string{"\n": "enter", " ": "space", "\x03": "interrupt", "\x04": "escape", "\x7f": "backspace", "é": "é"} {
		got, err := readKey(bufio.NewReader(strings.NewReader(input)), nil)
		if err != nil || got != want {
			t.Fatalf("readKey(%q) = %q, %v", input, got, err)
		}
	}
	browser := CatalogBrowser{Theme: Theme{color: true}, Familiar: "global"}
	if err := browser.Browse(skills); err == nil {
		t.Fatal("non-terminal browser must fail")
	}
	if got, changed := editCatalogQuery("é", "backspace"); !changed || got != "" {
		t.Fatalf("backspace = %q, %t", got, changed)
	}
	if got, changed := editCatalogQuery("", "backspace"); changed || got != "" {
		t.Fatalf("empty backspace = %q, %t", got, changed)
	}
	if got, changed := editCatalogQuery("", "space"); !changed || got != " " {
		t.Fatalf("space = %q, %t", got, changed)
	}
	if got, changed := editCatalogQuery("query", "enter"); changed || got != "query" {
		t.Fatalf("control query edit = %q, %t", got, changed)
	}
	for _, size := range []viewport{{columns: 20, rows: 4}, {columns: 70, rows: 18}} {
		lines, _ := browser.frame(skills, "alpha", 99, size.columns, size.rows)
		if len(lines) != size.rows {
			t.Fatalf("browser frame %v = %d", size, len(lines))
		}
	}
}

func TestCoverageFamiliarBindingAndCastErrors(t *testing.T) {
	paths := testPaths(t)
	familiars := availableFamiliars(paths)
	familiarPicker := FamiliarPicker{Theme: Theme{color: true}, Home: paths.Home}
	if _, _, err := familiarPicker.Pick(familiars, "global"); err == nil {
		t.Fatal("non-terminal familiar picker must fail")
	}
	for _, size := range []viewport{{columns: 20, rows: 4}, {columns: 100, rows: 30}} {
		lines, _ := familiarPicker.frame(familiars, 1, "global", true, size.columns, size.rows)
		if len(lines) != size.rows {
			t.Fatalf("familiar frame %v = %d", size, len(lines))
		}
	}
	if familiarLabelFrom(familiars, "missing") != "missing" || familiarArtFits([]string{"abcdef"}, 3, 1) {
		t.Fatal("familiar helpers")
	}
	if !strings.Contains(paintCatLine(Theme{color: true}, "Meow ●", true), "\x1b[") {
		t.Fatal("cat paint")
	}
	if _, err := decodeBinding("", json.RawMessage(`[]`), "", ""); err == nil {
		t.Fatal("empty binding path")
	}
	if _, err := decodeBinding("/repo", json.RawMessage(`["../escape"]`), "", ""); err == nil {
		t.Fatal("unsafe binding")
	}
	if _, err := decodeBinding("/repo", json.RawMessage(`{}`), "", ""); err == nil {
		t.Fatal("invalid binding")
	}
	if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.BindingsFile(), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfiguredBindings(paths); err == nil {
		t.Fatal("invalid bindings file")
	}
	if err := os.Remove(paths.BindingsFile()); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(paths.Home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := selectedPaths(repo, []Skill{{Dir: filepath.Join(paths.Home, "outside")}}); err == nil {
		t.Fatal("outside skill")
	}
	makeSkill(t, paths, "", "alpha", "First")
	var out bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &out, Err: &out, Paths: paths, Config: Config{Boring: true}}
	if code := cli.Run(context.Background(), []string{"familiar"}); code != 1 {
		t.Fatalf("boring familiar = %d", code)
	}
	out.Reset()
	if code := cli.Run(context.Background(), []string{"cast"}); code != 1 || !strings.Contains(out.String(), "use grimoire cast SKILL or hook:NAME") {
		t.Fatalf("unnamed cast = %d: %s", code, out.String())
	}
	out.Reset()
	if code := cli.Run(context.Background(), []string{"cast", "one", "two"}); code != 1 {
		t.Fatalf("multi cast = %d", code)
	}
}

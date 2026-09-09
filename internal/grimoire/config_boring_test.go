package grimoire

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoringConfigPersistsAndAcceptsOnlyExactBooleans(t *testing.T) {
	paths := testPaths(t)
	var out bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &out, Err: &out, Paths: paths}

	if code := cli.Run(context.Background(), []string{"config", "boring", "true"}); code != 0 {
		t.Fatalf("enable boring mode = %d: %s", code, out.String())
	}
	config, err := configuredOptions(paths)
	if err != nil || !config.Boring {
		t.Fatalf("saved config = %#v, error = %v", config, err)
	}

	for _, value := range []string{"TRUE", "False", "1", "t", "yes", " true"} {
		out.Reset()
		if code := cli.Run(context.Background(), []string{"config", "boring", value}); code != 1 {
			t.Errorf("value %q returned %d, want 1", value, code)
		}
		if !strings.Contains(out.String(), "boring must be true or false") {
			t.Errorf("value %q output = %q", value, out.String())
		}
		if !cli.Config.Boring {
			t.Errorf("value %q changed the in-memory config", value)
		}
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"config", "boring", "false"}); code != 0 {
		t.Fatalf("disable boring mode = %d: %s", code, out.String())
	}
	setPathEnv(t, paths)
	reloaded := NewCLI(strings.NewReader(""), &out, &out)
	if reloaded.configErr != nil || reloaded.Config.Boring {
		t.Fatalf("reloaded config = %#v, error = %v", reloaded.Config, reloaded.configErr)
	}
	out.Reset()
	if code := reloaded.Run(context.Background(), []string{"config"}); code != 0 || out.String() != "boring=false\n" {
		t.Fatalf("config list = %d, %q", code, out.String())
	}
}

func TestBoringConfigRepairsAnInvalidConfig(t *testing.T) {
	paths := testPaths(t)
	if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ConfigFile(), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	setPathEnv(t, paths)

	var out bytes.Buffer
	cli := NewCLI(strings.NewReader(""), &out, &out)
	if cli.configErr == nil {
		t.Fatal("invalid config did not cause an error")
	}
	if code := cli.Run(context.Background(), []string{"config", "boring", "true"}); code != 0 {
		t.Fatalf("repair config = %d: %s", code, out.String())
	}
	if cli.configErr != nil || !cli.Config.Boring {
		t.Fatalf("repaired config = %#v, error = %v", cli.Config, cli.configErr)
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"help"}); code != 0 {
		t.Fatalf("help after repair = %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "config boring [true|false]") {
		t.Fatalf("help does not list config: %s", out.String())
	}
	requireBoringOutput(t, out.String())
}

func TestBoringModeUsesPlainOutputAndRequiresExplicitNames(t *testing.T) {
	paths := testPaths(t)
	initGit(t, paths.Repo)
	makeSkill(t, paths, "", "alpha", "First")
	t.Chdir(paths.Repo)

	var out bytes.Buffer
	cli := &CLI{
		In:     strings.NewReader(""),
		Out:    &out,
		Err:    &out,
		Paths:  paths,
		Config: Config{Boring: true},
	}
	run := func(args ...string) (int, string) {
		t.Helper()
		out.Reset()
		code := cli.Run(context.Background(), args)
		body := out.String()
		requireBoringOutput(t, body)
		return code, body
	}

	if code, body := run("help"); code != 0 || !strings.Contains(body, "config boring [true|false]") {
		t.Fatalf("boring help = %d: %s", code, body)
	}
	if code, body := run("toc"); code != 0 || body != "alpha\tnot installed\n" {
		t.Fatalf("boring toc = %d: %q", code, body)
	}
	if code, body := run("familiar"); code != 1 || !strings.Contains(body, "pass one familiar name") {
		t.Fatalf("boring familiar guidance = %d: %s", code, body)
	}
	if code, body := run("cast"); code != 1 || !strings.Contains(body, "grimoire cast SKILL") {
		t.Fatalf("boring cast guidance = %d: %s", code, body)
	}
	if code, body := run("cast", "alpha"); code != 0 || body != "alpha installed\n" {
		t.Fatalf("boring cast = %d: %q", code, body)
	}
	if code, body := run("banish"); code != 1 || !strings.Contains(body, "grimoire banish SKILL") {
		t.Fatalf("boring banish guidance = %d: %s", code, body)
	}
	if code, body := run("banish", "alpha"); code != 0 || body != "alpha removed\n" {
		t.Fatalf("boring banish = %d: %q", code, body)
	}
	if code, body := run("effigy"); code != 1 || !strings.Contains(body, "grimoire effigy SKILL") {
		t.Fatalf("boring effigy guidance = %d: %s", code, body)
	}
	if code, body := run("volley"); code != 0 || body != "alpha installed\ninstalled=1 skipped=0 failed=0\n" {
		t.Fatalf("boring volley = %d: %q", code, body)
	}
	if code, body := run("hone"); code != 0 || body != "nothing to repair\n" {
		t.Fatalf("boring hone = %d: %q", code, body)
	}
	if code, body := run("bind"); code != 1 || !strings.Contains(body, "grimoire bind NAME") {
		t.Fatalf("boring bind guidance = %d: %s", code, body)
	}
	if code, body := run("bind", "alpha"); code != 0 || !strings.Contains(body, "1 skill bound") {
		t.Fatalf("boring bind = %d: %s", code, body)
	}
	if code, body := run("unbind"); code != 1 || !strings.Contains(body, "grimoire unbind NAME") {
		t.Fatalf("boring unbind guidance = %d: %s", code, body)
	}
	if code, body := run("unbind", "alpha"); code != 0 || body != "alpha unbound\n" {
		t.Fatalf("boring unbind = %d: %q", code, body)
	}
	if code, body := run("index"); code != 0 || !strings.Contains(body, "grimoire index --refresh") {
		t.Fatalf("boring index = %d: %s", code, body)
	}
	if code, body := run("effigy", "alpha"); code != 0 || !strings.Contains(body, filepath.Join("output", "alpha.zip")) {
		t.Fatalf("boring effigy = %d: %s", code, body)
	}
}

func TestRichVolleyRetainsDetailedAndAggregateOutput(t *testing.T) {
	paths := testPaths(t)
	makeSkill(t, paths, "", "alpha", "First")
	makeSkill(t, paths, "", "beta", "Second")
	catalog, err := LoadCatalog(paths)
	if err != nil {
		t.Fatal(err)
	}
	beta, err := catalog.Find("beta")
	if err != nil || beta == nil {
		t.Fatalf("find beta = %#v, %v", beta, err)
	}
	if result := Install(paths, *beta); result.Status != Installed {
		t.Fatalf("install beta = %#v", result)
	}

	var out bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"volley"}); code != 0 {
		t.Fatalf("rich volley = %d: %s", code, out.String())
	}
	if got, want := out.String(), "alpha installed\n1 already installed\ninstalled 1, skipped 1, failed 0\n"; got != want {
		t.Fatalf("rich volley output = %q, want %q", got, want)
	}
}

func TestBoringVolleyRetainsPlainDetailsAndSkippedCount(t *testing.T) {
	paths := testPaths(t)
	makeSkill(t, paths, "", "alpha", "First")
	makeSkill(t, paths, "", "beta", "Second")
	catalog, err := LoadCatalog(paths)
	if err != nil {
		t.Fatal(err)
	}
	beta, err := catalog.Find("beta")
	if err != nil || beta == nil {
		t.Fatalf("find beta = %#v, %v", beta, err)
	}
	if result := Install(paths, *beta); result.Status != Installed {
		t.Fatalf("install beta = %#v", result)
	}

	var out bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &out, Err: &out, Paths: paths, Config: Config{Boring: true}}
	if code := cli.Run(context.Background(), []string{"volley"}); code != 0 {
		t.Fatalf("boring volley = %d: %s", code, out.String())
	}
	want := "alpha installed\n1 already installed\ninstalled=1 skipped=1 failed=0\n"
	if got := out.String(); got != want {
		t.Fatalf("boring volley output = %q, want %q", got, want)
	}
	requireBoringOutput(t, out.String())
}

func requireBoringOutput(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(body, "\x1b[") {
		t.Fatalf("boring output contains an ANSI sequence: %q", body)
	}
	for _, character := range body {
		if character > 127 {
			t.Fatalf("boring output contains non-ASCII decoration %q: %q", character, body)
		}
	}
}

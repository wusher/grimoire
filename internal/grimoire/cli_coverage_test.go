package grimoire

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIRunReportsInitializationAndDispatchErrors(t *testing.T) {
	tests := []struct {
		name string
		cli  CLI
		args []string
		want string
	}{
		{name: "setup", cli: CLI{setupErr: errors.New("setup failed")}, args: []string{"toc"}, want: "setup failed"},
		{name: "familiar", cli: CLI{familiarErr: errors.New("familiar failed")}, args: []string{"toc"}, want: "familiar failed"},
		{name: "config", cli: CLI{configErr: errors.New("config failed")}, args: []string{"toc"}, want: "config failed"},
		{name: "unknown", cli: CLI{}, args: []string{"abracadabra"}, want: "no command called abracadabra"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			test.cli.In = strings.NewReader("")
			test.cli.Out = &output
			test.cli.Err = &output
			test.cli.Config.Boring = true
			if code := test.cli.Run(context.Background(), test.args); code != 1 {
				t.Fatalf("Run() = %d, want 1", code)
			}
			if !strings.Contains(output.String(), test.want) {
				t.Fatalf("output = %q, want %q", output.String(), test.want)
			}
		})
	}
	var output bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &output, Err: &output, Config: Config{Boring: true}}
	if code := cli.Run(context.Background(), nil); code != 0 || !strings.Contains(output.String(), "Usage:") {
		t.Fatalf("default help = %d, %q", code, output.String())
	}
}

func TestCLIChooseCoversExplicitAndNonTerminalPaths(t *testing.T) {
	paths := testPaths(t)
	alphaDir := makeSkill(t, paths, "one", "alpha", "First")
	makeSkill(t, paths, "two", "beta", "Second")
	catalog, err := LoadCatalog(paths)
	if err != nil {
		t.Fatal(err)
	}
	cli := &CLI{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, Paths: paths}
	if _, err := cli.choose([]string{"alpha", "beta"}, catalog, catalog.Skills, "install"); err == nil || !strings.Contains(err.Error(), "at most one") {
		t.Fatalf("multiple selection error = %v", err)
	}
	chosen, err := cli.choose([]string{filepath.Join("one", "alpha")}, catalog, catalog.Skills, "install")
	if err != nil || len(chosen) != 1 || !samePath(chosen[0].Dir, alphaDir) {
		t.Fatalf("explicit selection = %#v, %v", chosen, err)
	}
	if chosen, err := cli.choose([]string{"missing"}, catalog, catalog.Skills, "install"); err == nil || chosen != nil {
		t.Fatalf("missing selection = %#v, %v", chosen, err)
	}
	if chosen, err := cli.choose(nil, catalog, nil, "remove"); err != nil || len(chosen) != 0 {
		t.Fatalf("empty pool = %#v, %v", chosen, err)
	}
	if _, err := cli.choose(nil, catalog, catalog.Skills, "install"); err == nil || !strings.Contains(err.Error(), "terminal cannot show") {
		t.Fatalf("non-terminal selection error = %v", err)
	}
	cli.Config.Boring = true
	if _, err := cli.choose(nil, catalog, catalog.Skills, "pack"); err == nil || !strings.Contains(err.Error(), "effigy") {
		t.Fatalf("boring selection error = %v", err)
	}
}

func TestCLIBoringErrorAndEmptyCommandPaths(t *testing.T) {
	paths := testPaths(t)
	var output bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &output, Err: &output, Paths: paths, Config: Config{Boring: true}}
	run := func(args ...string) (int, string) {
		t.Helper()
		output.Reset()
		return cli.Run(context.Background(), args), output.String()
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"toc", "extra"}, "toc takes no arguments"},
		{[]string{"volley", "extra"}, "volley takes no arguments"},
		{[]string{"hone", "--unknown"}, "unknown hone option"},
		{[]string{"familiar", "a", "b"}, "familiar accepts one name"},
		{[]string{"cast", "a", "b"}, "at most one resource"},
		{[]string{"banish", "a", "b"}, "at most one resource"},
	} {
		if code, body := run(test.args...); code != 1 || !strings.Contains(body, test.want) {
			t.Errorf("%v = %d, %q; want %q", test.args, code, body, test.want)
		}
	}
	if code, body := run("toc"); code != 0 || body != "no bound skills or hooks\n" {
		t.Fatalf("empty toc = %d, %q", code, body)
	}
	if code, body := run("volley"); code != 0 || !strings.Contains(body, "installed=0 skipped=0 failed=0") {
		t.Fatalf("empty volley = %d, %q", code, body)
	}
	if code, body := run("effigy", "missing"); code != 1 || !strings.Contains(body, "no skill or hook called missing") {
		t.Fatalf("missing effigy = %d, %q", code, body)
	}
}

func TestCLIEffigyReportsPackFailure(t *testing.T) {
	paths := testPaths(t)
	makeSkill(t, paths, "", "alpha", "First")
	paths.Output = filepath.Join(paths.Home, "output-file")
	if err := os.WriteFile(paths.Output, []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &output, Err: &output, Paths: paths, Config: Config{Boring: true}}
	if code := cli.Run(context.Background(), []string{"effigy", "alpha"}); code != 1 {
		t.Fatalf("effigy = %d, output %q", code, output.String())
	}
	normalized := strings.Join(strings.Fields(output.String()), " ")
	if !strings.Contains(normalized, "alpha") || !strings.Contains(normalized, "not a directory") {
		t.Fatalf("effigy failure output = %q", output.String())
	}
}

func TestCLIFormattingBranches(t *testing.T) {
	if icon, color := installResultStyle(InstallBlocked); icon != "cross" || color != Red {
		t.Fatalf("blocked style = %q, %v", icon, color)
	}
	if icon, color := installResultStyle(Removed); icon != "trash" || color != Cyan {
		t.Fatalf("removed style = %q, %v", icon, color)
	}
	if weight(1023) != "1023 B" || weight(1024) != "1.0 KB" || weight(1024*1024) != "1.0 MB" {
		t.Fatalf("unexpected weights: %q, %q, %q", weight(1023), weight(1024), weight(1024*1024))
	}
	var output bytes.Buffer
	cli := &CLI{Out: &output, Err: &output, Config: Config{Boring: true}}
	cli.writeResponsive(&output, "cross", Red, strings.Repeat("word ", 30), Red)
	if strings.Count(output.String(), "\n") < 2 {
		t.Fatalf("long responsive output did not wrap: %q", output.String())
	}
}

//go:build !windows

package grimoire

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLICommandsReturnCatalogAndOptionFailures(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	var output bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &output, Err: &output, Paths: paths}
	for _, run := range []struct {
		name string
		call func() error
	}{
		{"toc", func() error { return cli.toc(nil) }},
		{"cast", func() error { _, err := cli.cast([]string{"alpha"}); return err }},
		{"banish", func() error { _, err := cli.banish([]string{"alpha"}); return err }},
		{"volley", func() error { _, err := cli.volley(nil); return err }},
		{"effigy", func() error { _, err := cli.effigy([]string{"alpha"}); return err }},
	} {
		t.Run(run.name, func(t *testing.T) {
			if err := run.call(); err == nil || !strings.Contains(err.Error(), "no skills are bound") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if _, err := cli.bind([]string{"--unknown"}); err == nil || !strings.Contains(err.Error(), "unknown binding option") {
		t.Fatalf("bind option error = %v", err)
	}
	if _, err := cli.unbind([]string{"--unknown"}); err == nil || !strings.Contains(err.Error(), "unknown binding option") {
		t.Fatalf("unbind option error = %v", err)
	}
	if _, err := cli.index([]string{"--unknown"}); err == nil || !strings.Contains(err.Error(), "unknown index option") {
		t.Fatalf("index option error = %v", err)
	}

	wide := newPageWidth(Theme{columns: 80}, 80)
	body := appendHelpBlock(nil, wide, Theme{columns: 80}, "empty", [][2]string{{"command", ""}})
	if len(body) == 0 {
		t.Fatal("wide empty help block")
	}
}

func TestCLIClashesEmptyPoolsAndBindingErrors(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	first, second := filepath.Join(paths.Home, "first"), filepath.Join(paths.Home, "second")
	makeSkillIn(t, first, "", "clash", "First")
	makeSkillIn(t, second, "", "clash", "Second")
	if err := writeBindings(paths, []BoundRepository{{Path: first, Skills: []string{"clash"}}, {Path: second, Skills: []string{"clash"}}}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &output, Err: &output, Paths: paths}
	if code, err := cli.cast([]string{"clash"}); code != 1 || err == nil || !strings.Contains(err.Error(), "nothing was installed") {
		t.Fatalf("clashing cast = %d, %v", code, err)
	}
	if code, err := cli.volley(nil); code != 1 || err == nil || !strings.Contains(err.Error(), "nothing was installed") {
		t.Fatalf("clashing volley = %d, %v", code, err)
	}
	if err := cli.toc(nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Rename one") {
		t.Fatalf("clash warning = %q", output.String())
	}

	paths = testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repository")
	alphaDir := makeSkillIn(t, repository, "", "alpha", "Alpha")
	if err := writeBindings(paths, []BoundRepository{{Path: repository, Skills: []string{"alpha"}}}); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	cli = &CLI{In: strings.NewReader(""), Out: &output, Err: &output, Paths: paths}
	if code, err := cli.banish(nil); code != 0 || err != nil || !strings.Contains(output.String(), "nothing picked") {
		t.Fatalf("empty banish = %d, %v, %q", code, err, output.String())
	}
	link := filepath.Join(paths.ClaudeHome, "skills", "alpha")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, []byte("foreign"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, err := cli.banish([]string{"alpha"}); code != 1 || err != nil {
		t.Fatalf("blocked banish = %d, %v", code, err)
	}
	if alphaDir == "" {
		t.Fatal("missing alpha")
	}

	bad := testPaths(t)
	bad.Repo = ""
	if err := os.MkdirAll(bad.ConfigHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad.BindingsFile(), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	brokenCLI := &CLI{Out: &output, Err: &output, Paths: bad}
	if _, err := brokenCLI.unbind([]string{"alpha"}); err == nil || !strings.Contains(err.Error(), "read repository bindings") {
		t.Fatalf("broken unbind = %v", err)
	}
}

func TestCLIVolleyCoversInstalledSkippedAndBlockedResults(t *testing.T) {
	paths := testPaths(t)
	installedDir := makeSkill(t, paths, "", "installed", "Installed")
	makeSkill(t, paths, "", "new", "New")
	makeSkill(t, paths, "", "blocked", "Blocked")
	installedLink := filepath.Join(paths.ClaudeHome, "skills", "installed")
	blockedLink := filepath.Join(paths.ClaudeHome, "skills", "blocked")
	if err := os.MkdirAll(filepath.Dir(installedLink), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createSymlink(installedDir, installedLink, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blockedLink, []byte("foreign"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &output, Err: &output, Paths: paths}
	if code, err := cli.volley(nil); code != 1 || err != nil {
		t.Fatalf("mixed volley = %d, %v", code, err)
	}
	for _, want := range []string{"installed 1", "skipped 1", "failed 1", "blocked"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("volley output missing %q: %q", want, output.String())
		}
	}
}

func TestCLIBoringIndexAndBindingFailureViews(t *testing.T) {
	paths := testPaths(t)
	missing := BoundRepository{Path: filepath.Join(paths.Home, "missing")}
	repository := filepath.Join(paths.Home, "repository")
	makeSkillIn(t, repository, "", "alpha", "Alpha")
	broken := BoundRepository{Path: repository, Skills: []string{"alpha", "missing"}}
	var output bytes.Buffer
	cli := &CLI{Out: &output, Err: &output, Paths: paths, Config: Config{Boring: true}}
	cli.showIndex([]BoundRepository{missing, broken})
	if code, err := cli.showBindingResults("bind", "", []BindResult{{Status: Blocked, Path: paths.Binding(), Message: "blocked"}}, true); code != 1 || err != nil {
		t.Fatalf("boring failed binding = %d, %v", code, err)
	}
	for _, want := range []string{"missing folder", "missing resource", "blocked"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("output missing %q: %q", want, output.String())
		}
	}
}

//go:build !windows

package grimoire

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRichCLIViewsRenderAllOperationalStates(t *testing.T) {
	paths := testPaths(t)
	var output bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &output, Err: &output, Paths: paths}

	cli.help(&output)
	cli.printTOC(Catalog{})

	firstDir := makeSkill(t, paths, "group", "alpha", "First skill with a description")
	secondDir := makeSkill(t, paths, "", "beta", "Second skill")
	first := ReadSkill(firstDir, filepath.Join(paths.Repo, "skills"), paths.SkillsHomes())
	second := ReadSkill(secondDir, filepath.Join(paths.Repo, "skills"), paths.SkillsHomes())
	for _, link := range first.LinkPaths() {
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := createSymlink(first.Dir, link, true); err != nil {
			t.Fatal(err)
		}
	}
	cli.printTOC(Catalog{Skills: []Skill{first, second}})

	repositories := []BoundRepository{
		{Path: paths.Repo, Skills: []string{first.RepoPath(), second.RepoPath()}},
		{Path: filepath.Join(paths.Home, "missing-repository")},
	}
	blocked := filepath.Join(paths.ClaudeHome, "skills", second.Name)
	if err := os.MkdirAll(filepath.Dir(blocked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("foreign"), 0o644); err != nil {
		t.Fatal(err)
	}
	cli.showIndex(repositories)

	cli.showRefresh([]RefreshResult{
		{Repo: paths.Repo, Status: CurrentBranch, Message: "current"},
		{Repo: paths.Repo, Status: Pulled, Message: "pulled", repaired: 2, Changes: []RefreshChange{{Action: "renamed", Name: "alpha", Message: "moved"}, {Action: "relinked", Message: "fixed"}}},
		{Repo: paths.Repo, Status: RefreshSkipped, Message: "skipped"},
		{Repo: paths.Repo, Status: RefreshFailed, Message: "failed"},
	})

	results := []BindResult{
		{Status: Bound, Path: paths.Binding(), Target: first.Dir, Message: "bound", Changes: []PathChange{{Action: "created link", Path: paths.Binding(), Target: first.Dir}}},
		{Status: Already, Path: paths.Binding(), Target: second.Dir, Message: "already bound"},
		{Status: Blocked, Path: paths.Binding(), Message: "blocked"},
	}
	if code, err := cli.showBindingResults("bind", "binding", results, true); code != 1 || err != nil {
		t.Fatalf("failed binding view = %d, %v", code, err)
	}
	if code, err := cli.showBindingResults("unbind", "unbinding", []BindResult{{Status: Unbound, Target: first.Dir, Message: "unbound"}}, false); code != 0 || err != nil {
		t.Fatalf("unbind view = %d, %v", code, err)
	}
	if code, err := cli.showBindingResults("unbind", "unbinding", []BindResult{{Status: NotBound, Target: first.Dir, Message: "not bound"}, {Status: Unbound, Target: second.Dir, Message: "unbound"}}, false); code != 0 || err != nil {
		t.Fatalf("multi-unbind view = %d, %v", code, err)
	}

	text := output.String()
	for _, want := range []string{"grimoire", "table of contents", "repositories and familiar homes", "pulled", "failed", "binding", "unbinding"} {
		if !strings.Contains(text, want) {
			t.Errorf("rich output missing %q", want)
		}
	}
}

func TestRichCLIHelpersCoverNarrowAndStatusBranches(t *testing.T) {
	theme := Theme{columns: 34}
	page := newPageWidth(theme, 34)
	body := appendHelpBlock(nil, page, theme, "commands", [][2]string{
		{"a-command-name-that-is-long", "a description that wraps over several narrow lines"},
		{"--flag", ""},
	})
	if len(body) < 4 {
		t.Fatalf("narrow help body = %#v", body)
	}

	paths := testPaths(t)
	var output bytes.Buffer
	cli := &CLI{Out: &output, Err: &output, Paths: paths}
	cli.installSummary([]InstallResult{
		{Status: Installed, Skill: Skill{Name: "installed"}},
		{Status: AlreadyStatus, Skill: Skill{Name: "existing"}},
		{Status: Removed, Skill: Skill{Name: "removed"}},
		{Status: Missing, Skill: Skill{Name: "missing"}},
		{Status: InstallBlocked, Skill: Skill{Name: "blocked"}},
	}, "linked into")

	for _, result := range []InstallResult{
		{Status: Installed, Skill: Skill{Name: "installed"}},
		{Status: Removed, Skill: Skill{Name: "removed"}},
		{Status: AlreadyStatus, Skill: Skill{Name: "existing"}},
		{Status: Missing, Skill: Skill{Name: "missing"}},
		{Status: InstallBlocked, Skill: Skill{Name: "blocked"}},
	} {
		cli.writeInstallResult(result)
	}
	cli.warnCatalog(Catalog{
		Clashes: map[string][]Skill{"alpha": {{Name: "alpha", Group: "one"}, {Name: "alpha", Group: "two"}}},
		TooDeep: []string{"group/deep/alpha"},
	})
	cli.writeResponsive(&output, "star", Amber, strings.Repeat("long message ", 20), Violet)
	cli.writeResponsive(&output, "star", Amber, "", Violet)

	text := output.String()
	for _, want := range []string{"installed", "removed", "already there", "not installed", "blocked", "Rename one", "sits too deep"} {
		if !strings.Contains(text, want) {
			t.Errorf("helper output missing %q: %q", want, text)
		}
	}
}

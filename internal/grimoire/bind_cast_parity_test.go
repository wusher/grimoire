package grimoire

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBindInstallsSelectedSkillsAndReportsPartialFailure(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repo := filepath.Join(paths.Home, "repo")
	initGit(t, repo)
	alpha := makeSkillIn(t, repo, "one", "alpha", "First")
	beta := makeSkillIn(t, repo, "two", "beta", "Second")
	t.Chdir(repo)

	blocked := filepath.Join(paths.ClaudeHome, "skills", "beta")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(blocked, "foreign")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cli := &CLI{
		In: strings.NewReader(""), Out: &out, Err: &out, Paths: paths,
		Config: Config{Boring: true},
	}
	if code := cli.Run(context.Background(), []string{"bind", "alpha", "beta"}); code != 1 {
		t.Fatalf("bind code = %d, want 1: %s", code, out.String())
	}
	assertLinkTarget(t, filepath.Join(paths.Binding(), "alpha"), alpha)
	assertLinkTarget(t, filepath.Join(paths.Binding(), "beta"), beta)
	assertLinkTarget(t, filepath.Join(paths.ClaudeHome, "skills", "alpha"), alpha)
	if body, err := os.ReadFile(marker); err != nil || string(body) != "keep" {
		t.Fatalf("blocked destination changed: %q, %v", body, err)
	}
	for _, want := range []string{"alpha installed", "beta a real folder already holds this name", "binding saved", "rerun grimoire bind"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("bind output does not contain %q: %s", want, out.String())
		}
	}
}

func TestBindRetriesInstallationForAnAlreadyBoundSkill(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repo := filepath.Join(paths.Home, "repo")
	initGit(t, repo)
	alphaDir := makeSkillIn(t, repo, "nested", "alpha", "First")
	alpha := ReadSkill(alphaDir, repo, paths.SkillsHomes())
	if result := BindSkills(paths, repo, []Skill{alpha}); !result.OK() {
		t.Fatal(result.Message)
	}
	t.Chdir(repo)

	var out bytes.Buffer
	cli := &CLI{
		In: strings.NewReader(""), Out: &out, Err: &out, Paths: paths,
		Config: Config{Boring: true},
	}
	selector := "." + string(filepath.Separator) + filepath.Join("nested", "alpha") + string(filepath.Separator)
	if code := cli.Run(context.Background(), []string{"bond", selector}); code != 0 {
		t.Fatalf("bond code = %d: %s", code, out.String())
	}
	assertLinkTarget(t, filepath.Join(paths.ClaudeHome, "skills", "alpha"), alphaDir)
	if !strings.Contains(out.String(), "already bound") || !strings.Contains(out.String(), "alpha installed") {
		t.Fatalf("retry output = %s", out.String())
	}
}

func TestRichBindShowsBindingAndInstallOutputWithoutCastArt(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repo := filepath.Join(paths.Home, "repo")
	initGit(t, repo)
	alpha := makeSkillIn(t, repo, "nested", "alpha", "First")
	t.Chdir(repo)

	var out bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"bind", "alpha"}); code != 0 {
		t.Fatalf("bind code = %d: %s", code, out.String())
	}
	assertLinkTarget(t, filepath.Join(paths.ClaudeHome, "skills", "alpha"), alpha)
	for _, want := range []string{spaced("bind"), sealSigil[0], "alpha installed", "1 installed", "linked into"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("rich bind output does not contain %q: %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), wandSigil[0]) {
		t.Fatalf("bind output contains cast wand art: %s", out.String())
	}
}

func TestCastNormalizesRepositoryPathSelectorsLikeBind(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repo := filepath.Join(paths.Home, "repo")
	initGit(t, repo)
	alpha := makeSkillIn(t, repo, "nested", "alpha", "First")
	if result := BindLibrary(paths, repo); !result.OK() {
		t.Fatal(result.Message)
	}

	var out bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &out, Err: &out, Paths: paths, Config: Config{Boring: true}}
	selector := "./nested/alpha/"
	if code := cli.Run(context.Background(), []string{"cast", selector}); code != 0 {
		t.Fatalf("cast code = %d: %s", code, out.String())
	}
	assertLinkTarget(t, filepath.Join(paths.ClaudeHome, "skills", "alpha"), alpha)
}

func TestInstallRejectsInvalidSourcesBeforeMutation(t *testing.T) {
	paths := testPaths(t)
	tests := []struct {
		name    string
		prepare func(string)
		want    string
	}{
		{
			name: "missing directory",
			prepare: func(string) {
			},
			want: "source skill directory",
		},
		{
			name: "source is a file",
			prepare: func(dir string) {
				if err := os.WriteFile(dir, []byte("not a directory"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "not a directory",
		},
		{
			name: "missing manifest",
			prepare: func(dir string) {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			},
			want: "source skill SKILL.md",
		},
		{
			name: "manifest is a directory",
			prepare: func(dir string) {
				if err := os.MkdirAll(filepath.Join(dir, "SKILL.md"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			want: "directory, not a skill marker",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := filepath.Join(paths.Home, strings.ReplaceAll(test.name, " ", "-"))
			test.prepare(dir)
			skill := Skill{Dir: dir, Name: filepath.Base(dir), homes: []string{filepath.Join(paths.ClaudeHome, "skills")}}
			result := Install(paths, skill)
			if result.Status != InstallBlocked || !strings.Contains(result.Message, test.want) {
				t.Fatalf("install = %#v, want blocked containing %q", result, test.want)
			}
			if _, err := os.Lstat(filepath.Join(paths.ClaudeHome, "skills", skill.Name)); !os.IsNotExist(err) {
				t.Fatalf("install mutated destination: %v", err)
			}
		})
	}
}

func TestBindAndBondRequireAFamiliar(t *testing.T) {
	if !commandNeedsFamiliar("bind") || !commandNeedsFamiliar("bond") {
		t.Fatal("bind aliases do not require a familiar")
	}
}

func TestSymlinkedSkillManifestRetainsLegacyCompatibility(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repo := filepath.Join(paths.Home, "repo")
	initGit(t, repo)
	dir := filepath.Join(repo, "skills", "linked")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(paths.Home, "external-skill.md")
	if err := os.WriteFile(external, []byte("---\nname: linked\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "SKILL.md")
	if err := os.Symlink(external, manifest); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	root, discovered, err := DiscoverRepositorySkills(repo, paths.SkillsHomes())
	if err != nil || !samePath(root, repo) {
		t.Fatalf("discovery root = %q, error = %v", root, err)
	}
	if len(discovered) != 1 {
		t.Fatalf("symlinked manifest was not discovered: %#v", discovered)
	}
	skill := ReadSkill(dir, repo, paths.SkillsHomes())
	if result := BindSkills(paths, repo, []Skill{skill}); !result.OK() {
		t.Fatalf("bind symlinked manifest = %#v", result)
	}
	result := Install(paths, skill)
	if result.Status != Installed {
		t.Fatalf("install symlinked manifest = %#v", result)
	}
	assertLinkTarget(t, filepath.Join(paths.ClaudeHome, "skills", "linked"), dir)
}

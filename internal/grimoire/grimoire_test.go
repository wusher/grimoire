package grimoire

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func testPaths(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "library")
	if err := os.MkdirAll(filepath.Join(repo, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Paths{
		Home:         root,
		ConfigHome:   filepath.Join(root, "config", "grimoire"),
		ClaudeHome:   filepath.Join(root, "claude"),
		OpenCodeHome: filepath.Join(root, "opencode"),
		CodexHome:    filepath.Join(root, "codex"),
		Repo:         repo,
		Familiar:     "claude",
	}
}

func setPathEnv(t *testing.T, paths Paths) {
	t.Helper()
	t.Setenv("GRIMOIRE_HOME", paths.ConfigHome)
	t.Setenv("GRIMOIRE_REPO", paths.Repo)
	t.Setenv("GRIMOIRE_CLAUDE_HOME", paths.ClaudeHome)
	t.Setenv("GRIMOIRE_OPENCODE_HOME", paths.OpenCodeHome)
	t.Setenv("GRIMOIRE_CODEX_HOME", paths.CodexHome)
}

func makeSkill(t *testing.T, paths Paths, group, name, description string) string {
	t.Helper()
	return makeSkillIn(t, filepath.Join(paths.Repo, "skills"), group, name, description)
}

func makeSkillIn(t *testing.T, skillsRoot, group, name, description string) string {
	t.Helper()
	dir := skillsRoot
	if group != "" {
		dir = filepath.Join(dir, group)
	}
	dir = filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func initGit(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "init", "-q", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
}

func assertLinkTarget(t *testing.T, link, want string) {
	t.Helper()
	target, err := os.Readlink(link)
	if err != nil || !samePath(target, want) {
		t.Fatalf("link %s = %s, want %s (error: %v)", link, target, want, err)
	}
}

func assertFrameFits(t *testing.T, lines []string, size viewport) {
	t.Helper()
	if len(lines) != size.rows {
		t.Fatalf("frame height = %d, want %d", len(lines), size.rows)
	}
	for index, line := range lines {
		if width := visibleWidth(line); width > size.columns {
			t.Errorf("line %d width = %d, maximum %d: %q", index, width, size.columns, line)
		}
	}
}

func TestBindRequiresGitAndDiscoversSkillsRecursively(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	notRepo := filepath.Join(paths.Home, "not-a-repo")
	if err := os.MkdirAll(notRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if result := BindLibrary(paths, notRepo); result.Status != Blocked || !strings.Contains(result.Message, "Git repository") {
		t.Fatalf("outside Git = %#v", result)
	}

	repo := filepath.Join(paths.Home, "repo")
	initGit(t, repo)
	alpha := makeSkillIn(t, repo, "tools/review", "alpha", "Nested skill")
	beta := makeSkillIn(t, repo, "", "beta", "Root-level skill")
	gamma := makeSkillIn(t, repo, "ignored", "gamma", "Ignored but still discoverable")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "deep", "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, skills, err := DiscoverRepositorySkills(filepath.Join(repo, "deep", "inside"), paths.SkillsHomes())
	if err != nil || root != repo || len(skills) != 3 {
		t.Fatalf("discovery root = %s, skills = %#v, error = %v", root, skills, err)
	}
	if skills[0].Dir != beta || skills[1].Dir != gamma || skills[2].Dir != alpha || skills[2].Group != filepath.Join("tools", "review") {
		t.Fatalf("discovered skills = %#v", skills)
	}
}

func TestBindConnectsOnlySelectedSkillsAndUpdatesSelection(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repo := filepath.Join(paths.Home, "repo")
	initGit(t, repo)
	alphaDir := makeSkillIn(t, repo, "one", "alpha", "First")
	betaDir := makeSkillIn(t, repo, "two/deep", "beta", "Second")
	alpha := ReadSkill(alphaDir, repo, paths.SkillsHomes())
	beta := ReadSkill(betaDir, repo, paths.SkillsHomes())

	if result := BindSkills(paths, repo, []Skill{alpha}); result.Status != Bound {
		t.Fatalf("bind = %#v", result)
	}
	assertLinkTarget(t, filepath.Join(paths.Binding(), "alpha"), alphaDir)
	if _, err := os.Lstat(filepath.Join(paths.Binding(), "beta")); !os.IsNotExist(err) {
		t.Fatalf("unselected beta was connected: %v", err)
	}
	if result := BindSkills(paths, repo, []Skill{beta}); result.Status != Rebound {
		t.Fatalf("rebind = %#v", result)
	}
	if _, err := os.Lstat(filepath.Join(paths.Binding(), "alpha")); !os.IsNotExist(err) {
		t.Fatalf("deselected alpha remains: %v", err)
	}
	assertLinkTarget(t, filepath.Join(paths.Binding(), "beta"), betaDir)
	catalog, err := LoadCatalog(paths)
	if err != nil || len(catalog.Skills) != 1 || catalog.Skills[0].Dir != betaDir {
		t.Fatalf("catalog = %#v, error = %v", catalog, err)
	}
	if result := BindSkills(paths, repo, []Skill{beta}); result.Status != Already {
		t.Fatalf("same selection = %#v", result)
	}
	if result := UnbindLibrary(paths, filepath.Join(repo, "two")); result.Status != Unbound {
		t.Fatalf("unbind = %#v", result)
	}
	if _, err := os.Stat(betaDir); err != nil {
		t.Fatalf("unbind touched source: %v", err)
	}
}

func TestBoundSkillsIncludesEveryManifestEntryAndMissingSource(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	first := filepath.Join(paths.Home, "first")
	second := filepath.Join(paths.Home, "second")
	initGit(t, first)
	initGit(t, second)
	alphaDir := makeSkillIn(t, first, "tools", "alpha", "First")
	betaDir := makeSkillIn(t, second, "skills", "beta", "Second")
	if result := BindSkills(paths, first, []Skill{ReadSkill(alphaDir, first, paths.SkillsHomes())}); !result.OK() {
		t.Fatal(result.Message)
	}
	if result := BindSkills(paths, second, []Skill{ReadSkill(betaDir, second, paths.SkillsHomes())}); !result.OK() {
		t.Fatal(result.Message)
	}
	if err := os.RemoveAll(betaDir); err != nil {
		t.Fatal(err)
	}

	bound, err := BoundSkills(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(bound) != 2 || bound[0].Dir != alphaDir || bound[1].Dir != betaDir {
		t.Fatalf("bound skills = %#v", bound)
	}
	if bound[0].Description != "First" || bound[1].Description != "" {
		t.Fatalf("bound descriptions = %q, %q", bound[0].Description, bound[1].Description)
	}
	groups := NewSkillTree(bound).GroupNames()
	if len(groups) != 2 || groups[0] != filepath.Join(first, "tools") || groups[1] != second {
		t.Fatalf("bound skill groups = %#v", groups)
	}
	if result := UnbindSkills(paths, []Skill{bound[1]}); result.Status != Unbound {
		t.Fatalf("unbind missing source = %#v", result)
	}
	if _, err := os.Lstat(filepath.Join(paths.Binding(), "beta")); !os.IsNotExist(err) {
		t.Fatalf("stale catalog link remains after unbind: %v", err)
	}
	bound, err = BoundSkills(paths)
	if err != nil || len(bound) != 1 || bound[0].Name != "alpha" {
		t.Fatalf("bound skills after stale entry removal = %#v, error = %v", bound, err)
	}
}

func TestUnbindSkillsKeepsSourcesInstalledLinksAndOtherBindings(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	first := filepath.Join(paths.Home, "first")
	second := filepath.Join(paths.Home, "second")
	initGit(t, first)
	initGit(t, second)
	alphaDir := makeSkillIn(t, first, "tools", "alpha", "First")
	betaDir := makeSkillIn(t, first, "tools", "beta", "Second")
	gammaDir := makeSkillIn(t, second, "skills", "gamma", "Third")
	firstSkills := []Skill{
		ReadSkill(alphaDir, first, paths.SkillsHomes()),
		ReadSkill(betaDir, first, paths.SkillsHomes()),
	}
	if result := BindSkills(paths, first, firstSkills); !result.OK() {
		t.Fatal(result.Message)
	}
	if result := BindSkills(paths, second, []Skill{ReadSkill(gammaDir, second, paths.SkillsHomes())}); !result.OK() {
		t.Fatal(result.Message)
	}
	if result := Install(firstSkills[0]); result.Status != Installed {
		t.Fatalf("install = %#v", result)
	}

	if result := UnbindSkills(paths, []Skill{firstSkills[0]}); result.Status != Unbound || result.Message != "1 skill unbound" {
		t.Fatalf("unbind alpha = %#v", result)
	}
	if _, err := os.Stat(alphaDir); err != nil {
		t.Fatalf("unbind removed source: %v", err)
	}
	assertLinkTarget(t, filepath.Join(paths.ClaudeHome, "skills", "alpha"), alphaDir)
	if _, err := os.Lstat(filepath.Join(paths.Binding(), "alpha")); !os.IsNotExist(err) {
		t.Fatalf("catalog link remains after unbind: %v", err)
	}
	assertLinkTarget(t, filepath.Join(paths.Binding(), "beta"), betaDir)
	assertLinkTarget(t, filepath.Join(paths.Binding(), "gamma"), gammaDir)

	bound, err := BoundSkills(paths)
	if err != nil || len(bound) != 2 || bound[0].Name != "beta" || bound[1].Name != "gamma" {
		t.Fatalf("remaining bound skills = %#v, error = %v", bound, err)
	}
	if result := UnbindSkills(paths, []Skill{bound[0]}); result.Status != Unbound {
		t.Fatalf("unbind beta = %#v", result)
	}
	libraries, err := paths.Libraries()
	if err != nil || len(libraries) != 1 || libraries[0] != second {
		t.Fatalf("libraries after first repository emptied = %#v, error = %v", libraries, err)
	}
	bound, err = BoundSkills(paths)
	if err != nil || len(bound) != 1 || bound[0].Name != "gamma" {
		t.Fatalf("last bound skill = %#v, error = %v", bound, err)
	}
	if result := UnbindSkills(paths, bound); result.Status != Unbound {
		t.Fatalf("unbind gamma = %#v", result)
	}
	body, err := os.ReadFile(paths.BindingsFile())
	if err != nil || string(body) != "[]\n" {
		t.Fatalf("empty bindings = %q, error = %v", body, err)
	}
	if entries, err := os.ReadDir(paths.Binding()); err != nil || len(entries) != 0 {
		t.Fatalf("empty binding directory = %#v, error = %v", entries, err)
	}
}

func TestUnbindSkillsDoesNotReplaceAForeignCatalogRoot(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repo := filepath.Join(paths.Home, "repo")
	initGit(t, repo)
	alphaDir := makeSkillIn(t, repo, "", "alpha", "First")
	alpha := ReadSkill(alphaDir, repo, paths.SkillsHomes())
	if result := BindSkills(paths, repo, []Skill{alpha}); !result.OK() {
		t.Fatal(result.Message)
	}
	if err := os.RemoveAll(paths.Binding()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(paths.Home, paths.Binding()); err != nil {
		t.Fatal(err)
	}
	if result := UnbindSkills(paths, []Skill{alpha}); result.Status != Blocked || !strings.Contains(result.Message, "foreign symlink") {
		t.Fatalf("unbind over foreign root = %#v", result)
	}
	target, err := os.Readlink(paths.Binding())
	if err != nil || target != paths.Home {
		t.Fatalf("foreign root = %q, error = %v", target, err)
	}
	bound, err := BoundSkills(paths)
	if err != nil || len(bound) != 1 || bound[0].Name != "alpha" {
		t.Fatalf("bindings after blocked unbind = %#v, error = %v", bound, err)
	}
}

func TestBindNeverOverwritesForeignSkill(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repo := filepath.Join(paths.Home, "repo")
	initGit(t, repo)
	alphaDir := makeSkillIn(t, repo, "", "alpha", "First")
	if err := os.MkdirAll(paths.Binding(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(paths.Binding(), "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	result := BindSkills(paths, repo, []Skill{ReadSkill(alphaDir, repo, paths.SkillsHomes())})
	if result.Status != Blocked || !strings.Contains(result.Message, "real file or directory") {
		t.Fatalf("foreign destination = %#v", result)
	}
}

func TestBindMigratesLegacyWholeLibrarySymlink(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	legacy := filepath.Join(paths.Home, "legacy")
	newRepo := filepath.Join(paths.Home, "new")
	initGit(t, legacy)
	initGit(t, newRepo)
	alpha := makeSkillIn(t, filepath.Join(legacy, "skills"), "", "alpha", "Legacy")
	beta := makeSkillIn(t, newRepo, "nested", "beta", "New")
	if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(legacy, "skills"), paths.Binding()); err != nil {
		t.Fatal(err)
	}
	if result := BindLibrary(paths, newRepo); result.Status != Bound {
		t.Fatalf("bind = %#v", result)
	}
	info, err := os.Lstat(paths.Binding())
	if err != nil || !info.IsDir() {
		t.Fatalf("binding was not migrated to a directory: %v, %#v", err, info)
	}
	assertLinkTarget(t, filepath.Join(paths.Binding(), "alpha"), alpha)
	assertLinkTarget(t, filepath.Join(paths.Binding(), "beta"), beta)
	libraries, err := paths.Libraries()
	if err != nil || len(libraries) != 2 || libraries[0] != legacy || libraries[1] != newRepo {
		t.Fatalf("repositories = %#v, error = %v", libraries, err)
	}
}

func TestCatalogGroupsClashesAndTooDeep(t *testing.T) {
	paths := testPaths(t)
	makeSkill(t, paths, "one", "alpha", "First")
	makeSkill(t, paths, "two", "alpha", "Second")
	makeSkill(t, paths, "", "beta", "Loose")
	makeSkill(t, paths, filepath.Join("group", "deeper"), "buried", "Hidden")
	catalog, err := LoadCatalog(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Skills) != 3 {
		t.Fatalf("skills = %d, want 3", len(catalog.Skills))
	}
	if len(catalog.Clashes["alpha"]) != 2 {
		t.Fatalf("alpha clash = %#v", catalog.Clashes["alpha"])
	}
	if len(catalog.TooDeep) != 1 || catalog.TooDeep[0] != filepath.Join("group", "deeper", "buried") {
		t.Fatalf("too deep = %#v", catalog.TooDeep)
	}
	if _, err := catalog.Find("alpha"); err == nil || !strings.Contains(err.Error(), "one/alpha") {
		t.Fatalf("clashing find error = %v", err)
	}
}

func TestCatalogCombinesEveryBoundLibrary(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	first := filepath.Join(paths.Home, "first")
	second := filepath.Join(paths.Home, "second")
	initGit(t, first)
	initGit(t, second)
	firstPaths := paths
	firstPaths.Repo = first
	secondPaths := paths
	secondPaths.Repo = second
	makeSkill(t, firstPaths, "", "alpha", "First")
	makeSkill(t, secondPaths, "", "beta", "Second")
	for _, root := range []string{first, second} {
		if got := BindLibrary(paths, root); !got.OK() {
			t.Fatal(got.Message)
		}
	}
	catalog, err := LoadCatalog(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Skills) != 2 || catalog.Skills[0].Name != "alpha" || catalog.Skills[1].Name != "beta" {
		t.Fatalf("skills = %#v", catalog.Skills)
	}
	if len(catalog.Roots) != 2 || catalog.Root != filepath.Join(first, "skills") {
		t.Fatalf("catalog roots = %#v, primary = %s", catalog.Roots, catalog.Root)
	}
	groups := orderedGroups(catalog.Skills)
	if len(groups) != 2 || groups[0].name != "first" || groups[1].name != "second" {
		t.Fatalf("repository groups = %#v", groups)
	}
	if skill, err := catalog.Find(filepath.Join("skills", "alpha")); err != nil || skill == nil || skill.Name != "alpha" {
		t.Fatalf("find by repository path = %#v, %v", skill, err)
	}
}

func TestInstallAndUninstallUseTheChosenFamiliar(t *testing.T) {
	paths := testPaths(t)
	dir := makeSkill(t, paths, "tools", "alpha", "First")
	catalog, err := LoadCatalog(paths)
	if err != nil {
		t.Fatal(err)
	}
	skill, err := catalog.Find("alpha")
	if err != nil || skill == nil {
		t.Fatalf("find: %v %#v", err, skill)
	}
	if result := Install(*skill); result.Status != Installed {
		t.Fatalf("install = %#v", result)
	}
	if !skill.Installed() {
		t.Fatal("skill should be installed in the familiar home")
	}
	if result := Install(*skill); result.Status != AlreadyStatus {
		t.Fatalf("second install = %s", result.Status)
	}
	if result := Uninstall(*skill); result.Status != Removed {
		t.Fatalf("uninstall = %#v", result)
	}
	for _, link := range skill.LinkPaths() {
		if _, err := os.Lstat(link); !os.IsNotExist(err) {
			t.Fatalf("link still exists: %s", link)
		}
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("skill directory was touched: %v", err)
	}
}

func TestInstallDoesNotOverwriteTheFamiliarDestination(t *testing.T) {
	paths := testPaths(t)
	makeSkill(t, paths, "", "alpha", "First")
	catalog, _ := LoadCatalog(paths)
	skill, _ := catalog.Find("alpha")
	link := skill.LinkPaths()[0]
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}
	result := Install(*skill)
	if result.Status != InstallBlocked {
		t.Fatalf("install = %s, want blocked", result.Status)
	}
	if info, err := os.Stat(link); err != nil || !info.IsDir() {
		t.Fatal("the existing destination was changed")
	}
}

func TestSkillsHomeUsesOneFamiliar(t *testing.T) {
	paths := testPaths(t)
	tests := []struct {
		name string
		home string
	}{
		{name: "claude", home: filepath.Join(paths.ClaudeHome, "skills")},
		{name: "opencode", home: filepath.Join(paths.OpenCodeHome, "skills")},
		{name: "codex", home: filepath.Join(paths.CodexHome, "skills")},
		{name: "", home: ""},
	}
	for _, test := range tests {
		paths.Familiar = test.name
		got := paths.SkillsHomes()
		if test.home == "" && len(got) != 0 {
			t.Errorf("skill homes without a familiar = %#v", got)
		} else if test.home != "" && (len(got) != 1 || got[0] != test.home) {
			t.Errorf("skill home for %s = %#v, want %s", test.name, got, test.home)
		}
	}
}

func TestInstallerRequiresAFamiliar(t *testing.T) {
	paths := testPaths(t)
	dir := makeSkill(t, paths, "", "alpha", "First")
	skill := ReadSkill(dir, filepath.Join(paths.Repo, "skills"), nil)
	if result := Install(skill); result.Status != InstallBlocked || result.Message != "no familiar chosen" {
		t.Fatalf("install without familiar = %#v", result)
	}
	if result := Uninstall(skill); result.Status != InstallBlocked || result.Message != "no familiar chosen" {
		t.Fatalf("uninstall without familiar = %#v", result)
	}
}

func TestCLIConfiguresFamiliarAndCastUsesSelection(t *testing.T) {
	paths := testPaths(t)
	makeSkill(t, paths, "", "alpha", "First")
	var out bytes.Buffer
	input := strings.NewReader("")
	cli := &CLI{In: input, Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"familiar", "OpenCode"}); code != 0 {
		t.Fatalf("familiar code = %d: %s", code, out.String())
	}
	name, err := configuredFamiliar(paths)
	if err != nil || name != "opencode" {
		t.Fatalf("configured familiar = %q, error = %v", name, err)
	}
	setPathEnv(t, paths)
	reloaded := NewCLI(strings.NewReader(""), &out, &out)
	if reloaded.setupErr != nil || reloaded.familiarErr != nil || reloaded.Paths.Familiar != "opencode" {
		t.Fatalf("reloaded familiar = %q, setup error = %v, familiar error = %v", reloaded.Paths.Familiar, reloaded.setupErr, reloaded.familiarErr)
	}
	out.Reset()
	if code := reloaded.Run(context.Background(), []string{"cast", "alpha"}); code != 0 {
		t.Fatalf("cast code = %d: %s", code, out.String())
	}
	assertLinkTarget(t, filepath.Join(paths.OpenCodeHome, "skills", "alpha"), filepath.Join(paths.Repo, "skills", "alpha"))
	for _, home := range []string{paths.ClaudeHome, paths.CodexHome} {
		if _, err := os.Lstat(filepath.Join(home, "skills", "alpha")); !os.IsNotExist(err) {
			t.Fatalf("alpha was installed outside the familiar %s: %v", home, err)
		}
	}
}

func TestFamiliarConfigurationRejectsUnknownNames(t *testing.T) {
	for _, name := range []string{"other", ""} {
		if _, err := normalizeFamiliar(name); err == nil {
			t.Errorf("normalizeFamiliar(%q) did not fail", name)
		}
	}
}

func TestFamiliarChangeDoesNotMoveExistingLinks(t *testing.T) {
	paths := testPaths(t)
	source := makeSkill(t, paths, "", "alpha", "First")
	beta := makeSkill(t, paths, "", "beta", "Second")
	catalog, err := LoadCatalog(paths)
	if err != nil {
		t.Fatal(err)
	}
	if result := Install(catalog.Skills[0]); result.Status != Installed {
		t.Fatalf("install = %#v", result)
	}
	var out bytes.Buffer
	input := strings.NewReader("")
	cli := &CLI{In: input, Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"familiar", "opencode"}); code != 0 {
		t.Fatalf("familiar code = %d: %s", code, out.String())
	}
	assertLinkTarget(t, filepath.Join(paths.ClaudeHome, "skills", "alpha"), source)
	if _, err := os.Lstat(filepath.Join(paths.OpenCodeHome, "skills", "alpha")); !os.IsNotExist(err) {
		t.Fatalf("familiar change moved alpha: %v", err)
	}
	setPathEnv(t, paths)
	out.Reset()
	reloaded := NewCLI(strings.NewReader(""), &out, &out)
	if code := reloaded.Run(context.Background(), []string{"cast", "beta"}); code != 0 {
		t.Fatalf("cast after reload = %d: %s", code, out.String())
	}
	assertLinkTarget(t, filepath.Join(paths.OpenCodeHome, "skills", "beta"), beta)
	if _, err := os.Lstat(filepath.Join(paths.ClaudeHome, "skills", "beta")); !os.IsNotExist(err) {
		t.Fatalf("future cast used the old familiar: %v", err)
	}
}

func TestCommandWithoutFamiliarExplainsFirstUse(t *testing.T) {
	paths := testPaths(t)
	paths.Familiar = ""
	makeSkill(t, paths, "", "alpha", "First")
	var out bytes.Buffer
	input := strings.NewReader("")
	cli := &CLI{In: input, Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"cast", "alpha"}); code != 1 {
		t.Fatalf("cast code = %d, want 1", code)
	}
	if !strings.Contains(out.String(), "run grimoire familiar") {
		t.Fatalf("first-use error = %s", out.String())
	}
}

func TestCLIRepairsInvalidFamiliarConfiguration(t *testing.T) {
	paths := testPaths(t)
	if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.FamiliarFile(), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	setPathEnv(t, paths)
	var out bytes.Buffer
	cli := NewCLI(strings.NewReader(""), &out, &out)
	if cli.familiarErr == nil {
		t.Fatal("invalid familiar file did not cause an error")
	}
	if code := cli.Run(context.Background(), []string{"help"}); code != 0 {
		t.Fatalf("help code = %d: %s", code, out.String())
	}
	out.Reset()
	if code := cli.Run(context.Background(), []string{"familiar", "opencode"}); code != 0 {
		t.Fatalf("repair code = %d: %s", code, out.String())
	}
	reloaded := NewCLI(strings.NewReader(""), &out, &out)
	if reloaded.familiarErr != nil || reloaded.Paths.Familiar != "opencode" {
		t.Fatalf("repaired familiar = %q, error = %v", reloaded.Paths.Familiar, reloaded.familiarErr)
	}
}

func TestHoneRepairsTheFamiliarHome(t *testing.T) {
	paths := testPaths(t)
	wanted := makeSkill(t, paths, "tools", "alpha", "First")
	for _, home := range paths.SkillsHomes() {
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(paths.Home, "old-library", "skills", "alpha"), filepath.Join(home, "alpha")); err != nil {
			t.Fatal(err)
		}
	}
	changes, err := Hone(paths, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %#v", changes)
	}
	for _, home := range paths.SkillsHomes() {
		target, err := os.Readlink(filepath.Join(home, "alpha"))
		if err != nil || target != wanted {
			t.Fatalf("repaired target = %s, %v", target, err)
		}
	}
}

func TestEffigyContainsTopLevelSkillFolder(t *testing.T) {
	paths := testPaths(t)
	makeSkill(t, paths, "", "alpha", "First")
	catalog, _ := LoadCatalog(paths)
	skill, _ := catalog.Find("alpha")
	path, err := Pack(paths, *skill)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()
	found := false
	for _, file := range archive.File {
		if file.Name == "alpha/SKILL.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("archive entries = %#v", archive.File)
	}
}

func TestEffigyUsesTheSelectedSkillsRepository(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	first := filepath.Join(paths.Home, "first")
	second := filepath.Join(paths.Home, "second")
	initGit(t, first)
	initGit(t, second)
	makeSkillIn(t, first, "", "alpha", "First")
	makeSkillIn(t, second, "nested", "beta", "Second")
	if result := BindLibrary(paths, first); !result.OK() {
		t.Fatal(result.Message)
	}
	if result := BindLibrary(paths, second); !result.OK() {
		t.Fatal(result.Message)
	}
	catalog, err := LoadCatalog(paths)
	if err != nil {
		t.Fatal(err)
	}
	beta, err := catalog.Find("beta")
	if err != nil || beta == nil {
		t.Fatalf("beta = %#v, %v", beta, err)
	}
	archive, err := Pack(paths, *beta)
	if err != nil {
		t.Fatal(err)
	}
	if archive != filepath.Join(second, "output", "beta.zip") {
		t.Fatalf("archive = %s", archive)
	}
}

func TestCLIEndToEndCastAndBanish(t *testing.T) {
	paths := testPaths(t)
	makeSkill(t, paths, "", "alpha", "First")
	var out bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"cast", "alpha"}); code != 0 {
		t.Fatalf("cast code = %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "1 installed") {
		t.Fatalf("cast output = %s", out.String())
	}
	out.Reset()
	if code := cli.Run(context.Background(), []string{"banish", "alpha"}); code != 0 {
		t.Fatalf("banish code = %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "1 removed") {
		t.Fatalf("banish output = %s", out.String())
	}
}

func TestCLICommandsAcceptBoundRepositoryPaths(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repo := filepath.Join(paths.Home, "repo")
	initGit(t, repo)
	alpha := makeSkillIn(t, repo, "nested", "alpha", "First")
	if result := BindLibrary(paths, repo); !result.OK() {
		t.Fatal(result.Message)
	}
	var out bytes.Buffer
	input := strings.NewReader("")
	cli := &CLI{In: input, Out: &out, Err: &out, Paths: paths}
	selector := filepath.Join("nested", "alpha")
	if code := cli.Run(context.Background(), []string{"cast", selector}); code != 0 {
		t.Fatalf("cast code = %d: %s", code, out.String())
	}
	for _, home := range paths.SkillsHomes() {
		assertLinkTarget(t, filepath.Join(home, "alpha"), alpha)
	}
	out.Reset()
	if code := cli.Run(context.Background(), []string{"banish", selector}); code != 0 {
		t.Fatalf("banish code = %d: %s", code, out.String())
	}
	for _, home := range paths.SkillsHomes() {
		if _, err := os.Lstat(filepath.Join(home, "alpha")); !os.IsNotExist(err) {
			t.Fatalf("alpha remains in %s: %v", home, err)
		}
	}
}

func TestCatalogCommandsUseEveryBoundLibrary(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	first := filepath.Join(paths.Home, "first")
	second := filepath.Join(paths.Home, "second")
	initGit(t, first)
	initGit(t, second)
	alpha := makeSkillIn(t, filepath.Join(first, "skills"), "", "alpha", "From the first library")
	beta := makeSkillIn(t, second, "", "beta", "From the direct folder")
	for _, root := range []string{first, second} {
		if result := BindLibrary(paths, root); !result.OK() {
			t.Fatal(result.Message)
		}
	}

	var out bytes.Buffer
	input := strings.NewReader("")
	cli := &CLI{In: input, Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"toc"}); code != 0 {
		t.Fatalf("toc code = %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "alpha") || !strings.Contains(out.String(), "beta") {
		t.Fatalf("toc did not combine libraries: %s", out.String())
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"cast", "beta"}); code != 0 {
		t.Fatalf("cast code = %d: %s", code, out.String())
	}
	for _, home := range paths.SkillsHomes() {
		link := filepath.Join(home, "beta")
		target, err := os.Readlink(link)
		if err != nil || !samePath(target, beta) {
			t.Fatalf("beta link = %s, error = %v", target, err)
		}
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"banish", "beta"}); code != 0 {
		t.Fatalf("banish code = %d: %s", code, out.String())
	}
	for _, home := range paths.SkillsHomes() {
		if _, err := os.Lstat(filepath.Join(home, "beta")); !os.IsNotExist(err) {
			t.Fatalf("beta link remains in %s: %v", home, err)
		}
	}

	out.Reset()
	if code := cli.Run(context.Background(), []string{"volley"}); code != 0 {
		t.Fatalf("volley code = %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "installed 2") {
		t.Fatalf("volley output = %s", out.String())
	}
	for name, source := range map[string]string{"alpha": alpha, "beta": beta} {
		for _, home := range paths.SkillsHomes() {
			target, err := os.Readlink(filepath.Join(home, name))
			if err != nil || !samePath(target, source) {
				t.Fatalf("%s link = %s, error = %v", name, target, err)
			}
		}
	}
}

func TestTOCRejectsArguments(t *testing.T) {
	paths := testPaths(t)
	makeSkill(t, paths, "", "alpha", "First")
	var out bytes.Buffer
	input := strings.NewReader("")
	cli := &CLI{In: input, Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"toc", "alpha"}); code != 1 {
		t.Fatalf("toc code = %d, want 1", code)
	}
	if !strings.Contains(out.String(), "toc takes no arguments") {
		t.Fatalf("toc error = %s", out.String())
	}
}

func TestCatalogBrowserKeepsBookPageStylingWhileFiltering(t *testing.T) {
	paths := testPaths(t)
	makeSkill(t, paths, "spells", "alpha", "First charm")
	makeSkill(t, paths, "spells", "beta", "Second charm")
	catalog, err := LoadCatalog(paths)
	if err != nil {
		t.Fatal(err)
	}
	browser := CatalogBrowser{Theme: Theme{columns: 60}, Familiar: "claude"}
	lines, _ := browser.frame(catalog.Skills, "second", 0, 60, 24)
	if len(lines) != 24 {
		t.Fatalf("browser height = %d, want 24", len(lines))
	}
	for index, line := range lines {
		if width := visibleWidth(line); width != 60 {
			t.Errorf("line %d width = %d, want 60: %q", index, width, line)
		}
	}
	page := strings.Join(lines, "\n")
	for _, want := range []string{"╓", spaced("grimoire"), "table of contents", "I.", "spells", "beta", "Second charm", "not cast", "search", "familiar: claude"} {
		if !strings.Contains(page, want) {
			t.Errorf("browser page does not contain %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "alpha") || strings.Contains(page, "First charm") {
		t.Fatalf("browser search did not filter the page:\n%s", page)
	}
}

func TestCatalogBrowserUsesTheTerminalWidth(t *testing.T) {
	paths := testPaths(t)
	makeSkill(t, paths, "", "alpha", "First charm")
	catalog, err := LoadCatalog(paths)
	if err != nil {
		t.Fatal(err)
	}
	lines, _ := (CatalogBrowser{Theme: Theme{columns: 120}}).frame(catalog.Skills, "", 0, 120, 24)
	for index, line := range lines {
		if width := visibleWidth(line); width != 120 {
			t.Errorf("line %d width = %d, want 120", index, width)
		}
	}
}

func TestCatalogBrowserFitsNarrowAndSmallViewports(t *testing.T) {
	skills := []Skill{{
		Name:        strings.Repeat("long-skill-name-", 5),
		Description: strings.Repeat("long description ", 8),
		Group:       strings.Repeat("long-group-", 5),
	}}
	browser := CatalogBrowser{Theme: Theme{color: true, columns: 40}, Familiar: "opencode"}
	narrow := viewport{columns: 40, rows: 13}
	lines, _ := browser.frame(skills, "a long search query", 0, narrow.columns, narrow.rows)
	assertFrameFits(t, lines, narrow)

	small := viewport{columns: 30, rows: 6}
	lines, _ = browser.frame(skills, "", 0, small.columns, small.rows)
	assertFrameFits(t, lines, small)
	if !strings.Contains(strings.Join(lines, "\n"), "resize terminal") {
		t.Fatalf("small catalog frame has no resize guidance:\n%s", strings.Join(lines, "\n"))
	}
}

func TestEmptyTOCShowsTheFamiliar(t *testing.T) {
	paths := testPaths(t)
	var out bytes.Buffer
	cli := &CLI{Out: &out, Err: &out, Paths: paths}
	cli.printTOC(Catalog{})
	if !strings.Contains(out.String(), "an empty book  ·  familiar: claude") {
		t.Fatalf("empty toc does not show familiar:\n%s", out.String())
	}
}

func TestFamiliarPickerUsesCompactAndFullCatArt(t *testing.T) {
	paths := testPaths(t)
	picker := FamiliarPicker{Theme: Theme{columns: 80}, Home: paths.Home}
	compact, usedFull := picker.frame(availableFamiliars(paths), 0, "", false, 80, 24)
	if usedFull || len(compact) != 24 {
		t.Fatalf("compact frame: full = %v, height = %d", usedFull, len(compact))
	}
	compactPage := strings.Join(compact, "\n")
	for _, want := range []string{spaced("familiar"), "/\\_/\\", "Meow", "Claude Code", "OpenCode", "Codex", "the circle awaits"} {
		if !strings.Contains(compactPage, want) {
			t.Errorf("compact familiar page does not contain %q:\n%s", want, compactPage)
		}
	}

	picker.Theme = Theme{color: true, columns: 180}
	full, usedFull := picker.frame(availableFamiliars(paths), 1, "claude", false, 180, 60)
	if !usedFull || len(full) != 60 {
		t.Fatalf("full frame: full = %v, height = %d", usedFull, len(full))
	}
	fullPage := strings.Join(full, "\n")
	if !strings.Contains(fullPage, "⢀⣀⣀⣀⣀⣠⣤⣤⣤⣄⡀") {
		t.Fatal("full familiar page does not contain the supplied cat")
	}
	if !strings.Contains(fullPage, "\x1b[38;5;214m⠟⠇") || !strings.Contains(fullPage, "\x1b[38;5;214mMeow") {
		t.Fatal("full familiar cat does not have amber eyes and Meow text")
	}
	boundary, boundaryFull := picker.frame(availableFamiliars(paths), 0, "", false, 180, 41)
	if !boundaryFull || len(boundary) != 41 {
		t.Fatalf("boundary frame: full = %v, height = %d", boundaryFull, len(boundary))
	}
}

func TestFamiliarPickerFitsNarrowAndSmallViewports(t *testing.T) {
	paths := testPaths(t)
	picker := FamiliarPicker{Theme: Theme{color: true, columns: 40}, Home: paths.Home}
	narrow := viewport{columns: 40, rows: 19}
	lines, usedFull := picker.frame(availableFamiliars(paths), 2, "opencode", false, narrow.columns, narrow.rows)
	if usedFull {
		t.Fatal("narrow familiar frame used the full art")
	}
	assertFrameFits(t, lines, narrow)

	small := viewport{columns: 24, rows: 8}
	lines, _ = picker.frame(availableFamiliars(paths), 0, "", false, small.columns, small.rows)
	assertFrameFits(t, lines, small)
}

func TestCLIBindUsesCurrentDirectory(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repo := filepath.Join(paths.Home, "cli-repo")
	initGit(t, repo)
	alpha := makeSkillIn(t, repo, "nested", "alpha", "First")
	t.Chdir(filepath.Join(repo, "nested"))
	var out bytes.Buffer
	input := strings.NewReader("")
	cli := &CLI{In: input, Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"bind", "alpha"}); code != 0 {
		t.Fatalf("bind code = %d: %s", code, out.String())
	}
	assertLinkTarget(t, filepath.Join(paths.Binding(), "alpha"), alpha)
}

func TestCLIBindAcceptsRepositorySkillPaths(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repo := filepath.Join(paths.Home, "repo")
	initGit(t, repo)
	alpha := makeSkillIn(t, repo, "deep/tools", "alpha", "First")
	t.Chdir(repo)
	var out bytes.Buffer
	input := strings.NewReader("")
	cli := &CLI{In: input, Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"bind", filepath.Join("deep", "tools", "alpha")}); code != 0 {
		t.Fatalf("bind code = %d: %s", code, out.String())
	}
	assertLinkTarget(t, filepath.Join(paths.Binding(), "alpha"), alpha)
	if !strings.Contains(out.String(), spaced("bind")) || !strings.Contains(out.String(), sealSigil[0]) {
		t.Fatalf("bind page lost the Ruby UI:\n%s", out.String())
	}
}

func TestCLIUnbindAcceptsMultipleBoundSkillsFromAnyDirectory(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repo := filepath.Join(paths.Home, "repo")
	initGit(t, repo)
	makeSkillIn(t, repo, "one", "alpha", "First")
	makeSkillIn(t, repo, "two", "beta", "Second")
	t.Chdir(repo)
	var out bytes.Buffer
	input := strings.NewReader("")
	cli := &CLI{In: input, Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"bind", "alpha", "beta"}); code != 0 {
		t.Fatalf("bind code = %d: %s", code, out.String())
	}
	libraries, err := paths.Libraries()
	if err != nil || len(libraries) != 1 || libraries[0] != repo {
		t.Fatalf("libraries = %#v, error = %v", libraries, err)
	}
	outside := filepath.Join(paths.Home, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(outside)
	out.Reset()
	betaSelector := "." + string(filepath.Separator) + filepath.Join("two", "beta") + string(filepath.Separator)
	if code := cli.Run(context.Background(), []string{"unbind", "alpha", betaSelector}); code != 0 {
		t.Fatalf("unbind code = %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), spaced("unbind")) || !strings.Contains(out.String(), "alpha") || !strings.Contains(out.String(), "beta") {
		t.Fatalf("unbind output = %s", out.String())
	}
	if bound, err := BoundSkills(paths); err != nil || len(bound) != 0 {
		t.Fatalf("bound skills after unbind = %#v, error = %v", bound, err)
	}
}

func TestCLIUnbindValidatesEveryNameBeforeChangingBindings(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repo := filepath.Join(paths.Home, "repo")
	initGit(t, repo)
	alphaDir := makeSkillIn(t, repo, "one", "alpha", "First")
	if result := BindSkills(paths, repo, []Skill{ReadSkill(alphaDir, repo, paths.SkillsHomes())}); !result.OK() {
		t.Fatal(result.Message)
	}
	var out bytes.Buffer
	input := strings.NewReader("")
	cli := &CLI{In: input, Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"unbind", "alpha", "missing"}); code != 1 {
		t.Fatalf("unbind code = %d, want 1: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "no bound skill called missing") {
		t.Fatalf("unbind error = %s", out.String())
	}
	assertLinkTarget(t, filepath.Join(paths.Binding(), "alpha"), alphaDir)
}

func TestCLIUnbindWithoutTerminalRequiresAName(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repo := filepath.Join(paths.Home, "repo")
	initGit(t, repo)
	alphaDir := makeSkillIn(t, repo, "", "alpha", "First")
	if result := BindSkills(paths, repo, []Skill{ReadSkill(alphaDir, repo, paths.SkillsHomes())}); !result.OK() {
		t.Fatal(result.Message)
	}
	var out bytes.Buffer
	input := strings.NewReader("")
	cli := &CLI{In: input, Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"unbind"}); code != 1 {
		t.Fatalf("unbind code = %d, want 1: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "Pass skill names or paths instead") {
		t.Fatalf("unbind error = %s", out.String())
	}
	assertLinkTarget(t, filepath.Join(paths.Binding(), "alpha"), alphaDir)
}

func TestCLIUnbindReportsAnEmptyCatalog(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	var out bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"unbind"}); code != 0 {
		t.Fatalf("unbind code = %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "no skills are bound") {
		t.Fatalf("empty unbind output = %s", out.String())
	}
	out.Reset()
	if code := cli.Run(context.Background(), []string{"unbind", "missing"}); code != 1 {
		t.Fatalf("explicit empty unbind code = %d, want 1: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "no bound skill called missing") {
		t.Fatalf("explicit empty unbind output = %s", out.String())
	}
}

func TestPageBindingKeepsEveryLineTheSameWidth(t *testing.T) {
	page := NewPage(Theme{columns: 60})
	lines := page.Bind([]string{"one", page.Centered("two")}, "grimoire", "a subtitle", "folio")
	for index, line := range lines {
		if width := visibleWidth(line); width != 60 {
			t.Errorf("line %d width = %d, want 60: %q", index, width, line)
		}
	}
}

func TestPageUsesSmallReportedWidths(t *testing.T) {
	for _, columns := range []int{7, 18, 39} {
		page := NewPage(Theme{columns: columns})
		if page.Width != columns {
			t.Errorf("page width = %d, want reported width %d", page.Width, columns)
			continue
		}
		for index, line := range page.Bind([]string{strings.Repeat("long content ", 8)}, "grimoire", "subtitle", "folio") {
			if width := visibleWidth(line); width != columns {
				t.Errorf("%d-column page line %d width = %d: %q", columns, index, width, line)
			}
		}
	}
}

func TestPageClipsLongPaintedContentToItsWidth(t *testing.T) {
	theme := Theme{color: true, columns: 40}
	page := NewPage(theme)
	status := theme.Paint("installed", Green)
	body := []string{
		theme.Paint(strings.Repeat("x", 100), Violet),
		page.Fill(theme.Paint(strings.Repeat("long-name-", 10), Violet), status, "· "),
	}
	lines := page.Bind(body, strings.Repeat("title", 20), strings.Repeat("subtitle", 20), strings.Repeat("folio", 20))
	for index, line := range lines {
		if width := visibleWidth(line); width != page.Width {
			t.Errorf("line %d width = %d, want %d: %q", index, width, page.Width, line)
		}
	}
	if !strings.Contains(strings.Join(lines, "\n"), "installed") {
		t.Fatal("a narrow filled line removed its right-hand status")
	}
}

func TestNarrowHelpStacksDescriptionsWithoutClipping(t *testing.T) {
	theme := Theme{columns: 40}
	page := NewPage(theme)
	body := appendHelpBlock(nil, page, theme, "commands", [][2]string{
		{"cast [SKILL]", "Installs one bound skill. Without SKILL, opens the tree."},
	})
	lines := page.Bind(body, "help", "commands", "help")
	for index, line := range lines {
		if width := visibleWidth(line); width != 40 {
			t.Errorf("line %d width = %d, want 40: %q", index, width, line)
		}
	}
	plain := strings.Join(lines, "\n")
	for _, want := range []string{"cast [SKILL]", "Installs one bound skill.", "Without SKILL, opens the", "tree."} {
		if !strings.Contains(plain, want) {
			t.Errorf("narrow help does not contain %q:\n%s", want, plain)
		}
	}
}

func TestPickerFramesFitLongContentAndShortScreens(t *testing.T) {
	row := PickerRow{
		Name:        strings.Repeat("long-name-", 8),
		Description: strings.Repeat("long description ", 8),
		Skills:      []Skill{{Name: "long"}},
	}
	picker := Picker{Prompt: strings.Repeat("prompt ", 5), Theme: Theme{color: true, columns: 20}}
	size := viewport{columns: 20, rows: 4}
	lines := picker.frame([]PickerRow{row}, strings.Repeat("query", 8), 0, map[string]Skill{}, size)
	assertFrameFits(t, lines, size)

	small := viewport{columns: 12, rows: 2}
	lines = picker.frame([]PickerRow{row}, "", 0, map[string]Skill{}, small)
	assertFrameFits(t, lines, small)
}

func TestAnimationFramesFollowTheCurrentViewport(t *testing.T) {
	body := []string{strings.Repeat("x", 80), "last"}
	for _, size := range []viewport{{columns: 80, rows: 24}, {columns: 18, rows: 5}} {
		assertFrameFits(t, animationFrame(body, size), size)
	}
}

func TestEveryNerdFontIconIsOneRune(t *testing.T) {
	for name, icon := range icons {
		if count := utf8.RuneCountInString(icon); count != 1 {
			t.Errorf("icon %s has %d runes: %q", name, count, icon)
		}
	}
}

func TestHelpAndSpellResultsCarryOriginalVisualLanguage(t *testing.T) {
	paths := testPaths(t)
	makeSkill(t, paths, "", "alpha", "First")
	var out bytes.Buffer
	input := strings.NewReader("")
	cli := &CLI{In: input, Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"help"}); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(out.String(), tomeSigil[0]) || !strings.Contains(out.String(), "H O W   T O   S A Y   I T") {
		t.Fatalf("help page:\n%s", out.String())
	}
	for _, want := range []string{"toc | list | ls", "unbind [SKILL...]", "Forgets bound skills.", "help | -h | --help", "Full-screen views redraw after a terminal resize"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help page does not contain %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "unbind [PATH") {
		t.Fatalf("help still describes repository unbind:\n%s", out.String())
	}
	out.Reset()
	if code := cli.Run(context.Background(), []string{"cast", "alpha"}); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(out.String(), wandSigil[0]) || !strings.Contains(out.String(), "1 installed") {
		t.Fatalf("cast output:\n%s", out.String())
	}
}

func TestMatchScorePrefersTightEarlyMatches(t *testing.T) {
	tight, ok := MatchScore("cat", "cat")
	if !ok {
		t.Fatal("cat should match cat")
	}
	loose, ok := MatchScore("cat", "xxc-a-t-herder")
	if !ok || tight >= loose {
		t.Fatalf("tight=%d loose=%d ok=%v", tight, loose, ok)
	}
	if _, ok := MatchScore("ksa", "some-real-skill"); ok {
		t.Fatal("out-of-order query matched")
	}
}

func TestSkillTreeGroupsRowsAndFiltering(t *testing.T) {
	paths := testPaths(t)
	makeSkill(t, paths, "coding", "alpha", "First")
	makeSkill(t, paths, "coding", "beta", "Second")
	makeSkill(t, paths, "", "loose", "Outside")
	catalog, err := LoadCatalog(paths)
	if err != nil {
		t.Fatal(err)
	}
	tree := NewSkillTree(catalog.Skills)
	rows := tree.Rows("", map[string]bool{"coding": false})
	if len(rows) != 2 || rows[0].Name != "loose" || !rows[1].Group || len(rows[1].Skills) != 2 {
		t.Fatalf("closed rows = %#v", rows)
	}
	rows = tree.Rows("bt", map[string]bool{"coding": false})
	if len(rows) != 2 || !rows[0].Group || rows[1].Name != "beta" || !rows[0].Open {
		t.Fatalf("filtered rows = %#v", rows)
	}
}

func TestSkillTreeSearchesDisplayedRepositoryNames(t *testing.T) {
	tree := NewSkillTree([]Skill{
		{Name: "alpha", Repository: filepath.Join(t.TempDir(), "work")},
		{Name: "beta", Repository: filepath.Join(t.TempDir(), "personal")},
	})
	rows := tree.Rows("personal", map[string]bool{})
	if len(rows) != 2 || !rows[0].Group || rows[0].Name != "personal" || rows[1].Name != "beta" {
		t.Fatalf("repository search rows = %#v", rows)
	}
}

func TestReadKeyRecognizesArrowsAndLoneEscape(t *testing.T) {
	for sequence, want := range map[string]string{
		"\x1b[A": "up",
		"\x1b[B": "down",
		"\x1b[C": "right",
		"\x1b[D": "left",
		"\x1b":   "escape",
		"\x04":   "escape",
	} {
		reader := bufio.NewReader(strings.NewReader(sequence))
		got, err := readKey(reader, nil)
		if err != nil || got != want {
			t.Errorf("readKey(%q) = %q, %v; want %q", sequence, got, err, want)
		}
	}
}

func gitCommitAll(t *testing.T, dir, message string) {
	t.Helper()
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.name=Grimoire", "-c", "user.email=grimoire@test", "commit", "-q", "-m", message},
	} {
		command := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
		}
	}
}

func gitClone(t *testing.T, origin, clone string) {
	t.Helper()
	if output, err := exec.Command("git", "clone", "-q", origin, clone).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v: %s", err, output)
	}
}

func TestIndexRefreshPullsOnlyCleanRepositories(t *testing.T) {
	paths := testPaths(t)
	origin := filepath.Join(paths.Home, "origin")
	clone := filepath.Join(paths.Home, "clone")
	initGit(t, origin)
	makeSkillIn(t, origin, "", "alpha", "First")
	gitCommitAll(t, origin, "one")
	gitClone(t, origin, clone)

	got := PullLatest(clone)
	if got.Status != CurrentBranch {
		t.Fatalf("clean refresh with nothing new = %#v", got)
	}

	makeSkillIn(t, origin, "", "beta", "Second")
	gitCommitAll(t, origin, "two")
	got = PullLatest(clone)
	if got.Status != Pulled || !strings.Contains(got.Message, "updated") {
		t.Fatalf("refresh with new commits = %#v", got)
	}
	if _, err := os.Stat(filepath.Join(clone, "beta", "SKILL.md")); err != nil {
		t.Fatalf("pulled commit did not arrive: %v", err)
	}
	if got := PullLatest(clone); got.Status != CurrentBranch {
		t.Fatalf("second refresh = %#v", got)
	}

	if err := os.WriteFile(filepath.Join(clone, "draft.md"), []byte("work in progress"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := PullLatest(clone); got.Status != RefreshSkipped {
		t.Fatalf("dirty repository = %#v", got)
	}

	plain := filepath.Join(paths.Home, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := PullLatest(plain); got.Status != RefreshFailed {
		t.Fatalf("non-repository = %#v", got)
	}
	if got := PullLatest(filepath.Join(paths.Home, "missing")); got.Status != RefreshFailed {
		t.Fatalf("missing folder = %#v", got)
	}
}

func TestCLIIndexReportsAndRefreshesBoundRepositories(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	origin := filepath.Join(paths.Home, "origin")
	clone := filepath.Join(paths.Home, "clone")
	initGit(t, origin)
	makeSkillIn(t, origin, "", "alpha", "First")
	gitCommitAll(t, origin, "one")
	gitClone(t, origin, clone)
	if result := BindLibrary(paths, clone); !result.OK() {
		t.Fatal(result.Message)
	}

	var out bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"index"}); code != 0 {
		t.Fatalf("index code = %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "clone") || !strings.Contains(out.String(), "1 skill bound") || !strings.Contains(out.String(), "1 repositories") {
		t.Fatalf("index output = %s", out.String())
	}
	out.Reset()
	if code := cli.Run(context.Background(), []string{"index", "--bogus"}); code != 1 {
		t.Fatalf("bad option code = %d, want 1", code)
	}
	out.Reset()
	if code := cli.Run(context.Background(), []string{"index", "--refresh"}); code != 0 {
		t.Fatalf("refresh code = %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "already up to date") {
		t.Fatalf("refresh output = %s", out.String())
	}
}

func TestCLIIndexNeedsABoundRepository(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	var out bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &out, Err: &out, Paths: paths}
	if code := cli.Run(context.Background(), []string{"index"}); code != 1 {
		t.Fatalf("index code = %d, want 1", code)
	}
	if !strings.Contains(out.String(), "no skills are bound") {
		t.Fatalf("index error = %s", out.String())
	}
}

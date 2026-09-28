package grimoire

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeHookIn(t *testing.T, repository, group, name, description string) string {
	t.Helper()
	dir := filepath.Join(repository, "hooks")
	if group != "" {
		dir = filepath.Join(dir, group)
	}
	dir = filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "HOOK.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHookDiscoveryIsScopedRegularAndKindAware(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	initGit(t, repository)
	skillDir := makeSkillIn(t, repository, "deep/group", "alpha", "skill")
	hookDir := makeHookIn(t, repository, "group/deep", "alpha", "hook")

	outside := filepath.Join(repository, "app", "false-hook")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "HOOK.md"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkedSkill := filepath.Join(repository, "symlink-skill")
	if err := os.MkdirAll(symlinkedSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(skillDir, "SKILL.md"), filepath.Join(symlinkedSkill, "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	root, resources, err := DiscoverRepositoryResources(paths, filepath.Join(repository, "hooks", "group"))
	if err != nil || !samePath(root, repository) {
		t.Fatalf("discovery root=%s error=%v", root, err)
	}
	if len(resources) != 3 {
		t.Fatalf("resources=%#v", resources)
	}
	byIdentity := map[string]Skill{}
	for _, resource := range resources {
		byIdentity[resource.identity()] = resource
	}
	if _, ok := byIdentity[(Skill{Kind: SkillKind, Dir: skillDir}).identity()]; !ok {
		t.Fatalf("regular skill missing: %#v", resources)
	}
	if _, ok := byIdentity[(Skill{Kind: SkillKind, Dir: symlinkedSkill}).identity()]; !ok {
		t.Fatalf("released symlink skill behavior changed: %#v", resources)
	}
	foundHook, ok := byIdentity[(Skill{Kind: HookKind, Dir: hookDir}).identity()]
	if !ok || foundHook.MarkerPath() != filepath.Join(hookDir, "HOOK.md") {
		t.Fatalf("hook=%#v", foundHook)
	}
	_, skillsOnly, err := DiscoverRepositorySkills(repository, paths.SkillsHomes())
	if err != nil || len(skillsOnly) != 2 {
		t.Fatalf("skill-only API resources=%#v error=%v", skillsOnly, err)
	}
	for _, resource := range skillsOnly {
		if resource.Kind != SkillKind {
			t.Fatalf("skill-only API returned %#v", resource)
		}
	}
}

func TestHookBindingRoundTripSelectorsAndDestinations(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	initGit(t, repository)
	skillDir := makeSkillIn(t, repository, "tools", "shared", "skill")
	hookDir := makeHookIn(t, repository, "git", "shared", "hook")
	skill := ReadSkill(skillDir, repository, paths.SkillsHomes())
	hook := ReadHook(hookDir, repository, paths.HooksHomes())

	result := BindSkills(paths, repository, []Skill{skill, hook})
	if !result.OK() {
		t.Fatal(result.Message)
	}
	assertLinkTarget(t, filepath.Join(paths.ConfigHome, "skills", "shared"), skillDir)
	assertLinkTarget(t, filepath.Join(paths.ConfigHome, "hooks", "shared"), hookDir)

	body, err := os.ReadFile(paths.BindingsFile())
	if err != nil || !strings.Contains(string(body), `"version": 2`) || !strings.Contains(string(body), `"hooks"`) {
		t.Fatalf("v2 bindings=%s error=%v", body, err)
	}
	repositories, err := BoundRepositories(paths)
	if err != nil || len(repositories) != 1 || len(repositories[0].Skills) != 1 || len(repositories[0].Hooks) != 1 {
		t.Fatalf("round trip=%#v error=%v", repositories, err)
	}
	catalog, err := LoadCatalog(paths)
	if err != nil {
		t.Fatal(err)
	}
	if found, findErr := catalog.Find("shared"); found != nil || findErr == nil {
		t.Fatalf("bare selector=%#v error=%v", found, findErr)
	}
	foundSkill, err := catalog.Find("skill:shared")
	if err != nil || foundSkill == nil || foundSkill.Kind != SkillKind {
		t.Fatalf("skill selector=%#v error=%v", foundSkill, err)
	}
	foundHook, err := catalog.Find("hook:hooks/git/shared")
	if err != nil || foundHook == nil || foundHook.Kind != HookKind {
		t.Fatalf("hook selector=%#v error=%v", foundHook, err)
	}
	if result := Install(paths, *foundSkill); result.Status != Installed {
		t.Fatalf("install skill=%#v", result)
	}
	if result := Install(paths, *foundHook); result.Status != Installed {
		t.Fatalf("install hook=%#v", result)
	}
	assertLinkTarget(t, filepath.Join(paths.ClaudeHome, "skills", "shared"), skillDir)
	assertLinkTarget(t, filepath.Join(paths.ClaudeHome, "hooks", "shared"), hookDir)
}

func TestHookSameKindClashAndForeignCatalogBlockerAreAtomic(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	first := filepath.Join(paths.Home, "first")
	second := filepath.Join(paths.Home, "second")
	initGit(t, first)
	initGit(t, second)
	one := ReadHook(makeHookIn(t, first, "a", "duplicate", "one"), first, paths.HooksHomes())
	two := ReadHook(makeHookIn(t, second, "b", "duplicate", "two"), second, paths.HooksHomes())
	if result := BindSkills(paths, first, []Skill{one}); !result.OK() {
		t.Fatal(result.Message)
	}
	blockedInstall := filepath.Join(paths.ClaudeHome, "hooks", "duplicate")
	if err := os.MkdirAll(blockedInstall, 0o755); err != nil {
		t.Fatal(err)
	}
	if result := Install(paths, one); result.Status != InstallBlocked || !strings.Contains(result.Message, "real folder") {
		t.Fatalf("foreign install blocker=%#v", result)
	}
	if result := BindSkills(paths, second, []Skill{two}); result.Status != Blocked || !strings.Contains(result.Message, "hook duplicate") {
		t.Fatalf("same-kind clash=%#v", result)
	}

	other := ReadHook(makeHookIn(t, second, "b", "other", "other"), second, paths.HooksHomes())
	foreign := filepath.Join(paths.Home, "foreign")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, filepath.Join(paths.ConfigHome, "hooks", "other")); err != nil {
		t.Fatal(err)
	}
	if result := BindSkills(paths, second, []Skill{other}); result.Status != Blocked || !strings.Contains(result.Message, "foreign link") {
		t.Fatalf("foreign blocker=%#v", result)
	}
	repositories, err := BoundRepositories(paths)
	if err != nil || len(repositories) != 1 || !samePath(repositories[0].Path, first) {
		t.Fatalf("state changed=%#v error=%v", repositories, err)
	}
	assertLinkTarget(t, filepath.Join(paths.ConfigHome, "hooks", "duplicate"), one.Dir)
}

func TestDirectoryCanRepresentBothSkillAndHook(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	initGit(t, repository)
	dir := makeHookIn(t, repository, "", "dual", "hook side")
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: dual\ndescription: skill side\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, resources, err := DiscoverRepositoryResources(paths, repository)
	if err != nil || len(resources) != 2 {
		t.Fatalf("resources=%#v error=%v", resources, err)
	}
	if !samePath(resources[0].Dir, dir) || !samePath(resources[1].Dir, dir) || resources[0].Kind == resources[1].Kind {
		t.Fatalf("dual resources=%#v", resources)
	}
	if result := BindSkills(paths, repository, resources); !result.OK() {
		t.Fatal(result.Message)
	}
	assertLinkTarget(t, filepath.Join(paths.ConfigHome, "skills", "dual"), dir)
	assertLinkTarget(t, filepath.Join(paths.ConfigHome, "hooks", "dual"), dir)
}

func TestHookLegacyStateMigrationHoneAndEffigy(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	initGit(t, repository)
	skillDir := makeSkillIn(t, repository, "", "alpha", "skill")
	hookDir := makeHookIn(t, repository, "", "preflight", "hook")
	if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal([]BoundRepository{{Path: repository, Skills: []string{"alpha"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.BindingsFile(), append(legacy, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	// The old skill-only array remains readable and is upgraded only when a hook is selected.
	if result := BindSkills(paths, repository, []Skill{ReadHook(hookDir, repository, paths.HooksHomes())}); !result.OK() {
		t.Fatal(result.Message)
	}
	assertLinkTarget(t, filepath.Join(paths.ConfigHome, "skills", "alpha"), skillDir)
	assertLinkTarget(t, filepath.Join(paths.ConfigHome, "hooks", "preflight"), hookDir)

	hook := ReadHook(hookDir, repository, paths.HooksHomes())
	link := filepath.Join(paths.ClaudeHome, "hooks", hook.Name)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hook.Dir, link); err != nil {
		t.Fatal(err)
	}
	changes, err := Hone(paths, true)
	if err != nil || len(changes) != 1 || changes[0].Action != "adopted" {
		t.Fatalf("dry-run=%#v error=%v", changes, err)
	}
	if ownership, _ := configuredOwnership(paths); len(ownership.Links) != 0 {
		t.Fatalf("dry-run wrote ownership=%#v", ownership)
	}
	changes, err = Hone(paths, false)
	if err != nil || len(changes) != 1 || !ownershipProves(mustOwnership(t, paths), link) {
		t.Fatalf("hone=%#v error=%v", changes, err)
	}

	paths.Output = filepath.Join(paths.Home, "output")
	archive, err := Pack(paths, hook)
	if err != nil {
		t.Fatal(err)
	}
	packed, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = packed.Close() }()
	foundMarker := false
	for _, file := range packed.File {
		if file.Name == "preflight/HOOK.md" {
			foundMarker = true
		}
	}
	if !foundMarker {
		t.Fatalf("HOOK.md missing from %s", archive)
	}
}

func TestHookCommandsAliasesAndOutputModes(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	initGit(t, repository)
	skill := ReadSkill(makeSkillIn(t, repository, "", "alpha", "skill"), repository, paths.SkillsHomes())
	hook := ReadHook(makeHookIn(t, repository, "", "preflight", "hook"), repository, paths.HooksHomes())
	if result := BindSkills(paths, repository, []Skill{skill, hook}); !result.OK() {
		t.Fatal(result.Message)
	}
	paths.Output = filepath.Join(paths.Home, "output")
	var output bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &output, Err: &output, Paths: paths, Config: Config{Boring: true}}
	run := func(args ...string) string {
		t.Helper()
		output.Reset()
		if code := cli.Run(context.Background(), args); code != 0 {
			t.Fatalf("%v code=%d output=%s", args, code, output.String())
		}
		return output.String()
	}
	for _, alias := range []string{"toc", "list", "ls"} {
		if body := run(alias); !strings.Contains(body, "alpha") || !strings.Contains(body, "hook:preflight") {
			t.Fatalf("%s output=%s", alias, body)
		}
	}
	if body := run("cast", "hook:preflight"); !strings.Contains(body, "hook:preflight installed") {
		t.Fatal(body)
	}
	if body := run("banish", "hook:preflight"); !strings.Contains(body, "hook:preflight removed") {
		t.Fatal(body)
	}
	if body := run("volley"); !strings.Contains(body, "installed=") {
		t.Fatal(body)
	}
	if body := run("hone", "--dry-run"); !strings.Contains(body, "dry run") {
		t.Fatal(body)
	}
	if body := run("effigy", "hook:preflight"); !strings.Contains(body, "packed preflight") {
		t.Fatal(body)
	}
	if body := run("unbond", "hook:preflight"); !strings.Contains(body, "preflight unbound") {
		t.Fatal(body)
	}
	makeHookIn(t, repository, "nested", "second", "second hook")
	t.Chdir(repository)
	if body := run("bond", "hook:second"); !strings.Contains(body, "hook bound") {
		t.Fatal(body)
	}
	if body := run("index"); !strings.Contains(body, "hooks=") || !strings.Contains(body, "1 skill and 1 hook bound") {
		t.Fatal(body)
	}
	if body := run("familiar", "codex"); !strings.Contains(body, "skills=") || !strings.Contains(body, "hooks=") {
		t.Fatal(body)
	}
	if _, err := os.Stat(filepath.Join(paths.Output, "hooks", "preflight.zip")); err != nil {
		t.Fatal(err)
	}

	// Rich non-terminal rendering identifies hooks without depending on color or
	// Nerd Font availability. The icon itself is controlled by Theme.icons.
	output.Reset()
	cli.Config.Boring = false
	if code := cli.Run(context.Background(), []string{"toc"}); code != 0 || !strings.Contains(output.String(), "alpha") {
		t.Fatalf("rich toc code=%d output=%s", code, output.String())
	}
	page := newPageWidth(Theme{icons: true, columns: 80}, 80)
	body, _ := tocSkillBody(page, Theme{icons: true, columns: 80}, []Skill{hook}, false)
	if !strings.Contains(strings.Join(body, "\n"), icons["hook"]) {
		t.Fatalf("hook icon missing: %#v", body)
	}
	body, _ = tocSkillBody(page, Theme{icons: false, columns: 80}, []Skill{hook}, false)
	if strings.Contains(strings.Join(body, "\n"), icons["hook"]) {
		t.Fatalf("hook icon ignored icon-off theme: %#v", body)
	}
}

func TestHookRefreshRenameRepairsCatalogAndInstalledLinks(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	initGit(t, repository)
	oldDir := makeHookIn(t, repository, "", "before", "same hook")
	gitCommitAll(t, repository, "before")
	before := repositoryRevision(repository)
	hook := ReadHook(oldDir, repository, paths.HooksHomes())
	if result := BindSkills(paths, repository, []Skill{hook}); !result.OK() {
		t.Fatal(result.Message)
	}
	if result := Install(paths, hook); result.Status != Installed {
		t.Fatalf("install=%#v", result)
	}
	newDir := filepath.Join(repository, "hooks", "after")
	if err := os.Rename(oldDir, newDir); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(newDir, "HOOK.md")
	body, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(strings.Replace(string(body), "name: before", "name: after", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, repository, "after")
	after := repositoryRevision(repository)
	renamed := skillRenames(repository, before, after)
	if renamed["hook:hooks/before"] != filepath.Join("hooks", "after") {
		t.Fatalf("hook renames=%#v", renamed)
	}
	changes, repaired, err := repairRepositoryBinding(paths, repository, renamed, after)
	if err != nil || repaired < 2 || len(changes) == 0 {
		t.Fatalf("changes=%#v repaired=%d error=%v", changes, repaired, err)
	}
	assertLinkTarget(t, filepath.Join(paths.ConfigHome, "hooks", "after"), newDir)
	assertLinkTarget(t, filepath.Join(paths.ClaudeHome, "hooks", "after"), newDir)
	for _, old := range []string{filepath.Join(paths.ConfigHome, "hooks", "before"), filepath.Join(paths.ClaudeHome, "hooks", "before")} {
		if _, err := os.Lstat(old); !os.IsNotExist(err) {
			t.Fatalf("old link remains %s: %v", old, err)
		}
	}
}

func TestEveryFamiliarHasKindSpecificHomes(t *testing.T) {
	paths := testPaths(t)
	for _, familiar := range familiarMetadata {
		paths.Familiar = familiar.name
		skills, hooks := paths.SkillsHomes(), paths.HooksHomes()
		if len(skills) != 1 || len(hooks) != 1 || filepath.Base(skills[0]) != "skills" || filepath.Base(hooks[0]) != "hooks" || filepath.Dir(skills[0]) != filepath.Dir(hooks[0]) {
			t.Errorf("%s skills=%#v hooks=%#v", familiar.name, skills, hooks)
		}
	}
}

func TestHookRelativePathsUsePortableSeparators(t *testing.T) {
	clean, err := validateHookRelative(`hooks\group\preflight`)
	if err != nil || clean != "hooks/group/preflight" {
		t.Fatalf("clean=%q error=%v", clean, err)
	}
	if _, err := validateHookRelative(`outside\preflight`); err == nil {
		t.Fatal("backslash path outside hooks was accepted")
	}
}

func TestSameNameSkillAndHookPackToDistinctArchives(t *testing.T) {
	paths := testPaths(t)
	repository := paths.Repo
	skill := ReadSkill(makeSkillIn(t, repository, "", "shared", "skill"), repository, nil)
	hook := ReadHook(makeHookIn(t, repository, "", "shared", "hook"), repository, nil)
	paths.Output = filepath.Join(paths.Home, "output")
	skillArchive, err := Pack(paths, skill)
	if err != nil {
		t.Fatal(err)
	}
	hookArchive, err := Pack(paths, hook)
	if err != nil {
		t.Fatal(err)
	}
	if skillArchive != filepath.Join(paths.Output, "shared.zip") {
		t.Fatalf("skill archive=%s", skillArchive)
	}
	if hookArchive != filepath.Join(paths.Output, "hooks", "shared.zip") {
		t.Fatalf("hook archive=%s", hookArchive)
	}
	assertArchiveMarker := func(archive, marker string) {
		t.Helper()
		reader, err := zip.OpenReader(archive)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = reader.Close() }()
		for _, file := range reader.File {
			if file.Name == filepath.ToSlash(filepath.Join("shared", marker)) {
				return
			}
		}
		t.Fatalf("%s missing %s", archive, marker)
	}
	assertArchiveMarker(skillArchive, "SKILL.md")
	assertArchiveMarker(hookArchive, "HOOK.md")
}

func TestHookBindingMutationRecoveryCompletesV2State(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	initGit(t, repository)
	first := ReadHook(makeHookIn(t, repository, "", "first", "first"), repository, paths.HooksHomes())
	second := ReadHook(makeHookIn(t, repository, "", "second", "second"), repository, paths.HooksHomes())
	if result := BindSkills(paths, repository, []Skill{first}); !result.OK() {
		t.Fatal(result.Message)
	}
	before, err := configuredBindings(paths)
	if err != nil {
		t.Fatal(err)
	}
	after := cloneBindings(before)
	after[0].Hooks = []string{second.RepoPath()}
	mutation, err := prepareBindingMutation(paths, before, after, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if mutation.journal.Version != 2 {
		t.Fatalf("journal version=%d", mutation.journal.Version)
	}
	if err := writeBindingMutationJournal(paths, mutation.journal); err != nil {
		t.Fatal(err)
	}
	if err := recoverBindingMutation(paths); err != nil {
		t.Fatal(err)
	}
	assertLinkTarget(t, filepath.Join(paths.ConfigHome, "hooks", "second"), second.Dir)
	if _, err := os.Lstat(filepath.Join(paths.ConfigHome, "hooks", "first")); !os.IsNotExist(err) {
		t.Fatalf("old recovered link remains: %v", err)
	}
	recovered, err := configuredBindings(paths)
	if err != nil || len(recovered) != 1 || len(recovered[0].Hooks) != 1 || recovered[0].Hooks[0] != second.RepoPath() {
		t.Fatalf("recovered=%#v error=%v", recovered, err)
	}
}

func TestSkillOnlyMutationJournalStaysV1AndRecovers(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	initGit(t, repository)
	skillDir := makeSkillIn(t, repository, "", "alpha", "skill")
	after := []BoundRepository{{Path: repository, Skills: []string{"alpha"}}}
	mutation, err := prepareBindingMutation(paths, nil, after, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if mutation.journal.Version != legacyBindingMutationVersion {
		t.Fatalf("skill-only journal version=%d", mutation.journal.Version)
	}
	if err := writeBindingMutationJournal(paths, mutation.journal); err != nil {
		t.Fatal(err)
	}
	if err := recoverBindingMutation(paths); err != nil {
		t.Fatal(err)
	}
	assertLinkTarget(t, filepath.Join(paths.ConfigHome, "skills", "alpha"), skillDir)
	body, err := os.ReadFile(paths.BindingsFile())
	if err != nil || len(body) == 0 || body[0] != '[' {
		t.Fatalf("skill-only state=%s error=%v", body, err)
	}
}

func TestV1MutationJournalRejectsHookSelections(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	initGit(t, repository)
	hookDir := makeHookIn(t, repository, "", "preflight", "hook")
	journal := bindingMutationJournal{
		Version: legacyBindingMutationVersion,
		After:   []BoundRepository{{Path: repository, Hooks: []string{filepath.Join("hooks", "preflight")}}},
	}
	if err := writeJSONFile(paths.BindingMutationFile(), journal); err != nil {
		t.Fatal(err)
	}
	if err := recoverBindingMutation(paths); err == nil || !strings.Contains(err.Error(), "version 1 cannot contain hook selections") {
		t.Fatalf("recovery error=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(paths.ConfigHome, "hooks", "preflight")); !os.IsNotExist(err) {
		t.Fatalf("v1 hook journal changed catalog: %v", err)
	}
	if _, err := os.Stat(hookDir); err != nil {
		t.Fatalf("source hook changed: %v", err)
	}
}

func TestHookBindingStateUpgradeIsOneWay(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	initGit(t, repository)
	skill := ReadSkill(makeSkillIn(t, repository, "", "alpha", "skill"), repository, paths.SkillsHomes())
	hook := ReadHook(makeHookIn(t, repository, "", "preflight", "hook"), repository, paths.HooksHomes())
	if result := BindSkills(paths, repository, []Skill{skill, hook}); !result.OK() {
		t.Fatal(result.Message)
	}
	if result := UnbindSkills(paths, []Skill{hook}); !result.OK() {
		t.Fatal(result.Message)
	}
	body, err := os.ReadFile(paths.BindingsFile())
	if err != nil || !strings.Contains(string(body), `"version": 2`) {
		t.Fatalf("state downgraded=%s error=%v", body, err)
	}
	var oldReleaseShape []BoundRepository
	if err := json.Unmarshal(body, &oldReleaseShape); err == nil {
		t.Fatalf("old-release array decoder unexpectedly accepted upgraded state: %#v", oldReleaseShape)
	}
	repositories, err := configuredBindings(paths)
	if err != nil || len(repositories) != 1 || len(repositories[0].Skills) != 1 || len(repositories[0].Hooks) != 0 {
		t.Fatalf("upgraded state=%#v error=%v", repositories, err)
	}

	second := ReadSkill(makeSkillIn(t, repository, "", "beta", "second skill"), repository, paths.SkillsHomes())
	after := cloneBindings(repositories)
	after[0].Skills = append(after[0].Skills, second.RepoPath())
	mutation, err := prepareBindingMutation(paths, repositories, after, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if mutation.journal.Version != bindingMutationVersion {
		t.Fatalf("post-upgrade skill-only journal version=%d", mutation.journal.Version)
	}
	if result := BindSkills(paths, repository, []Skill{second}); !result.OK() {
		t.Fatal(result.Message)
	}
	body, err = os.ReadFile(paths.BindingsFile())
	if err != nil || !strings.Contains(string(body), `"version": 2`) {
		t.Fatalf("post-upgrade bind downgraded state=%s error=%v", body, err)
	}
	if result := UnbindSkills(paths, []Skill{second}); !result.OK() {
		t.Fatal(result.Message)
	}
	body, err = os.ReadFile(paths.BindingsFile())
	if err != nil || !strings.Contains(string(body), `"version": 2`) {
		t.Fatalf("post-upgrade unbind downgraded state=%s error=%v", body, err)
	}
}

func TestFailedFirstHookMutationRestoresV1BindingFormat(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	initGit(t, repository)
	skill := ReadSkill(makeSkillIn(t, repository, "", "alpha", "skill"), repository, paths.SkillsHomes())
	hook := ReadHook(makeHookIn(t, repository, "", "preflight", "hook"), repository, paths.HooksHomes())
	if result := BindSkills(paths, repository, []Skill{skill}); !result.OK() {
		t.Fatal(result.Message)
	}
	beforeBody, err := os.ReadFile(paths.BindingsFile())
	if err != nil || len(beforeBody) == 0 || beforeBody[0] != '[' {
		t.Fatalf("initial v1 state=%s error=%v", beforeBody, err)
	}
	before, err := configuredBindings(paths)
	if err != nil {
		t.Fatal(err)
	}
	after := cloneBindings(before)
	after[0].Hooks = []string{hook.RepoPath()}
	mutation, err := prepareBindingMutation(paths, before, after, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if mutation.beforeVersion2 || mutation.journal.Version != bindingMutationVersion {
		t.Fatalf("beforeVersion2=%v journalVersion=%d", mutation.beforeVersion2, mutation.journal.Version)
	}
	beforeOwnership := ownershipState{Version: ownershipVersion}
	afterOwnership := beforeOwnership.clone()
	hookHome := filepath.Join(paths.ClaudeHome, "hooks")
	if err := os.MkdirAll(hookHome, 0o755); err != nil {
		t.Fatal(err)
	}
	installedHook := filepath.Join(hookHome, "preflight")
	afterOwnership.Links = []ownedLink{{Path: installedHook, Target: hook.Dir}}
	installedPlan := &symlinkPlan{}
	if err := installedPlan.Create(installedHook, hook.Dir, true); err != nil {
		t.Fatal(err)
	}
	mutation.SetLinkAndOwnershipUpdate(installedPlan, beforeOwnership, afterOwnership)

	// Force failure after catalog and bindings have been written. A directory at
	// the ownership file path makes the atomic ownership-file replacement fail.
	if err := os.MkdirAll(paths.OwnershipFile(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := mutation.Commit(); err == nil || !strings.Contains(err.Error(), "save ownership") {
		t.Fatalf("commit error=%v", err)
	}
	afterBody, err := os.ReadFile(paths.BindingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterBody, beforeBody) || len(afterBody) == 0 || afterBody[0] != '[' {
		t.Fatalf("rollback changed v1 state\nbefore=%s\nafter=%s", beforeBody, afterBody)
	}
	assertLinkTarget(t, filepath.Join(paths.ConfigHome, "skills", "alpha"), skill.Dir)
	if _, err := os.Lstat(filepath.Join(paths.ConfigHome, "hooks", "preflight")); !os.IsNotExist(err) {
		t.Fatalf("rolled-back hook catalog link remains: %v", err)
	}
	if _, err := os.Lstat(installedHook); !os.IsNotExist(err) {
		t.Fatalf("rolled-back installed hook link remains: %v", err)
	}
	if _, err := os.Lstat(paths.BindingMutationFile()); !os.IsNotExist(err) {
		t.Fatalf("rolled-back journal remains: %v", err)
	}
}

func TestPersistedHookPathsAreStrictlyValidated(t *testing.T) {
	t.Run("v2 binding outside hooks", func(t *testing.T) {
		paths := testPaths(t)
		paths.Repo = ""
		repository := filepath.Join(paths.Home, "repo")
		state := struct {
			Version      int               `json:"version"`
			Repositories []BoundRepository `json:"repositories"`
		}{Version: 2, Repositories: []BoundRepository{{Path: repository, Hooks: []string{filepath.Join("app", "preflight")}}}}
		if err := writeJSONFile(paths.BindingsFile(), state); err != nil {
			t.Fatal(err)
		}
		if _, err := readConfiguredBindings(paths); err == nil || !strings.Contains(err.Error(), "top-level hooks") {
			t.Fatalf("binding error=%v", err)
		}
	})

	t.Run("v2 journal outside hooks", func(t *testing.T) {
		paths := testPaths(t)
		paths.Repo = ""
		journal := bindingMutationJournal{Version: 2, After: []BoundRepository{{Path: filepath.Join(paths.Home, "repo"), Hooks: []string{"preflight"}}}}
		if err := writeJSONFile(paths.BindingMutationFile(), journal); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readBindingMutationJournal(paths); err == nil || !strings.Contains(err.Error(), "top-level hooks") {
			t.Fatalf("journal error=%v", err)
		}
	})

	t.Run("legacy array hooks field", func(t *testing.T) {
		paths := testPaths(t)
		paths.Repo = ""
		body := []byte(`[{"path":"/repo","skills":[],"hooks":[]}]`)
		if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths.BindingsFile(), body, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := readConfiguredBindings(paths); err == nil || !strings.Contains(err.Error(), "legacy array records cannot contain hooks") {
			t.Fatalf("legacy error=%v", err)
		}
	})
}

func TestSymlinkedHookPathIsRejectedByBindStateAndEffigy(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	initGit(t, repository)
	realHook := filepath.Join(paths.Home, "outside", "preflight")
	if err := os.MkdirAll(realHook, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realHook, "HOOK.md"), []byte("---\nname: preflight\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repository, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(repository, "hooks", "preflight")
	if err := os.Symlink(realHook, alias); err != nil {
		t.Fatal(err)
	}
	hook := ReadHook(alias, repository, paths.HooksHomes())
	if result := BindSkills(paths, repository, []Skill{hook}); result.Status != Blocked || !strings.Contains(result.Message, "contains a symlink") {
		t.Fatalf("bind result=%#v", result)
	}
	paths.Output = filepath.Join(paths.Home, "output")
	if _, err := Pack(paths, hook); err == nil || !strings.Contains(err.Error(), "contains a symlink") {
		t.Fatalf("pack error=%v", err)
	}
	if _, err := os.Lstat(paths.Output); !os.IsNotExist(err) {
		t.Fatalf("pack created output before validation: %v", err)
	}

	state := struct {
		Version      int               `json:"version"`
		Repositories []BoundRepository `json:"repositories"`
	}{Version: 2, Repositories: []BoundRepository{{Path: repository, Hooks: []string{filepath.Join("hooks", "preflight")}}}}
	if err := writeJSONFile(paths.BindingsFile(), state); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfiguredBindings(paths); err == nil || !strings.Contains(err.Error(), "contains a symlink") {
		t.Fatalf("state error=%v", err)
	}
	if err := os.Remove(paths.BindingsFile()); err != nil {
		t.Fatal(err)
	}

	markerAlias := filepath.Join(repository, "hooks", "marker-alias")
	if err := os.MkdirAll(markerAlias, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(realHook, "HOOK.md"), filepath.Join(markerAlias, "HOOK.md")); err != nil {
		t.Fatal(err)
	}
	markerHook := ReadHook(markerAlias, repository, paths.HooksHomes())
	if result := BindSkills(paths, repository, []Skill{markerHook}); result.Status != Blocked || !strings.Contains(result.Message, "regular non-symlink") {
		t.Fatalf("symlink marker bind result=%#v", result)
	}
}

func TestHookRefreshRejectsRenameOutsideHooks(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	initGit(t, repository)
	oldDir := makeHookIn(t, repository, "", "before", "same")
	gitCommitAll(t, repository, "before")
	beforeRevision := repositoryRevision(repository)
	hook := ReadHook(oldDir, repository, paths.HooksHomes())
	if result := BindSkills(paths, repository, []Skill{hook}); !result.OK() {
		t.Fatal(result.Message)
	}
	outside := filepath.Join(repository, "outside", "after")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldDir, outside); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, repository, "outside")
	afterRevision := repositoryRevision(repository)
	if renames := skillRenames(repository, beforeRevision, afterRevision); len(renames) != 0 {
		t.Fatalf("out-of-scope hook rename=%#v", renames)
	}
	malicious := map[string]string{"hook:hooks/before": filepath.Join("outside", "after")}
	if _, _, err := repairRepositoryBinding(paths, repository, malicious, afterRevision); err == nil || !strings.Contains(err.Error(), "top-level hooks") {
		t.Fatalf("repair error=%v", err)
	}
	alias := filepath.Join(repository, "hooks", "after")
	if err := os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}
	malicious = map[string]string{"hook:hooks/before": filepath.Join("hooks", "after")}
	if _, _, err := repairRepositoryBinding(paths, repository, malicious, afterRevision); err == nil || !strings.Contains(err.Error(), "contains a symlink") {
		t.Fatalf("symlinked repair error=%v", err)
	}
}

func TestLiteralQualifiedPrefixSkillNamesRemainSelectable(t *testing.T) {
	resources := []Skill{
		{Name: "hook:literal", Dir: filepath.Join("repo", "hook:literal")},
		{Name: "skill:literal", Dir: filepath.Join("repo", "skill:literal")},
	}
	catalog := Catalog{Skills: append([]Skill(nil), resources...)}
	for _, name := range []string{"hook:literal", "skill:literal"} {
		found, err := catalog.Find(name)
		if err != nil || found == nil || found.Name != name {
			t.Errorf("Find(%q)=%#v error=%v", name, found, err)
		}
		cli := &CLI{}
		chosen, err := cli.chooseSkills([]string{name}, resources, "discovered", "bind")
		if err != nil || len(chosen) != 1 || chosen[0].Name != name {
			t.Errorf("chooseSkills(%q)=%#v error=%v", name, chosen, err)
		}
	}
	withQualifiedTarget := Catalog{Skills: append(append([]Skill(nil), resources...), Skill{Kind: HookKind, Name: "literal", Dir: filepath.Join("repo", "hooks", "literal")})}
	found, err := withQualifiedTarget.Find("hook:literal")
	if err != nil || found == nil || found.Kind != HookKind || found.Name != "literal" {
		t.Fatalf("qualified selector did not win: %#v error=%v", found, err)
	}
}

func TestKnownResourceKindRejectsPhysicallyAmbiguousAliasedHomes(t *testing.T) {
	paths := testPaths(t)
	common := filepath.Join(paths.Home, "common-resource-home")
	if err := os.MkdirAll(common, 0o755); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(paths.Home, ".agents")
	if err := os.MkdirAll(agent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(common, filepath.Join(agent, "skills")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(common, filepath.Join(agent, "hooks")); err != nil {
		t.Fatal(err)
	}
	if _, ok := knownResourceKind(paths, filepath.Join(common, "shared")); ok {
		t.Fatal("physically overlapping aliased homes were classified")
	}
	if kind, ok := knownResourceKind(paths, filepath.Join(agent, "hooks", "shared")); !ok || kind != HookKind {
		t.Fatalf("exact lexical hook home kind=%q ok=%v", kind.Name(), ok)
	}
	target := filepath.Join(paths.Home, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	ambiguousLink := filepath.Join(common, "shared")
	if err := os.Symlink(target, ambiguousLink); err != nil {
		t.Fatal(err)
	}
	state := ownershipState{Version: ownershipVersion, Links: []ownedLink{{Path: ambiguousLink, Target: target}}}
	if err := writeOwnership(paths, state); err != nil {
		t.Fatal(err)
	}
	changes, err := Hone(paths, false)
	if err != nil || len(changes) != 0 {
		t.Fatalf("hone changes=%#v error=%v", changes, err)
	}
	if owned, err := configuredOwnership(paths); err != nil || !ownershipProves(owned, ambiguousLink) {
		t.Fatalf("hone misclassified ambiguous link: %#v error=%v", owned, err)
	}
}

func TestInvalidSkillMarkerTypeSurfaces(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	initGit(t, repository)
	if err := os.MkdirAll(filepath.Join(repository, "broken", "SKILL.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := DiscoverRepositorySkills(repository, paths.SkillsHomes()); err == nil || !strings.Contains(err.Error(), "not a skill marker") {
		t.Fatalf("discovery error=%v", err)
	}
}

func TestMutationJournalRejectsUntrustedInstalledLinksAndOwnership(t *testing.T) {
	t.Run("v1 link outside familiar homes", func(t *testing.T) {
		paths := testPaths(t)
		paths.Repo = ""
		repository := filepath.Join(paths.Home, "repo")
		target := makeSkillIn(t, repository, "", "alpha", "skill")
		outside := filepath.Join(paths.Home, "outside-link")
		if err := os.Symlink(target, outside); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		journal := bindingMutationJournal{
			Version: legacyBindingMutationVersion,
			Before:  []BoundRepository{{Path: repository, Skills: []string{"alpha"}}},
			After:   []BoundRepository{{Path: repository, Skills: []string{"alpha"}}},
			Links: []journalSymlinkAction{{
				Kind: symlinkRemove, Path: outside, BeforeTarget: target, BeforeDirectory: true,
			}},
		}
		if err := writeJSONFile(paths.BindingMutationFile(), journal); err != nil {
			t.Fatal(err)
		}
		if err := recoverBindingMutation(paths); err == nil || !strings.Contains(err.Error(), "direct child") {
			t.Fatalf("recovery error=%v", err)
		}
		assertLinkTarget(t, outside, target)
	})

	t.Run("v2 link to unselected hook source", func(t *testing.T) {
		paths := testPaths(t)
		paths.Repo = ""
		repository := filepath.Join(paths.Home, "repo")
		selected := makeHookIn(t, repository, "", "preflight", "hook")
		unlisted := makeHookIn(t, repository, "", "other", "other hook")
		home := filepath.Join(paths.ClaudeHome, "hooks")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(home, "preflight")
		journal := bindingMutationJournal{
			Version: bindingMutationVersion,
			After:   []BoundRepository{{Path: repository, Hooks: []string{filepath.Join("hooks", "preflight")}}},
			Links: []journalSymlinkAction{{
				Kind: symlinkCreate, Path: link, Target: unlisted, Directory: true,
			}},
		}
		if err := writeJSONFile(paths.BindingMutationFile(), journal); err != nil {
			t.Fatal(err)
		}
		if err := recoverBindingMutation(paths); err == nil || !strings.Contains(err.Error(), "not a selected hook source") {
			t.Fatalf("recovery error=%v selected=%s", err, selected)
		}
		if _, err := os.Lstat(link); !os.IsNotExist(err) {
			t.Fatalf("malicious link was created: %v", err)
		}
	})

	for _, test := range []struct {
		name   string
		link   func(Paths) string
		target func(string, string) string
		want   string
	}{
		{
			name:   "ownership path outside homes",
			link:   func(paths Paths) string { return filepath.Join(paths.Home, "outside-owned") },
			target: func(selected, _ string) string { return selected },
			want:   "direct child",
		},
		{
			name:   "ownership target unselected",
			link:   func(paths Paths) string { return filepath.Join(paths.ClaudeHome, "skills", "alpha") },
			target: func(_, unlisted string) string { return unlisted },
			want:   "not a selected skill source",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := testPaths(t)
			paths.Repo = ""
			if err := os.MkdirAll(filepath.Join(paths.ClaudeHome, "skills"), 0o755); err != nil {
				t.Fatal(err)
			}
			repository := filepath.Join(paths.Home, "repo")
			selected := makeSkillIn(t, repository, "", "alpha", "skill")
			unlisted := makeSkillIn(t, repository, "", "other", "other")
			journal := bindingMutationJournal{
				Version: legacyBindingMutationVersion,
				After:   []BoundRepository{{Path: repository, Skills: []string{"alpha"}}},
				Ownership: &ownershipState{Version: ownershipVersion, Links: []ownedLink{{
					Path: test.link(paths), Target: test.target(selected, unlisted),
				}}},
			}
			if err := writeJSONFile(paths.BindingMutationFile(), journal); err != nil {
				t.Fatal(err)
			}
			if err := recoverBindingMutation(paths); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("recovery error=%v", err)
			}
			if _, err := os.Lstat(paths.OwnershipFile()); !os.IsNotExist(err) {
				t.Fatalf("malicious ownership was persisted: %v", err)
			}
		})
	}
}

func TestMutationJournalRejectsSymlinkedFamiliarResourceHomes(t *testing.T) {
	for _, test := range []struct {
		name string
		kind ResourceKind
		rel  string
	}{
		{name: "skill home", kind: SkillKind, rel: "alpha"},
		{name: "hook home", kind: HookKind, rel: filepath.Join("hooks", "preflight")},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := testPaths(t)
			paths.Repo = ""
			repository := filepath.Join(paths.Home, "repo")
			var target string
			if test.kind == HookKind {
				target = makeHookIn(t, repository, "", "preflight", "hook")
			} else {
				target = makeSkillIn(t, repository, "", "alpha", "skill")
			}

			external := filepath.Join(paths.Home, "external-"+test.kind.Plural())
			if err := os.MkdirAll(external, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(paths.ClaudeHome, 0o755); err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(paths.ClaudeHome, test.kind.Plural())
			if err := os.Symlink(external, home); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			link := filepath.Join(home, filepath.Base(test.rel))
			binding := BoundRepository{Path: repository}
			if test.kind == HookKind {
				binding.Hooks = []string{test.rel}
			} else {
				binding.Skills = []string{test.rel}
			}
			journal := bindingMutationJournal{
				Version: bindingMutationJournalVersion(paths, nil, []BoundRepository{binding}),
				After:   []BoundRepository{binding},
				Links: []journalSymlinkAction{{
					Kind: symlinkCreate, Path: link, Target: target, Directory: true,
				}},
			}
			if err := writeJSONFile(paths.BindingMutationFile(), journal); err != nil {
				t.Fatal(err)
			}
			if err := recoverBindingMutation(paths); err == nil || !strings.Contains(err.Error(), "symlinked path component") {
				t.Fatalf("recovery error=%v", err)
			}
			if _, err := os.Lstat(filepath.Join(external, filepath.Base(test.rel))); !os.IsNotExist(err) {
				t.Fatalf("external path was mutated: %v", err)
			}
		})
	}
}

func TestBindingMutationValidatesJournalBeforeWritingOrApplying(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	target := makeSkillIn(t, repository, "", "alpha", "skill")
	external := filepath.Join(paths.Home, "external-skills")
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.ClaudeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(paths.ClaudeHome, "skills")
	if err := os.Symlink(external, home); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	link := filepath.Join(home, "alpha")
	after := []BoundRepository{{Path: repository, Skills: []string{"alpha"}}}
	mutation, err := prepareBindingMutation(paths, nil, after, false, false)
	if err != nil {
		t.Fatal(err)
	}
	plan := &symlinkPlan{}
	if err := plan.Create(link, target, true); err != nil {
		t.Fatal(err)
	}
	beforeOwnership := ownershipState{Version: ownershipVersion}
	afterOwnership := ownershipState{Version: ownershipVersion, Links: []ownedLink{{Path: link, Target: target}}}
	mutation.SetLinkAndOwnershipUpdate(plan, beforeOwnership, afterOwnership)
	if err := mutation.Commit(); err == nil || !strings.Contains(err.Error(), "validate binding mutation journal") {
		t.Fatalf("commit error=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(external, "alpha")); !os.IsNotExist(err) {
		t.Fatalf("external path was mutated: %v", err)
	}
	if _, err := os.Lstat(paths.BindingMutationFile()); !os.IsNotExist(err) {
		t.Fatalf("invalid journal was persisted: %v", err)
	}
}

func TestMutationJournalRejectsMissingFamiliarResourceHomes(t *testing.T) {
	for _, test := range []struct {
		name string
		kind ResourceKind
		rel  string
	}{
		{name: "skill home", kind: SkillKind, rel: "alpha"},
		{name: "hook home", kind: HookKind, rel: filepath.Join("hooks", "preflight")},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := testPaths(t)
			paths.Repo = ""
			repository := filepath.Join(paths.Home, "repo")
			var target string
			if test.kind == HookKind {
				target = makeHookIn(t, repository, "", "preflight", "hook")
			} else {
				target = makeSkillIn(t, repository, "", "alpha", "skill")
			}
			if err := os.MkdirAll(paths.ClaudeHome, 0o755); err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(paths.ClaudeHome, test.kind.Plural())
			link := filepath.Join(home, filepath.Base(test.rel))
			binding := BoundRepository{Path: repository}
			if test.kind == HookKind {
				binding.Hooks = []string{test.rel}
			} else {
				binding.Skills = []string{test.rel}
			}
			journal := bindingMutationJournal{
				Version: bindingMutationJournalVersion(paths, nil, []BoundRepository{binding}),
				After:   []BoundRepository{binding},
				Links: []journalSymlinkAction{{
					Kind: symlinkCreate, Path: link, Target: target, Directory: true,
				}},
			}
			if err := writeBindingMutationJournal(paths, journal); err != nil {
				t.Fatal(err)
			}
			if err := recoverBindingMutation(paths); err == nil || !strings.Contains(err.Error(), "resource home") {
				t.Fatalf("recovery error=%v", err)
			}
			if _, err := os.Lstat(link); !os.IsNotExist(err) {
				t.Fatalf("link was created below missing home: %v", err)
			}
		})
	}
}

func TestMutationJournalPreservesUnchangedUnboundOwnership(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	selected := makeSkillIn(t, repository, "", "alpha", "selected")
	unboundTarget := makeSkillIn(t, filepath.Join(paths.Home, "unbound-repo"), "", "orphan", "unbound")
	home := filepath.Join(paths.ClaudeHome, "skills")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	unboundLink := filepath.Join(home, "orphan")
	if err := os.Symlink(unboundTarget, unboundLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	beforeOwnership := ownershipState{Version: ownershipVersion, Links: []ownedLink{{Path: unboundLink, Target: unboundTarget}}}
	if err := writeOwnership(paths, beforeOwnership); err != nil {
		t.Fatal(err)
	}
	after := []BoundRepository{{Path: repository, Skills: []string{"alpha"}}}
	mutation, err := prepareBindingMutation(paths, nil, after, false, false)
	if err != nil {
		t.Fatal(err)
	}
	mutation.SetLinkAndOwnershipUpdate(&symlinkPlan{}, beforeOwnership, beforeOwnership.clone())
	if err := writeBindingMutationJournal(paths, mutation.journal); err != nil {
		t.Fatal(err)
	}
	if err := recoverBindingMutation(paths); err != nil {
		t.Fatal(err)
	}
	assertLinkTarget(t, filepath.Join(paths.ConfigHome, "skills", "alpha"), selected)
	assertLinkTarget(t, unboundLink, unboundTarget)
	ownership, err := configuredOwnership(paths)
	if err != nil || !ownershipEqual(ownership, beforeOwnership) {
		t.Fatalf("recovered ownership=%#v error=%v", ownership, err)
	}
}

func TestMutationJournalRecoversLegacyV1WithRetainedUnboundOwnership(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	alpha := makeSkillIn(t, repository, "", "alpha", "alpha")
	beta := makeSkillIn(t, repository, "", "beta", "beta")
	unboundTarget := makeSkillIn(t, filepath.Join(paths.Home, "unbound-repo"), "", "orphan", "unbound")
	home := filepath.Join(paths.ClaudeHome, "skills")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	installedLink := filepath.Join(home, "installed")
	unboundLink := filepath.Join(home, "orphan")
	if err := os.Symlink(alpha, installedLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(unboundTarget, unboundLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	beforeOwnership := ownershipState{Version: ownershipVersion, Links: []ownedLink{
		{Path: installedLink, Target: alpha},
		{Path: unboundLink, Target: unboundTarget},
	}}
	afterOwnership := ownershipState{Version: ownershipVersion, Links: []ownedLink{
		{Path: installedLink, Target: beta},
		{Path: unboundLink, Target: unboundTarget},
	}}
	if err := writeOwnership(paths, beforeOwnership); err != nil {
		t.Fatal(err)
	}
	bindings := []BoundRepository{{Path: repository, Skills: []string{"alpha", "beta"}}}
	journal := bindingMutationJournal{
		Version: legacyBindingMutationVersion, Before: bindings, After: bindings,
		Links: []journalSymlinkAction{{
			Kind: symlinkReplace, Path: installedLink, Target: beta, Directory: true,
			BeforeTarget: alpha, BeforeDirectory: true,
		}},
		Ownership: &afterOwnership,
	}
	if err := writeJSONFile(paths.BindingMutationFile(), journal); err != nil {
		t.Fatal(err)
	}
	if err := recoverBindingMutation(paths); err != nil {
		t.Fatal(err)
	}
	assertLinkTarget(t, installedLink, beta)
	assertLinkTarget(t, unboundLink, unboundTarget)
	ownership, err := configuredOwnership(paths)
	if err != nil || !ownershipEqual(ownership, afterOwnership) {
		t.Fatalf("recovered ownership=%#v error=%v", ownership, err)
	}
}

func TestMutationJournalRecoversCompletedLegacyV1AfterOwnershipWrite(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	target := makeSkillIn(t, repository, "", "alpha", "skill")
	home := filepath.Join(paths.ClaudeHome, "skills")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	afterOwnership := ownershipState{Version: ownershipVersion}
	if err := writeOwnership(paths, afterOwnership); err != nil {
		t.Fatal(err)
	}
	bindings := []BoundRepository{{Path: repository, Skills: []string{"alpha"}}}
	journal := bindingMutationJournal{
		Version: legacyBindingMutationVersion, Before: bindings, After: bindings,
		Links: []journalSymlinkAction{{
			Kind: symlinkRemove, Path: filepath.Join(home, "alpha"),
			BeforeTarget: target, BeforeDirectory: true,
		}},
		Ownership: &afterOwnership,
	}
	if err := writeJSONFile(paths.BindingMutationFile(), journal); err != nil {
		t.Fatal(err)
	}
	if err := recoverBindingMutation(paths); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(paths.BindingMutationFile()); !os.IsNotExist(err) {
		t.Fatalf("completed legacy journal remains: %v", err)
	}
}

func TestMutationJournalRejectsLegacyV1DroppingPersistedOwnership(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	makeSkillIn(t, repository, "", "alpha", "selected")
	unboundTarget := makeSkillIn(t, filepath.Join(paths.Home, "unbound-repo"), "", "orphan", "unbound")
	home := filepath.Join(paths.ClaudeHome, "skills")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	unboundLink := filepath.Join(home, "orphan")
	if err := os.Symlink(unboundTarget, unboundLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	beforeOwnership := ownershipState{Version: ownershipVersion, Links: []ownedLink{{Path: unboundLink, Target: unboundTarget}}}
	if err := writeOwnership(paths, beforeOwnership); err != nil {
		t.Fatal(err)
	}
	journal := bindingMutationJournal{
		Version: legacyBindingMutationVersion,
		After:   []BoundRepository{{Path: repository, Skills: []string{"alpha"}}},
		Ownership: &ownershipState{
			Version: ownershipVersion,
		},
	}
	if err := writeJSONFile(paths.BindingMutationFile(), journal); err != nil {
		t.Fatal(err)
	}
	if err := recoverBindingMutation(paths); err == nil || !strings.Contains(err.Error(), "drops persisted ownership") {
		t.Fatalf("recovery error=%v", err)
	}
	assertLinkTarget(t, unboundLink, unboundTarget)
	ownership, err := configuredOwnership(paths)
	if err != nil || !ownershipEqual(ownership, beforeOwnership) {
		t.Fatalf("ownership changed=%#v error=%v", ownership, err)
	}
}

func TestMutationJournalRejectsForgedOwnershipWithoutLinkAction(t *testing.T) {
	for _, version := range []int{legacyBindingMutationVersion, bindingMutationVersion} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			paths := testPaths(t)
			paths.Repo = ""
			repository := filepath.Join(paths.Home, "repo")
			target := makeSkillIn(t, repository, "", "alpha", "skill")
			home := filepath.Join(paths.ClaudeHome, "skills")
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(home, "alpha")
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			journal := bindingMutationJournal{
				Version: version,
				After:   []BoundRepository{{Path: repository, Skills: []string{"alpha"}}},
				Ownership: &ownershipState{Version: ownershipVersion, Links: []ownedLink{{
					Path: link, Target: target,
				}}},
			}
			if err := writeJSONFile(paths.BindingMutationFile(), journal); err != nil {
				t.Fatal(err)
			}
			if err := recoverBindingMutation(paths); err == nil || !strings.Contains(err.Error(), "not justified") {
				t.Fatalf("recovery error=%v", err)
			}
			assertLinkTarget(t, link, target)
			if _, err := os.Lstat(paths.OwnershipFile()); !os.IsNotExist(err) {
				t.Fatalf("forged ownership was persisted: %v", err)
			}
		})
	}
}

func TestMutationJournalRejectsDestructiveActionWithoutPriorOwnership(t *testing.T) {
	for _, actionKind := range []symlinkActionKind{symlinkReplace, symlinkRemove, symlinkMove} {
		t.Run(string(actionKind), func(t *testing.T) {
			paths := testPaths(t)
			paths.Repo = ""
			repository := filepath.Join(paths.Home, "repo")
			alpha := makeSkillIn(t, repository, "", "alpha", "alpha")
			beta := makeSkillIn(t, repository, "", "beta", "beta")
			home := filepath.Join(paths.ClaudeHome, "skills")
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			oldLink := filepath.Join(home, "old")
			newLink := filepath.Join(home, "new")
			if err := os.Symlink(alpha, oldLink); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			action := journalSymlinkAction{
				Kind: actionKind, Path: oldLink, BeforeTarget: alpha, BeforeDirectory: true,
			}
			afterOwnership := ownershipState{Version: ownershipVersion}
			switch actionKind {
			case symlinkReplace:
				action.Target, action.Directory = beta, true
				afterOwnership.Links = []ownedLink{{Path: oldLink, Target: beta}}
			case symlinkMove:
				action.NewPath, action.Target, action.Directory = newLink, beta, true
				afterOwnership.Links = []ownedLink{{Path: newLink, Target: beta}}
			}
			journal := bindingMutationJournal{
				Version: legacyBindingMutationVersion,
				After:   []BoundRepository{{Path: repository, Skills: []string{"alpha", "beta"}}},
				Links:   []journalSymlinkAction{action}, Ownership: &afterOwnership,
			}
			if err := writeJSONFile(paths.BindingMutationFile(), journal); err != nil {
				t.Fatal(err)
			}
			if err := recoverBindingMutation(paths); err == nil || !strings.Contains(err.Error(), "prior ownership") {
				t.Fatalf("recovery error=%v", err)
			}
			assertLinkTarget(t, oldLink, alpha)
			if _, err := os.Lstat(newLink); !os.IsNotExist(err) {
				t.Fatalf("forged move created a new link: %v", err)
			}
		})
	}
}

func TestMutationJournalRejectsForgedBeforeOwnershipWhenAfterActionsAreIncomplete(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	target := makeSkillIn(t, repository, "", "alpha", "skill")
	home := filepath.Join(paths.ClaudeHome, "skills")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "alpha")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	beforeOwnership := ownershipState{Version: ownershipVersion, Links: []ownedLink{{Path: link, Target: target}}}
	afterOwnership := ownershipState{Version: ownershipVersion}
	if err := writeOwnership(paths, afterOwnership); err != nil {
		t.Fatal(err)
	}
	bindings := []BoundRepository{{Path: repository, Skills: []string{"alpha"}}}
	journal := bindingMutationJournal{
		Version: legacyBindingMutationVersion, Before: bindings, After: bindings,
		Links: []journalSymlinkAction{{
			Kind: symlinkRemove, Path: link, BeforeTarget: target, BeforeDirectory: true,
		}},
		BeforeOwnership: &beforeOwnership, Ownership: &afterOwnership,
	}
	if err := writeJSONFile(paths.BindingMutationFile(), journal); err != nil {
		t.Fatal(err)
	}
	if err := recoverBindingMutation(paths); err == nil || !strings.Contains(err.Error(), "actions are incomplete") {
		t.Fatalf("recovery error=%v", err)
	}
	assertLinkTarget(t, link, target)
}

func TestMutationJournalAcceptsAfterOwnershipOnlyWhenActionsAreComplete(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repo")
	target := makeSkillIn(t, repository, "", "alpha", "skill")
	home := filepath.Join(paths.ClaudeHome, "skills")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "alpha")
	beforeOwnership := ownershipState{Version: ownershipVersion, Links: []ownedLink{{Path: link, Target: target}}}
	afterOwnership := ownershipState{Version: ownershipVersion}
	if err := writeOwnership(paths, afterOwnership); err != nil {
		t.Fatal(err)
	}
	bindings := []BoundRepository{{Path: repository, Skills: []string{"alpha"}}}
	journal := bindingMutationJournal{
		Version: legacyBindingMutationVersion, Before: bindings, After: bindings,
		Links: []journalSymlinkAction{{
			Kind: symlinkRemove, Path: link, BeforeTarget: target, BeforeDirectory: true,
		}},
		BeforeOwnership: &beforeOwnership, Ownership: &afterOwnership,
	}
	if err := writeJSONFile(paths.BindingMutationFile(), journal); err != nil {
		t.Fatal(err)
	}
	if err := recoverBindingMutation(paths); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(paths.BindingMutationFile()); !os.IsNotExist(err) {
		t.Fatalf("completed journal remains: %v", err)
	}
}

func TestMutationJournalRecoversValidatedInstalledLinkActions(t *testing.T) {
	for _, actionKind := range []symlinkActionKind{symlinkCreate, symlinkReplace, symlinkRemove, symlinkMove} {
		t.Run(string(actionKind), func(t *testing.T) {
			paths := testPaths(t)
			paths.Repo = ""
			repository := filepath.Join(paths.Home, "repo")
			alpha := makeSkillIn(t, repository, "", "alpha", "alpha")
			beta := makeSkillIn(t, repository, "", "beta", "beta")
			home := filepath.Join(paths.ClaudeHome, "skills")
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			oldLink := filepath.Join(home, "old")
			newLink := filepath.Join(home, "new")
			action := journalSymlinkAction{Kind: actionKind, Path: oldLink}
			beforeOwnership := ownershipState{Version: ownershipVersion}
			afterOwnership := ownershipState{Version: ownershipVersion}
			switch actionKind {
			case symlinkCreate:
				action.Target, action.Directory = alpha, true
				afterOwnership.Links = []ownedLink{{Path: oldLink, Target: alpha}}
			case symlinkReplace:
				if err := os.Symlink(alpha, oldLink); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				action.Target, action.Directory = beta, true
				action.BeforeTarget, action.BeforeDirectory = alpha, true
				beforeOwnership.Links = []ownedLink{{Path: oldLink, Target: alpha}}
				afterOwnership.Links = []ownedLink{{Path: oldLink, Target: beta}}
			case symlinkRemove:
				if err := os.Symlink(alpha, oldLink); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				action.BeforeTarget, action.BeforeDirectory = alpha, true
				beforeOwnership.Links = []ownedLink{{Path: oldLink, Target: alpha}}
			case symlinkMove:
				if err := os.Symlink(alpha, oldLink); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				action.NewPath = newLink
				action.Target, action.Directory = beta, true
				action.BeforeTarget, action.BeforeDirectory = alpha, true
				beforeOwnership.Links = []ownedLink{{Path: oldLink, Target: alpha}}
				afterOwnership.Links = []ownedLink{{Path: newLink, Target: beta}}
			}
			if err := writeOwnership(paths, beforeOwnership); err != nil {
				t.Fatal(err)
			}
			bindings := []BoundRepository{{Path: repository, Skills: []string{"alpha", "beta"}}}
			journal := bindingMutationJournal{
				Version: legacyBindingMutationVersion, Before: bindings, After: bindings,
				Links: []journalSymlinkAction{action}, BeforeOwnership: &beforeOwnership, Ownership: &afterOwnership,
			}
			if err := writeJSONFile(paths.BindingMutationFile(), journal); err != nil {
				t.Fatal(err)
			}
			if err := recoverBindingMutation(paths); err != nil {
				t.Fatal(err)
			}
			switch actionKind {
			case symlinkCreate:
				assertLinkTarget(t, oldLink, alpha)
			case symlinkReplace:
				assertLinkTarget(t, oldLink, beta)
			case symlinkRemove:
				if _, err := os.Lstat(oldLink); !os.IsNotExist(err) {
					t.Fatalf("removed link remains: %v", err)
				}
			case symlinkMove:
				if _, err := os.Lstat(oldLink); !os.IsNotExist(err) {
					t.Fatalf("moved source remains: %v", err)
				}
				assertLinkTarget(t, newLink, beta)
			}
		})
	}
}

func TestRefreshRejectsUnsafeHookTreesBeforeFastForward(t *testing.T) {
	for _, test := range []struct {
		name  string
		group string
	}{
		{name: "selected hook directory"},
		{name: "hook ancestor", group: "group"},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := testPaths(t)
			paths.Repo = ""
			origin := filepath.Join(paths.Home, "origin")
			clone := filepath.Join(paths.Home, "clone")
			initGit(t, origin)
			makeHookIn(t, origin, test.group, "preflight", "hook")
			gitCommitAll(t, origin, "safe hook")
			gitClone(t, origin, clone)
			selectedDir := filepath.Join(clone, "hooks")
			if test.group != "" {
				selectedDir = filepath.Join(selectedDir, test.group)
			}
			selectedDir = filepath.Join(selectedDir, "preflight")
			hook := ReadHook(selectedDir, clone, paths.HooksHomes())
			if result := BindSkills(paths, clone, []Skill{hook}); !result.OK() {
				t.Fatal(result.Message)
			}
			if result := Install(paths, hook); result.Status != Installed {
				t.Fatalf("install=%#v", result)
			}
			bindings, err := readConfiguredBindings(paths)
			if err != nil || len(bindings) != 1 {
				t.Fatalf("bindings=%#v error=%v", bindings, err)
			}
			before := repositoryRevision(clone)
			catalogLink := filepath.Join(paths.ConfigHome, "hooks", "preflight")
			installedLink := filepath.Join(paths.ClaudeHome, "hooks", "preflight")

			unsafe := filepath.Join(origin, "hooks", "preflight")
			if test.group != "" {
				unsafe = filepath.Join(origin, "hooks", test.group)
			}
			if err := os.RemoveAll(unsafe); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join("..", "outside"), unsafe); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			gitCommitAll(t, origin, "unsafe hook symlink")

			result := RefreshRepository(paths, bindings[0])
			if result.Status != RefreshFailed || !strings.Contains(result.Message, "unsafe hook update") {
				t.Fatalf("refresh=%#v", result)
			}
			if after := repositoryRevision(clone); after != before {
				t.Fatalf("HEAD changed from %s to %s", before, after)
			}
			assertLinkTarget(t, catalogLink, selectedDir)
			assertLinkTarget(t, installedLink, selectedDir)
			if info, err := os.Lstat(selectedDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				t.Fatalf("selected hook redirected after rejected refresh: info=%v error=%v", info, err)
			}
		})
	}
}

func TestRefreshAllowsHookDeletion(t *testing.T) {
	for _, test := range []struct {
		name       string
		removeHook func(*testing.T, string)
	}{
		{
			name: "whole directory",
			removeHook: func(t *testing.T, hookDir string) {
				t.Helper()
				if err := os.RemoveAll(hookDir); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "marker only",
			removeHook: func(t *testing.T, hookDir string) {
				t.Helper()
				if err := os.Remove(filepath.Join(hookDir, HookKind.Marker())); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := testPaths(t)
			paths.Repo = ""
			origin := filepath.Join(paths.Home, "origin")
			clone := filepath.Join(paths.Home, "clone")
			initGit(t, origin)
			originHook := makeHookIn(t, origin, "", "preflight", "hook")
			if err := os.WriteFile(filepath.Join(originHook, "README.md"), []byte("keeps the hook directory in Git\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitCommitAll(t, origin, "hook before deletion")
			gitClone(t, origin, clone)
			cloneHook := filepath.Join(clone, "hooks", "preflight")
			hook := ReadHook(cloneHook, clone, paths.HooksHomes())
			if result := BindSkills(paths, clone, []Skill{hook}); !result.OK() {
				t.Fatal(result.Message)
			}
			bindings, err := readConfiguredBindings(paths)
			if err != nil || len(bindings) != 1 {
				t.Fatalf("bindings=%#v error=%v", bindings, err)
			}

			test.removeHook(t, originHook)
			gitCommitAll(t, origin, "delete hook")
			wantRevision := repositoryRevision(origin)
			result := RefreshRepository(paths, bindings[0])
			if result.Status != Pulled {
				t.Fatalf("refresh=%#v", result)
			}
			if got := repositoryRevision(clone); got != wantRevision {
				t.Fatalf("clone revision=%s want=%s", got, wantRevision)
			}
			if _, err := os.Lstat(filepath.Join(cloneHook, HookKind.Marker())); !os.IsNotExist(err) {
				t.Fatalf("deleted hook marker remains: %v", err)
			}
		})
	}
}

func TestRefreshAllowsSafeHookRenameAfterTreePreflight(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	origin := filepath.Join(paths.Home, "origin")
	clone := filepath.Join(paths.Home, "clone")
	initGit(t, origin)
	makeHookIn(t, origin, "", "before", "same hook")
	gitCommitAll(t, origin, "hook before rename")
	gitClone(t, origin, clone)
	oldDir := filepath.Join(clone, "hooks", "before")
	hook := ReadHook(oldDir, clone, paths.HooksHomes())
	if result := BindSkills(paths, clone, []Skill{hook}); !result.OK() {
		t.Fatal(result.Message)
	}
	if result := Install(paths, hook); result.Status != Installed {
		t.Fatalf("install=%#v", result)
	}
	bindings, err := readConfiguredBindings(paths)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%#v error=%v", bindings, err)
	}

	originOld := filepath.Join(origin, "hooks", "before")
	originNew := filepath.Join(origin, "hooks", "after")
	if err := os.Rename(originOld, originNew); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(originNew, "HOOK.md")
	body, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(strings.Replace(string(body), "name: before", "name: after", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, origin, "safe hook rename")

	result := RefreshRepository(paths, bindings[0])
	if result.Status != Pulled {
		t.Fatalf("refresh=%#v", result)
	}
	newDir := filepath.Join(clone, "hooks", "after")
	assertLinkTarget(t, filepath.Join(paths.ConfigHome, "hooks", "after"), newDir)
	assertLinkTarget(t, filepath.Join(paths.ClaudeHome, "hooks", "after"), newDir)
	for _, oldLink := range []string{
		filepath.Join(paths.ConfigHome, "hooks", "before"),
		filepath.Join(paths.ClaudeHome, "hooks", "before"),
	} {
		if _, err := os.Lstat(oldLink); !os.IsNotExist(err) {
			t.Fatalf("old link remains %s: %v", oldLink, err)
		}
	}
}

func mustOwnership(t *testing.T, paths Paths) ownershipState {
	t.Helper()
	state, err := configuredOwnership(paths)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

package grimoire

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBindInstallsSelectedHookAndRepositoryCatalogFindsIt(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repository")
	initGit(t, repository)
	hookDir := makeHookIn(t, repository, "git", "preflight", "Preflight")
	t.Chdir(repository)

	var output bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &output, Err: &output, Paths: paths, Config: Config{Boring: true}}
	if code := cli.Run(context.Background(), []string{"bind", "hook:hooks/git/preflight"}); code != 0 {
		t.Fatalf("bind hook code = %d: %s", code, output.String())
	}
	assertLinkTarget(t, filepath.Join(paths.ConfigHome, "hooks", "preflight"), hookDir)
	assertLinkTarget(t, filepath.Join(paths.HooksHomes()[0], "preflight"), hookDir)
	if !strings.Contains(output.String(), "preflight installed") {
		t.Fatalf("bind hook output = %s", output.String())
	}

	localPaths := paths
	localPaths.Repo = repository
	catalog, err := LoadCatalog(localPaths)
	if err != nil {
		t.Fatal(err)
	}
	found, err := catalog.Find("hook:hooks/git/preflight")
	if err != nil || found == nil || found.Kind != HookKind {
		t.Fatalf("local hook = %#v, %v", found, err)
	}
	if len(paths.KnownHooksHomes()) == 0 {
		t.Fatal("known hook homes are empty")
	}
}

func TestInstallRejectsHookOutsideRepositoryHooksContainer(t *testing.T) {
	paths := testPaths(t)
	repository := filepath.Join(paths.Home, "repository")
	external := filepath.Join(repository, "external-hook")
	if err := os.MkdirAll(external, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "HOOK.md"), []byte("---\nname: external\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hook := Skill{Kind: HookKind, Dir: external, Name: "external", Repository: repository, homes: paths.HooksHomes()}
	result := Install(paths, hook)
	if result.Status != InstallBlocked || !strings.Contains(result.Message, "top-level hooks directory") {
		t.Fatalf("outside hook install = %#v", result)
	}
	if _, err := os.Lstat(filepath.Join(paths.HooksHomes()[0], "external")); !os.IsNotExist(err) {
		t.Fatalf("outside hook install mutated destination: %v", err)
	}
}

func TestJournalSymlinkActionValidationCoversEveryActionShape(t *testing.T) {
	paths := testPaths(t)
	skillsHome := paths.SkillsHomes()[0]
	hooksHome := paths.HooksHomes()[0]
	for _, home := range []string{skillsHome, hooksHome} {
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	repository := filepath.Join(paths.Home, "repository")
	skillBefore := filepath.Join(repository, "before")
	skillAfter := filepath.Join(repository, "after")
	hookBefore := filepath.Join(repository, "hooks", "before")
	hookAfter := filepath.Join(repository, "hooks", "after")
	bindings := []BoundRepository{{
		Path: repository, Skills: []string{"before", "after"},
		Hooks: []string{"hooks/before", "hooks/after"},
	}}
	allowed, err := journalAllowedResourceTargets(bindings)
	if err != nil {
		t.Fatal(err)
	}

	skillLink := filepath.Join(skillsHome, "alpha")
	skillNewLink := filepath.Join(skillsHome, "beta")
	hookLink := filepath.Join(hooksHome, "alpha")
	valid := []journalSymlinkAction{
		{Kind: symlinkCreate, Path: skillLink, Target: skillAfter, Directory: true},
		{Kind: symlinkReplace, Path: skillLink, Target: skillAfter, BeforeTarget: skillBefore, Directory: true, BeforeDirectory: true},
		{Kind: symlinkRemove, Path: skillLink, BeforeTarget: skillBefore, BeforeDirectory: true},
		{Kind: symlinkMove, Path: skillLink, NewPath: skillNewLink, Target: skillAfter, BeforeTarget: skillBefore, Directory: true, BeforeDirectory: true},
		{Kind: symlinkReplace, Path: hookLink, Target: hookAfter, BeforeTarget: hookBefore, Directory: true, BeforeDirectory: true},
	}
	for _, action := range valid {
		if err := validateJournalSymlinkAction(paths, action, allowed); err != nil {
			t.Fatalf("valid action %#v: %v", action, err)
		}
	}

	invalid := []journalSymlinkAction{
		{Kind: symlinkCreate, Path: skillLink, NewPath: skillNewLink, Target: skillAfter, Directory: true},
		{Kind: symlinkCreate, Path: skillLink, Target: "relative", Directory: true},
		{Kind: symlinkCreate, Path: skillLink, Target: skillAfter, BeforeTarget: skillBefore, Directory: true},
		{Kind: symlinkCreate, Path: skillLink, Target: skillAfter},
		{Kind: symlinkReplace, Path: skillLink, Target: skillAfter, BeforeTarget: skillBefore},
		{Kind: symlinkReplace, Path: skillLink, NewPath: skillNewLink, Target: skillAfter, BeforeTarget: skillBefore, Directory: true, BeforeDirectory: true},
		{Kind: symlinkReplace, Path: skillLink, Target: "relative", BeforeTarget: skillBefore, Directory: true, BeforeDirectory: true},
		{Kind: symlinkReplace, Path: skillLink, Target: skillAfter, BeforeTarget: "relative", Directory: true, BeforeDirectory: true},
		{Kind: symlinkRemove, Path: skillLink, Target: skillAfter, BeforeTarget: skillBefore, BeforeDirectory: true},
		{Kind: symlinkRemove, Path: skillLink, NewPath: skillNewLink, BeforeTarget: skillBefore, BeforeDirectory: true},
		{Kind: symlinkRemove, Path: skillLink, BeforeTarget: "relative", BeforeDirectory: true},
		{Kind: symlinkRemove, Path: skillLink, BeforeTarget: skillBefore},
		{Kind: symlinkMove, Path: skillLink, NewPath: hookLink, Target: skillAfter, BeforeTarget: skillBefore, Directory: true, BeforeDirectory: true},
		{Kind: symlinkMove, Path: skillLink, NewPath: "relative", Target: skillAfter, BeforeTarget: skillBefore, Directory: true, BeforeDirectory: true},
		{Kind: symlinkMove, Path: skillLink, NewPath: skillNewLink, Target: "relative", BeforeTarget: skillBefore, Directory: true, BeforeDirectory: true},
		{Kind: symlinkMove, Path: skillLink, NewPath: skillNewLink, Target: skillAfter, BeforeTarget: "relative", Directory: true, BeforeDirectory: true},
		{Kind: symlinkMove, Path: skillLink, NewPath: skillNewLink, Target: skillAfter, BeforeTarget: skillBefore},
		{Kind: symlinkActionKind("unknown"), Path: skillLink},
	}
	for _, action := range invalid {
		if err := validateJournalSymlinkAction(paths, action, allowed); err == nil {
			t.Fatalf("invalid action was accepted: %#v", action)
		}
	}
}

func TestHookSelectionRejectsInvalidScopeAndFilesystemShapes(t *testing.T) {
	paths := testPaths(t)
	repository := filepath.Join(paths.Home, "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateHookSelection(repository, "../outside", false); err == nil {
		t.Fatal("unsafe hook path was accepted")
	}
	if err := validateHookSelection("relative", "hooks/missing", false); err == nil {
		t.Fatal("relative repository was accepted")
	}
	if err := validateHookSelection(repository, "hooks/missing", true); err == nil {
		t.Fatal("missing required hook was accepted")
	}
	if err := os.WriteFile(filepath.Join(repository, "hooks"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateHookSelection(repository, "hooks/blocked", false); err == nil {
		t.Fatal("non-directory hook path was accepted")
	}

	markerRepository := filepath.Join(paths.Home, "marker-repository")
	hookDir := filepath.Join(markerRepository, "hooks", "missing-marker")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateHookSelection(markerRepository, "hooks/missing-marker", true); err == nil {
		t.Fatal("missing hook marker was accepted")
	}
	if err := os.Mkdir(filepath.Join(hookDir, "HOOK.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateHookSelection(markerRepository, "hooks/missing-marker", true); err == nil {
		t.Fatal("directory hook marker was accepted")
	}

	fileRepository := filepath.Join(paths.Home, "repository-file")
	if err := os.WriteFile(fileRepository, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateHookSelection(fileRepository, "hooks/preflight", false); err == nil {
		t.Fatal("repository file was accepted")
	}
}

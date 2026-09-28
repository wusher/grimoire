package grimoire

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPackStoresSymlinksWithoutFollowingThem(t *testing.T) {
	paths := testPaths(t)
	skillDir := makeSkill(t, paths, "", "alpha", "First")
	outside := filepath.Join(paths.Home, "outside-secret")
	if err := os.WriteFile(outside, []byte("must not be archived"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(skillDir, "reference")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	archivePath, err := Pack(paths, ReadSkill(skillDir, filepath.Dir(skillDir), nil))
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name != "alpha/reference" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read symlink entry: %v, %v", readErr, closeErr)
		}
		if string(body) != outside || strings.Contains(string(body), "must not") {
			t.Fatalf("symlink entry = %q, want target path only", body)
		}
		return
	}
	t.Fatal("archive omitted symlink entry")
}

func TestPackErrorPathsLeaveNoTemporaryArchive(t *testing.T) {
	paths := testPaths(t)
	skillDir := makeSkill(t, paths, "", "alpha", "First")
	skill := ReadSkill(skillDir, filepath.Dir(skillDir), nil)
	paths.Output = filepath.Join(paths.Home, "output-file")
	if err := os.WriteFile(paths.Output, []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Pack(paths, skill); err == nil {
		t.Fatal("Pack succeeded with a regular file as output directory")
	}

	paths.Output = filepath.Join(paths.Home, "output")
	skill.Dir = filepath.Join(paths.Home, "missing-skill")
	if _, err := Pack(paths, skill); err == nil {
		t.Fatal("Pack succeeded for a missing skill directory")
	}
	entries, err := os.ReadDir(paths.Output)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if isPackTemporary(entry.Name()) {
			t.Fatalf("temporary archive leaked after failure: %s", entry.Name())
		}
	}
}

func TestRenamePackedArchiveCopiesAcrossFilesystems(t *testing.T) {
	shared := "/dev/shm"
	if info, err := os.Stat(shared); err != nil || !info.IsDir() {
		t.Skip("/dev/shm is unavailable")
	}
	sourceDir := t.TempDir()
	outputDir, err := os.MkdirTemp(shared, "grimoire-pack-test-")
	if err != nil {
		t.Skipf("cannot create cross-filesystem output: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outputDir) })
	source := filepath.Join(sourceDir, "archive.tmp")
	destination := filepath.Join(outputDir, "archive.zip")
	if err := os.WriteFile(source, []byte("archive bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := renamePackedArchive(source, destination, outputDir); err != nil {
		t.Skipf("filesystems do not exercise rename fallback: %v", err)
	}
	body, err := os.ReadFile(destination)
	if err != nil || string(body) != "archive bytes" {
		t.Fatalf("copied archive = %q, %v", body, err)
	}
}

func TestInstallAndUninstallEdgeStates(t *testing.T) {
	t.Run("invalid ownership", func(t *testing.T) {
		paths := testPaths(t)
		dir := makeSkill(t, paths, "", "alpha", "First")
		skill := ReadSkill(dir, filepath.Join(paths.Repo, "skills"), paths.SkillsHomes())
		if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths.OwnershipFile(), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		for name, result := range map[string]InstallResult{"install": Install(paths, skill), "uninstall": Uninstall(paths, skill)} {
			if result.Status != InstallBlocked || !strings.Contains(result.Message, "ownership") {
				t.Errorf("%s = %#v", name, result)
			}
		}
	})

	t.Run("foreign link", func(t *testing.T) {
		paths := testPaths(t)
		dir := makeSkill(t, paths, "", "alpha", "First")
		skill := ReadSkill(dir, filepath.Join(paths.Repo, "skills"), paths.SkillsHomes())
		link := skill.LinkPaths()[0]
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(paths.Home, "foreign"), link); err != nil {
			t.Fatal(err)
		}
		result := Install(paths, skill)
		if result.Status != InstallBlocked || !strings.Contains(result.Message, "not ours") {
			t.Fatalf("foreign install = %#v", result)
		}
	})

	t.Run("stale ownership is released", func(t *testing.T) {
		paths := testPaths(t)
		dir := makeSkill(t, paths, "", "alpha", "First")
		skill := ReadSkill(dir, filepath.Join(paths.Repo, "skills"), paths.SkillsHomes())
		link := skill.LinkPaths()[0]
		state := ownershipState{Version: ownershipVersion}
		state.set(link, dir)
		if err := writeOwnership(paths, state); err != nil {
			t.Fatal(err)
		}
		result := Uninstall(paths, skill)
		if result.Status != Missing || !strings.Contains(result.Message, "stale ownership removed") || len(result.Changes) != 1 {
			t.Fatalf("stale uninstall = %#v", result)
		}
	})

	t.Run("changed target is released but untouched", func(t *testing.T) {
		paths := testPaths(t)
		dir := makeSkill(t, paths, "", "alpha", "First")
		skill := ReadSkill(dir, filepath.Join(paths.Repo, "skills"), paths.SkillsHomes())
		link := skill.LinkPaths()[0]
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		foreign := filepath.Join(paths.Home, "foreign")
		if err := os.Symlink(foreign, link); err != nil {
			t.Fatal(err)
		}
		state := ownershipState{Version: ownershipVersion}
		state.set(link, dir)
		if err := writeOwnership(paths, state); err != nil {
			t.Fatal(err)
		}
		result := Uninstall(paths, skill)
		if result.Status != Missing || len(result.Changes) != 1 {
			t.Fatalf("changed-target uninstall = %#v", result)
		}
		assertLinkTarget(t, link, foreign)
	})

	t.Run("regular file cannot be inspected as installed link", func(t *testing.T) {
		paths := testPaths(t)
		dir := makeSkill(t, paths, "", "alpha", "First")
		skill := ReadSkill(dir, filepath.Join(paths.Repo, "skills"), paths.SkillsHomes())
		link := skill.LinkPaths()[0]
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(link, []byte("foreign"), 0o644); err != nil {
			t.Fatal(err)
		}
		result := Uninstall(paths, skill)
		if result.Status != InstallBlocked || !strings.Contains(result.Message, "cannot inspect") {
			t.Fatalf("regular-file uninstall = %#v", result)
		}
	})
}

func TestPathsResolveOverridesAndBindingLayouts(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	t.Setenv("GRIMOIRE_HOME", filepath.Join(root, "grimoire"))
	t.Setenv("GRIMOIRE_CLAUDE_HOME", filepath.Join(root, "claude"))
	t.Setenv("GRIMOIRE_OPENCODE_HOME", filepath.Join(root, "opencode"))
	t.Setenv("GRIMOIRE_CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("GRIMOIRE_REPO", filepath.Join(root, "repo"))
	t.Setenv("GRIMOIRE_OUTPUT", filepath.Join(root, "archives"))
	paths, err := PathsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if paths.Home != root || paths.Output != filepath.Join(root, "archives") || paths.ConfigHome != filepath.Join(root, "grimoire") {
		t.Fatalf("paths from environment = %#v", paths)
	}
	if envOr("GRIMOIRE_MISSING_TEST", "fallback") != "fallback" {
		t.Fatal("envOr did not use fallback")
	}

	if err := os.MkdirAll(filepath.Join(paths.Repo, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	wantRepo, _ := filepath.Abs(paths.Repo)
	if libraries, err := paths.Libraries(); err != nil || !reflect.DeepEqual(libraries, []string{wantRepo}) {
		t.Fatalf("override libraries = %#v, %v", libraries, err)
	}
	if roots, err := paths.SkillsRoots(); err != nil || !reflect.DeepEqual(roots, []string{filepath.Join(wantRepo, "skills")}) {
		t.Fatalf("override roots = %#v, %v", roots, err)
	}
	if root, err := paths.RepoSkills(); err != nil || root != filepath.Join(wantRepo, "skills") {
		t.Fatalf("repo skills = %q, %v", root, err)
	}
	if output, err := paths.OutputDir(); err != nil || output != paths.Output {
		t.Fatalf("output override = %q, %v", output, err)
	}

	paths.Repo = ""
	paths.Output = ""
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.MkdirAll(filepath.Join(first, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	bindings := []BoundRepository{{Path: first}, {Path: second}}
	if err := writeBindings(paths, bindings); err != nil {
		t.Fatal(err)
	}
	if libraries, err := paths.Libraries(); err != nil || !reflect.DeepEqual(libraries, []string{first, second}) {
		t.Fatalf("manifest libraries = %#v, %v", libraries, err)
	}
	if roots, err := paths.SkillsRoots(); err != nil || !reflect.DeepEqual(roots, []string{filepath.Join(first, "skills"), second}) {
		t.Fatalf("manifest roots = %#v, %v", roots, err)
	}
	if output, err := paths.OutputDir(); err != nil || output != filepath.Join(first, "output") {
		t.Fatalf("default output = %q, %v", output, err)
	}
}

func TestPathsReportMissingAndMalformedBindings(t *testing.T) {
	paths := Paths{ConfigHome: t.TempDir()}
	for name, call := range map[string]func() error{
		"libraries": func() error { _, err := paths.Libraries(); return err },
		"roots":     func() error { _, err := paths.SkillsRoots(); return err },
		"repo":      func() error { _, err := paths.RepoSkills(); return err },
		"output":    func() error { _, err := paths.OutputDir(); return err },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "no skills are bound") {
			t.Errorf("%s error = %v", name, err)
		}
	}
	if err := os.WriteFile(paths.BindingsFile(), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := paths.Libraries(); err == nil || !strings.Contains(err.Error(), "bindings") {
		t.Fatalf("malformed bindings error = %v", err)
	}
}

package grimoire

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatcherCoversWhitespaceDistanceAndRankingEdges(t *testing.T) {
	if score, ok := MatchScore("   ", "anything"); !ok || score != 0 {
		t.Fatalf("blank query = %d, %v", score, ok)
	}
	if _, ok := MatchScore("a z", "a0123456789z"); ok {
		t.Fatal("overly sparse match was accepted")
	}
	if !oneEditApart([]rune("alpha"), []rune("alphas")) {
		t.Fatal("trailing insertion was not one edit")
	}
	if oneEditApart([]rune("a"), []rune("abcd")) {
		t.Fatal("large length difference was accepted")
	}
	if score, ok := skillMatchScore("   ", Skill{Name: "alpha"}); !ok || score != 0 {
		t.Fatalf("blank skill query = %d, %v", score, ok)
	}
	ranked := rankSkills("a", []Skill{{Name: "far-away"}, {Name: "alpha"}, {Name: "beta"}})
	if len(ranked) != 3 {
		t.Fatalf("ranked skills = %#v", ranked)
	}
}

func TestOwnershipRestoreAndHookResourceValidationBranches(t *testing.T) {
	paths := testPaths(t)
	if err := os.MkdirAll(paths.ConfigHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.OwnershipFile(), []byte(`{"version":1,"links":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := restoreOwnership(paths, ownershipState{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.OwnershipFile()); !os.IsNotExist(err) {
		t.Fatalf("unpersisted ownership was not removed: %v", err)
	}
	persisted := ownershipState{Version: ownershipVersion, persisted: true}
	persisted.set(filepath.Join(paths.SkillsHomes()[0], "alpha"), filepath.Join(paths.Home, "alpha"))
	if err := restoreOwnership(paths, persisted); err != nil {
		t.Fatal(err)
	}
	if _, err := configuredOwnership(paths); err != nil {
		t.Fatal(err)
	}

	if err := validateHookResource(Skill{Kind: HookKind, Dir: filepath.Join(paths.Home, "hooks", "missing")}, false); err == nil {
		t.Fatal("hook without repository was accepted")
	}
	repository := filepath.Join(paths.Home, "repository")
	if err := os.MkdirAll(filepath.Join(repository, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	resource := Skill{Kind: HookKind, Repository: repository, Dir: filepath.Join(repository, "hooks", "missing")}
	if err := validateHookResource(resource, false); err != nil {
		t.Fatalf("missing optional hook resource: %v", err)
	}
}

func TestWriteJSONFileReportsPreparationAndEncodingFailures(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFile(filepath.Join(blocked, "state.json"), map[string]string{}); err == nil {
		t.Fatal("blocked parent was accepted")
	}
	if err := writeJSONFile(filepath.Join(root, "channel.json"), make(chan int)); err == nil {
		t.Fatal("unsupported JSON value was accepted")
	}
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFile(target, map[string]string{"key": "value"}); err == nil {
		t.Fatal("directory target was replaced")
	}
}

func TestViewportAndColoredBlurbBoundaryHandling(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "not-a-terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if got := terminalViewport(file); got != (viewport{columns: 80, rows: 24}) {
		t.Fatalf("fallback viewport = %#v", got)
	}
	if frame := smallViewportFrame(viewport{}, viewport{columns: 40, rows: 12}); frame != nil {
		t.Fatalf("zero-sized small frame = %#v", frame)
	}
	if fitted := fitViewport([]string{"text"}, viewport{columns: 10}); fitted != nil {
		t.Fatalf("zero-height fit = %#v", fitted)
	}
	page := newPageWidth(Theme{color: true, columns: 28}, 28)
	lines := page.Blurb("one two three four five six seven eight nine ten eleven twelve thirteen fourteen")
	if len(lines) != 2 {
		t.Fatalf("colored blurb lines = %#v", lines)
	}
}

func TestCatalogSkipsMissingRootsAndRejectsInvalidSkillManifests(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = filepath.Join(paths.Home, "missing-repository")
	catalog, err := LoadCatalog(paths)
	if err != nil || len(catalog.Skills) != 0 {
		t.Fatalf("missing repository catalog = %#v, %v", catalog, err)
	}

	paths = testPaths(t)
	paths.Repo = ""
	repository := filepath.Join(paths.Home, "repository")
	if err := writeBindings(paths, []BoundRepository{{Path: repository, Skills: []string{"missing"}}}); err != nil {
		t.Fatal(err)
	}
	catalog, err = LoadCatalog(paths)
	if err != nil || len(catalog.Skills) != 0 {
		t.Fatalf("missing selected skill = %#v, %v", catalog, err)
	}
	manifest := filepath.Join(repository, "invalid", "SKILL.md")
	if err := os.MkdirAll(manifest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeBindings(paths, []BoundRepository{{Path: repository, Skills: []string{"invalid"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCatalog(paths); err == nil {
		t.Fatal("directory SKILL.md was accepted")
	}

	fileRoot := filepath.Join(paths.Home, "not-a-directory")
	if err := os.WriteFile(fileRoot, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (&Catalog{}).walk(fileRoot, fileRoot, 0, SkillKind, paths.SkillsHomes()); err == nil {
		t.Fatal("catalog walked a regular file")
	}
}

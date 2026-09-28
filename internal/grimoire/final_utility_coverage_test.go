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

func TestViewportAndColoredBlurbBoundaryHandling(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "not-a-terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
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
	if err := (&Catalog{}).walk(fileRoot, fileRoot, 0); err == nil {
		t.Fatal("catalog walked a regular file")
	}
}

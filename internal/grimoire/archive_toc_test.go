package grimoire

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackExcludesItsArchivesAndKeepsZipResources(t *testing.T) {
	paths := testPaths(t)
	skillDir := makeSkill(t, paths, "", "alpha", "First")
	paths.Output = filepath.Join(skillDir, "artifacts")
	if err := os.MkdirAll(paths.Output, 0o755); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(paths.Output, "alpha.zip")
	if err := os.WriteFile(destination, []byte("old archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	resource := filepath.Join(paths.Output, "reference.zip")
	if err := os.WriteFile(resource, []byte("zip resource"), 0o644); err != nil {
		t.Fatal(err)
	}

	archivePath, err := Pack(paths, ReadSkill(skillDir, filepath.Dir(skillDir), nil))
	if err != nil {
		t.Fatal(err)
	}
	if archivePath != destination {
		t.Fatalf("archive path = %q, want %q", archivePath, destination)
	}
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()

	foundResource := false
	for _, file := range archive.File {
		if file.Name == "alpha/artifacts/alpha.zip" {
			t.Fatal("archive contains the existing destination archive")
		}
		if strings.HasPrefix(file.Name, "alpha/artifacts/.alpha-") && strings.HasSuffix(file.Name, ".zip") {
			t.Fatalf("archive contains its temporary archive %q", file.Name)
		}
		if file.Name != "alpha/artifacts/reference.zip" {
			continue
		}
		foundResource = true
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if string(contents) != "zip resource" {
			t.Fatalf("zip resource = %q", contents)
		}
	}
	if !foundResource {
		t.Fatal("archive does not contain the unrelated zip resource")
	}
}

func TestPackExcludesArchivesThroughSymlinkedOutput(t *testing.T) {
	paths := testPaths(t)
	skillDir := makeSkill(t, paths, "", "alpha", "First")
	outputTarget := filepath.Join(skillDir, "artifacts")
	if err := os.MkdirAll(outputTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	paths.Output = filepath.Join(paths.Home, "output")
	if err := os.Symlink(outputTarget, paths.Output); err != nil {
		t.Skipf("create output symlink: %v", err)
	}
	destination := filepath.Join(outputTarget, "alpha.zip")
	if err := os.WriteFile(destination, []byte("old archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	resource := filepath.Join(outputTarget, "reference.zip")
	if err := os.WriteFile(resource, []byte("zip resource"), 0o644); err != nil {
		t.Fatal(err)
	}

	archivePath, err := Pack(paths, ReadSkill(skillDir, filepath.Dir(skillDir), nil))
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(paths.Output, "alpha.zip")
	if archivePath != wantPath {
		t.Fatalf("archive path = %q, want %q", archivePath, wantPath)
	}
	assertPackArchiveContents(t, archivePath, "alpha/artifacts/reference.zip")
}

func TestConcurrentPackCallsExcludeTemporaryArchives(t *testing.T) {
	t.Run("same skill", func(t *testing.T) {
		paths, skill := concurrentPackFixture(t)
		skills := make([]Skill, 6)
		for index := range skills {
			skills[index] = skill
		}
		assertConcurrentPacks(t, paths, skills, map[string]string{
			"alpha.zip": "alpha/zz-artifacts/reference.zip",
		})
	})

	t.Run("different skills", func(t *testing.T) {
		paths, alpha := concurrentPackFixture(t)
		betaDir := makeSkill(t, paths, "", "beta", "Second")
		writePackPayload(t, betaDir, 8<<20)
		beta := ReadSkill(betaDir, filepath.Dir(betaDir), nil)
		skills := []Skill{alpha}
		for range 5 {
			skills = append(skills, beta)
		}
		assertConcurrentPacks(t, paths, skills, map[string]string{
			"alpha.zip": "alpha/zz-artifacts/reference.zip",
		})
	})
}

func concurrentPackFixture(t *testing.T) (Paths, Skill) {
	t.Helper()
	paths := testPaths(t)
	skillDir := makeSkill(t, paths, "", "alpha", "First")
	writePackPayload(t, skillDir, 2<<20)
	paths.Output = filepath.Join(skillDir, "zz-artifacts")
	if err := os.MkdirAll(paths.Output, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Output, "reference.zip"), []byte("zip resource"), 0o644); err != nil {
		t.Fatal(err)
	}
	return paths, ReadSkill(skillDir, filepath.Dir(skillDir), nil)
}

func writePackPayload(t *testing.T, skillDir string, size int) {
	t.Helper()
	payload := make([]byte, size)
	state := uint32(1)
	for index := range payload {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		payload[index] = byte(state)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "00-payload.bin"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertConcurrentPacks(t *testing.T, paths Paths, skills []Skill, resources map[string]string) {
	t.Helper()
	type result struct {
		path string
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, len(skills))
	for _, skill := range skills {
		go func() {
			<-start
			path, err := Pack(paths, skill)
			results <- result{path: path, err: err}
		}()
	}
	close(start)

	archives := make(map[string]bool)
	for range skills {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		archives[result.path] = true
	}
	for archivePath := range archives {
		assertPackArchiveContents(t, archivePath, resources[filepath.Base(archivePath)])
	}
}

func assertPackArchiveContents(t *testing.T, archivePath, resourceName string) {
	t.Helper()
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()

	foundResource := false
	for _, file := range archive.File {
		name := filepath.Base(file.Name)
		if isPackTemporary(name) || strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".zip") {
			t.Fatalf("archive contains temporary file %q", file.Name)
		}
		if file.Name == resourceName {
			foundResource = true
		}
		if strings.HasSuffix(file.Name, "/"+filepath.Base(archivePath)) {
			t.Fatalf("archive contains the existing destination archive %q", file.Name)
		}
	}
	if resourceName != "" && !foundResource {
		t.Fatalf("archive does not contain %q", resourceName)
	}
}

func TestCatalogQueryKeepsSpacesAsTermSeparators(t *testing.T) {
	query, changed := editCatalogQuery("second", "space")
	if !changed || query != "second " {
		t.Fatalf("space edit = %q, %v", query, changed)
	}
	for key := range strings.SplitSeq("spells", "") {
		query, changed = editCatalogQuery(query, key)
		if !changed {
			t.Fatalf("key %q did not change the query", key)
		}
	}

	skills := []Skill{
		{Name: "alpha", Group: "spells", Description: "A second charm"},
		{Name: "beta", Group: "spells", Description: "A first charm"},
	}
	found := filterCatalogSkills(query, skills)
	if len(found) != 1 || found[0].Name != "alpha" {
		t.Fatalf("filter %q = %#v", query, found)
	}
	if joined := filterCatalogSkills("secondspells", skills); len(joined) != 0 {
		t.Fatalf("filter without separator = %#v", joined)
	}
}

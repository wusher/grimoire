package grimoire

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoverageCLIFormattingAndReports(t *testing.T) {
	paths := testPaths(t)
	first := makeSkill(t, paths, "group", "alpha", "First skill")
	second := makeSkill(t, paths, "", "beta", "Second skill")
	alpha := ReadSkill(first, filepath.Join(paths.Repo, "skills"), paths.SkillsHomes())
	beta := ReadSkill(second, filepath.Join(paths.Repo, "skills"), paths.SkillsHomes())
	var out bytes.Buffer
	cli := &CLI{In: strings.NewReader(""), Out: &out, Err: &out, Paths: paths, Config: Config{Boring: true}}
	if cli.Run(context.Background(), []string{"help"}) != 0 {
		t.Fatal("help")
	}
	if cli.Run(context.Background(), []string{"unknown"}) != 1 {
		t.Fatal("unknown command")
	}
	if cli.Run(context.Background(), []string{"toc", "extra"}) != 1 {
		t.Fatal("toc args")
	}
	catalog := Catalog{Root: paths.Repo, Roots: []string{paths.Repo}, Homes: paths.SkillsHomes(), Skills: []Skill{alpha, beta}, Clashes: map[string][]Skill{}}
	cli.printTOC(catalog)
	cli.installSummary([]InstallResult{{Status: Installed, Skill: alpha}, {Status: InstallBlocked, Skill: beta}}, "linked into")
	for _, status := range []InstallStatus{Installed, Removed, Missing, AlreadyStatus, InstallBlocked} {
		cli.writeInstallResult(InstallResult{Status: status, Skill: alpha, Message: string(status), Changes: []PathChange{{Action: "created link", Path: alpha.Dir, Target: alpha.Dir}}})
	}
	for _, result := range []RefreshResult{{Repo: paths.Repo, Status: Pulled, Message: "updated", Changes: []RefreshChange{{Action: "renamed", Name: "alpha", Message: "ok"}}}, {Repo: paths.Repo, Status: RefreshSkipped, Message: "skip"}, {Repo: paths.Repo, Status: RefreshFailed, Message: "bad"}} {
		cli.showRefresh([]RefreshResult{result})
	}
	if _, err := cli.showBindingResults("bind", "subtitle", []BindResult{{Status: Bound, Target: paths.Repo, Message: "bound", Changes: []PathChange{{Action: "updated binding", Path: paths.BindingsFile()}}}}, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseBindingOptions([]string{"--unknown"}); err == nil {
		t.Fatal("unknown binding option")
	}
	if options, names, err := parseBindingOptions([]string{"--replace-legacy-root", "alpha"}); err != nil || !options.ReplaceLegacyCatalogRoot || len(names) != 1 {
		t.Fatal("binding options")
	}
	page := NewPage(Theme{color: true, icons: true, columns: 44})
	for _, text := range []string{page.Line("text"), page.Fill("left", "right", "· "), page.Fill(strings.Repeat("x", 80), "right", " "), page.border("╓", "┐", "mark"), page.Entry("alpha", "mark", true)} {
		if visibleWidth(text) > page.Width {
			t.Fatalf("page width: %q", text)
		}
	}
	if len(page.Blurb("one two three four five six seven eight nine")) == 0 || len(drawSigil([]string{"x"}, page, page.theme, Green)) == 0 {
		t.Fatal("page helpers")
	}
	if roman(944) != "CMXLIV" || weight(1024) == "" || shortPath(filepath.Join(paths.Home, "x"), paths.Home) == "" {
		t.Fatal("format helpers")
	}
}

func TestCoverageCatalogPathsAndSymlinkPlans(t *testing.T) {
	paths := testPaths(t)
	paths.Repo = ""
	if _, err := paths.RepoSkills(); err == nil {
		t.Fatal("unbound repo skills")
	}
	if _, err := paths.OutputDir(); err == nil {
		t.Fatal("unbound output")
	}
	if len(paths.KnownSkillsHomes()) == 0 || len(paths.SkillsHomes()) == 0 {
		t.Fatal("homes")
	}
	root := filepath.Join(paths.Home, "catalog")
	one := makeSkillIn(t, root, "one", "alpha", "First")
	two := makeSkillIn(t, root, "two", "alpha", "Second")
	deep := makeSkillIn(t, root, "one/deep/nested", "gamma", "Deep")
	catalog := Catalog{Root: root, Homes: paths.SkillsHomes(), Clashes: map[string][]Skill{}}
	if err := catalog.walk(root, root, 0, SkillKind, paths.SkillsHomes()); err != nil {
		t.Fatal(err)
	}
	catalog.finish()
	if len(catalog.TooDeep) == 0 || len(catalog.Clashes) == 0 {
		t.Fatalf("catalog = %#v", catalog)
	}
	if _, err := catalog.Find("alpha"); err == nil {
		t.Fatal("clash lookup")
	}
	if found, err := catalog.Find("one/alpha"); err != nil || found == nil {
		t.Fatalf("path lookup = %#v, %v", found, err)
	}
	if !catalog.Owns(one) || catalog.Owns(filepath.Join(root, "missing")) || relative(root, deep) == "" {
		t.Fatal("catalog ownership")
	}
	if ReadSkill(one, root, paths.SkillsHomes()).SkillMD() == "" || ReadSkill(two, root, nil).Mismatch() {
		t.Fatal("skill helpers")
	}

	dir := filepath.Join(paths.Home, "links")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	targetA, targetB := filepath.Join(paths.Home, "target-a"), filepath.Join(paths.Home, "target-b")
	if err := os.MkdirAll(targetA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(targetB, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	plan := &symlinkPlan{}
	if err := plan.Create(link, targetA, true); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil || plan.Verify() != nil {
		t.Fatalf("create plan: %v", err)
	}
	before, err := snapshotSymlink(link)
	if err != nil {
		t.Fatal(err)
	}
	plan = &symlinkPlan{}
	if err := plan.Replace(link, before, targetB, true); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil || plan.Verify() != nil {
		t.Fatalf("replace plan: %v", err)
	}
	before, err = snapshotSymlink(link)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(dir, "moved")
	plan = &symlinkPlan{}
	if err := plan.Move(link, moved, before, targetB, true); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil || plan.Verify() != nil {
		t.Fatalf("move plan: %v", err)
	}
	before, err = snapshotSymlink(moved)
	if err != nil {
		t.Fatal(err)
	}
	plan = &symlinkPlan{}
	if err := plan.Remove(moved, before); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if err := plan.Rollback(); err != nil {
		t.Fatal(err)
	}
}

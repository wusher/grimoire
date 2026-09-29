package grimoire

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type CLI struct {
	In          io.Reader
	Out         io.Writer
	Err         io.Writer
	Paths       Paths
	Config      Config
	setupErr    error
	familiarErr error
	configErr   error
}

func NewCLI(in io.Reader, out, errOut io.Writer) *CLI {
	paths, err := PathsFromEnv()
	var familiarErr error
	var config Config
	var configErr error
	if err == nil {
		paths.Familiar, familiarErr = configuredFamiliar(paths)
		config, configErr = configuredOptions(paths)
	}
	return &CLI{In: in, Out: out, Err: errOut, Paths: paths, Config: config, setupErr: err, familiarErr: familiarErr, configErr: configErr}
}

func (c *CLI) Run(_ context.Context, args []string) int {
	command := "help"
	if len(args) > 0 {
		command, args = args[0], args[1:]
	}
	help := command == "help" || command == "-h" || command == "--help"
	if c.setupErr != nil && !help {
		c.trouble(c.setupErr.Error())
		return 1
	}
	if c.familiarErr != nil && !help && command != "familiar" {
		c.trouble(c.familiarErr.Error())
		return 1
	}
	if c.configErr != nil && !help && command != "config" {
		c.trouble(c.configErr.Error())
		return 1
	}
	if commandNeedsFamiliar(command) && c.Paths.Familiar == "" {
		ready, err := c.ensureFamiliar()
		if err != nil {
			c.trouble(err.Error())
			return 1
		}
		if !ready {
			return 0
		}
	}
	var err error
	var code int
	switch command {
	case "help", "-h", "--help":
		c.help(c.Out)
	case "toc", "list", "ls":
		err = c.toc(args)
	case "familiar":
		code, err = c.configureFamiliar(args)
	case "config":
		code, err = c.configure(args)
	case "cast":
		code, err = c.cast(args)
	case "banish":
		code, err = c.banish(args)
	case "volley":
		code, err = c.volley(args)
	case "hone":
		err = c.hone(args)
	case "bind", "bond":
		code, err = c.bind(args)
	case "unbind", "unbond":
		code, err = c.unbind(args)
	case "index":
		code, err = c.index(args)
	case "effigy":
		code, err = c.effigy(args)
	default:
		c.trouble("no command called " + command)
		c.help(c.Err)
		return 1
	}
	if err != nil {
		c.trouble(err.Error())
		return 1
	}
	return code
}

func (c *CLI) help(out io.Writer) {
	if c.Config.Boring {
		c.boringHelp(out)
		return
	}
	theme := c.theme(out)
	page := NewPage(theme)
	content := Page{theme: theme, Width: min(page.Width, 94), Inner: min(page.Inner, 88)}
	body := []string{}
	if page.Inner >= 58 {
		body = drawSigil(tomeSigil, page, theme, Violet)
	} else {
		body = append(body, page.Centered(theme.Paint("❦", Violet)))
	}
	if len(body) > 0 {
		body = append(body, "")
	}
	help := []string{content.Section("how to say it"), ""}
	help = append(help, content.Centered(theme.Paint("grimoire", Violet)+" "+theme.Paint("<command> [arguments]", Dim)), "")
	help = appendHelpBlock(help, content, theme, "the book", [][2]string{
		{"toc | list | ls", "Opens the searchable catalog."},
	})
	help = appendHelpBlock(help, content, theme, "the spells", [][2]string{
		{"cast [RESOURCE]", "Links one bound skill or hook. Without a selector, opens the tree."},
		{"banish [RESOURCE]", "Removes one linked resource. Without a selector, opens the tree."},
		{"volley", "Links every skill and hook in the book."},
		{"effigy [RESOURCE]", "Zips one bound skill or hook. Without a selector, opens a picker."},
	})
	help = appendHelpBlock(help, content, theme, "the binding", [][2]string{
		{"bind [SELECTOR...]", "Adds and installs skills and hooks from this Git repository. Alias: bond."},
		{"unbind [SELECTOR...]", "Forgets bound skills. Hook selectors work too. Without selectors, opens the tree. Alias: unbond."},
		{"  --replace-legacy-root", "Approves replacement of an old catalog-root link."},
		{"index", "Lists bound repositories and every familiar home."},
		{"  --refresh", "Refreshes now and repairs managed links."},
		{"familiar [NAME]", "Chooses Global, Claude, OpenCode, or Codex."},
		{"config boring [true|false]", "Turns minimal, non-interactive output on or off."},
		{"hone", "Repairs the links you already installed."},
		{"  --dry-run", "Shows the report. Changes nothing."},
		{"help | -h | --help", "Shows this page."},
	})
	help = append(help, content.Section("worth knowing"), "")
	for _, note := range []string{
		"Bind finds SKILL.md recursively and HOOK.md only below the top-level hooks/ folder.",
		"Use skill:NAME or hook:NAME when a bare name or path is ambiguous.",
		"The old unbind [SKILL...] form remains valid; hooks use hook:NAME.",
		"Hook links are storage only; Grimoire does not register or activate hooks.",
		"Refresh can change code used by separately activated hooks; review updates before refreshing.",
		"Full-screen views redraw after a terminal resize.",
		"Pass names or paths to skip pickers in scripts.",
		"Global uses ~/.agents/skills and ~/.agents/hooks and is the default familiar.",
		"NO_COLOR=1 turns color off. GRIMOIRE_ICONS=0 turns icons off.",
	} {
		lines := wrapWords(note, max(1, content.Inner-8))
		for index, line := range lines {
			marker := "      "
			if index == 0 {
				marker = "   " + theme.Paint("·", Amber) + "  "
			}
			help = append(help, marker+theme.Paint(line, Grey))
		}
	}
	contentPad := strings.Repeat(" ", max(0, (page.Inner-content.Inner)/2))
	for _, line := range help {
		if line == "" {
			body = append(body, "")
		} else {
			body = append(body, contentPad+line)
		}
	}
	fmt.Fprintln(out)
	writeLines(out, page.Bind(body, "grimoire", "keeps agent resources in one book", "grimoire toc  ·  grimoire cast"))
	fmt.Fprintln(out)
}

func (c *CLI) boringHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage: grimoire <command> [arguments]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Commands:")
	for _, line := range []string{
		"  toc | list | ls             List bound skills and hooks.",
		"  cast [RESOURCE]             Link one bound skill or hook.",
		"  banish [RESOURCE]           Remove one linked skill or hook.",
		"  volley                      Link all bound skills and hooks.",
		"  effigy [RESOURCE]           Write one resource zip file.",
		"  bind [SELECTOR...]          Add and install resources from this repository. Alias: bond.",
		"  unbind [SELECTOR...]        Unbind resources. Alias: unbond.",
		"    --replace-legacy-root     Approve replacement of an old catalog-root link.",
		"  index [--refresh]           List repositories and familiar homes; optionally refresh.",
		"  familiar [NAME]             Set global, claude, opencode, or codex.",
		"  config boring [true|false]  Set minimal, non-interactive output.",
		"  hone [--dry-run]            Repair installed links.",
		"  help | -h | --help          Show this help.",
	} {
		if visibleWidth(line) > c.theme(out).columns {
			c.writeResponsive(out, "", Grey, strings.TrimSpace(line), Grey)
		} else {
			fmt.Fprintln(out, line)
		}
	}
}

func appendHelpBlock(body []string, page Page, theme Theme, name string, rows [][2]string) []string {
	body = append(body, page.Section(name), "")
	for _, row := range rows {
		color := Violet
		if strings.HasPrefix(row[0], " ") {
			color = Amber
		}
		if page.Inner < 56 || visibleWidth(row[0]) > 22 {
			head := "    " + theme.Paint(strings.TrimSpace(row[0]), color)
			body = append(body, head)
			for _, line := range wrapWords(row[1], max(1, page.Inner-8)) {
				body = append(body, "        "+theme.Paint(line, Dim))
			}
			continue
		}
		head := "    " + theme.Paint(fmt.Sprintf("%-22s", row[0]), color)
		description := wrapWords(row[1], page.Inner-26)
		if len(description) == 0 {
			body = append(body, head)
			continue
		}
		body = append(body, head+theme.Paint(description[0], Dim))
		for _, line := range description[1:] {
			body = append(body, strings.Repeat(" ", 26)+theme.Paint(line, Dim))
		}
	}
	return append(body, "")
}

func (c *CLI) toc(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("toc takes no arguments")
	}
	catalog, err := LoadCatalog(c.Paths)
	if err != nil {
		return err
	}
	if !c.Config.Boring && len(catalog.Skills) > 0 {
		if input, output, ok := terminalFiles(c.In, c.Out); ok {
			err := (CatalogBrowser{In: input, Out: output, Theme: c.theme(output), Familiar: c.Paths.Familiar}).Browse(catalog.Skills)
			c.warnCatalog(catalog)
			return err
		}
	}
	c.printTOC(catalog)
	c.warnCatalog(catalog)
	return nil
}

func (c *CLI) printTOC(catalog Catalog) {
	if c.Config.Boring {
		if len(catalog.Skills) == 0 {
			c.writeResponsive(c.Out, "", Grey, "no bound skills or hooks", Grey)
			return
		}
		for _, skill := range catalog.Skills {
			status := "not installed"
			if skill.Installed() {
				status = "installed"
			}
			c.writeResponsive(c.Out, "", Grey, resourceDisplayName(skill)+"\t"+status, Grey)
		}
		return
	}
	theme := c.theme(c.Out)
	page := newPageWidth(theme, theme.columns)
	body := []string{}
	installed := 0
	if len(catalog.Skills) == 0 {
		body = append(body,
			page.Centered(theme.Paint("no skills in this book yet", Grey)),
			"",
			page.Centered(theme.Paint("find them first: grimoire bind", Dim)),
		)
	} else {
		body, installed = tocSkillBody(page, theme, catalog.Skills, multipleSkillRepositories(catalog.Skills))
	}
	folio := "an empty book"
	if len(catalog.Skills) > 0 {
		folio = tocFolio(installed, len(catalog.Skills), c.Paths.Familiar)
	} else if c.Paths.Familiar != "" {
		folio += "  ·  familiar: " + c.Paths.Familiar
	}
	fmt.Fprintln(c.Out)
	writeLines(c.Out, page.Bind(body, "grimoire", "table of contents", folio))
	fmt.Fprintln(c.Out)
}

func tocFolio(installed, total int, familiar string) string {
	return fmt.Sprintf("%d of %d installed  ·  ❦  ·  familiar: %s", installed, total, familiar)
}

func tocSkillBody(page Page, theme Theme, skills []Skill, multipleRepositories bool) ([]string, int) {
	body := []string{}
	installed := 0
	groups := orderedGroupsFor(skills, multipleRepositories)
	for index, group := range groups {
		label := group.name
		if label == "" {
			label = "loose leaves"
		}
		groupInstalled := 0
		for _, skill := range group.skills {
			if skill.Installed() {
				groupInstalled++
			}
		}
		body = append(body, page.Chapter(index+1, label, fmt.Sprintf("%d of %d installed", groupInstalled, len(group.skills)))...)
		for _, skill := range group.skills {
			isInstalled := skill.Installed()
			status := theme.Tag("skull", Red) + theme.Paint(fmt.Sprintf("%-9s", "not cast"), Blood)
			if isInstalled {
				status = theme.Tag("candle", Amber) + theme.Paint(fmt.Sprintf("%-9s", "installed"), Green)
				installed++
			}
			name := skill.Name
			if skill.Kind == HookKind {
				name = theme.Tag("hook", Magenta) + "hook:" + name
			}
			body = append(body, page.Entry(name, status, isInstalled))
			if skill.Description != "" {
				body = append(body, page.Blurb(skill.Description)...)
			}
		}
		if index < len(groups)-1 {
			body = append(body, "")
		}
	}
	return body, installed
}

type skillGroup struct {
	name   string
	skills []Skill
}

func orderedGroups(skills []Skill) []skillGroup {
	return orderedGroupsFor(skills, multipleSkillRepositories(skills))
}

func orderedGroupsFor(skills []Skill, multipleRepositories bool) []skillGroup {
	held := map[string][]Skill{}
	for _, skill := range skills {
		group := skillGroupKey(skill, multipleRepositories)
		held[group] = append(held[group], skill)
	}
	names := make([]string, 0, len(held))
	for name := range held {
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if _, ok := held[""]; ok {
		names = append([]string{""}, names...)
	}
	groups := make([]skillGroup, 0, len(names))
	for _, name := range names {
		groups = append(groups, skillGroup{name: name, skills: held[name]})
	}
	return groups
}

func roman(number int) string {
	values := []struct {
		value int
		mark  string
	}{{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"}, {100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"}, {10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"}}
	var out strings.Builder
	for _, item := range values {
		for number >= item.value {
			out.WriteString(item.mark)
			number -= item.value
		}
	}
	return out.String()
}

func (c *CLI) cast(args []string) (int, error) {
	catalog, err := LoadCatalog(c.Paths)
	if err != nil {
		return 1, err
	}
	if len(catalog.Clashes) > 0 {
		c.warnCatalog(catalog)
		return 1, fmt.Errorf("nothing was installed")
	}
	chosen, err := c.choose(args, catalog, catalog.Available(), "install")
	if err != nil {
		return 1, err
	}
	if len(chosen) == 0 {
		c.note("nothing picked")
		return 0, nil
	}
	return c.installResources(chosen, true)
}

func (c *CLI) installResources(chosen []Skill, showArt bool) (int, error) {
	results := make([]InstallResult, 0, len(chosen))
	blocked := false
	for _, resource := range chosen {
		result := Install(c.Paths, resource)
		results = append(results, result)
		blocked = blocked || result.Status == InstallBlocked
		c.writeInstallResult(result)
	}
	if showArt && !blocked && !c.Config.Boring {
		theme := c.theme(c.Out)
		page := NewPage(theme)
		if art := drawSigil(wandSigil, page, theme, Violet); len(art) > 0 {
			fmt.Fprintln(c.Out)
			writeLines(c.Out, art)
		}
	}
	if !c.Config.Boring {
		c.installSummary(results, "linked into")
	}
	if blocked && !showArt {
		c.note("binding saved; fix the blocked destination, then rerun grimoire bind to retry installation")
	}
	if blocked {
		return 1, nil
	}
	return 0, nil
}

func (c *CLI) banish(args []string) (int, error) {
	catalog, err := LoadCatalog(c.Paths)
	if err != nil {
		return 1, err
	}
	chosen, err := c.choose(args, catalog, catalog.Installed(), "remove")
	if err != nil {
		return 1, err
	}
	if len(chosen) == 0 {
		c.note("nothing picked")
		return 0, nil
	}
	results := make([]InstallResult, 0, len(chosen))
	blocked := false
	for _, skill := range chosen {
		result := Uninstall(c.Paths, skill)
		results = append(results, result)
		blocked = blocked || result.Status == InstallBlocked
		c.writeInstallResult(result)
	}
	if !blocked && !c.Config.Boring {
		theme := c.theme(c.Out)
		page := NewPage(theme)
		if art := drawSigil(bladeSigil, page, theme, Violet); len(art) > 0 {
			fmt.Fprintln(c.Out)
			writeLines(c.Out, art)
		}
	}
	if !c.Config.Boring {
		c.installSummary(results, "cut from")
	}
	if blocked {
		return 1, nil
	}
	return 0, nil
}

func (c *CLI) choose(args []string, catalog Catalog, pool []Skill, verb string) ([]Skill, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("%s accepts at most one resource selector", verb)
	}
	if len(args) == 1 {
		skill, err := catalog.Find(args[0])
		if err != nil {
			return nil, err
		}
		if skill == nil {
			return nil, fmt.Errorf("no skill or hook called %s", args[0])
		}
		return []Skill{*skill}, nil
	}
	if len(pool) == 0 {
		c.note("no skills left to " + verb + "; no hooks left")
		return nil, nil
	}
	if c.Config.Boring {
		command := map[string]string{"install": "cast", "remove": "banish", "pack": "effigy"}[verb]
		return nil, fmt.Errorf("boring mode has no picker; use grimoire %s SKILL or hook:NAME", command)
	}
	inFile, inOK := c.In.(*os.File)
	errFile, errOK := c.Err.(*os.File)
	if !inOK || !errOK {
		return nil, fmt.Errorf("this terminal cannot show a picker. Pass a name instead")
	}
	return (Picker{In: inFile, Out: errFile, Prompt: verb + " which?", Theme: c.theme(errFile)}).Pick(pool)
}

func (c *CLI) installSummary(results []InstallResult, where string) {
	theme := c.theme(c.Out)
	page := NewPage(theme)
	counts := map[InstallStatus]int{}
	names := make([]string, 0, len(results))
	for _, result := range results {
		counts[result.Status]++
		names = append(names, resourceDisplayName(result.Skill))
	}
	words := []string{}
	order := []struct {
		status InstallStatus
		word   string
		color  Color
	}{{Installed, "installed", Green}, {AlreadyStatus, "already there", Grey}, {Removed, "removed", Cyan}, {Missing, "not installed", Grey}, {InstallBlocked, "blocked", Red}}
	for _, item := range order {
		if count := counts[item.status]; count > 0 {
			words = append(words, theme.Paint(strconv.Itoa(count)+" "+item.word, item.color))
		}
	}
	rule := theme.Paint(strings.Repeat("─", 12)+" ✦ "+strings.Repeat("─", 12), Dim)
	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, page.Centered(clipVisible(rule, page.Inner)))
	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, page.Centered(theme.Paint(theme.Trim(strings.Join(names, "  ·  "), page.Inner), Violet)))
	fmt.Fprintln(c.Out, page.Centered(clipVisible(strings.Join(words, theme.Paint("  ·  ", Dim)), page.Inner)))
	fmt.Fprintln(c.Out)
	homes := map[string]bool{}
	for _, result := range results {
		for _, home := range result.Skill.LinkPaths() {
			homes[filepath.Dir(home)] = true
		}
	}
	orderedHomes := make([]string, 0, len(homes))
	for home := range homes {
		orderedHomes = append(orderedHomes, home)
	}
	sort.Strings(orderedHomes)
	for index, home := range orderedHomes {
		lead := "and"
		if index == 0 {
			lead = where
		}
		line := theme.Trim(lead+" "+shortPath(home, c.Paths.Home)+"/", page.Inner)
		fmt.Fprintln(c.Out, page.Centered(page.Quiet(line)))
	}
}

func installResultStyle(status InstallStatus) (string, Color) {
	icon, color := "cross", Red
	switch status {
	case Installed:
		icon, color = "check", Green
	case Removed:
		icon, color = "trash", Cyan
	case AlreadyStatus:
		icon, color = "star", Grey
	case Missing:
		icon, color = "circle", Grey
	}
	return icon, color
}

func (c *CLI) writeInstallResult(result InstallResult) {
	icon, color := installResultStyle(result.Status)
	if c.Config.Boring {
		icon = ""
	}
	c.writeResponsive(c.Out, icon, color, resourceDisplayName(result.Skill)+" "+result.Message, color)
	for _, change := range result.Changes {
		c.writePathChange(c.Out, change)
	}
}

func (c *CLI) volley(args []string) (int, error) {
	if len(args) != 0 {
		return 1, fmt.Errorf("volley takes no arguments")
	}
	catalog, err := LoadCatalog(c.Paths)
	if err != nil {
		return 1, err
	}
	if len(catalog.Clashes) > 0 {
		c.warnCatalog(catalog)
		return 1, fmt.Errorf("nothing was installed")
	}
	installed, skipped := []InstallResult{}, []InstallResult{}
	failed := []InstallResult{}
	for _, skill := range catalog.Skills {
		result := Install(c.Paths, skill)
		switch result.Status {
		case Installed:
			installed = append(installed, result)
		case AlreadyStatus:
			skipped = append(skipped, result)
		default:
			failed = append(failed, result)
		}
	}
	if c.Config.Boring {
		for _, result := range installed {
			c.writeInstallResult(result)
		}
		for _, result := range failed {
			c.writeInstallResult(result)
		}
		if len(skipped) > 0 {
			c.note(fmt.Sprintf("%d already installed", len(skipped)))
		}
		c.writeResponsive(c.Out, "", Grey, fmt.Sprintf("installed=%d skipped=%d failed=%d", len(installed), len(skipped), len(failed)), Grey)
	} else {
		theme := c.theme(c.Out)
		input, inputOK := c.In.(*os.File)
		output, outputOK := c.Out.(*os.File)
		if inputOK && outputOK {
			names := make([]string, 0, len(catalog.Skills))
			for _, skill := range catalog.Skills {
				names = append(names, skill.Name)
			}
			playFireworks(input, output, theme, names)
		}
		for _, result := range installed {
			c.writeInstallResult(result)
		}
		for _, result := range failed {
			c.writeInstallResult(result)
		}
		if len(skipped) > 0 {
			c.note(fmt.Sprintf("%d already installed", len(skipped)))
		}
		c.writeResponsive(c.Out, "flame", Amber, fmt.Sprintf("installed %d, skipped %d, failed %d", len(installed), len(skipped), len(failed)), map[bool]Color{true: Red, false: Grey}[len(failed) > 0])
	}
	if len(failed) > 0 {
		return 1, nil
	}
	return 0, nil
}

var playFireworks = runFireworks

func (c *CLI) hone(args []string) error {
	dryRun := false
	for _, arg := range args {
		if arg != "--dry-run" {
			return fmt.Errorf("unknown hone option %s", arg)
		}
		dryRun = true
	}
	changes, err := Hone(c.Paths, dryRun)
	if err != nil {
		changes = nil
	}
	if c.Config.Boring {
		if dryRun {
			c.writeResponsive(c.Out, "", Grey, "dry run; nothing was changed", Grey)
		}
		if err == nil && len(changes) == 0 {
			c.writeResponsive(c.Out, "", Grey, "nothing to repair", Grey)
		}
		for _, change := range changes {
			action := change.Action
			if dryRun {
				switch action {
				case "fixed":
					action = "would fix"
				case "removed":
					action = "would remove"
				case "adopted":
					action = "would adopt"
				case "released":
					action = "would release"
				}
			}
			c.writeResponsive(c.Out, "", Grey, fmt.Sprintf("%s %s: %s (%s)", action, change.Name, change.Message, shortPath(filepath.Join(change.Home, change.Name), c.Paths.Home)), Grey)
		}
		catalog, loadErr := LoadCatalog(c.Paths)
		if loadErr == nil {
			c.warnTooDeep(catalog)
		}
		return err
	}
	theme := c.theme(c.Out)
	c.writeResponsive(c.Out, "wrench", Magenta, "hone", Magenta)
	if dryRun {
		c.writeResponsive(c.Out, "star", Grey, "dry run. Nothing was changed.", Grey)
	}
	if err == nil && len(changes) == 0 {
		c.writeResponsive(c.Out, "star", Grey, "nothing to repair", Grey)
	}
	counts := map[string]int{}
	for _, change := range changes {
		counts[change.Action]++
		icon, color := "warn", Red
		action := change.Action
		switch change.Action {
		case "fixed":
			icon, color = "wrench", Cyan
			if dryRun {
				action = "would fix"
			}
		case "removed":
			icon, color = "trash", Amber
			if dryRun {
				action = "would remove"
			}
		case "adopted":
			icon, color = "check", Green
			if dryRun {
				action = "would adopt"
			}
		case "released":
			icon, color = "circle", Grey
			if dryRun {
				action = "would release"
			}
		}
		message := fmt.Sprintf("%s %s: %s (%s)", action, change.Name, change.Message, shortPath(filepath.Join(change.Home, change.Name), c.Paths.Home))
		c.writeResponsive(c.Out, icon, color, message, color)
	}
	if len(changes) > 0 {
		words := []string{}
		if count := counts["fixed"]; count > 0 {
			verb := "repaired"
			if dryRun {
				verb = "to repair"
			}
			words = append(words, theme.Paint(fmt.Sprintf("%d %s", count, verb), Cyan))
		}
		if count := counts["removed"]; count > 0 {
			verb := "removed"
			if dryRun {
				verb = "to remove"
			}
			words = append(words, theme.Paint(fmt.Sprintf("%d %s", count, verb), Amber))
		}
		if count := counts["clash"]; count > 0 {
			words = append(words, theme.Paint(fmt.Sprintf("%d blocked", count), Red))
		}
		if count := counts["adopted"]; count > 0 {
			words = append(words, theme.Paint(fmt.Sprintf("%d ownership recorded", count), Green))
		}
		if count := counts["released"]; count > 0 {
			words = append(words, theme.Paint(fmt.Sprintf("%d ownership released", count), Grey))
		}
		c.writeResponsive(c.Out, "", Grey, ansiPattern.ReplaceAllString(strings.Join(words, "  ·  "), ""), Grey)
	}
	catalog, loadErr := LoadCatalog(c.Paths)
	if loadErr == nil {
		c.warnTooDeep(catalog)
	}
	return err
}

func (c *CLI) effigy(args []string) (int, error) {
	catalog, err := LoadCatalog(c.Paths)
	if err != nil {
		return 1, err
	}
	chosen, err := c.choose(args, catalog, catalog.Skills, "pack")
	if err != nil {
		return 1, err
	}
	if len(chosen) == 0 {
		c.note("nothing packed")
		return 0, nil
	}
	failed := false
	for _, skill := range chosen {
		path, packErr := Pack(c.Paths, skill)
		if packErr != nil {
			failed = true
			c.writeResponsive(c.Err, "cross", Red, skill.Name+" "+packErr.Error(), Red)
			continue
		}
		info, _ := os.Stat(path)
		c.writeResponsive(c.Out, "plus", Green, fmt.Sprintf("packed %s to %s (%s)", skill.Name, shortPath(path, c.Paths.Home), weight(info.Size())), Green)
	}
	if failed {
		return 1, nil
	}
	return 0, nil
}

func (c *CLI) bind(args []string) (int, error) {
	options, args, err := parseBindingOptions(args)
	if err != nil {
		return 1, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return 1, err
	}
	root, discovered, err := DiscoverRepositoryResources(c.Paths, cwd)
	if err != nil {
		return 1, err
	}
	if len(discovered) == 0 {
		return 1, fmt.Errorf("no skills or hooks found (hooks must be below the top-level hooks/ folder)")
	}
	chosen, err := c.chooseSkills(args, discovered, "discovered", "bind")
	if err != nil {
		return 1, err
	}
	if len(chosen) == 0 {
		c.note("nothing picked")
		return 0, nil
	}
	result := BindSkillsWithOptions(c.Paths, root, chosen, options)
	if !result.OK() {
		return c.showBindingResults("bind", "the book takes your hand", []BindResult{result}, true)
	}
	if _, err := c.showBindingResults("bind", "the book takes your hand", []BindResult{result}, false); err != nil {
		return 1, err
	}
	return c.installResources(chosen, false)
}

func (c *CLI) chooseSkills(args []string, skills []Skill, scope, command string) ([]Skill, error) {
	if len(args) > 0 {
		chosen := make([]Skill, 0, len(args))
		seen := map[string]bool{}
		for _, wanted := range args {
			kind, selector, qualified := parseResourceSelector(wanted)
			matches := matchResourceSelectors(skills, selector, &kind)
			if !qualified || len(matches) == 0 {
				matches = matchResourceSelectors(skills, wanted, nil)
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("no %s skill called %s (or hook with that selector)", scope, wanted)
			}
			if len(matches) > 1 {
				return nil, fmt.Errorf("%s matches more than one resource; use skill:%s or hook:%s", wanted, selector, selector)
			}
			if !seen[matches[0].identity()] {
				seen[matches[0].identity()] = true
				chosen = append(chosen, matches[0])
			}
		}
		return chosen, nil
	}
	if c.Config.Boring {
		return nil, fmt.Errorf("boring mode has no picker; use grimoire %s NAME or hook:NAME", command)
	}
	inFile, inOK := c.In.(*os.File)
	errFile, errOK := c.Err.(*os.File)
	if !inOK || !errOK {
		return nil, fmt.Errorf("this terminal cannot show a picker. Pass skill names or paths instead; hooks use hook:NAME or hook:path")
	}
	picker := Picker{In: inFile, Out: errFile, Prompt: command + " which?", Theme: c.theme(errFile)}
	if command == "bind" {
		picker.SelectAllLabel = "bind all"
	}
	return picker.Pick(skills)
}

func matchResourceSelectors(resources []Skill, selector string, kind *ResourceKind) []Skill {
	var matches []Skill
	for _, resource := range resources {
		if (kind == nil || resource.Kind == *kind) && (resource.RepoPath() == normalizeResourceSelector(resource.Kind, selector) || resource.Name == selector) {
			matches = append(matches, resource)
		}
	}
	return matches
}

func resourceDisplayName(resource Skill) string {
	if resource.Kind == HookKind {
		return "hook:" + resource.Name
	}
	return resource.Name
}

func (c *CLI) index(args []string) (int, error) {
	refresh := false
	for _, arg := range args {
		if arg != "--refresh" {
			return 1, fmt.Errorf("unknown index option %s", arg)
		}
		refresh = true
	}
	repositories, err := BoundRepositories(c.Paths)
	if err != nil {
		return 1, err
	}
	if !refresh {
		c.showIndex(repositories)
		if c.Config.Boring {
			c.writeResponsive(c.Out, "", Grey, "run grimoire index --refresh to refresh repositories", Grey)
			return 0, nil
		}
		refresh, err = c.confirmIndexRefresh()
		if err != nil {
			return 1, err
		}
		if !refresh {
			return 0, nil
		}
	}
	results := make([]RefreshResult, 0, len(repositories))
	failed := false
	for _, repository := range repositories {
		result := RefreshRepository(c.Paths, repository)
		results = append(results, result)
		if result.Status == RefreshFailed {
			failed = true
		}
	}
	c.showRefresh(results)
	if failed {
		return 1, nil
	}
	return 0, nil
}

func (c *CLI) showIndex(repositories []BoundRepository) {
	familiars := indexFamiliars(c.Paths, repositories)
	if c.Config.Boring {
		for _, repository := range repositories {
			status := selectionMessage(repository.Skills, repository.Hooks, "bound")
			if len(repository.Skills)+len(repository.Hooks) == 0 {
				status = "whole repository"
			}
			if info, err := os.Stat(repository.Path); err != nil || !info.IsDir() {
				status = "missing folder"
			} else if missing, broken, issueErr := repositoryIssues(c.Paths, repository); issueErr != nil {
				status = "inspection failed: " + issueErr.Error()
			} else if missing > 0 || broken > 0 {
				issues := []string{}
				if missing > 0 {
					issues = append(issues, fmt.Sprintf("%d missing resource%s", missing, plural(missing)))
				}
				if broken > 0 {
					issues = append(issues, fmt.Sprintf("%d broken link%s", broken, plural(broken)))
				}
				status = strings.Join(issues, ", ")
			}
			c.writeResponsive(c.Out, "", Grey, shortPath(repository.Path, c.Paths.Home)+"\t"+status, Grey)
		}
		for _, familiar := range familiars {
			message := fmt.Sprintf("familiar=%s\tskills=%s\thooks=%s\tinstalled=%d\tbound=%d\tblocked=%d", familiar.Name, shortPath(familiar.Home, c.Paths.Home), shortPath(familiar.HookHome, c.Paths.Home), len(familiar.Skills), familiar.Bound, familiar.Blocked)
			c.writeResponsive(c.Out, "", Grey, message, Grey)
			for _, skill := range familiar.Skills {
				c.writeResponsive(c.Out, "", Grey, skill.Kind.Name()+"="+skill.Name+"\tlink="+shortPath(skill.Link, c.Paths.Home)+"\ttarget="+shortPath(skill.Target, c.Paths.Home), Grey)
			}
		}
		return
	}
	theme := c.theme(c.Out)
	page := NewPage(theme)
	body := []string{}
	for index, repository := range repositories {
		status := theme.Paint(selectionMessage(repository.Skills, repository.Hooks, "bound"), Green)
		if len(repository.Skills)+len(repository.Hooks) == 0 {
			status = theme.Paint("whole repository", Grey)
		}
		if info, err := os.Stat(repository.Path); err != nil || !info.IsDir() {
			status = theme.Tag("warn", Red) + theme.Paint("missing folder", Red)
		} else if missing, broken, issueErr := repositoryIssues(c.Paths, repository); issueErr != nil {
			status = theme.Tag("warn", Red) + theme.Paint("inspection failed: "+issueErr.Error(), Red)
		} else if missing > 0 || broken > 0 {
			issues := []string{}
			if missing > 0 {
				issues = append(issues, fmt.Sprintf("%d missing resource%s", missing, plural(missing)))
			}
			if broken > 0 {
				issues = append(issues, fmt.Sprintf("%d broken link%s", broken, plural(broken)))
			}
			status = theme.Tag("warn", Amber) + theme.Paint(strings.Join(issues, ", "), Amber)
		}
		entry := theme.Paint(fmt.Sprintf("%5s", roman(index+1)+"."), Amber) + " " + theme.Paint(shortPath(repository.Path, c.Paths.Home), Violet)
		body = append(body, page.Fill(entry, status, "· "))
	}
	body = append(body, "", page.Section("familiar homes"), "")
	for _, familiar := range familiars {
		entry := theme.Paint(familiar.Label, Amber)
		home := theme.Paint("skills "+shortPath(familiar.Home, c.Paths.Home)+"  ·  hooks "+shortPath(familiar.HookHome, c.Paths.Home), Violet)
		body = append(body, page.Fill(entry, home, "· "))
		kindWord := "resource"
		if familiar.HookBound == 0 {
			kindWord = "skill"
		} else if familiar.SkillBound == 0 {
			kindWord = "hook"
		}
		status := fmt.Sprintf("%d of %d bound %s%s installed", len(familiar.Skills), familiar.Bound, kindWord, plural(familiar.Bound))
		if familiar.Blocked > 0 {
			status += fmt.Sprintf("  ·  %d blocked", familiar.Blocked)
		}
		body = append(body, "    "+theme.Paint(status, Dim))
		for _, skill := range familiar.Skills {
			link := shortPath(skill.Link, c.Paths.Home) + " -> " + shortPath(skill.Target, c.Paths.Home)
			body = append(body, "    "+theme.Paint(resourceDisplayName(Skill{Kind: skill.Kind, Name: skill.Name}), Green))
			for _, line := range wrapWords(link, max(1, page.Inner-8)) {
				body = append(body, "        "+theme.Paint(line, Dim))
			}
		}
	}
	fmt.Fprintln(c.Out)
	writeLines(c.Out, page.Bind(body, "index", "repositories and familiar homes", fmt.Sprintf("%d repositories  ·  %d familiars", len(repositories), len(familiars))))
	fmt.Fprintln(c.Out)
}

type indexedFamiliar struct {
	Name       string
	Label      string
	Home       string
	HookHome   string
	Bound      int
	SkillBound int
	HookBound  int
	Blocked    int
	Skills     []indexedFamiliarSkill
}

type indexedFamiliarSkill struct {
	Name   string
	Kind   ResourceKind
	Link   string
	Target string
}

func indexFamiliars(paths Paths, repositories []BoundRepository) []indexedFamiliar {
	familiars := availableFamiliars(paths)
	indexed := make([]indexedFamiliar, 0, len(familiars))
	for _, familiar := range familiars {
		entry := indexedFamiliar{Name: familiar.Name, Label: familiar.Label, Home: familiar.Home, HookHome: filepath.Join(filepath.Dir(familiar.Home), "hooks")}
		for _, repository := range repositories {
			for _, resource := range repositoryResources(repository) {
				rel, kind := resource.path, resource.kind
				entry.Bound++
				if kind == HookKind {
					entry.HookBound++
				} else {
					entry.SkillBound++
				}
				target := filepath.Join(repository.Path, rel)
				home := entry.Home
				if kind == HookKind {
					home = entry.HookHome
				}
				link := filepath.Join(home, filepath.Base(rel))
				if _, err := os.Lstat(link); err != nil {
					if !os.IsNotExist(err) {
						entry.Blocked++
					}
					continue
				}
				actual, err := linkTarget(link)
				if err != nil || !samePath(actual, target) {
					entry.Blocked++
					continue
				}
				entry.Skills = append(entry.Skills, indexedFamiliarSkill{Name: filepath.Base(rel), Kind: kind, Link: link, Target: target})
			}
		}
		sort.Slice(entry.Skills, func(i, j int) bool {
			if entry.Skills[i].Name == entry.Skills[j].Name {
				return entry.Skills[i].Kind.Name() < entry.Skills[j].Kind.Name()
			}
			return entry.Skills[i].Name < entry.Skills[j].Name
		})
		indexed = append(indexed, entry)
	}
	return indexed
}

func (c *CLI) showRefresh(results []RefreshResult) {
	if c.Config.Boring {
		current, pulled, skipped, failed, repaired := 0, 0, 0, 0, 0
		for _, result := range results {
			switch result.Status {
			case CurrentBranch:
				current++
			case RefreshSkipped:
				skipped++
			case RefreshFailed:
				failed++
			case Pulled:
				pulled++
			}
			c.writeResponsive(c.Out, "", Grey, shortPath(result.Repo, c.Paths.Home)+" "+result.Message, Grey)
			for _, change := range result.Changes {
				label := change.Action
				if change.Name != "" {
					label += " " + change.Name
				}
				c.writeResponsive(c.Out, "", Grey, label+" "+change.Message, Grey)
			}
			repaired += result.repaired
		}
		c.writeResponsive(c.Out, "", Grey, fmt.Sprintf("pulled=%d current=%d skipped=%d failed=%d repaired=%d", pulled, current, skipped, failed, repaired), Grey)
		return
	}
	theme := c.theme(c.Out)
	page := NewPage(theme)
	current, pulled, skipped, failed, repaired := 0, 0, 0, 0, 0
	for _, result := range results {
		icon, color := "check", Green
		switch result.Status {
		case CurrentBranch:
			icon, color = "star", Grey
			current++
		case RefreshSkipped:
			icon, color = "warn", Amber
			skipped++
		case RefreshFailed:
			icon, color = "cross", Red
			failed++
		case Pulled:
			pulled++
		}
		c.writeResponsive(c.Out, icon, color, shortPath(result.Repo, c.Paths.Home)+" "+result.Message, color)
		for _, change := range result.Changes {
			changeColor := Cyan
			if change.Action == "renamed" || change.Action == "moved" {
				changeColor = Magenta
			}
			label := change.Action
			if change.Name != "" {
				label += " " + change.Name
			}
			c.writeResponsive(c.Out, "wrench", changeColor, label+" "+change.Message, changeColor)
		}
		repaired += result.repaired
	}
	words := []string{
		theme.Paint(fmt.Sprintf("%d pulled", pulled), Green),
		theme.Paint(fmt.Sprintf("%d current", current), Grey),
	}
	if skipped > 0 {
		words = append(words, theme.Paint(fmt.Sprintf("%d skipped", skipped), Amber))
	}
	if failed > 0 {
		words = append(words, theme.Paint(fmt.Sprintf("%d failed", failed), Red))
	}
	if repaired > 0 {
		words = append(words, theme.Paint(fmt.Sprintf("%d link%s repaired", repaired, plural(repaired)), Cyan))
	}
	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, page.Centered(clipVisible(strings.Join(words, theme.Paint("  ·  ", Dim)), page.Inner)))
}

func (c *CLI) unbind(args []string) (int, error) {
	options, args, err := parseBindingOptions(args)
	if err != nil {
		return 1, err
	}
	bound, err := BoundSkills(c.Paths)
	if err != nil {
		return 1, err
	}
	if len(bound) == 0 {
		if len(args) > 0 {
			return 1, fmt.Errorf("no bound skill called %s", args[0])
		}
		c.note("no skills are bound; no hooks are bound")
		return 0, nil
	}
	chosen, err := c.chooseSkills(args, bound, "bound", "unbind")
	if err != nil {
		return 1, err
	}
	if len(chosen) == 0 {
		c.note("nothing picked")
		return 0, nil
	}
	result := UnbindSkillsWithOptions(c.Paths, chosen, options)
	if !result.OK() {
		return c.showBindingResults("unbind", "the book lets go", []BindResult{result}, true)
	}
	results := make([]BindResult, 0, len(chosen))
	for _, skill := range chosen {
		results = append(results, BindResult{Status: Unbound, Path: c.Paths.Binding(), Target: skill.Dir, Message: "unbound"})
	}
	results[0].Changes = result.Changes
	return c.showBindingResults("unbind", "the book lets go", results, false)
}

func parseBindingOptions(args []string) (BindingOptions, []string, error) {
	options := BindingOptions{}
	names := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--replace-legacy-root" {
			options.ReplaceLegacyCatalogRoot = true
			continue
		}
		if strings.HasPrefix(arg, "--") {
			return BindingOptions{}, nil, fmt.Errorf("unknown binding option %s", arg)
		}
		names = append(names, arg)
	}
	return options, names, nil
}

func (c *CLI) showBindingResults(command, subtitle string, results []BindResult, failed bool) (int, error) {
	if c.Config.Boring {
		for _, result := range results {
			label := shortPath(result.Target, c.Paths.Home)
			if command == "unbind" && result.Target != "" {
				label = filepath.Base(result.Target)
			}
			if label == "" {
				label = shortPath(result.Path, c.Paths.Home)
			}
			c.writeResponsive(c.Out, "", Grey, strings.TrimSpace(label+" "+result.Message), Grey)
			for _, change := range result.Changes {
				c.writePathChange(c.Out, change)
			}
		}
		if failed {
			return 1, nil
		}
		return 0, nil
	}
	folio := ""
	if command == "unbind" && len(results) == 1 && results[0].Target != "" {
		folio = filepath.Base(results[0].Target)
	} else if command == "unbind" && len(results) > 1 {
		folio = fmt.Sprintf("%d skills", len(results))
	} else if len(results) == 1 {
		folio = shortPath(results[0].Target, c.Paths.Home)
		if folio == "" {
			folio = shortPath(results[0].Path, c.Paths.Home)
		}
	} else {
		folio = fmt.Sprintf("%d repositories", len(results))
	}
	if input, ok := c.In.(*os.File); ok {
		if output, ok := c.Out.(*os.File); ok {
			runCackle(input, output, func(art []string, hue Color, size viewport) []string {
				minimum := viewport{columns: 40, rows: 12}
				if size.columns < minimum.columns || size.rows < minimum.rows {
					return smallViewportFrame(size, minimum)
				}
				theme := c.theme(output)
				theme.columns = size.columns
				page := NewPage(theme)
				frameFolio := theme.Trim(folio, page.Inner)
				lines := page.Bind(c.bindingBody(page, theme, art, hue, command), command, subtitle, frameFolio)
				if len(lines) > size.rows {
					lines = page.Bind(c.bindingBody(page, theme, nil, hue, command), command, subtitle, frameFolio)
				}
				return fitViewport(lines, size)
			})
		}
	}
	theme := c.theme(c.Out)
	page := NewPage(theme)
	folio = theme.Trim(folio, page.Inner)
	body := c.bindingBody(page, theme, sealSigil, Violet, command)
	fmt.Fprintln(c.Out)
	writeLines(c.Out, page.Bind(body, command, subtitle, folio))
	fmt.Fprintln(c.Out)
	for _, result := range results {
		icon, color := "flame", Amber
		if result.Status == Already || result.Status == NotBound {
			icon, color = "star", Grey
		} else if result.Status == Unbound {
			icon, color = "trash", Amber
		} else if !result.OK() {
			icon, color = "cross", Red
		}
		label := shortPath(result.Target, c.Paths.Home)
		if command == "unbind" && result.Target != "" {
			label = filepath.Base(result.Target)
		}
		if label == "" {
			label = "skills/"
		}
		c.writeResponsive(c.Out, icon, color, label+" "+result.Message, color)
		for _, change := range result.Changes {
			c.writePathChange(c.Out, change)
		}
	}
	if failed {
		return 1, nil
	}
	return 0, nil
}

func (c *CLI) bindingBody(page Page, theme Theme, art []string, hue Color, command string) []string {
	body := drawSigil(art, page, theme, hue)
	if len(body) > 0 {
		body = append(body, "")
	}
	omens := []string{
		"the chosen resources now answer from the book",
		"their source stays in this repository",
		"bind again here to add more resources",
	}
	if command == "unbind" {
		omens = []string{
			"the book forgets the selected resources",
			"the source folders and installed links remain",
			"the other bound resources remain connected",
		}
	}
	for _, omen := range omens {
		body = append(body, page.Centered(page.Quiet(theme.Trim(omen, page.Inner))))
	}
	return body
}

func (c *CLI) warnCatalog(catalog Catalog) {
	names := make([]string, 0, len(catalog.Clashes))
	for name := range catalog.Clashes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		paths := make([]string, 0, len(catalog.Clashes[name]))
		for _, skill := range catalog.Clashes[name] {
			paths = append(paths, skill.RepoPath())
		}
		c.trouble(fmt.Sprintf("%s is used by %s. Rename one.", name, strings.Join(paths, " and ")))
	}
	c.warnTooDeep(catalog)
}

func (c *CLI) warnTooDeep(catalog Catalog) {
	for _, path := range catalog.TooDeep {
		message := path + " sits too deep. One group folder is the limit."
		c.writeResponsive(c.Err, "warn", Amber, message, Amber)
	}
}

func (c *CLI) trouble(message string) {
	c.writeResponsive(c.Err, "cross", Red, message, Red)
}

func (c *CLI) note(message string) {
	c.writeResponsive(c.Out, "star", Grey, message, Grey)
}

func (c *CLI) writePathChange(output io.Writer, change PathChange) {
	message := change.Action + " " + shortPath(change.Path, c.Paths.Home)
	if change.Target != "" {
		message += " -> " + shortPath(change.Target, c.Paths.Home)
	}
	c.writeResponsive(output, "wrench", Cyan, message, Cyan)
}

func (c *CLI) writeResponsive(output io.Writer, icon string, iconColor Color, message string, color Color) {
	theme := c.theme(output)
	prefix := theme.Tag(icon, iconColor)
	if visibleWidth(prefix) >= theme.columns {
		prefix = ""
	}
	room := max(1, theme.columns-visibleWidth(prefix))
	lines := []string{message}
	if visibleWidth(message) > room {
		lines = wrapWords(message, room)
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	indent := strings.Repeat(" ", visibleWidth(prefix))
	for index, line := range lines {
		lead := indent
		if index == 0 {
			lead = prefix
		}
		fmt.Fprintln(output, lead+theme.Paint(line, color))
	}
}

func writeLines(output io.Writer, lines []string) {
	for _, line := range lines {
		fmt.Fprintln(output, line)
	}
}

func shortPath(path, home string) string {
	if home != "" && (path == home || strings.HasPrefix(path, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func weight(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	kb := float64(bytes) / 1024
	if kb < 1024 {
		return fmt.Sprintf("%.1f KB", kb)
	}
	return fmt.Sprintf("%.1f MB", kb/1024)
}

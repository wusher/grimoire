package grimoire

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"
)

type agentFamiliar struct {
	Name  string
	Label string
	Home  string
}

type familiarDefinition struct {
	name  string
	label string
	home  func(Paths) string
}

const defaultFamiliar = "global"

var familiarMetadata = [...]familiarDefinition{
	{name: "global", label: "Global", home: func(paths Paths) string {
		if paths.Home == "" {
			return ""
		}
		return filepath.Join(paths.Home, ".agents")
	}},
	{name: "claude", label: "Claude Code", home: func(paths Paths) string { return paths.ClaudeHome }},
	{name: "opencode", label: "OpenCode", home: func(paths Paths) string { return paths.OpenCodeHome }},
	{name: "codex", label: "Codex", home: func(paths Paths) string { return paths.CodexHome }},
}

func (f familiarDefinition) skillsHome(paths Paths) string {
	return filepath.Join(f.home(paths), "skills")
}

func availableFamiliars(paths Paths) []agentFamiliar {
	familiars := make([]agentFamiliar, 0, len(familiarMetadata))
	for _, familiar := range familiarMetadata {
		familiars = append(familiars, agentFamiliar{
			Name:  familiar.name,
			Label: familiar.label,
			Home:  familiar.skillsHome(paths),
		})
	}
	return familiars
}

func normalizeFamiliar(raw string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(raw))
	for _, familiar := range familiarMetadata {
		if familiar.name == name {
			return name, nil
		}
	}
	return "", fmt.Errorf("unknown familiar %s; choose global, claude, opencode, or codex", raw)
}

func configuredFamiliar(paths Paths) (string, error) {
	body, err := os.ReadFile(paths.FamiliarFile())
	if os.IsNotExist(err) {
		return defaultFamiliar, nil
	}
	if err != nil {
		return "", fmt.Errorf("read familiar: %w", err)
	}
	var name string
	if err := json.Unmarshal(body, &name); err != nil {
		return "", fmt.Errorf("read familiar: %w", err)
	}
	return normalizeFamiliar(name)
}

func writeFamiliar(paths Paths, name string) error {
	return writeJSONFile(paths.FamiliarFile(), name)
}

func commandNeedsFamiliar(command string) bool {
	switch command {
	case "toc", "list", "ls", "cast", "banish", "volley", "hone":
		return true
	default:
		return false
	}
}

func (c *CLI) configureFamiliar(args []string) (int, error) {
	if len(args) > 1 {
		return 1, fmt.Errorf("familiar accepts one name")
	}
	if len(args) == 0 && c.Config.Boring {
		return 1, fmt.Errorf("boring mode has no picker; pass one familiar name: global, claude, opencode, or codex")
	}
	current := c.Paths.Familiar
	selected := ""
	if len(args) == 1 {
		var err error
		selected, err = normalizeFamiliar(args[0])
		if err != nil {
			return 1, err
		}
	} else if input, output, ok := terminalFiles(c.In, c.Err); ok {
		var saved bool
		var err error
		selected, saved, err = (FamiliarPicker{In: input, Out: output, Theme: c.theme(output), Home: c.Paths.Home}).Pick(availableFamiliars(c.Paths), current)
		if err != nil {
			return 1, err
		}
		if !saved {
			if c.familiarErr != nil {
				return 1, c.familiarErr
			}
			c.note("the familiar remains unchanged")
			return 0, nil
		}
	} else {
		if c.familiarErr != nil {
			return 1, c.familiarErr
		}
		if current == "" {
			return 1, noFamiliarError()
		}
		c.showFamiliar(current)
		return 0, nil
	}

	if c.familiarErr == nil && current == selected {
		c.note("already bound to the " + familiarLabel(c.Paths, selected) + " familiar")
		return 0, nil
	}
	if err := c.saveFamiliar(selected); err != nil {
		return 1, err
	}
	c.showFamiliar(selected)
	if current != "" && current != selected {
		c.note("existing links remain with " + familiarLabel(c.Paths, current))
	}
	return 0, nil
}

func (c *CLI) ensureFamiliar() (bool, error) {
	if c.Paths.Familiar != "" {
		return true, nil
	}
	if c.Config.Boring {
		return false, noFamiliarError()
	}
	input, output, ok := terminalFiles(c.In, c.Err)
	if !ok {
		return false, noFamiliarError()
	}
	selected, saved, err := (FamiliarPicker{In: input, Out: output, Theme: c.theme(output), Home: c.Paths.Home}).Pick(availableFamiliars(c.Paths), "")
	if err != nil {
		return false, err
	}
	if !saved {
		c.note("no familiar chosen")
		return false, nil
	}
	if err := c.saveFamiliar(selected); err != nil {
		return false, err
	}
	c.showFamiliar(selected)
	return true, nil
}

func (c *CLI) saveFamiliar(name string) error {
	if err := writeFamiliar(c.Paths, name); err != nil {
		return fmt.Errorf("save familiar: %w", err)
	}
	c.Paths.Familiar = name
	c.familiarErr = nil
	return nil
}

func noFamiliarError() error {
	return fmt.Errorf("no familiar chosen; run grimoire familiar global, claude, opencode, or codex")
}

func (c *CLI) showFamiliar(name string) {
	for _, familiar := range availableFamiliars(c.Paths) {
		if familiar.Name == name {
			if c.Config.Boring {
				fmt.Fprintf(c.Out, "familiar=%s\n", familiar.Name)
				fmt.Fprintf(c.Out, "skills=%s\n", shortPath(familiar.Home, c.Paths.Home))
				return
			}
			theme := c.theme(c.Out)
			fmt.Fprintln(c.Out, theme.Tag("paw", Amber)+theme.Paint("the grimoire is bound to ", Grey)+theme.Paint(familiar.Label, Violet))
			fmt.Fprintln(c.Out, theme.Tag("star", Grey)+theme.Paint(shortPath(familiar.Home, c.Paths.Home), Grey))
			return
		}
	}
}

func familiarLabel(paths Paths, name string) string {
	for _, familiar := range availableFamiliars(paths) {
		if familiar.Name == name {
			return familiar.Label
		}
	}
	return name
}

type FamiliarPicker struct {
	In    *os.File
	Out   *os.File
	Theme Theme
	Home  string
}

func (p FamiliarPicker) Pick(familiars []agentFamiliar, current string) (string, bool, error) {
	if p.In == nil || p.Out == nil || !term.IsTerminal(int(p.In.Fd())) || !term.IsTerminal(int(p.Out.Fd())) {
		return "", false, fmt.Errorf("this terminal cannot choose a familiar; pass global, claude, opencode, or codex")
	}
	if len(familiars) == 0 {
		return "", false, fmt.Errorf("no familiars are available")
	}
	old, err := term.MakeRaw(int(p.In.Fd()))
	if err != nil {
		return "", false, fmt.Errorf("open familiar picker: %w", err)
	}
	defer term.Restore(int(p.In.Fd()), old) //nolint:errcheck
	_, _ = fmt.Fprint(p.Out, "\x1b[?1049h\x1b[?25l")
	defer func() { _, _ = fmt.Fprint(p.Out, "\x1b[?25h\x1b[?1049l") }()

	cursor := 0
	for index, familiar := range familiars {
		if familiar.Name == current {
			cursor = index
			break
		}
	}
	reader := bufio.NewReader(p.In)
	drawn := false
	lastSize := viewport{}
	for {
		size := terminalViewport(p.Out)
		if !drawn || size != lastSize {
			p.draw(familiars, cursor, current, false, size)
			drawn = true
			lastSize = size
		}
		if inputPollingSupported() && !inputWaiting(p.In, 100) {
			continue
		}
		key, err := readKey(reader, p.In)
		if err != nil {
			if err == io.EOF {
				return "", false, nil
			}
			return "", false, err
		}
		switch key {
		case "enter", "space":
			p.draw(familiars, cursor, current, true, terminalViewport(p.Out))
			time.Sleep(140 * time.Millisecond)
			return familiars[cursor].Name, true, nil
		case "escape", "interrupt":
			return "", false, nil
		case "up", "left":
			cursor = (cursor - 1 + len(familiars)) % len(familiars)
		case "down", "right":
			cursor = (cursor + 1) % len(familiars)
		}
		drawn = false
	}
}

func (p FamiliarPicker) draw(familiars []agentFamiliar, cursor int, current string, flare bool, size viewport) {
	lines, _ := p.frame(familiars, cursor, current, flare, size.columns, size.rows)
	drawViewport(p.Out, lines, size)
}

func (p FamiliarPicker) frame(familiars []agentFamiliar, cursor int, current string, flare bool, columns, height int) ([]string, bool) {
	minimum := viewport{columns: 40, rows: 19}
	size := viewport{columns: columns, rows: height}
	if columns < minimum.columns || height < minimum.rows {
		return smallViewportFrame(size, minimum), false
	}
	theme := p.Theme
	theme.columns = columns
	page := newPageWidth(theme, columns)
	bodyHeight := max(10, height-9)
	fullArt := familiarArtFits(fullFamiliarCat, page.Inner, bodyHeight-10)
	art := compactFamiliarCat
	if fullArt {
		art = fullFamiliarCat
	}
	body := paintFamiliarCat(art, page, theme, flare)
	body = append(body, "", page.Section("choose your familiar"), "")
	showPaths := fullArt || bodyHeight >= 14
	for index, familiar := range familiars {
		body = append(body, familiarChoice(page, theme, familiar, index, cursor, current, flare))
		if showPaths {
			body = append(body, "          "+theme.Paint(theme.Trim(shortPath(familiar.Home, p.Home), max(1, page.Inner-10)), Dim))
		}
	}
	controls := page.Centered(page.Quiet(theme.Trim("↑ ↓ choose  ·  enter seals the pact  ·  esc leaves", page.Inner)))
	for len(body) < bodyHeight-1 {
		body = append(body, "")
	}
	if len(body) >= bodyHeight {
		body = body[:bodyHeight-1]
	}
	body = append(body, controls)
	folio := "the circle awaits"
	if current != "" {
		folio = "bound to " + familiarLabelFrom(familiars, current)
	}
	if flare {
		folio = "the pact is sealed"
	}
	return fitViewport(page.Bind(body, "familiar", "choose the voice that carries each spell", folio), size), fullArt
}

func familiarChoice(page Page, theme Theme, familiar agentFamiliar, index, cursor int, current string, flare bool) string {
	here := index == cursor
	pointer := " "
	nameColor := Grey
	if here {
		pointer = "❯"
		nameColor = Violet
	}
	if flare && here {
		nameColor = Amber
	}
	left := theme.Paint(fmt.Sprintf("%5s", roman(index+1)+"."), Amber) + " " + theme.Paint(pointer, Magenta) + " " + theme.Paint(familiar.Label, nameColor)
	status := theme.Paint("waiting", Dim)
	if familiar.Name == current {
		status = theme.Tag("candle", Amber) + theme.Paint("bound", Green)
	}
	if here && familiar.Name != current {
		status = theme.Paint("chosen", Magenta)
	}
	if flare && here {
		status = theme.Tag("paw", Amber) + theme.Paint("familiar", Amber)
	}
	return page.Fill(left, status, "· ✦ ")
}

func familiarLabelFrom(familiars []agentFamiliar, name string) string {
	for _, familiar := range familiars {
		if familiar.Name == name {
			return familiar.Label
		}
	}
	return name
}

func familiarArtFits(art []string, width, height int) bool {
	if len(art) > height {
		return false
	}
	for _, line := range art {
		if visibleWidth(line) > width {
			return false
		}
	}
	return true
}

func paintFamiliarCat(art []string, page Page, theme Theme, flare bool) []string {
	lines := make([]string, 0, len(art))
	for _, line := range art {
		painted := paintCatLine(theme, line, flare)
		lines = append(lines, page.Centered(painted))
	}
	return lines
}

func paintCatLine(theme Theme, line string, flare bool) string {
	highlights := []string{"Meow", "⠟⠇", "⣻⣇", "●"}
	var out strings.Builder
	for line != "" {
		at, token := len(line), ""
		for _, candidate := range highlights {
			if found := strings.Index(line, candidate); found >= 0 && found < at {
				at, token = found, candidate
			}
		}
		if token == "" {
			out.WriteString(theme.Paint(line, Violet))
			break
		}
		out.WriteString(theme.Paint(line[:at], Violet))
		eye := theme.Paint(token, Amber)
		if flare {
			eye = theme.Paint(eye, Bold)
		}
		out.WriteString(eye)
		line = line[at+len(token):]
	}
	return out.String()
}

var compactFamiliarCat = []string{
	`        /\_/\`,
	`   ✦   ( ●.● )   ✦`,
	`        >  ^  <      Meow`,
}

var fullFamiliarCat = []string{
	`⠀Meow⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣀⣀⣀⣀⣠⣤⣤⣤⣄⡀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣁⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣀⣤⣤⣶⣿⣿⣿⣿⣿⣿⣿⣿⣷⣿⣿⣤⣤⣄⣀⡀⠀⠀⠀⠀⠀⠀⠀⠀⣈⡉⢀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢺⠿⠿⣦⡀⠢⡀⠀⢀⣤⣾⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣷⣤⡀⠀⠀⢀⣴⣿⣿⡇⠘⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠸⡷⢀⣹⣿⠦⠈⠢⡈⢻⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⡿⠋⠀⢀⡴⠿⣯⣽⣿⠃⠂⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢳⢄⠀⣐⣒⣀⠀⠈⠢⡈⠻⢿⣿⣿⡿⠿⠛⢛⣛⣛⣛⣛⣛⡛⠿⠿⣿⣿⣿⠿⠋⠀⢀⣤⣍⣉⠉⢩⣿⠇⡄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠀⠐⠚⠛⠿⠃⠀⠀⠈⠑⠒⠾⠑⢶⡾⢿⡿⣿⣿⡿⢿⣿⣿⢓⠲⡶⠄⠒⠀⢀⠔⢉⠐⠛⠻⠿⣿⡏⠈⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠉⠹⠙⠂⠀⠀⠀⠀⠀⣀⣀⠀⢠⠁⠟⠇⣷⡀⣻⣇⣀⠂⠀⠀⠀⠚⠃⢘⣾⣭⣴⣿⣿⢀⠀⣤⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⡆⠀⠀⠀⠀⠐⠀⠀⠀⠀⠀⠀⡆⠀⠘⢿⠀⢺⢀⡆⠀⣮⠁⠀⠸⢻⠀⠠⠀⡀⠀⠀⠈⠙⠿⣿⣌⡻⢼⢠⣿⡇⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⠀⢰⣾⣷⡀⠀⠀⠀⠀⠀⢀⣶⣶⣤⣦⡌⠀⠀⠀⠀⠈⠄⠅⠘⠈⠂⠀⠀⠰⢲⢄⣁⠀⠶⢀⣀⠀⠙⡁⠨⠡⠂⣸⣿⣷⡄⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⢀⣾⣿⣿⣧⠀⠀⠀⢀⣤⣖⠨⠽⠤⠌⠁⠀⠀⠀⠀⠀⠀⠀⣄⠀⠈⠀⠀⠀⠀⣀⠁⢴⣦⣉⣏⣀⣉⠀⠀⡔⣰⣿⣿⣿⣿⠂⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⢸⣿⣿⣿⣿⠁⠀⠀⠛⠛⠙⠃⣠⣤⠀⠀⠀⠀⠀⠀⠀⠀⢤⠀⠀⠀⠀⠀⠀⠀⢨⣭⡀⢉⠻⣿⣿⣿⣆⠰⣷⣿⣿⣿⣿⣿⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⢸⣿⣿⣿⣿⠀⠀⠀⠀⠀⠀⠀⣿⣿⠀⢸⡂⠀⠀⠀⠀⠀⣀⠻⠰⡄⠀⢰⡆⡀⣸⣿⡗⠈⠐⢬⣍⣻⣿⣷⡈⣿⣿⣿⣿⣿⡆⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⢸⣿⣿⣿⣷⠀⠀⠀⠀⠀⢢⠀⠙⠷⣬⠟⣁⡀⠀⠀⠀⠀⣸⣦⡈⠣⣦⣈⠻⠤⠟⠋⠀⠈⠀⣈⣟⣉⣻⣿⣿⣿⣿⣿⣿⣿⡇⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⢸⣿⣿⣿⣿⣧⠀⠀⠀⠀⠀⠈⡳⢶⣄⠀⠀⠀⠀⠀⠠⠘⣿⣿⣇⠀⠘⣯⠧⠀⠀⠀⠀⠀⣼⡿⢹⣿⣿⣿⣿⣿⣿⣿⣿⣿⡇⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⠈⣿⣿⣿⣿⣿⣧⠀⠀⠀⠀⠀⠀⠀⠉⠀⠀⡀⠀⠀⢰⣿⣿⣿⡏⠀⢀⣿⡤⠀⣀⠀⠀⣼⠋⢉⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⠀⢸⣿⣿⣿⣿⣿⡄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠞⠛⠿⣿⣧⡀⠈⣻⡗⠛⠿⠄⠈⠀⠠⠀⣾⣿⣿⣿⣿⣿⣿⣿⣿⠟⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⢿⣿⣿⣿⣿⠇⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠉⠀⡀⠁⢀⣼⣏⣛⣡⡴⠋⠉⠙⠟⠛⠯⣭⣿⣿⣿⣿⣿⣿⡟⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠿⣿⠟⠁⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⡀⠀⠉⠶⢿⡍⣭⣍⡺⢶⣉⠩⠙⣱⣞⣒⣿⣿⣿⣿⣿⣿⣿⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠁⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠁⡤⠀⠀⠲⢂⣀⣽⣿⢦⣁⠀⣵⣖⣬⣽⡟⣿⣿⣿⣿⠟⠁⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣠⣴⣿⣿⡏⢤⡘⠿⢮⠿⢿⣿⣿⣿⣿⡿⠟⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠦⠺⠛⠛⠁⠀⠁⠀⠀⠀⠀⠀⠀⠸⣿⣿⣿⣿⠇⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
	`⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠉⠙⠛⠃⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`,
}

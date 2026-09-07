package grimoire

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

type CatalogBrowser struct {
	In       *os.File
	Out      *os.File
	Theme    Theme
	Familiar string
}

// Browse shows the book page in a searchable, full-screen view.
func (b CatalogBrowser) Browse(skills []Skill) error {
	if b.In == nil || b.Out == nil || !term.IsTerminal(int(b.In.Fd())) || !term.IsTerminal(int(b.Out.Fd())) {
		return fmt.Errorf("this terminal cannot show the table of contents")
	}
	old, err := term.MakeRaw(int(b.In.Fd()))
	if err != nil {
		return fmt.Errorf("open table of contents: %w", err)
	}
	defer term.Restore(int(b.In.Fd()), old) //nolint:errcheck
	_, _ = fmt.Fprint(b.Out, "\x1b[?1049h\x1b[?25l")
	defer func() { _, _ = fmt.Fprint(b.Out, "\x1b[?25h\x1b[?1049l") }()

	query := ""
	scroll := 0
	reader := bufio.NewReader(b.In)
	drawn := false
	lastSize := viewport{}
	maxScroll := 0
	for {
		size := terminalViewport(b.Out)
		if !drawn || size != lastSize {
			maxScroll = b.draw(skills, query, scroll, size)
			scroll = min(scroll, maxScroll)
			drawn = true
			lastSize = size
		}
		if inputPollingSupported() && !inputWaiting(b.In, 100) {
			continue
		}
		key, err := readKey(reader, b.In)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch key {
		case "enter", "escape", "interrupt":
			return nil
		case "up":
			scroll = max(0, scroll-1)
		case "down":
			scroll = min(maxScroll, scroll+1)
		case "backspace":
			if query != "" {
				_, size := utf8.DecodeLastRuneInString(query)
				query = query[:len(query)-size]
				scroll = 0
			}
		case "space":
			// Spaces do not change the fuzzy match, so ignore them.
		default:
			if utf8.RuneCountInString(key) == 1 {
				query += key
				scroll = 0
			}
		}
		drawn = false
	}
}

func (b CatalogBrowser) draw(skills []Skill, query string, scroll int, size viewport) int {
	lines, maxScroll := b.frame(skills, query, scroll, size.columns, size.rows)
	drawViewport(b.Out, lines, size)
	return maxScroll
}

func (b CatalogBrowser) frame(skills []Skill, query string, scroll, columns, height int) ([]string, int) {
	minimum := viewport{columns: 40, rows: 13}
	size := viewport{columns: columns, rows: height}
	if columns < minimum.columns || height < minimum.rows {
		return smallViewportFrame(size, minimum), max(0, scroll)
	}
	theme := b.Theme
	theme.columns = columns
	page := newPageWidth(theme, columns)
	multipleRepositories := multipleSkillRepositories(skills)
	found := filterCatalogSkills(query, skills)
	content, _ := tocSkillBody(page, theme, found, multipleRepositories)
	if len(found) == 0 {
		content = []string{
			"",
			page.Centered(theme.Paint("nothing matches this search", Grey)),
		}
	}

	bodyHeight := max(4, height-9)
	viewHeight := max(1, bodyHeight-3)
	maxScroll := max(0, len(content)-viewHeight)
	scroll = min(max(0, scroll), maxScroll)
	end := min(len(content), scroll+viewHeight)
	visible := append([]string(nil), content[scroll:end]...)
	for len(visible) < viewHeight {
		visible = append(visible, "")
	}

	matchText := fmt.Sprintf("%d of %d found", len(found), len(skills))
	prompt := theme.Tag("wand", Magenta) + theme.Paint("search", Bold)
	queryRoom := max(1, page.Inner-visibleWidth(prompt)-visibleWidth(matchText)-5)
	shownQuery := theme.Trim(query, queryRoom)
	if shownQuery == "" {
		shownQuery = theme.Paint("type to search", Dim)
	} else {
		shownQuery = theme.Paint(shownQuery, Amber)
	}
	searchLine := page.Fill("  "+prompt+"  "+shownQuery, theme.Paint(matchText, Grey), "· ")
	controls := "↑ ↓ scroll  ·  type to search  ·  enter or esc closes"
	if maxScroll > 0 {
		controls = fmt.Sprintf("↑ ↓ scroll %d/%d  ·  type to search  ·  enter or esc closes", scroll+1, maxScroll+1)
	}
	body := []string{searchLine, ""}
	body = append(body, visible...)
	body = append(body, page.Centered(page.Quiet(theme.Trim(controls, page.Inner))))
	folio := tocFolio(installedSkillCount(skills), len(skills), b.Familiar)
	return fitViewport(page.Bind(body, "grimoire", "table of contents", folio), size), maxScroll
}

func filterCatalogSkills(query string, skills []Skill) []Skill {
	if strings.TrimSpace(query) == "" {
		return append([]Skill(nil), skills...)
	}
	multipleRepositories := multipleSkillRepositories(skills)
	found := make([]Skill, 0, len(skills))
	for _, skill := range skills {
		group := skillGroupKey(skill, multipleRepositories)
		if group == "" {
			group = "loose leaves"
		}
		if _, ok := MatchScore(query, group+" "+skill.Name+" "+skill.Description); ok {
			found = append(found, skill)
		}
	}
	return found
}

func installedSkillCount(skills []Skill) int {
	installed := 0
	for _, skill := range skills {
		if skill.Installed() {
			installed++
		}
	}
	return installed
}

func terminalFiles(input io.Reader, output io.Writer) (*os.File, *os.File, bool) {
	in, inOK := input.(*os.File)
	out, outOK := output.(*os.File)
	if !inOK || !outOK {
		return nil, nil, false
	}
	return in, out, term.IsTerminal(int(in.Fd())) && term.IsTerminal(int(out.Fd()))
}

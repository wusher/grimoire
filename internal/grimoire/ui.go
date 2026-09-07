package grimoire

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

type Color string

const (
	Violet  Color = "38;5;141"
	Magenta Color = "38;5;177"
	Amber   Color = "38;5;214"
	Green   Color = "38;5;114"
	Red     Color = "38;5;203"
	Cyan    Color = "38;5;117"
	Grey    Color = "38;5;245"
	Blood   Color = "38;5;131"
	Italic  Color = "3"
	Bold    Color = "1"
	Dim     Color = "2"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

type Theme struct {
	color   bool
	icons   bool
	columns int
}

func NewTheme(output io.Writer) Theme {
	theme := Theme{columns: 80}
	file, ok := output.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return theme
	}
	if columns, _, err := term.GetSize(int(file.Fd())); err == nil && columns > 0 {
		theme.columns = columns
	}
	_, noColor := os.LookupEnv("NO_COLOR")
	theme.color = !noColor
	theme.icons = theme.color && os.Getenv("GRIMOIRE_ICONS") != "0"
	return theme
}

func (t Theme) Paint(text string, color Color) string {
	if !t.color || text == "" {
		return text
	}
	return "\x1b[" + string(color) + "m" + text + "\x1b[0m"
}

var icons = map[string]string{
	"book":   "\uf02d",
	"wand":   "\uf0d0",
	"flame":  "\uf06d",
	"star":   "\uf005",
	"check":  "\uf00c",
	"circle": "\uf10c",
	"skull":  "\U000f068c",
	"candle": "\U000f05e2",
	"warn":   "\uf071",
	"wrench": "\uf0ad",
	"trash":  "\uf014",
	"plus":   "\uf067",
	"cross":  "\uf00d",
	"paw":    "\uf1b0",
}

func (t Theme) Tag(name string, color Color) string {
	if !t.icons {
		return ""
	}
	return t.Paint(icons[name], color) + " "
}

func (t Theme) Trim(text string, room int) string {
	plain := ansiPattern.ReplaceAllString(text, "")
	if utf8.RuneCountInString(plain) <= room {
		return text
	}
	if room <= 0 {
		return ""
	}
	// Trim is used on unpainted prose and paths. Keeping that invariant makes
	// truncation predictable and prevents cutting an ANSI escape sequence.
	runes := []rune(plain)
	if room <= 3 {
		return string(runes[:min(room, len(runes))])
	}
	return string(runes[:room-3]) + "..."
}

func visibleWidth(text string) int {
	return utf8.RuneCountInString(ansiPattern.ReplaceAllString(text, ""))
}

func clipVisible(text string, room int) string {
	if room <= 0 {
		return ""
	}
	if visibleWidth(text) <= room {
		return text
	}
	var out strings.Builder
	visible := 0
	painted := false
	for text != "" && visible < room {
		if text[0] == '\x1b' {
			match := ansiPattern.FindStringIndex(text)
			if match != nil && match[0] == 0 {
				out.WriteString(text[:match[1]])
				text = text[match[1]:]
				painted = true
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(text)
		out.WriteString(text[:size])
		text = text[size:]
		visible++
	}
	if painted {
		out.WriteString("\x1b[0m")
	}
	return out.String()
}

type viewport struct {
	columns int
	rows    int
}

func terminalViewport(output *os.File) viewport {
	columns, rows, err := term.GetSize(int(output.Fd()))
	if err != nil {
		return viewport{columns: 80, rows: 24}
	}
	if columns <= 0 {
		columns = 80
	}
	if rows <= 0 {
		rows = 24
	}
	return viewport{columns: columns, rows: rows}
}

func smallViewportFrame(size viewport, minimum viewport) []string {
	if size.columns <= 0 || size.rows <= 0 {
		return nil
	}
	lines := make([]string, size.rows)
	message := fmt.Sprintf("resize terminal to at least %d x %d", minimum.columns, minimum.rows)
	message = clipVisible(message, size.columns)
	row := max(0, (size.rows-1)/2)
	lines[row] = strings.Repeat(" ", max(0, (size.columns-visibleWidth(message))/2)) + message
	return lines
}

func fitViewport(lines []string, size viewport) []string {
	if size.columns <= 0 || size.rows <= 0 {
		return nil
	}
	fitted := make([]string, size.rows)
	for index := 0; index < min(len(lines), size.rows); index++ {
		fitted[index] = clipVisible(lines[index], size.columns)
	}
	return fitted
}

func drawViewport(output *os.File, lines []string, size viewport) {
	lines = fitViewport(lines, size)
	_, _ = io.WriteString(output, "\x1b[2J\x1b[H"+strings.Join(lines, "\r\n"))
}

type Page struct {
	theme Theme
	Width int
	Inner int
}

func NewPage(theme Theme) Page {
	width := min(theme.columns, 78)
	return newPage(theme, width)
}

func newPageWidth(theme Theme, width int) Page {
	return newPage(theme, width)
}

func newPage(theme Theme, width int) Page {
	width = max(width, 7)
	return Page{theme: theme, Width: width, Inner: width - 6}
}

func (p Page) Bind(body []string, title, subtitle, folio string) []string {
	lines := []string{p.border("╓", "┐", "❦")}
	lines = append(lines, p.Blank(), p.Line(p.Centered(p.theme.Paint(spaced(title), Bold))))
	if subtitle != "" {
		lines = append(lines, p.Line(p.Centered(p.Quiet("✦ "+subtitle+" ✦"))))
	}
	lines = append(lines, p.Blank(), p.Line(p.divider()), p.Blank())
	for _, line := range body {
		lines = append(lines, p.Line(line))
	}
	lines = append(lines, p.Blank(), p.border("╙", "┘", folio))
	return lines
}

func (p Page) Line(text string) string {
	text = clipVisible(text, p.Inner)
	padding := max(0, p.Inner-visibleWidth(text))
	return p.theme.Paint("║", Grey) + "  " + text + strings.Repeat(" ", padding) + "  " + p.theme.Paint("│", Grey)
}

func (p Page) Blank() string { return p.Line("") }

func (p Page) Centered(text string) string {
	return strings.Repeat(" ", max(0, (p.Inner-visibleWidth(text))/2)) + text
}

func (p Page) Quiet(text string) string {
	return p.theme.Paint(p.theme.Paint(text, Italic), Dim)
}

func (p Page) Section(name string) string {
	label := p.theme.Paint(spaced(name), Cyan)
	tail := max(0, p.Inner-visibleWidth(label)-3)
	return " " + label + " " + p.theme.Paint(strings.Repeat("─", tail), Dim)
}

func (p Page) Chapter(number int, name, note string) []string {
	left := p.theme.Paint(fmt.Sprintf("%5s", roman(number)+"."), Amber) + " " +
		p.theme.Paint("❦", Magenta) + " " + p.theme.Paint(p.theme.Paint(name, Cyan), Bold)
	if note != "" {
		left = p.Fill(left, p.theme.Paint(note, Dim), " ")
	}
	return []string{left, "        " + p.theme.Paint(strings.Repeat("─", max(0, p.Inner-9)), Dim)}
}

func (p Page) Entry(name, mark string, installed bool) string {
	color := Red
	if installed {
		color = Violet
	}
	return p.Fill("        "+p.theme.Paint(name, color), mark, "· ✦  ")
}

func (p Page) Blurb(text string) []string {
	const indent = 10
	room := max(1, p.Inner-indent)
	wrapped := wrapWords(text, room)
	if p.theme.color && len(wrapped) > 2 {
		wrapped = wrapped[:2]
		wrapped[1] = p.theme.Trim(wrapped[1]+" ...", room)
	}
	lines := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		lines = append(lines, strings.Repeat(" ", indent)+p.Quiet(line))
	}
	return lines
}

func (p Page) Fill(left, right, leader string) string {
	gap := p.Inner - visibleWidth(left) - visibleWidth(right) - 2
	if gap <= 0 {
		right = clipVisible(right, p.Inner)
		left = clipVisible(left, max(0, p.Inner-visibleWidth(right)-1))
		if left == "" {
			return right
		}
		return left + " " + right
	}
	if strings.TrimSpace(leader) == "" {
		return left + strings.Repeat(" ", gap+2) + right
	}
	step := visibleWidth(leader)
	count := max(0, (gap-1)/step)
	run := strings.Repeat(" ", max(0, gap-count*step-1)) + strings.Repeat(leader, count) + "·"
	return left + " " + p.theme.Paint(run, Dim) + " " + right
}

func (p Page) border(left, right, mark string) string {
	inside := p.Width - 2
	middle := ""
	if mark != "" {
		mark = p.theme.Trim(mark, max(0, inside-2))
		color := Magenta
		if left == "╙" {
			color = Grey
		}
		middle = " " + p.theme.Paint(mark, color) + " "
	}
	markWidth := visibleWidth(middle)
	first := (inside - markWidth) / 2
	last := inside - markWidth - first
	return p.theme.Paint(left, Grey) + p.theme.Paint(strings.Repeat("─", first), Grey) + middle +
		p.theme.Paint(strings.Repeat("─", last), Grey) + p.theme.Paint(right, Grey)
}

func (p Page) divider() string {
	arm := strings.Repeat("─", max(p.Inner/3, 4))
	return p.Centered(p.theme.Paint(arm+" ✦ "+arm, Dim))
}

func spaced(text string) string {
	return strings.Join(strings.Split(strings.ToUpper(text), ""), " ")
}

func wrapWords(text string, room int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	lines := []string{}
	for _, word := range words {
		if len(lines) == 0 || utf8.RuneCountInString(lines[len(lines)-1])+1+utf8.RuneCountInString(word) > room {
			lines = append(lines, word)
		} else {
			lines[len(lines)-1] += " " + word
		}
	}
	return lines
}

func drawSigil(art []string, page Page, theme Theme, color Color) []string {
	width := 0
	for _, line := range art {
		width = max(width, visibleWidth(line))
	}
	if len(art) == 0 || width > page.Inner {
		return nil
	}
	pad := strings.Repeat(" ", max(0, (page.Inner-width)/2))
	lines := make([]string, len(art))
	for index, line := range art {
		lines[index] = pad + theme.Paint(line, color)
	}
	return lines
}

var tomeSigil = []string{
	"⠀⠀⠀⠀⣀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
	"⠀⣠⡾⠛⠛⠛⠛⠛⠛⠛⠛⠛⠻⠿⠿⠷⠶⠶⠶⠶⣶⡀⠀⠀⠀⠀⠀⠀",
	"⢰⡿⠀⠀⠸⣷⠀⠀⠀⡀⠀⠀⠀⠀⠀⠀⠀⠀⢀⡀⠸⣧⠀⠀⠀⠀⠀⠀",
	"⠸⣧⠀⠀⠀⢻⡆⠀⠀⢻⣦⡤⠀⠀⠀⠀⠀⣀⣾⡇⠀⢹⣇⠀⠀⠀⠀⠀",
	"⠀⢿⡄⠀⠀⠈⣿⡀⠀⠀⠙⠿⣶⡤⠀⠀⢠⣾⠟⠁⠀⠀⢻⡆⠀⠀⠀⠀",
	"⠀⠸⡇⠀⠀⠀⢹⣇⠀⠀⡀⠀⠀⠀⠀⠀⠀⠁⢀⠀⠀⡄⠈⢿⡄⠀⠀⠀",
	"⠀⠀⢿⠀⠀⠀⠀⢿⡄⠀⢹⣷⣄⡀⣶⣦⡀⢀⣿⣆⣾⡇⠀⠘⣷⠀⠀⠀",
	"⠀⠀⠘⡇⠀⠀⠀⠸⣷⠀⠀⢻⡿⣿⣿⣿⣿⣿⣿⣿⣿⡇⠀⠀⠸⣆⠀⠀",
	"⠀⠀⠀⣿⡀⠀⠀⠀⢻⡆⠀⠀⠀⠈⢻⣿⠃⠙⢿⡏⠈⠇⠀⠀⠀⢹⡄⠀",
	"⠀⠀⠀⢸⣇⠀⠀⠀⠈⠉⠀⠀⠀⠀⠀⠁⠀⠀⠈⠀⠀⠀⠀⠀⠀⠈⣿⡀",
	"⠀⠀⠀⠀⣿⣀⣴⠾⠛⠛⠛⠛⠛⠛⠛⠛⠛⠛⠛⠿⠿⠿⠷⠶⠶⠶⠾⠷",
	"⠀⠀⠀⠀⢸⡿⠁⣴⣾⣿⣿⣿⣿⣿⣷⣶⣶⣶⣶⣶⣶⣶⣶⣶⣶⠶⠂⠀",
	"⠀⠀⠀⠀⠀⠁⢸⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣧⠀⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠈⠉⠉⠉⠉⠉⠉⠉⠙⠛⠛⠛⠛⠛⠛⠛⠛⠛⠛⠀⠀⠀",
}

var wandSigil = []string{
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢠⡄⠀⠀⠀⣄⣼⣶⠖⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠙⠋⣀⠴⠂⠀⠋⠻⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣀⠀",
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣦⡄⠀⣠⠖⠉⠀⠀⠀⠀⠀⠀⠀⠀⠀⠸⣇⣀⣀⣀⠀⠀⠻⠇",
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠻⠣⠀⠙⢤⣀⡀⢀⣀⣤⠴⠶⠖⠛⠛⠋⠉⠉⠉⣉⣽⠷⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠐⠶⢄⣴⡟⠋⠉⢛⠛⠒⠒⠒⠒⠚⠛⢋⠉⢉⣸⣦⠄⠀",
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠳⠦⣤⣀⣸⣥⣤⣶⣖⣒⣋⣽⠿⠀⠀⠛⠉⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠰⢏⣀⣠⢼⠇⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣀⣤⡶⠛⠉⠉⠉⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣠⡶⠟⠉⠁⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
	"⠀⠀⠀⡠⠒⠒⠂⠤⢤⡀⠀⠀⠀⢀⣠⡴⠞⠋⠁⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
	"⠀⣠⠎⠀⠘⣄⣓⠶⡢⢤⣭⣷⠾⠋⠁⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
	"⠈⠀⠀⠀⠀⠈⣁⣀⣀⣹⠉⠁⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
	"⠀⠀⣀⠤⠖⠻⠿⠋⠀⠉⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
}

var bladeSigil = []string{
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣀⠄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣠⣾⡏⠀⠀⠀⠀⢀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠀⠀⣶⡄⠀⢰⣿⣿⣧⠀⠀⠀⠀⣼⣧⠀⠀⢸⡄⠀⠀⠀⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠀⠀⢸⣿⡄⠈⢿⣿⣿⣿⣶⣶⣿⣿⣿⠀⠀⢸⣿⠀⠀⠀⠀⠀",
	"⠀⠀⢀⠀⠀⣠⠀⠀⣸⣿⡇⠀⠀⢻⣿⣿⣿⣿⣿⣿⠏⠀⠀⣼⣿⡇⠀⣀⠀⠀",
	"⠀⢀⣿⠀⢰⣿⣄⣰⣿⣿⣇⠀⠀⠀⣿⣿⣿⣿⣿⣯⣀⣀⣴⣿⣿⡇⢀⣿⡆⠀",
	"⠀⣸⣿⡀⠸⣿⣿⣿⣿⠿⣿⢿⣿⣿⣿⣿⣿⣿⣿⣿⢿⣿⠿⣿⣿⣇⣼⣿⣧⠀",
	"⠀⣿⣿⣷⣄⢿⣿⣿⣿⠀⣿⠀⠉⠻⢿⣿⣿⠿⠛⠁⢸⣿⠀⣿⣿⣿⣿⣿⣿⠀",
	"⠀⢸⣿⣿⣿⣿⣿⣿⣿⠀⣿⠀⠀⠀⠀⣿⡇⠀⠀⠀⢸⣿⠀⣿⣿⣿⣿⣿⡟⠀",
	"⠀⠈⢿⣿⣿⣿⣿⣿⣿⠀⣿⠀⠀⠀⠀⣿⡇⠀⠀⠀⢸⣿⠀⣿⣿⣿⣿⣿⠇⠀",
	"⠀⠀⠀⠻⣿⣿⣿⣿⣿⠀⣿⠀⠀⠀⠀⣿⡇⠀⠀⠀⢸⣿⠀⣿⣿⣿⣿⠏⠀⠀",
	"⠀⠀⠀⠀⠈⠻⣿⣿⣿⠀⠿⣦⣄⠀⠀⣿⡇⠀⢀⣤⣾⠟⠀⣿⣿⡿⠃⠀⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠈⠻⢿⣷⣦⣄⠙⠻⢶⣿⣧⠾⠛⢉⣠⣴⣾⠿⠋⠀⠀⠀⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠉⠛⠿⣿⣷⣦⣄⣠⣴⣾⡿⠟⠋⠁⠀⠀⠀⠀⠀⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠉⠙⠛⠛⠉⠁⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
}

var sealSigil = []string{
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢸⡀⠀⠀⠀⠀",
	"⠀⠀⢸⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢸⡇⠀⠀⠀⠀",
	"⠀⠀⢸⣄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣸⣧⠀⠀⠀⠀",
	"⠀⠀⠘⣿⣦⡀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣠⣴⣾⣿⣿⠀⠀⠀⠀",
	"⠀⠀⠀⢻⣿⣿⣶⣤⣀⡀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣀⡀⣴⣿⣿⣿⣿⡿⠁⠀⠀⢰⠀",
	"⠀⠀⠀⠀⠻⣿⣿⣿⣿⣿⣦⣤⣀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣠⣾⣿⣷⣿⣿⣿⣿⣿⠁⠀⠀⠀⢨⣇",
	"⣰⠀⠀⠀⠀⠉⢿⣿⣿⣿⣿⣾⣿⣿⣷⣦⣄⡀⠀⠀⠀⠀⠀⠀⠀⢀⣴⣾⣿⣿⣿⣿⣿⣿⣿⣿⣇⠀⠀⠀⢀⣼⣿",
	"⢿⡄⠀⠀⠀⠀⠋⠉⠉⠙⠛⠿⠟⠻⠿⠿⠿⠿⠷⣤⣀⠀⠀⢀⠴⠟⠛⠛⠛⠋⠉⠁⠉⠁⠀⠀⠀⠀⠀⠀⣼⣿⣿",
	"⠘⣿⣶⣄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⡀⢰⣄⣿⣿⣿⡇",
	"⠀⠹⣿⣿⣧⣀⣀⢀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢰⣶⣿⣿⣸⣿⣿⣿⡷⠀",
	"⠀⠀⠉⣿⣿⣿⣿⢸⣿⣦⣦⣄⡀⠀⢠⣶⢰⣦⣶⣰⣆⣶⣰⣦⣄⢰⣴⡄⣴⠀⠀⣦⣷⣿⣿⣿⣿⣯⣿⣿⣿⠁⠀",
	"⠀⠀⠀⠸⢿⣿⣿⣿⣿⣿⣿⣿⣷⠀⣼⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣾⣿⣿⣿⡇⢸⣿⣿⣿⣿⣿⣿⣿⣿⡟⠁⠀⠀",
	"⠀⠀⠀⠀⠀⠋⣿⣿⣿⣿⣿⣿⣿⡇⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⢸⣿⣿⣿⣿⣿⡿⣿⠿⠀⠀⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠉⢿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣸⣿⣿⣿⣿⡿⠇⠇⠀⠀⠀⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠀⠈⢿⢹⠘⣿⣿⣿⣿⣿⣿⣿⡟⣿⣿⣿⣿⣿⣿⡟⢹⣿⣿⣿⣿⣿⣿⠹⠛⠁⠀⠀⠀⠀⠀⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠀⠀⠈⠟⠏⢿⢿⢿⢧⠘⣿⢿⣿⡟⠿⡇⠸⣿⠿⢻⡟⠛⠈⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠀⠀⠘⠀⠀⠈⠈⠁⠀⠃⠀⠀⠀⠀⠃⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀",
}

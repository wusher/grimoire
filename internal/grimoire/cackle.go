package grimoire

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	cackleFrame = 70 * time.Millisecond
	cackleRate  = 1.1
	cackleMouth = 12
	cackleBlank = '⠀'
)

// runCackle animates the bind page until a key is pressed. Like the Ruby TUI,
// it is entirely skipped when either side is not a terminal.
func runCackle(input, output *os.File, page func([]string, Color, viewport) []string) bool {
	if input == nil || output == nil || !term.IsTerminal(int(input.Fd())) || !term.IsTerminal(int(output.Fd())) {
		return false
	}
	if !inputPollingSupported() {
		return false
	}
	old, err := term.MakeRaw(int(input.Fd()))
	if err != nil {
		return false
	}
	defer func() { _ = term.Restore(int(input.Fd()), old) }()
	defer func() {
		_, _ = fmt.Fprint(output, "\x1b[2J\x1b[H\x1b[?25h")
	}()
	_, _ = fmt.Fprint(output, "\x1b[?25l")

	reader := bufio.NewReader(input)
	started := time.Now()
	for {
		if reader.Buffered() > 0 || inputWaiting(input, 0) {
			_, _ = readKey(reader, input)
			return true
		}
		size := terminalViewport(output)
		age := time.Since(started).Seconds()
		beat := math.Sin(age * cackleRate * 2 * math.Pi)
		hue := Violet
		if math.Sin(age*0.37*2*math.Pi+0.6) > 0.3 {
			hue = Magenta
		}
		art := cackleArt(sealSigil, beat, age)
		drawAnimationFrame(output, page(art, hue, size), size)
		time.Sleep(cackleFrame)
	}
}

func cackleArt(source []string, beat, age float64) []string {
	rows := append([]string(nil), source...)
	if beat > 0.75 && len(rows) > cackleMouth {
		rows = append(rows[:cackleMouth], append([]string{rows[cackleMouth]}, rows[cackleMouth:]...)...)
		rows = rows[1:]
	}
	shift := int(math.Round(beat * 1.4))
	shift = max(-1, min(1, shift))
	for index, row := range rows {
		rows[index] = shiftRunes(row, shift)
	}
	return cacklePuffs(rows, age)
}

func shiftRunes(text string, by int) string {
	if by == 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return text
	}
	if by > 0 {
		return string(append([]rune{cackleBlank}, runes[:len(runes)-1]...))
	}
	return string(append(runes[1:], cackleBlank))
}

func cacklePuffs(rows []string, age float64) []string {
	puffs := []string{"ha", "HA", "ha!", "HA!"}
	count := int(age / 1.5)
	for index := max(0, count-3); index <= count; index++ {
		part := (age - float64(index)*1.5) / 2.4
		if part < 0 || part > 1 {
			continue
		}
		row := 8 - int(math.Round(part*7))
		middle := utf8.RuneCountInString(sealSigil[0]) / 2
		column := middle + 4
		if index%2 == 0 {
			column = middle - 8
		}
		stampRunes(rows, puffs[index%len(puffs)], row, column)
	}
	return rows
}

func stampRunes(rows []string, text string, row, column int) {
	if row < 0 || row >= len(rows) {
		return
	}
	line := []rune(rows[row])
	for offset, mark := range []rune(text) {
		at := column + offset
		if at >= 0 && at < len(line) {
			line[at] = mark
		}
	}
	rows[row] = string(line)
}

func animationFrame(body []string, size viewport) []string {
	widest := 0
	for _, line := range body {
		widest = max(widest, visibleWidth(line))
	}
	left := max(0, (size.columns-widest)/2)
	top := max(0, (size.rows-len(body))/2)
	lines := make([]string, size.rows)
	for row := range size.rows {
		at := row - top
		if at >= 0 && at < len(body) {
			lines[row] = strings.Repeat(" ", left) + body[at]
		}
	}
	return fitViewport(lines, size)
}

func drawAnimationFrame(output *os.File, body []string, size viewport) {
	var frame strings.Builder
	for row, line := range animationFrame(body, size) {
		fmt.Fprintf(&frame, "\x1b[%d;1H%s\x1b[K", row+1, line)
	}
	frame.WriteString("\x1b[J")
	_, _ = io.WriteString(output, frame.String())
}

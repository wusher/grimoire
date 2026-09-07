package grimoire

import (
	"bufio"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

const (
	fireworkSpan  = 5 * time.Second
	fireworkFrame = 50 * time.Millisecond
	fireworkRise  = 0.5
	fireworkBloom = 0.9
)

type fireworkCell struct {
	mark  string
	color Color
}

type fireworkShell struct {
	born    float64
	lane    int
	burstAt int
	reach   float64
	mark    string
	colors  []Color
	label   string
}

var fireworkColors = [][]Color{
	{Violet, Magenta, Violet, Grey},
	{Green, Cyan, Green, Grey},
	{Magenta, Violet, Violet, Grey},
	{Cyan, Green, Green, Grey},
	{Amber, Violet, Magenta, Grey},
}

var bannerFont = map[rune][]string{
	'G': {" ███ ", "█    ", "█  ██", "█   █", " ███ "},
	'R': {"████ ", "█   █", "████ ", "█  █ ", "█   █"},
	'I': {"█████", "  █  ", "  █  ", "  █  ", "█████"},
	'M': {"█   █", "██ ██", "█ █ █", "█   █", "█   █"},
	'O': {" ███ ", "█   █", "█   █", "█   █", " ███ "},
	'E': {"█████", "█    ", "████ ", "█    ", "█████"},
	'V': {"█   █", "█   █", "█   █", " █ █ ", "  █  "},
	'L': {"█    ", "█    ", "█    ", "█    ", "█████"},
	'Y': {"█   █", " █ █ ", "  █  ", "  █  ", "  █  "},
}

func runFireworks(input, output *os.File, theme Theme, names []string) {
	if input == nil || output == nil || !term.IsTerminal(int(input.Fd())) || !term.IsTerminal(int(output.Fd())) {
		return
	}
	old, err := term.MakeRaw(int(input.Fd()))
	if err != nil {
		return
	}
	defer func() { _ = term.Restore(int(input.Fd()), old) }()
	defer func() { _, _ = fmt.Fprint(output, "\x1b[2J\x1b[H\x1b[?25h") }()
	_, _ = fmt.Fprint(output, "\x1b[?25l")

	random := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec
	reader := bufio.NewReader(input)
	started := time.Now()
	var shells []fireworkShell
	for time.Since(started) < fireworkSpan {
		if reader.Buffered() > 0 || inputWaiting(input, 0) {
			_, _ = readKey(reader, input)
			break
		}
		size := terminalViewport(output)
		age := time.Since(started).Seconds()
		wanted := 0
		if age <= fireworkSpan.Seconds()-fireworkRise {
			wanted = 2 + int((age/fireworkFrame.Seconds())/2)
		}
		for len(shells) < wanted {
			shells = append(shells, newShell(random, age, size.columns, size.rows, names))
		}
		frame := make([][]fireworkCell, size.rows)
		for row := range frame {
			frame[row] = make([]fireworkCell, size.columns)
		}
		for _, shell := range shells {
			placeShell(frame, shell, age-shell.born)
		}
		stampBanner(frame, age)
		drawFireworkFrame(output, frame, theme)
		time.Sleep(fireworkFrame)
	}
}

func newShell(random *rand.Rand, age float64, columns, rows int, names []string) fireworkShell {
	first := max(int(math.Round(float64(columns)*0.45)), 4)
	last := max(columns-1, first+1)
	label := ""
	if len(names) > 0 {
		label = names[random.Intn(len(names))]
	}
	marks := []string{"✦", "✧", "*", "+", "·", "."}
	reachLimit := max(float64(rows)/4, 3)
	return fireworkShell{
		born: age, lane: 2 + random.Intn(max(1, max(3, rows-6)-2)),
		burstAt: first + random.Intn(last-first), reach: 2 + random.Float64()*(reachLimit-2),
		mark: marks[random.Intn(len(marks))], colors: fireworkColors[random.Intn(len(fireworkColors))], label: label,
	}
}

func placeShell(grid [][]fireworkCell, shell fireworkShell, age float64) {
	if age < 0 {
		return
	}
	if age < fireworkRise {
		part := age / fireworkRise
		column := int(math.Round(float64(shell.burstAt) * part))
		trails := []string{"·", "-", "="}
		mark := trails[min(len(trails)-1, int(part*float64(len(trails))))]
		setFireworkCell(grid, column, shell.lane, mark, shell.colors[0])
		start := column - len([]rune(shell.label)) - 1
		for offset, labelMark := range []rune(shell.label) {
			if labelMark != ' ' {
				setFireworkCell(grid, start+offset, shell.lane, string(labelMark), shell.colors[0])
			}
		}
		return
	}
	part := (age - fireworkRise) / fireworkBloom
	if part > 1 {
		return
	}
	radius := shell.reach * part
	colorAt := min(len(shell.colors)-1, int(math.Pow(part, 2.2)*float64(len(shell.colors))))
	mark := shell.mark
	if part > 0.75 {
		mark = "."
	}
	stampRing(grid, shell, radius, mark, shell.colors[colorAt])
	if radius > 2 {
		stampRing(grid, shell, radius*0.6, ".", shell.colors[colorAt])
	}
}

func stampRing(grid [][]fireworkCell, shell fireworkShell, radius float64, mark string, color Color) {
	arms := max(int(math.Round(radius*5)), 10)
	for arm := range arms {
		angle := math.Pi * 2 / float64(arms) * float64(arm)
		column := shell.burstAt + int(math.Round(math.Cos(angle)*radius*2))
		row := shell.lane + int(math.Round(math.Sin(angle)*radius))
		setFireworkCell(grid, column, row, mark, color)
	}
}

func setFireworkCell(grid [][]fireworkCell, column, row int, mark string, color Color) {
	if row >= 0 && row < len(grid) && column >= 0 && column < len(grid[row]) {
		grid[row][column] = fireworkCell{mark: mark, color: color}
	}
}

func stampBanner(grid [][]fireworkCell, age float64) {
	if len(grid) < 9 || len(grid[0]) < bannerMeasure(len("GRIMOIRE"))+2 {
		return
	}
	text := "GRIMOIRE"
	if bannerMeasure(len("GRIMOIRE VOLLEY"))+2 <= len(grid[0]) {
		text = "GRIMOIRE VOLLEY"
	}
	width := bannerMeasure(len(text))
	left := max(0, (len(grid[0])-width)/2)
	top := len(grid) - 5
	shine := left - 9 + int(math.Mod(age*70, float64(width+18)))
	for row := top; row < len(grid); row++ {
		for column := max(0, left-1); column < min(len(grid[row]), left+width+1); column++ {
			grid[row][column] = fireworkCell{}
		}
	}
	for index, letter := range text {
		if letter == ' ' {
			continue
		}
		color := Violet
		if index >= len("GRIMOIRE ") {
			color = Green
		}
		art := bannerFont[letter]
		column := left + index*6
		for row, line := range art {
			for cell, mark := range []rune(line) {
				if mark == ' ' {
					continue
				}
				lit := color
				if column+cell >= shine-4 && column+cell <= shine+12 {
					if color == Violet {
						lit = Magenta
					} else {
						lit = Cyan
					}
				}
				setFireworkCell(grid, column+cell, top+row, string(mark), lit)
			}
		}
	}
}

func bannerMeasure(count int) int { return count*6 - 1 }

func drawFireworkFrame(output *os.File, grid [][]fireworkCell, theme Theme) {
	var frame strings.Builder
	for row, cells := range grid {
		var line strings.Builder
		for _, cell := range cells {
			if cell.mark == "" {
				line.WriteByte(' ')
			} else {
				line.WriteString(theme.Paint(cell.mark, cell.color))
			}
		}
		fmt.Fprintf(&frame, "\x1b[%d;1H%s\x1b[K", row+1, line.String())
	}
	frame.WriteString("\x1b[J")
	_, _ = output.WriteString(frame.String())
}

package grimoire

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"golang.org/x/term"
)

type Picker struct {
	In             *os.File
	Out            *os.File
	Prompt         string
	Theme          Theme
	SelectAllLabel string
}

// Pick provides the same interaction as the Ruby tree picker: type to filter,
// arrows move, groups open and close, space marks, enter confirms, and escape
// quits.
func (p Picker) Pick(skills []Skill) ([]Skill, error) {
	if p.In == nil || p.Out == nil || !term.IsTerminal(int(p.In.Fd())) || !term.IsTerminal(int(p.Out.Fd())) {
		return nil, fmt.Errorf("this terminal cannot show a picker. Pass a name instead")
	}
	old, err := term.MakeRaw(int(p.In.Fd()))
	if err != nil {
		return nil, fmt.Errorf("open picker: %w", err)
	}
	defer term.Restore(int(p.In.Fd()), old) //nolint:errcheck
	defer func() { _, _ = fmt.Fprint(p.Out, "\x1b[2J\x1b[H") }()

	query := ""
	cursor := 0
	marked := map[string]Skill{}
	tree := NewSkillTree(skills)
	open := map[string]bool{}
	for _, group := range tree.GroupNames() {
		open[group] = true
	}
	reader := bufio.NewReader(p.In)
	drawn := false
	lastSize := viewport{}
	for {
		visible := p.rows(tree, skills, query, open)
		if cursor >= len(visible) {
			cursor = max(0, len(visible)-1)
		}
		size := terminalViewport(p.Out)
		if !drawn || size != lastSize {
			p.draw(visible, query, cursor, marked, size)
			drawn = true
			lastSize = size
		}
		if inputPollingSupported() && !inputWaiting(p.In, 100) {
			continue
		}
		key, err := readKey(reader, p.In)
		if err != nil {
			if err == io.EOF {
				return nil, nil
			}
			return nil, err
		}
		switch key {
		case "enter":
			if len(marked) > 0 {
				chosen := make([]Skill, 0, len(marked))
				for _, skill := range skills {
					if _, ok := marked[skill.Dir]; ok {
						chosen = append(chosen, skill)
					}
				}
				return chosen, nil
			}
			if len(visible) == 0 {
				return nil, nil
			}
			return append([]Skill(nil), visible[cursor].Skills...), nil
		case "escape", "interrupt":
			return nil, nil
		case "up":
			if len(visible) > 0 {
				cursor = (cursor - 1 + len(visible)) % len(visible)
			}
		case "down":
			if len(visible) > 0 {
				cursor = (cursor + 1) % len(visible)
			}
		case "right":
			if len(visible) > 0 && visible[cursor].Group {
				open[visible[cursor].Name] = true
			}
		case "left":
			if len(visible) == 0 {
				continue
			}
			row := visible[cursor]
			if row.Group {
				open[row.Name] = false
			} else {
				for index := cursor - 1; index >= 0; index-- {
					if visible[index].Group {
						open[visible[index].Name] = false
						cursor = index
						break
					}
				}
			}
		case "space":
			if len(visible) > 0 {
				row := visible[cursor]
				all := markedAll(row, marked)
				for _, skill := range row.Skills {
					if all {
						delete(marked, skill.Dir)
					} else {
						marked[skill.Dir] = skill
					}
				}
			}
		case "backspace":
			if query != "" {
				_, size := utf8.DecodeLastRuneInString(query)
				query = query[:len(query)-size]
				cursor = 0
			}
		default:
			if utf8.RuneCountInString(key) == 1 {
				query += key
				cursor = 0
			}
		}
		drawn = false
	}
}

func (p Picker) rows(tree SkillTree, skills []Skill, query string, open map[string]bool) []PickerRow {
	rows := tree.Rows(query, open)
	if p.SelectAllLabel != "" && query == "" {
		all := PickerRow{Name: p.SelectAllLabel, Description: fmt.Sprintf("%d skill%s", len(skills), plural(len(skills))), Skills: skills}
		rows = append([]PickerRow{all}, rows...)
	}
	return rows
}

func (p Picker) draw(skills []PickerRow, query string, cursor int, marked map[string]Skill, size viewport) {
	drawViewport(p.Out, p.frame(skills, query, cursor, marked, size), size)
}

func (p Picker) frame(skills []PickerRow, query string, cursor int, marked map[string]Skill, size viewport) []string {
	minimum := viewport{columns: 20, rows: 4}
	if size.columns < minimum.columns || size.rows < minimum.rows {
		return smallViewportFrame(size, minimum)
	}
	theme := p.Theme
	if theme.columns == 0 {
		theme = Theme{columns: size.columns}
	}
	theme.columns = size.columns
	limit := max(1, size.rows-2)
	start := 0
	if cursor >= limit {
		start = cursor - limit + 1
	}
	end := min(len(skills), start+limit)
	prompt := theme.Tag("wand", Magenta) + theme.Paint(p.Prompt, Bold)
	queryRoom := max(0, size.columns-visibleWidth(prompt)-1)
	lines := []string{prompt + " " + theme.Paint(theme.Trim(query, queryRoom), Amber)}
	for index := start; index < end; index++ {
		row := skills[index]
		here := index == cursor
		box := "[ ]"
		if markedAll(row, marked) {
			box = theme.Paint("[x]", Green)
		} else if markedSome(row, marked) {
			box = theme.Paint("[~]", Amber)
		} else {
			box = theme.Paint(box, Grey)
		}
		fold := "  "
		if row.Group && row.Open {
			fold = "▾ "
		} else if row.Group {
			fold = "▸ "
		}
		nameColor := Violet
		if row.Group {
			nameColor = Cyan
		} else if here {
			nameColor = Bold
		}
		lead := theme.Paint(map[bool]string{true: ">", false: " "}[here], Magenta) + " " + box + " "
		line := lead + theme.Paint(fold, Grey)
		nameRoom := max(1, size.columns-visibleWidth(line))
		name := theme.Trim(row.Name, nameRoom)
		line += theme.Paint(name, nameColor)
		descriptionRoom := size.columns - visibleWidth(line) - 2
		if row.Description != "" && descriptionRoom >= 4 {
			line += "  " + theme.Paint(theme.Trim(row.Description, descriptionRoom), Dim)
		}
		lines = append(lines, line)
	}
	if len(skills) == 0 {
		lines = append(lines, theme.Paint("  (nothing matches)", Grey))
	}
	for len(lines) < size.rows-1 {
		lines = append(lines, "")
	}
	hint := "arrows move  space marks  right opens  left closes  enter confirms  esc quits"
	if size.columns < visibleWidth(hint) {
		hint = "↑↓ move  space marks  enter confirms  esc quits"
	}
	lines = append(lines, theme.Paint(theme.Trim(hint, size.columns), Dim))
	return fitViewport(lines, size)
}

func readKey(reader *bufio.Reader, input *os.File) (string, error) {
	first, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	switch first {
	case '\r', '\n':
		return "enter", nil
	case ' ':
		return "space", nil
	case 3:
		return "interrupt", nil
	case 4:
		return "escape", nil
	case 8, 127:
		return "backspace", nil
	case 27:
		if reader.Buffered() < 2 && !inputWaiting(input, 50) {
			return "escape", nil
		}
		second, _ := reader.ReadByte()
		third, _ := reader.ReadByte()
		if second == '[' {
			switch third {
			case 'A':
				return "up", nil
			case 'B':
				return "down", nil
			case 'C':
				return "right", nil
			case 'D':
				return "left", nil
			}
		}
		return "escape", nil
	default:
		if first < utf8.RuneSelf {
			return string(first), nil
		}
		_ = reader.UnreadByte()
		r, _, err := reader.ReadRune()
		return string(r), err
	}
}

func markedAll(row PickerRow, marked map[string]Skill) bool {
	if len(row.Skills) == 0 {
		return false
	}
	for _, skill := range row.Skills {
		if _, ok := marked[skill.Dir]; !ok {
			return false
		}
	}
	return true
}

func markedSome(row PickerRow, marked map[string]Skill) bool {
	for _, skill := range row.Skills {
		if _, ok := marked[skill.Dir]; ok {
			return true
		}
	}
	return false
}

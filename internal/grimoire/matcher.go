package grimoire

import (
	"sort"
	"strings"
	"unicode"
)

// MatchScore accepts a dense ordered-character match or a one-character typo.
// Lower scores are better; ok is false when the query does not match.
func MatchScore(query, text string) (score int, ok bool) {
	needle := []rune(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, query))
	haystack := []rune(strings.ToLower(text))
	if len(needle) == 0 {
		return 0, true
	}
	if len(needle) >= 4 && oneEditApart(needle, haystack) {
		return intAbs(len(needle)-len(haystack)) + len(haystack), true
	}
	first, last, at, gaps := -1, -1, 0, 0
	for _, wanted := range needle {
		found := -1
		for i := at; i < len(haystack); i++ {
			if haystack[i] == wanted {
				found = i
				break
			}
		}
		if found < 0 {
			return 0, false
		}
		if first < 0 {
			first = found
		}
		if last >= 0 {
			gaps += found - last - 1
		}
		last, at = found, found+1
	}
	if last-first+1 > len(needle)*2 {
		return 0, false
	}
	return first*10 + gaps*5 + len(haystack), true
}

func oneEditApart(left, right []rune) bool {
	if intAbs(len(left)-len(right)) > 1 {
		return false
	}
	i, j, edits := 0, 0, 0
	for i < len(left) && j < len(right) {
		if left[i] == right[j] {
			i++
			j++
			continue
		}
		edits++
		if edits > 1 {
			return false
		}
		switch {
		case len(left) > len(right):
			i++
		case len(right) > len(left):
			j++
		default:
			i++
			j++
		}
	}
	if i < len(left) || j < len(right) {
		edits++
	}
	return edits <= 1
}

func intAbs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func rankSkills(query string, skills []Skill) []Skill {
	if strings.TrimSpace(query) == "" {
		return append([]Skill(nil), skills...)
	}
	type ranked struct {
		score int
		index int
		skill Skill
	}
	var matches []ranked
	for index, skill := range skills {
		if score, ok := skillMatchScore(query, skill); ok {
			matches = append(matches, ranked{score: score, index: index, skill: skill})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			return matches[i].index < matches[j].index
		}
		return matches[i].score < matches[j].score
	})
	result := make([]Skill, len(matches))
	for i, match := range matches {
		result[i] = match.skill
	}
	return result
}

func skillMatchScore(query string, skill Skill) (int, bool) {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return 0, true
	}
	tokens := []string{skill.Name, skill.Group}
	for _, field := range []string{skill.Name, skill.Group, skill.Description} {
		tokens = append(tokens, strings.FieldsFunc(field, func(mark rune) bool {
			return !unicode.IsLetter(mark) && !unicode.IsNumber(mark)
		})...)
	}
	total := 0
	for _, term := range terms {
		best, matched := 0, false
		for _, token := range tokens {
			if score, ok := MatchScore(term, token); ok && (!matched || score < best) {
				best, matched = score, true
			}
		}
		if !matched {
			return 0, false
		}
		total += best
	}
	return total, true
}

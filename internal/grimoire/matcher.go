package grimoire

import (
	"sort"
	"strings"
	"unicode"
)

// MatchScore implements the Ruby command's loose ordered-character match.
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
	return first*10 + gaps*5 + len(haystack), true
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
		text := skill.Group + " " + skill.Name + " " + skill.Description
		if score, ok := MatchScore(query, text); ok {
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

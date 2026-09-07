package grimoire

import (
	"fmt"
	"sort"
	"strings"
)

type PickerRow struct {
	Group       bool
	Name        string
	Description string
	Skills      []Skill
	Open        bool
}

type SkillTree struct {
	skills               []Skill
	groups               []string
	multipleRepositories bool
}

func NewSkillTree(skills []Skill) SkillTree {
	multipleRepositories := multipleSkillRepositories(skills)
	held := map[string]bool{}
	for _, skill := range skills {
		if group := skillGroupKey(skill, multipleRepositories); group != "" {
			held[group] = true
		}
	}
	groups := make([]string, 0, len(held))
	for group := range held {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return SkillTree{skills: append([]Skill(nil), skills...), groups: groups, multipleRepositories: multipleRepositories}
}

func (t SkillTree) GroupNames() []string { return append([]string(nil), t.groups...) }

func (t SkillTree) Rows(query string, open map[string]bool) []PickerRow {
	byGroup := map[string][]Skill{}
	for _, skill := range t.skills {
		group := skillGroupKey(skill, t.multipleRepositories)
		byGroup[group] = append(byGroup[group], skill)
	}
	names := append([]string(nil), t.groups...)
	if len(byGroup[""]) > 0 {
		names = append([]string{""}, names...)
	}
	filtering := strings.TrimSpace(query) != ""
	var rows []PickerRow
	for _, group := range names {
		found := rankSkills(query, byGroup[group])
		if filtering {
			if _, groupMatches := MatchScore(query, group); groupMatches {
				found = append([]Skill(nil), byGroup[group]...)
			}
		}
		if len(found) == 0 {
			continue
		}
		if group == "" {
			for _, skill := range found {
				rows = append(rows, skillRow(skill))
			}
			continue
		}
		shown := filtering || open[group]
		rows = append(rows, PickerRow{
			Group:       true,
			Name:        group,
			Description: fmt.Sprintf("%d skill%s", len(found), plural(len(found))),
			Skills:      found,
			Open:        shown,
		})
		if shown {
			for _, skill := range found {
				rows = append(rows, skillRow(skill))
			}
		}
	}
	return rows
}

func skillRow(skill Skill) PickerRow {
	return PickerRow{Name: skill.Name, Description: skill.Description, Skills: []Skill{skill}}
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

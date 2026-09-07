package grimoire

import (
	"fmt"
	"os"
	"path/filepath"
)

type InstallStatus string

const (
	Installed      InstallStatus = "installed"
	Removed        InstallStatus = "removed"
	Missing        InstallStatus = "missing"
	InstallBlocked InstallStatus = "blocked"
)

type InstallResult struct {
	Status  InstallStatus
	Skill   Skill
	Message string
}

func Install(skill Skill) InstallResult {
	if len(skill.LinkPaths()) == 0 {
		return InstallResult{Status: InstallBlocked, Skill: skill, Message: "no familiar chosen"}
	}
	if skill.Installed() {
		return InstallResult{Status: AlreadyStatus, Skill: skill, Message: "already installed"}
	}
	for _, link := range skill.LinkPaths() {
		if message := obstruction(skill, link); message != "" {
			return InstallResult{Status: InstallBlocked, Skill: skill, Message: message}
		}
	}
	for _, link := range skill.LinkPaths() {
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return InstallResult{Status: InstallBlocked, Skill: skill, Message: err.Error()}
		}
	}
	var created []string
	for _, link := range skill.LinkPaths() {
		if skill.InstalledAt(link) {
			continue
		}
		if err := os.Symlink(skill.Dir, link); err != nil {
			for _, made := range created {
				_ = os.Remove(made)
			}
			return InstallResult{Status: InstallBlocked, Skill: skill, Message: err.Error()}
		}
		created = append(created, link)
	}
	return InstallResult{Status: Installed, Skill: skill, Message: "installed"}
}

const AlreadyStatus InstallStatus = "already"

func Uninstall(skill Skill) InstallResult {
	if len(skill.LinkPaths()) == 0 {
		return InstallResult{Status: InstallBlocked, Skill: skill, Message: "no familiar chosen"}
	}
	for _, link := range skill.LinkPaths() {
		if message := obstruction(skill, link); message != "" {
			return InstallResult{Status: InstallBlocked, Skill: skill, Message: message}
		}
	}
	owned := false
	for _, link := range skill.LinkPaths() {
		if skill.InstalledAt(link) {
			owned = true
		}
	}
	if !owned {
		return InstallResult{Status: Missing, Skill: skill, Message: "not installed"}
	}
	for _, link := range skill.LinkPaths() {
		if skill.InstalledAt(link) {
			if err := os.Remove(link); err != nil {
				return InstallResult{Status: InstallBlocked, Skill: skill, Message: fmt.Sprintf("remove link: %v", err)}
			}
		}
	}
	return InstallResult{Status: Removed, Skill: skill, Message: "removed"}
}

func obstruction(skill Skill, link string) string {
	if skill.InstalledAt(link) {
		return ""
	}
	if _, err := os.Readlink(link); err == nil {
		return "a link that is not ours already holds this name"
	}
	if _, err := os.Lstat(link); err == nil {
		return "a real folder already holds this name"
	} else if !os.IsNotExist(err) {
		return fmt.Sprintf("cannot inspect destination: %v", err)
	}
	return ""
}

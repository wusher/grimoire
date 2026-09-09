package grimoire

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const ownershipVersion = 1

type ownedLink struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

type ownershipState struct {
	Version   int         `json:"version"`
	Links     []ownedLink `json:"links"`
	persisted bool
}

func configuredOwnership(paths Paths) (ownershipState, error) {
	body, err := os.ReadFile(paths.OwnershipFile())
	if os.IsNotExist(err) {
		return ownershipState{Version: ownershipVersion}, nil
	}
	if err != nil {
		return ownershipState{}, fmt.Errorf("read ownership: %w", err)
	}
	var state ownershipState
	if err := json.Unmarshal(body, &state); err != nil {
		return ownershipState{}, fmt.Errorf("read ownership: %w", err)
	}
	if state.Version != ownershipVersion {
		return ownershipState{}, fmt.Errorf("unsupported ownership version %d", state.Version)
	}
	seen := map[string]bool{}
	for index := range state.Links {
		link := &state.Links[index]
		if !filepath.IsAbs(link.Path) || !filepath.IsAbs(link.Target) {
			return ownershipState{}, fmt.Errorf("ownership paths must be absolute")
		}
		link.Path = filepath.Clean(link.Path)
		link.Target = filepath.Clean(link.Target)
		if seen[link.Path] {
			return ownershipState{}, fmt.Errorf("duplicate ownership path %s", link.Path)
		}
		seen[link.Path] = true
	}
	state.persisted = true
	state.sort()
	return state, nil
}

func writeOwnership(paths Paths, state ownershipState) error {
	state.Version = ownershipVersion
	state.sort()
	return writeJSONFile(paths.OwnershipFile(), state)
}

func (s *ownershipState) sort() {
	sort.Slice(s.Links, func(i, j int) bool { return s.Links[i].Path < s.Links[j].Path })
}

func (s ownershipState) clone() ownershipState {
	return ownershipState{Version: s.Version, Links: append([]ownedLink(nil), s.Links...), persisted: s.persisted}
}

func ownershipEqual(left, right ownershipState) bool {
	if left.Version != right.Version || len(left.Links) != len(right.Links) {
		return false
	}
	left = left.clone()
	right = right.clone()
	left.sort()
	right.sort()
	for index := range left.Links {
		if left.Links[index] != right.Links[index] {
			return false
		}
	}
	return true
}

func commitOwnershipUpdate(paths Paths, before, after ownershipState, rollback func() error) (ownershipState, error) {
	if ownershipEqual(before, after) {
		return before, nil
	}
	if err := writeOwnership(paths, after); err != nil {
		var changeErr error
		if rollback != nil {
			if rollbackErr := rollback(); rollbackErr != nil {
				changeErr = fmt.Errorf("rollback link or binding changes: %w", rollbackErr)
			}
		}
		restoreErr := restoreOwnership(paths, before)
		if restoreErr != nil {
			restoreErr = fmt.Errorf("rollback ownership: %w", restoreErr)
		}
		return before, errors.Join(fmt.Errorf("save ownership: %w", err), changeErr, restoreErr)
	}
	after.persisted = true
	return after, nil
}

func restoreOwnership(paths Paths, state ownershipState) error {
	if !state.persisted {
		return removeIfExists(paths.OwnershipFile())
	}
	return writeOwnership(paths, state)
}

func (s ownershipState) target(path string) (string, bool) {
	path = filepath.Clean(path)
	for _, link := range s.Links {
		if link.Path == path {
			return link.Target, true
		}
	}
	return "", false
}

func (s *ownershipState) set(path, target string) {
	path, target = filepath.Clean(path), filepath.Clean(target)
	for index := range s.Links {
		if s.Links[index].Path == path {
			s.Links[index].Target = target
			return
		}
	}
	s.Links = append(s.Links, ownedLink{Path: path, Target: target})
}

func (s *ownershipState) remove(path string) bool {
	path = filepath.Clean(path)
	for index := range s.Links {
		if s.Links[index].Path == path {
			s.Links = append(s.Links[:index], s.Links[index+1:]...)
			return true
		}
	}
	return false
}

func linkTarget(path string) (string, error) {
	raw, err := readSymlink(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolveLink(path, raw)), nil
}

func ownershipProves(state ownershipState, path string) bool {
	recorded, ok := state.target(path)
	if !ok {
		return false
	}
	actual, err := linkTarget(path)
	return err == nil && actual == filepath.Clean(recorded)
}

func knownSkillHome(paths Paths, link string) bool {
	parent := filepath.Clean(filepath.Dir(link))
	for _, home := range paths.KnownSkillsHomes() {
		absoluteHome, err := absolute(home)
		if err == nil && parent == absoluteHome {
			return true
		}
	}
	return false
}

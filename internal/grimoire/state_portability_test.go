package grimoire

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type portabilityState struct {
	Generation int    `json:"generation"`
	Payload    string `json:"payload"`
}

func TestStateReplacementIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := writeJSONFile(path, portabilityState{}); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		for generation := 1; generation <= 16; generation++ {
			state := portabilityState{
				Generation: generation,
				Payload:    strings.Repeat(fmt.Sprintf("%02d", generation), 32*1024),
			}
			if err := writeJSONFile(path, state); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			if state := assertAtomicState(t, path); state.Generation != 16 {
				t.Fatalf("final state generation = %d, want 16", state.Generation)
			}
			matches, globErr := filepath.Glob(filepath.Join(filepath.Dir(path), ".state.json-*"))
			if globErr != nil || len(matches) != 0 {
				t.Fatalf("temporary state files = %v, error = %v", matches, globErr)
			}
			return
		default:
			assertAtomicState(t, path)
		}
	}
}

func TestStateOperationsSupportLongPaths(t *testing.T) {
	root := t.TempDir()
	for len(root) < 320 {
		root = filepath.Join(root, strings.Repeat("long-path-", 8))
	}
	repository := filepath.Join(root, "repository")
	if err := os.MkdirAll(filepath.Join(repository, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(repository, "state.json")
	if err := writeJSONFile(path, portabilityState{Generation: 1, Payload: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFile(path, portabilityState{Generation: 2, Payload: "second"}); err != nil {
		t.Fatal(err)
	}
	if state := readPortableState(t, path); state != (portabilityState{Generation: 2, Payload: "second"}) {
		t.Fatalf("state = %#v, want the second replacement", state)
	}

	if identity := repositoryIdentity(repository); identity == "" {
		t.Fatal("repository identity is empty")
	}

	target := filepath.Join(repository, "target")
	link := filepath.Join(repository, "link")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := platformCreateSymlink(target, link, true); err != nil {
		t.Fatal(err)
	}
	snapshot, err := snapshotSymlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.directory || !samePath(resolveLink(link, snapshot.target), target) {
		t.Fatalf("symlink snapshot = %#v, want directory target %s", snapshot, target)
	}
}

func TestPlatformRawPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		path := filepath.Join("relative", "state.json")
		if got := platformRawPath(path); got != path {
			t.Fatalf("platformRawPath(%q) = %q", path, got)
		}
		return
	}

	drive := filepath.VolumeName(t.TempDir())
	absolute := filepath.Join(drive+`\`, "state", "file.json")
	tests := []struct {
		path string
		want string
	}{
		{absolute, `\\?\` + absolute},
		{`\\server\share\state\file.json`, `\\?\UNC\server\share\state\file.json`},
		{`//server/share/state/file.json`, `\\?\UNC\server\share\state\file.json`},
		{`relative/target`, `relative\target`},
		{`\\?\C:\state\file.json`, `\\?\C:\state\file.json`},
		{`\\.\C:\state\file.json`, `\\.\C:\state\file.json`},
	}
	for _, test := range tests {
		if got := platformRawPath(test.path); got != test.want {
			t.Errorf("platformRawPath(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}

func readPortableState(t *testing.T, path string) portabilityState {
	t.Helper()
	var body []byte
	var err error
	for range 100 {
		body, err = os.ReadFile(path)
		if err == nil || runtime.GOOS != "windows" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	var state portabilityState
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatalf("read partial state: %v", err)
	}
	return state
}

func assertAtomicState(t *testing.T, path string) portabilityState {
	t.Helper()
	state := readPortableState(t, path)
	if state.Generation == 0 {
		if state.Payload != "" {
			t.Fatalf("initial state payload = %q", state.Payload)
		}
		return state
	}
	if state.Generation < 1 || state.Generation > 16 {
		t.Fatalf("state generation = %d", state.Generation)
	}
	want := strings.Repeat(fmt.Sprintf("%02d", state.Generation), 32*1024)
	if state.Payload != want {
		t.Fatalf("state generation %d has a partial payload", state.Generation)
	}
	return state
}

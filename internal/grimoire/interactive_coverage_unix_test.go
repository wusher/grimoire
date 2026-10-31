//go:build linux

package grimoire

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func withTerminalInput(t *testing.T, input string, run func(*os.File)) {
	t.Helper()
	master, terminal, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := pty.Setsize(terminal, &pty.Winsize{Cols: 100, Rows: 36}); err != nil {
		_ = master.Close()
		_ = terminal.Close()
		t.Fatal(err)
	}
	drained := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, master)
		close(drained)
	}()
	written := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			state, stateErr := unix.IoctlGetTermios(int(terminal.Fd()), unix.TCGETS)
			if stateErr != nil {
				written <- stateErr
				return
			}
			if state.Lflag&unix.ICANON == 0 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		var err error
		for index := range len(input) {
			if _, err = master.Write([]byte{input[index]}); err != nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		written <- err
	}()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		run(terminal)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		_ = terminal.Close()
		_ = master.Close()
		t.Fatal("terminal interaction timed out")
	}
	select {
	case err := <-written:
		if err != nil {
			_ = terminal.Close()
			_ = master.Close()
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		_ = terminal.Close()
		_ = master.Close()
		t.Fatal("terminal input was not written")
	}
	if err := terminal.Close(); err != nil {
		_ = master.Close()
		t.Fatal(err)
	}
	_ = master.Close()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("terminal output drain did not stop")
	}
}

func TestPickerInteractiveKeyboardPaths(t *testing.T) {
	skills := []Skill{{Name: "alpha", Dir: "/skills/alpha"}, {Name: "beta", Dir: "/skills/beta"}}
	withTerminalInput(t, "\r", func(terminal *os.File) {
		chosen, err := (Picker{In: terminal, Out: terminal, Prompt: "choose"}).Pick(skills)
		if err != nil || len(chosen) != 1 {
			t.Fatalf("marked picker = %#v, %v", chosen, err)
		}
	})
	withTerminalInput(t, "\x04", func(terminal *os.File) {
		chosen, err := (Picker{In: terminal, Out: terminal, Prompt: "choose"}).Pick(skills)
		if err != nil || chosen != nil {
			t.Fatalf("cancelled picker = %#v, %v", chosen, err)
		}
	})
	grouped := []Skill{{Name: "alpha", Group: "tools", Dir: "/skills/tools/alpha"}}
	withTerminalInput(t, "\r", func(terminal *os.File) {
		chosen, err := (Picker{In: terminal, Out: terminal, Prompt: "choose"}).Pick(grouped)
		if err != nil || len(chosen) != 1 {
			t.Fatalf("group picker = %#v, %v", chosen, err)
		}
	})
}

func TestCatalogAndFamiliarInteractiveKeyboardPaths(t *testing.T) {
	skills := []Skill{{Name: "alpha", Dir: "/skills/alpha"}, {Name: "beta", Dir: "/skills/beta"}}
	withTerminalInput(t, "\r", func(terminal *os.File) {
		if err := (CatalogBrowser{In: terminal, Out: terminal, Familiar: "claude"}).Browse(skills); err != nil {
			t.Fatal(err)
		}
	})
	familiars := []agentFamiliar{{Name: "global", Label: "Global"}, {Name: "claude", Label: "Claude"}}
	withTerminalInput(t, "\r", func(terminal *os.File) {
		name, saved, err := (FamiliarPicker{In: terminal, Out: terminal}).Pick(familiars, "global")
		if err != nil || !saved || name != "global" {
			t.Fatalf("familiar = %q, %v, %v", name, saved, err)
		}
	})
	withTerminalInput(t, "\x04", func(terminal *os.File) {
		name, saved, err := (FamiliarPicker{In: terminal, Out: terminal}).Pick(familiars, "")
		if err != nil || saved || name != "" {
			t.Fatalf("cancelled familiar = %q, %v, %v", name, saved, err)
		}
	})
}

func TestTerminalAnimationsAcceptInput(t *testing.T) {
	withTerminalInput(t, "\r", func(terminal *os.File) {
		if !runCackle(terminal, terminal, func(art []string, _ Color, size viewport) []string {
			return animationFrame(art, size)
		}) {
			t.Fatal("cackle did not observe terminal input")
		}
	})
	withTerminalInput(t, "\r", func(terminal *os.File) {
		runFireworks(terminal, terminal, Theme{}, []string{"alpha"})
	})
}

func TestPickerInteractiveSelectionAndNavigation(t *testing.T) {
	skills := []Skill{{Name: "alpha", Dir: "/skills/alpha"}, {Name: "beta", Dir: "/skills/beta"}}
	for _, test := range []struct {
		name, input, want string
	}{
		{name: "mark", input: " \r", want: "alpha"},
		{name: "move down", input: "\x1b[B\r", want: "beta"},
	} {
		t.Run(test.name, func(t *testing.T) {
			withTerminalInput(t, test.input, func(terminal *os.File) {
				chosen, err := (Picker{In: terminal, Out: terminal, Prompt: "choose"}).Pick(skills)
				if err != nil || len(chosen) != 1 || chosen[0].Name != test.want {
					t.Fatalf("picker = %#v, %v; want %s", chosen, err, test.want)
				}
			})
		})
	}
	withTerminalInput(t, "z\x7f\x04", func(terminal *os.File) {
		chosen, err := (Picker{In: terminal, Out: terminal, Prompt: "choose"}).Pick(skills)
		if err != nil || chosen != nil {
			t.Fatalf("filtered picker = %#v, %v", chosen, err)
		}
	})
	grouped := []Skill{{Name: "alpha", Group: "tools", Dir: "/skills/tools/alpha"}}
	withTerminalInput(t, "\x1b[C\x1b[D\x04", func(terminal *os.File) {
		chosen, err := (Picker{In: terminal, Out: terminal, Prompt: "choose"}).Pick(grouped)
		if err != nil || chosen != nil {
			t.Fatalf("group navigation = %#v, %v", chosen, err)
		}
	})
}

func TestFamiliarInteractiveNavigation(t *testing.T) {
	familiars := []agentFamiliar{{Name: "global", Label: "Global"}, {Name: "claude", Label: "Claude"}}
	withTerminalInput(t, "\x1b[B\x1b[C\x1b[A\x1b[D\r", func(terminal *os.File) {
		name, saved, err := (FamiliarPicker{In: terminal, Out: terminal}).Pick(familiars, "global")
		if err != nil || !saved || name != "global" {
			t.Fatalf("navigated familiar = %q, %v, %v", name, saved, err)
		}
	})
}

func TestCLIInteractiveFamiliarSelectionAndCancellation(t *testing.T) {
	withTerminalInput(t, "\x1b[B\r", func(terminal *os.File) {
		paths := testPaths(t)
		paths.Familiar = ""
		var output strings.Builder
		cli := &CLI{In: terminal, Out: &output, Err: terminal, Paths: paths}
		ready, err := cli.ensureFamiliar()
		if err != nil || !ready || cli.Paths.Familiar != "claude" {
			t.Fatalf("ensure familiar = %v, %v, %q", ready, err, cli.Paths.Familiar)
		}
		if _, err := os.Stat(paths.FamiliarFile()); err != nil {
			t.Fatalf("familiar was not saved: %v", err)
		}
	})

	withTerminalInput(t, "\x04", func(terminal *os.File) {
		paths := testPaths(t)
		paths.Familiar = ""
		var output strings.Builder
		cli := &CLI{In: terminal, Out: &output, Err: terminal, Paths: paths}
		ready, err := cli.ensureFamiliar()
		if err != nil || ready || !strings.Contains(output.String(), "no familiar chosen") {
			t.Fatalf("cancel ensure = %v, %v, %q", ready, err, output.String())
		}
	})

	withTerminalInput(t, "\x1b[B\r", func(terminal *os.File) {
		paths := testPaths(t)
		paths.Familiar = "global"
		var output strings.Builder
		cli := &CLI{In: terminal, Out: &output, Err: terminal, Paths: paths}
		code, err := cli.configureFamiliar(nil)
		if err != nil || code != 0 || cli.Paths.Familiar != "claude" {
			t.Fatalf("configure familiar = %d, %v, %q", code, err, cli.Paths.Familiar)
		}
		if !strings.Contains(output.String(), "existing links remain") {
			t.Fatalf("configure output = %q", output.String())
		}
	})

	withTerminalInput(t, "\x04", func(terminal *os.File) {
		paths := testPaths(t)
		var output strings.Builder
		cli := &CLI{In: terminal, Out: &output, Err: terminal, Paths: paths}
		if code, err := cli.configureFamiliar(nil); err != nil || code != 0 || !strings.Contains(output.String(), "remains unchanged") {
			t.Fatalf("cancel configure = %d, %v, %q", code, err, output.String())
		}
	})
}

func TestCLIInteractiveSkillSelectionAndCatalog(t *testing.T) {
	skills := []Skill{{Name: "alpha", Dir: "/skills/alpha"}, {Name: "beta", Dir: "/skills/beta"}}
	withTerminalInput(t, "\r", func(terminal *os.File) {
		cli := &CLI{In: terminal, Out: terminal, Err: terminal}
		chosen, err := cli.chooseSkills(nil, skills, "bound", "bind")
		if err != nil || len(chosen) != 2 {
			t.Fatalf("bind picker = %#v, %v", chosen, err)
		}
	})
	withTerminalInput(t, "\r", func(terminal *os.File) {
		cli := &CLI{In: terminal, Out: terminal, Err: terminal}
		chosen, err := cli.chooseSkills(nil, skills, "bound", "unbind")
		if err != nil || len(chosen) != 1 || chosen[0].Name != "alpha" {
			t.Fatalf("unbind picker = %#v, %v", chosen, err)
		}
	})
	withTerminalInput(t, "\r", func(terminal *os.File) {
		cli := &CLI{In: terminal, Out: terminal, Err: terminal}
		catalog := Catalog{Skills: skills, Clashes: map[string][]Skill{}}
		chosen, err := cli.choose(nil, catalog, skills, "install")
		if err != nil || len(chosen) != 1 || chosen[0].Name != "alpha" {
			t.Fatalf("cast picker = %#v, %v", chosen, err)
		}
	})

	withTerminalInput(t, "\x04", func(terminal *os.File) {
		paths := testPaths(t)
		makeSkill(t, paths, "", "alpha", "Alpha")
		cli := &CLI{In: terminal, Out: terminal, Err: terminal, Paths: paths}
		if err := cli.toc(nil); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCatalogBrowserNavigationAndRichBindingAnimation(t *testing.T) {
	skills := []Skill{{Name: "alpha", Dir: "/skills/alpha"}, {Name: "beta", Dir: "/skills/beta"}}
	withTerminalInput(t, "\x1b[B\x1b[Aa\x7f\r", func(terminal *os.File) {
		browser := CatalogBrowser{In: terminal, Out: terminal, Theme: Theme{columns: 100}, Familiar: "global"}
		if err := browser.Browse(skills); err != nil {
			t.Fatal(err)
		}
	})

	withTerminalInput(t, "\r", func(terminal *os.File) {
		paths := testPaths(t)
		cli := &CLI{In: terminal, Out: terminal, Err: terminal, Paths: paths}
		result := BindResult{Status: Bound, Path: paths.Home, Target: filepath.Join(paths.Home, "repository"), Message: "1 skill bound"}
		if code, err := cli.showBindingResults("bind", "the book takes your hand", []BindResult{result}, false); err != nil || code != 0 {
			t.Fatalf("binding results = %d, %v", code, err)
		}
	})
}

func TestConfirmIndexRefreshAcceptsYesAndDefaultsToNo(t *testing.T) {
	for _, test := range []struct {
		input string
		want  bool
	}{{"yes\n", true}, {"no\n", false}} {
		withTerminalInput(t, test.input, func(terminal *os.File) {
			cli := &CLI{In: terminal, Out: terminal}
			got, err := cli.confirmIndexRefresh()
			if err != nil || got != test.want {
				t.Fatalf("confirm refresh = %t, %v; want %t", got, err, test.want)
			}
		})
	}
}

func TestRelativePathResolutionFailsWhenWorkingDirectoryDisappears(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	working := parent + "/working"
	if err := os.Mkdir(working, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(working); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(working); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(original) }()
	if _, err := absolute("relative"); err == nil {
		t.Fatal("absolute path unexpectedly resolved")
	}
	if _, err := (Paths{Repo: "relative"}).Libraries(); err == nil {
		t.Fatal("relative library unexpectedly resolved")
	}
	if _, err := (Paths{Repo: "relative"}).SkillsRoots(); err == nil {
		t.Fatal("relative skills root unexpectedly resolved")
	}
}

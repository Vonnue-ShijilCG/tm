package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Vonnue-ShijilCG/tm/internal/state"
)

// configFile points this test's configuration at its own directory and
// returns a function that rewrites the file; the model re-reads it on every
// creation attempt, so no restart is needed for a change to apply.
func configFile(t *testing.T) func(content string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "tm"), 0o755); err != nil {
		t.Fatal(err)
	}
	return func(content string) {
		if err := os.WriteFile(filepath.Join(dir, "tm", "config.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEveryCreationKeyRespectsTheLimit(t *testing.T) {
	write := configFile(t)
	write("max_terminals = 2\n")
	m, spawned := newTestModel(t)
	m = press(m, "enter", "alt+n", "enter")
	p := m.ws.Projects[0]
	if len(*spawned) != 2 {
		t.Fatalf("setup spawned %d", len(*spawned))
	}
	attempts := [][]string{
		{"alt+n"}, {"alt+v"}, {"alt+h"}, // terminal focused
		{"esc", "n"}, {"esc", "v"}, {"esc", "h"}, // sidebar
	}
	for _, keys := range attempts {
		m = press(m, keys...)
		if m.form != nil {
			t.Errorf("%v: the form must not open at the limit", keys)
			m.form = nil
		}
		if !strings.Contains(m.notice, "maximum terminals reached (2)") || !strings.Contains(m.notice, "max_terminals") || !strings.Contains(m.notice, "config.toml") {
			t.Errorf("%v: notice = %q", keys, m.notice)
		}
		if !strings.Contains(m.View(), "maximum terminals reached") {
			t.Errorf("%v: the status bar should show the message", keys)
		}
		m = press(m, "enter") // would submit a form, or re-enter the running project: nothing new either way
		if len(*spawned) != 2 || len(p.Terminals) != 2 {
			t.Fatalf("%v: spawned=%d running=%d", keys, len(*spawned), len(p.Terminals))
		}
		if (*spawned)[0].closed || (*spawned)[1].closed {
			t.Fatalf("%v: refusing must not close anything", keys)
		}
	}
	if len(savedNames(m.root)) != 2 {
		t.Errorf("saved definitions changed: %v", savedNames(m.root))
	}

	// The panel's open (enter on a saved row) is refused the same way.
	m = press(m, "alt+x")
	m = exit(m, p.Terminals[1].ID) // one running, one saved
	write("max_terminals = 1\n")
	m = press(m, "esc", "m", "j", "enter") // second row: the saved definition
	if m.panel == nil || !strings.Contains(m.panel.notice, "maximum terminals reached (1)") {
		t.Errorf("panel open at the limit should be refused: %+v", m.panel)
	}
	if len(*spawned) != 2 {
		t.Errorf("panel open created a terminal: %d", len(*spawned))
	}
	if len(p.Saved) != 1 {
		t.Errorf("the saved definition must survive: %+v", p.Saved)
	}
}

func TestSavedDefinitionsDoNotConsumeCapacity(t *testing.T) {
	write := configFile(t)
	write("max_terminals = 2\n")
	m, spawned := newTestModel(t)
	m = press(m, "enter", "alt+n", "enter")
	p := m.ws.Projects[0]
	m = press(m, "alt+x")
	m = exit(m, p.Terminals[1].ID) // running 1, saved 1
	m = press(m, "alt+n", "enter")
	if len(*spawned) != 3 || len(p.Terminals) != 2 {
		t.Errorf("a saved definition must not block creation: spawned=%d running=%d", len(*spawned), len(p.Terminals))
	}
}

func TestConfigChangesApplyWithoutRestart(t *testing.T) {
	write := configFile(t)
	write("max_terminals = 2\n")
	m, spawned := newTestModel(t)
	m = press(m, "enter", "alt+n", "enter", "alt+n")
	p := m.ws.Projects[0]
	if m.form != nil || len(*spawned) != 2 {
		t.Fatalf("third terminal should be refused at 2: form=%v spawned=%d", m.form != nil, len(*spawned))
	}
	write("max_terminals = 4\n")   // the user edits the file...
	m = press(m, "alt+n", "enter") // ...and simply tries again
	m = press(m, "alt+n", "enter")
	if len(*spawned) != 4 || len(p.Terminals) != 4 {
		t.Fatalf("raised limit must apply immediately: spawned=%d running=%d", len(*spawned), len(p.Terminals))
	}
	m = press(m, "alt+n")
	if m.form != nil || !strings.Contains(m.notice, "(4)") {
		t.Errorf("fifth should be refused at 4: %q", m.notice)
	}

	// Lowering below the running count changes nothing that exists.
	write("max_terminals = 1\n")
	m = press(m, "alt+n")
	if m.form != nil || !strings.Contains(m.notice, "maximum terminals reached (1)") {
		t.Errorf("lowered limit: %q", m.notice)
	}
	if len(p.Terminals) != 4 || len(m.layouts[p].leaves()) != 4 {
		t.Errorf("existing terminals must stay running and visible: running=%d visible=%d", len(p.Terminals), len(m.layouts[p].leaves()))
	}
	for _, s := range *spawned {
		if s.closed {
			t.Error("a terminal was closed by lowering the limit")
		}
	}
	m = press(m, "alt+j", "alt+left", "alt+=") // the workspace keeps working
	f, _ := state.Load(m.root)
	if len(f.Projects) != 1 || len(f.Projects[0].Terminals) != 4 {
		t.Errorf("saved state must still hold all four: %+v", f)
	}

	// A form opened under a higher limit is refused at submit if the limit dropped meanwhile.
	write("max_terminals = 5\n")
	m = press(m, "alt+n")
	if m.form == nil {
		t.Fatal("form should open at 4 of 5")
	}
	write("max_terminals = 4\n")
	m = press(m, "enter")
	if m.form == nil || !strings.Contains(m.form.err, "maximum terminals reached (4)") || len(*spawned) != 4 {
		t.Errorf("submit must be refused by the workspace rule: form=%+v spawned=%d", m.form, len(*spawned))
	}
}

func TestInvalidConfigFallsBackToSixAndSaysSo(t *testing.T) {
	write := configFile(t)
	write("max_terminals = many\n")
	m, spawned := newTestModel(t)
	if !strings.Contains(m.notice, "config.toml") || !strings.Contains(m.notice, "positive integer") {
		t.Errorf("startup should report the bad value: %q", m.notice)
	}
	m = press(m, "enter")
	for i := 0; i < 5; i++ {
		m = press(m, "alt+n", "enter")
	}
	if len(*spawned) != 6 {
		t.Fatalf("fallback limit should allow six: %d", len(*spawned))
	}
	if !strings.Contains(m.notice, "config.toml") {
		t.Errorf("the problem should be shown again after using the limit: %q", m.notice)
	}
	m = press(m, "alt+n")
	if m.form != nil || !strings.Contains(m.notice, "maximum terminals reached (6)") {
		t.Errorf("seventh: %q", m.notice)
	}
	write("max_terminals = 7\n")
	m = press(m, "alt+n", "enter")
	if len(*spawned) != 7 || m.notice != "" {
		t.Errorf("fixed config: spawned=%d notice=%q", len(*spawned), m.notice)
	}
}

func TestEnterRestoresOnlyUpToTheLimitAndKeepsTheRestSaved(t *testing.T) {
	write := configFile(t)
	m, _ := newTestModel(t)
	m = press(m, "enter")
	for i := 0; i < 5; i++ {
		m = press(m, "alt+n", "enter")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if len(savedNames(m.root)) != 6 {
		t.Fatalf("saved %d", len(savedNames(m.root)))
	}

	write("max_terminals = 4\n")
	m2, spawned := restartModel(t, m)
	p := m2.ws.Projects[0]
	m2 = press(m2, "enter")
	if len(*spawned) != 1 || len(p.Terminals) != 1 || m2.notice != "" {
		t.Fatalf("enter restores one terminal: spawned=%d running=%d notice=%q", len(*spawned), len(p.Terminals), m2.notice)
	}
	m2 = press(m2, "esc", "m", "a")
	if len(*spawned) != 4 || len(p.Terminals) != 4 {
		t.Fatalf("restore all under a lower limit: spawned=%d running=%d", len(*spawned), len(p.Terminals))
	}
	if m2.notice != "restored 3 terminals; 2 not opened: max_terminals (4) reached" {
		t.Errorf("notice = %q", m2.notice)
	}
	if len(p.Saved) != 2 || len(savedNames(m2.root)) != 6 {
		t.Errorf("skipped definitions must stay saved: memory=%d file=%d", len(p.Saved), len(savedNames(m2.root)))
	}
	// The saved 3x2 layout is authoritative: with 5 and 6 not opened their
	// leaves collapse and the four visible panes keep the saved columns; the
	// layout itself is kept for when they come back.
	if len(m2.layouts[p].leaves()) != 4 || len(p.Layout.Leaves()) != 6 {
		t.Errorf("four restored terminals should all be visible, got %d", len(m2.layouts[p].leaves()))
	}
	checkTiling(t, m2, p)
	m2 = press(m2, "esc", "m", "a") // open all cannot go past the limit either
	if len(*spawned) != 4 || m2.panel == nil || !strings.Contains(m2.panel.notice, "2 not opened: max_terminals (4) reached") {
		t.Errorf("open all at the limit: spawned=%d panel=%+v", len(*spawned), m2.panel)
	}

	write("max_terminals = 6\n") // raise it: the rest comes back
	m2 = press(m2, "a")
	if len(*spawned) != 6 || len(p.Terminals) != 6 || len(p.Saved) != 0 {
		t.Errorf("after raising: spawned=%d running=%d saved=%d", len(*spawned), len(p.Terminals), len(p.Saved))
	}
	if m2.notice != "" {
		t.Errorf("clean restore should leave no notice: %q", m2.notice)
	}
	if shape(m2.layouts[p]) != "2,2,2" {
		t.Errorf("with everything back the saved 3x2 grid returns, got %s", shape(m2.layouts[p]))
	}
	checkTiling(t, m2, p)
}

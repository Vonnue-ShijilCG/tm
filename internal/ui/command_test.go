package ui

import (
	"strings"
	"testing"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

func TestAPaneIsLabelledByItsCommand(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "j", "enter")
	p, term := m.activeTerminal()
	term.Command = "npm run dev"

	panes := m.viewPanes(p, term)
	if !strings.Contains(panes, "npm run dev") {
		t.Errorf("pane header should show the command:\n%s", panes)
	}
	if strings.Contains(panes, "Terminal 1") {
		t.Errorf("the command should replace the terminal number:\n%s", panes)
	}
}

func TestAPaneHeaderEscapesControlCharsInTheCommand(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "j", "enter")
	p, term := m.activeTerminal()
	term.Command = "npm run dev\x1b[2Jrm -rf /"

	panes := m.viewPanes(p, term)
	if strings.Contains(panes, "\x1b[2J") {
		t.Errorf("pane header should not contain a raw ESC sequence from the command:\n%q", panes)
	}
	if !strings.Contains(panes, "npm run dev") {
		t.Errorf("pane header should still show the printable parts of the command:\n%s", panes)
	}

	// xterm in UTF-8 mode still honours U+009B (C1 CSI) as a one-byte
	// stand-in for ESC '['; stripControlChars must catch it too, not just
	// C0/DEL.
	term.Command = "npm run dev2Jrm -rf /"
	panes = m.viewPanes(p, term)
	if strings.Contains(panes, "") {
		t.Errorf("pane header should not contain a raw C1 CSI byte from the command:\n%q", panes)
	}
	if !strings.Contains(panes, "npm run dev") || !strings.Contains(panes, "2Jrm -rf /") {
		t.Errorf("pane header should still show the printable parts of the command:\n%s", panes)
	}

	// Ordinary multi-byte UTF-8 (accents, CJK, emoji) must survive untouched.
	term.Command = "café 日本語 🎉"
	panes = m.viewPanes(p, term)
	if !strings.Contains(panes, "café 日本語 🎉") {
		t.Errorf("pane header should preserve ordinary UTF-8 text:\n%q", panes)
	}
}

func TestAPaneWithoutACommandKeepsItsNumber(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "j", "enter")
	p, term := m.activeTerminal()

	if panes := m.viewPanes(p, term); !strings.Contains(panes, "Terminal 1") {
		t.Errorf("a terminal with no command should still show its number:\n%s", panes)
	}
}

func TestFormCreatesATerminalWithTheTypedCommand(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "j", "enter", "esc")

	// newTestModel's projects are not Git repositories, so the form has no
	// Branch field and opens with focus already on Command.
	m = press(m, "n")
	m = typeText(m, "npm run dev")
	m = press(m, "tab") // → Worktree (a no-op here: not a git repository)
	m = press(m, "enter")

	if m.form != nil {
		t.Fatalf("the form should have closed: %s", m.notice)
	}
	p := m.ws.Projects[1]
	last := p.Terminals[len(p.Terminals)-1]
	if last.Command != "npm run dev" {
		t.Fatalf("terminal command = %q, want %q", last.Command, "npm run dev")
	}
	s := (*spawned)[len(*spawned)-1]
	if len(s.text) == 0 || s.text[len(s.text)-1] != "npm run dev\n" {
		t.Errorf("shell received %q, want the command", s.text)
	}
}

func TestCommandFieldAcceptsLongerTextThanBranch(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "j", "enter", "esc", "n")

	long := strings.Repeat("a", 200)
	m = typeText(m, long)
	if got := m.form.command.Value(); got != long {
		t.Errorf("command field truncated a 200-char value to %d chars, want %d", len(got), len(long))
	}
}

func TestManagerLabelsARowByItsCommand(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "j", "enter")
	_, term := m.activeTerminal()
	term.Command = "npm run dev"

	m = press(m, "esc", "m")
	view := m.View()
	if !strings.Contains(view, "npm run dev") {
		t.Errorf("manager row should show the command:\n%s", view)
	}
	if strings.Contains(view, "Terminal 1") {
		t.Errorf("the command should replace the terminal number in the manager:\n%s", view)
	}
}

func TestManagerSavedRowLabelEscapesControlChars(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "j", "enter")
	p, _ := m.activeTerminal()
	// A saved definition never passed through the form (e.g. loaded from a
	// tampered state file), so its Command is untrusted free text just like
	// a running terminal's.
	p.Saved = append(p.Saved, workspace.SavedTerminal{Command: "deploy\x1b[2Jrm -rf /"})

	m = press(m, "esc", "m")
	view := m.View()
	if strings.Contains(view, "\x1b[2J") {
		t.Errorf("a saved row must not render a raw ESC sequence from the command:\n%q", view)
	}
	if !strings.Contains(view, "deploy") {
		t.Errorf("saved row should still show the printable parts of the command:\n%s", view)
	}
}

func TestFormTypesACommandInAGitProjectAndTabsThroughAllFields(t *testing.T) {
	m, spawned, _ := gitModel(t)
	app := m.ws.Projects[0]

	m = press(m, "n")
	if m.form == nil || m.form.field != fieldBranch {
		t.Fatalf("a git project's form should open focused on Branch: %+v", m.form)
	}
	m = press(m, "tab")
	if m.form.field != fieldCommand {
		t.Fatalf("tab from Branch should reach Command, field=%d", m.form.field)
	}
	m = typeText(m, "npm run dev")
	m = press(m, "tab")
	if m.form.field != fieldBranch {
		t.Fatalf("tab from Command should wrap back to Branch (no branch typed, no worktree needed), field=%d", m.form.field)
	}
	m = press(m, "enter") // branch still empty: a plain shell in the project checkout

	if m.form != nil {
		t.Fatalf("the form should have closed: %s", m.notice)
	}
	last := app.Terminals[len(app.Terminals)-1]
	if last.Command != "npm run dev" {
		t.Fatalf("terminal command = %q, want %q", last.Command, "npm run dev")
	}
	if last.Worktree != nil {
		t.Error("an empty branch must not create a worktree")
	}
	s := (*spawned)[len(*spawned)-1]
	if len(s.text) == 0 || s.text[len(s.text)-1] != "npm run dev\n" {
		t.Errorf("shell received %q, want the command", s.text)
	}
}

package ui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// tm paints every cell it owns: a cell drawn with no background falls
// through to the user's own terminal colour, which on a translucent
// terminal shows the desktop through tm's frame. Every other UI test runs
// under Lip Gloss's no-colour profile, where styles emit no escapes at all,
// so none of them can see a cell that lost its ground. This file turns
// colour on and reads the escapes back.

// withTrueColor makes the package's styles emit real colour for one test.
// They were built on the default renderer at init, so the profile has to be
// set there; a fresh renderer would not reach them.
func withTrueColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// bareRuns returns every run of visible text in s drawn while no background
// was armed: text at the start of a line, or after an SGR reset (or 49),
// before any SGR sets a background again. Each run is one stretch of cells
// the terminal would paint in its own colour.
func bareRuns(s string) []string {
	var runs []string
	var run strings.Builder
	armed := false
	flush := func() {
		if run.Len() > 0 {
			runs = append(runs, run.String())
			run.Reset()
		}
	}
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == '\x1b' && i+1 < len(s) && s[i+1] == '[': // CSI
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				if before := armed; applySGR(s[i+2:j], &armed) != before {
					flush()
				}
			}
			i = j + 1
		case c == '\x1b' && i+1 < len(s) && s[i+1] == ']': // OSC, up to BEL or ST
			j := i + 2
			for j < len(s) && s[j] != '\a' && !(s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\') {
				j++
			}
			if j < len(s) && s[j] == '\x1b' {
				j++
			}
			i = j + 1
		case c == '\x1b':
			i += 2
		case c == '\n':
			flush()
			armed = false
			i++
		case c < 0x20 || c == 0x7f:
			i++
		default:
			if !armed {
				run.WriteByte(c)
			}
			i++
		}
	}
	flush()
	return runs
}

// applySGR updates armed for one SGR parameter string and returns the new
// state. 0 (or empty) and 49 drop the background; 40-47, 100-107 and 48 set
// one. 38/48/58 extended colours consume their argument parameters so a
// component such as 2 or 5 is never mistaken for an attribute.
func applySGR(params string, armed *bool) bool {
	if params == "" {
		*armed = false
		return *armed
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		sub := strings.Contains(p, ":") // colon form: arguments are inside this part
		if sub {
			p = p[:strings.Index(p, ":")]
		}
		n, _ := strconv.Atoi(p)
		switch {
		case p == "" || n == 0:
			*armed = false
		case n == 38 || n == 48 || n == 58:
			if !sub && i+1 < len(parts) {
				switch parts[i+1] {
				case "2":
					i += 4
				case "5":
					i += 2
				}
			}
			if n == 48 {
				*armed = true
			}
		case (n >= 40 && n <= 47) || (n >= 100 && n <= 107):
			*armed = true
		case n == 49:
			*armed = false
		}
	}
	return *armed
}

func TestBareRunsSeesTextAfterAReset(t *testing.T) {
	// The detector itself has to be proven before anything is asserted with it.
	s := "\x1b[38;2;1;2;3;48;2;7;17;43m●\x1b[0m \x1b[1;48;2;7;17;43mTerminal 1\x1b[0m\nplain\x1b[48;5;17m ok\x1b[49mgone"
	got := bareRuns(s)
	want := []string{" ", "plain", "gone"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("bareRuns = %q, want %q", got, want)
	}
	if got := bareRuns("\x1b[48;2;7;17;43mall painted\x1b[0m\n\x1b[44mstill\x1b[0m"); len(got) != 0 {
		t.Fatalf("a fully painted string must report nothing, got %q", got)
	}
}

// bare fails the test for every screen that leaves a cell unpainted, naming
// the runs so the report says which text lost its ground.
func bare(t *testing.T, screens map[string]string) {
	t.Helper()
	for name, s := range screens {
		if runs := bareRuns(s); len(runs) > 0 {
			t.Errorf("%s: text drawn with no background: %q", name, runs)
		}
	}
}

func TestPaneHeaderAndStatusBarPaintEveryCell(t *testing.T) {
	withTrueColor(t)
	m, spawned := newTestModel(t)
	sidebarStatus := m.viewStatus()
	m = press(m, "j", "enter")
	(*spawned)[0].grounded = true
	terminalStatus := m.viewStatus()
	m.notice = "something went wrong"
	noticedStatus := m.viewStatus()
	bare(t, map[string]string{
		"pane header":            paneHeader("Terminal 1", " · main", 40, true, true),
		"pane header (exited)":   paneHeader("claude", "", 30, false, false),
		"status bar (sidebar)":   sidebarStatus,
		"status bar (terminal)":  terminalStatus,
		"status bar with notice": noticedStatus,
		"whole view, terminal":   m.View(),
	})
}

func TestSidebarSplitsAndManagerPaintEveryCell(t *testing.T) {
	withTrueColor(t)
	m, spawned := newTestModel(t)
	empty := m.View()
	m = press(m, "j", "enter")
	m = makeTerminal(t, m, "alt+n", "", "")
	for _, s := range *spawned {
		s.grounded = true
	}
	split := m.View()
	m = press(m, "alt+x") // one pane hidden: the other takes the whole area
	hidden := m.View()
	m = press(m, "alt+s", "m")
	if m.panel == nil {
		t.Fatal("m in the sidebar should open the Terminal Manager")
	}
	bare(t, map[string]string{
		"empty workspace":  empty,
		"two panes":        split,
		"one pane hidden":  hidden,
		"terminal manager": m.View(),
	})
}

func TestHelpScreenPaintsEveryCell(t *testing.T) {
	withTrueColor(t)
	m, _ := newTestModel(t)
	m = press(m, "s")
	if !m.help {
		t.Fatal("s in the sidebar should open help")
	}
	tall := m.View()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 14})
	short := next.(Model).View()
	bare(t, map[string]string{
		"help at 100x30":   tall,
		"help at 60x14":    short,
		"help body itself": viewHelp(96, 26),
	})
}

func TestFormPaintsEveryCell(t *testing.T) {
	withTrueColor(t)
	m, _ := newTestModel(t)
	m = press(m, "n") // a plain directory: the command field with its placeholder
	if m.form == nil {
		t.Fatal("n should open the form")
	}
	plain := m.View()
	m = typeText(m, "npm test")
	typed := m.View()

	g, _, _ := gitModel(t)
	g = press(g, "n")
	if g.form == nil || !g.form.canWorktree {
		t.Fatal("n on a git project should open the worktree form")
	}
	gitEmpty := g.View()
	g = typeText(g, "feature/paint") // a new branch: the From row appears
	gitNew := g.View()
	g = press(g, "tab", "tab")
	gitCommand := g.View()
	g = press(g, "enter", "enter") // an empty command on a new branch: the form reports back
	bare(t, map[string]string{
		"form, placeholder":     plain,
		"form, typed command":   typed,
		"git form, empty":       gitEmpty,
		"git form, new branch":  gitNew,
		"git form, command row": gitCommand,
		"git form, after enter": g.View(),
	})
}

func TestChangesViewPaintsEveryCell(t *testing.T) {
	withTrueColor(t)
	m, _, root := gitModel(t)
	if err := os.WriteFile(filepath.Join(root, "app", "fresh.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clean, _, _ := gitModel(t)
	m = press(m, "d")
	clean = press(clean, "d")
	if m.diff == nil || clean.diff == nil {
		t.Fatal("d should open the changes view")
	}
	bare(t, map[string]string{
		"changes view":        m.View(),
		"changes view, clean": clean.View(),
	})
}

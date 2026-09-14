// Command screenshot renders tm to SVG for the README.
//
// tm is a visual tool whose whole argument is what it looks like, so the
// README needs a picture — and a picture taken by hand goes stale the first
// time the layout changes. This drives a real tm in a real PTY against a
// throwaway workspace, feeds its output through the same terminal emulator
// tm uses for its own panes, and draws the resulting grid of cells as SVG.
// `make screenshots` regenerates every image from scratch.
//
// Determinism is the point, so nothing here touches the developer's
// environment: the workspace, HOME and XDG_CONFIG_HOME are all temporary, the
// shell is /bin/sh (whose prompt is a bare "$"), and the terminals run
// scripts written alongside the fake repositories rather than whatever
// happens to be installed.
package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"

	"github.com/Vonnue-ShijilCG/tm/internal/state"
	"github.com/Vonnue-ShijilCG/tm/internal/terminal"
)

const (
	cols, rows = 100, 30

	// Cell geometry in the SVG. The ratio is roughly a terminal's own: a cell
	// about twice as tall as it is wide, which is the same fact the pane gaps
	// are built on.
	cellW, cellH = 8.4, 18.0
	fontSize     = 14.0
	baseline     = 13.5 // where the glyph sits inside its cell
)

// shot is one image: the keys that get tm to the view, and where it is written.
type shot struct {
	name  string
	keys  []string
	pause time.Duration // extra settling after the keys, for views that read git
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "screenshot:", err)
		os.Exit(1)
	}
}

func run() error {
	out, err := filepath.Abs("docs")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	bin, cleanup, err := buildTm()
	if err != nil {
		return err
	}
	defer cleanup()

	for _, s := range []shot{
		{name: "panes", keys: []string{"\r"}},
		{name: "changes", keys: []string{"\r", "\x1bd"}, pause: 700 * time.Millisecond},
	} {
		svg, err := capture(bin, s)
		if err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
		path := filepath.Join(out, s.name+".svg")
		if err := os.WriteFile(path, []byte(svg), 0o644); err != nil {
			return err
		}
		fmt.Println("wrote", path)
	}
	return nil
}

// buildTm compiles the tm in this checkout, so the images always show the
// code they are committed beside.
func buildTm() (string, func(), error) {
	dir, err := os.MkdirTemp("", "tm-screenshot-bin")
	if err != nil {
		return "", nil, err
	}
	bin := filepath.Join(dir, "tm")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/tm")
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("build tm: %v\n%s", err, out)
	}
	return bin, func() { os.RemoveAll(dir) }, nil
}

// capture runs tm against a fresh workspace and returns the screen as SVG.
func capture(bin string, s shot) (string, error) {
	work, err := os.MkdirTemp("", "tm-screenshot")
	if err != nil {
		return "", err
	}
	if os.Getenv("SCREENSHOT_DEBUG") == "" {
		defer os.RemoveAll(work)
	}

	root := filepath.Join(work, "workspace")
	if err := buildWorkspace(root); err != nil {
		return "", err
	}

	cmd := exec.Command(bin)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"HOME="+work,
		"XDG_CONFIG_HOME="+filepath.Join(work, "config"),
		"SHELL=/bin/sh",
		"TERM=xterm-256color",
		"COLORTERM=truecolor", // tm uses exact hex; without this Lip Gloss quantises to 256 slots
		"PS1=$ ",
	)
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return "", err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = tty.Close()
		_, _ = cmd.Process.Wait()
	}()

	emu := vt.NewEmulator(cols, rows)
	// The emulator answers tm's startup questions itself — what the background
	// colour is, where the cursor is — by writing to its own reply side. That
	// side must be drained, or the first query blocks the write that parsed
	// it and tm waits for an answer that is stuck behind it. tm's own Session
	// runs exactly this loop for the same reason.
	go func() { _, _ = io.Copy(tty, emu) }()
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := tty.Read(buf)
			if n > 0 {
				_, _ = emu.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	time.Sleep(2500 * time.Millisecond) // tm draws, the shells start
	for _, k := range s.keys {
		if _, err := tty.WriteString(k); err != nil {
			return "", err
		}
		time.Sleep(500 * time.Millisecond)
	}
	time.Sleep(1500*time.Millisecond + s.pause)

	return render(emu), nil
}

// buildWorkspace creates the repositories the screenshots show, and the state
// file that puts terminals in them. Seeding the state is what makes the
// layout reproducible: tm restores the arrangement rather than being driven
// through the new-terminal form keystroke by keystroke.
func buildWorkspace(root string) error {
	// Invented names throughout. The images go in a public README, so nothing
	// here should look like anybody's real repository or branch.
	repos := []struct{ name, branch, file, committed, working string }{
		{"api", "feature/checkout-flow", "server.go",
			"func main() {\n\tlisten(\":8080\")\n}\n",
			"func main() {\n\tlisten(\":8080\")\n\tlog.Println(\"ready\")\n}\n"},
		{"web", "fix/token-refresh", "app.ts", "export const port = 3000\n", "export const port = 3000\n"},
		{"docs", "main", "README.md", "# docs\n", "# docs\n"},
	}
	// Two short scripts, so the panes show recognisable output instead of
	// whatever happens to be installed on the machine running this.
	scripts := map[string]string{
		"dev":   "#!/bin/sh\nprintf 'listening on :8080\\n'\nprintf 'compiled in 412ms\\n'\nsleep 600\n",
		"tests": "#!/bin/sh\nprintf 'PASS  server_test.go\\n'\nprintf 'PASS  routes_test.go\\n'\nprintf '\\n2 passed\\n'\nsleep 600\n",
	}

	for _, r := range repos {
		dir := filepath.Join(root, r.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, r.file), []byte(r.committed), 0o644); err != nil {
			return err
		}
		// Committed with everything else: they are what the panes run, not
		// something the changes view should be listing as new.
		if r.name == "api" {
			for name, body := range scripts {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
					return err
				}
			}
		}
		for _, args := range [][]string{
			{"init", "-q", "-b", "main"},
			{"-c", "user.name=tm", "-c", "user.email=tm@example.com", "add", "."},
			{"-c", "user.name=tm", "-c", "user.email=tm@example.com", "commit", "-q", "-m", "initial"},
		} {
			c := exec.Command("git", append([]string{"-C", dir}, args...)...)
			if out, err := c.CombinedOutput(); err != nil {
				return fmt.Errorf("git %v in %s: %v\n%s", args, r.name, err, out)
			}
		}
		if r.branch != "main" {
			c := exec.Command("git", "-C", dir, "checkout", "-q", "-b", r.branch)
			if out, err := c.CombinedOutput(); err != nil {
				return fmt.Errorf("git checkout -b %s in %s: %v\n%s", r.branch, r.name, err, out)
			}
		}
		if r.working != r.committed {
			if err := os.WriteFile(filepath.Join(dir, r.file), []byte(r.working), 0o644); err != nil {
				return err
			}
		}
	}
	// Something untracked as well, so the changes view shows what every form
	// of `git diff` would have missed.
	if err := os.WriteFile(filepath.Join(root, "api", "notes.md"), []byte("# notes\n\nthings to do\n"), 0o644); err != nil {
		return err
	}

	ratio := 0.5
	first, second := 0, 1
	f := state.File{Version: state.Version, Projects: []state.Project{{
		Name: "api",
		Terminals: []state.Terminal{
			{Command: "./dev"},
			{Command: "./tests"},
		},
		Layout: &state.Layout{
			Direction: state.DirStack, Ratio: ratio,
			First:  &state.Layout{Terminal: &first},
			Second: &state.Layout{Terminal: &second},
		},
	}}}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, state.FileName), data, 0o644)
}

// render draws the emulator's grid as SVG. Cells are emitted as runs sharing
// a style, so a screen of mostly one background costs a handful of rectangles
// rather than three thousand.
func render(emu *vt.Emulator) string {
	var b strings.Builder
	w, h := float64(cols)*cellW, float64(rows)*cellH
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f" font-family="ui-monospace, SFMono-Regular, Menlo, Consolas, monospace" font-size="%.0f">`,
		w, h, w, h, fontSize)
	fmt.Fprintf(&b, "\n<rect width=\"%.0f\" height=\"%.0f\" fill=\"%s\"/>", w, h, terminal.PaneGround)

	for y := 0; y < rows; y++ {
		// Backgrounds first, as runs, then the text over them.
		for x := 0; x < cols; {
			bg := bgOf(emu, x, y)
			run := 1
			for x+run < cols && bgOf(emu, x+run, y) == bg {
				run++
			}
			if bg != terminal.PaneGround {
				fmt.Fprintf(&b, "\n<rect x=\"%.2f\" y=\"%.2f\" width=\"%.2f\" height=\"%.2f\" fill=\"%s\"/>",
					float64(x)*cellW, float64(y)*cellH, float64(run)*cellW, cellH, bg)
			}
			x += run
		}
		for x := 0; x < cols; {
			fg, text, cells := fgOf(emu, x, y), strings.Builder{}, 0
			for x+cells < cols && fgOf(emu, x+cells, y) == fg {
				content, w := " ", 1
				if c := emu.CellAt(x+cells, y); c != nil {
					if c.Content != "" {
						content = c.Content
					}
					w = max(c.Width, 1)
				}
				text.WriteString(content)
				cells += w
			}
			if cells == 0 {
				x++
				continue
			}
			// Trailing blanks carry no ink; dropping them keeps the file small.
			// The cells they occupied come off the run's measured width too, or
			// the rest of the run would be stretched across them.
			trimmed := strings.TrimRight(text.String(), " ")
			if width := cells - (len(text.String()) - len(trimmed)); trimmed != "" && width > 0 {
				// textLength pins the run to the cell grid. Without it the run
				// is laid out at whatever advance the reader's monospace font
				// happens to have, and drifts away from the backgrounds drawn
				// under it — by a whole cell or two before the end of a line.
				fmt.Fprintf(&b, "\n<text x=\"%.2f\" y=\"%.2f\" fill=\"%s\" textLength=\"%.2f\" lengthAdjust=\"spacing\" xml:space=\"preserve\">%s</text>",
					float64(x)*cellW, float64(y)*cellH+baseline, fg, float64(width)*cellW, escape(trimmed))
			}
			x += cells
		}
	}
	b.WriteString("\n</svg>\n")
	return b.String()
}

func bgOf(emu *vt.Emulator, x, y int) string {
	if c := emu.CellAt(x, y); c != nil && c.Style.Bg != nil {
		return hex(c.Style.Bg)
	}
	return terminal.PaneGround
}

func fgOf(emu *vt.Emulator, x, y int) string {
	if c := emu.CellAt(x, y); c != nil && c.Style.Fg != nil {
		return hex(c.Style.Fg)
	}
	return "#c9d1d9" // the emulator's own default: what a program's plain text uses
}

func hex(c color.Color) string {
	r, g, bl, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, bl>>8)
}

func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

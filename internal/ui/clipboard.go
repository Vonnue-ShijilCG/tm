package ui

import (
	"os"
	"os/exec"
	"strings"
)

// OSC 52 alone is not enough: GNOME Terminal (VTE) ignores it without a word,
// so on the most common Linux desktop a copy reached no clipboard at all. tm
// therefore also hands the text to the desktop's own clipboard tool when the
// session can reach the desktop, and keeps sending OSC 52 for everything else —
// above all SSH, where the desktop a tool would reach is the remote machine's.

// localClipboard copies text with the desktop's clipboard tool. A variable so
// tests replace it and never touch the developer's own clipboard.
var localClipboard = desktopClipboard

// desktopClipboard runs the tool clipboardCommand picks, off the event loop:
// xclip and wl-copy stay behind to serve the selection, and nothing waits on
// them. With no tool or no desktop it does nothing; OSC 52 still went out.
func desktopClipboard(text string) {
	argv := clipboardCommand(os.Getenv, exec.LookPath)
	if argv == nil {
		return
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(text)
	// No stdout or stderr: a tool that forks to keep serving the selection
	// would otherwise hold the pipe open and Wait would never return.
	go func() {
		if err := cmd.Start(); err == nil {
			_ = cmd.Wait()
		}
	}()
}

// clipboardCommand is the clipboard tool to run, as argv, or nil. getenv and
// look stand in for os.Getenv and exec.LookPath.
func clipboardCommand(getenv func(string) string, look func(string) (string, error)) []string {
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return nil // the desktop here is not the user's
	}
	var tools [][]string
	if getenv("WAYLAND_DISPLAY") != "" {
		tools = append(tools, []string{"wl-copy"})
	}
	if getenv("DISPLAY") != "" { // X, or XWayland on a Wayland desktop
		tools = append(tools, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	}
	for _, t := range tools {
		if path, err := look(t[0]); err == nil {
			return append([]string{path}, t[1:]...)
		}
	}
	return nil
}

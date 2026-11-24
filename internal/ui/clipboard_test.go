package ui

import (
	"errors"
	"reflect"
	"testing"
)

// GNOME Terminal ignores OSC 52, so a copy reached no clipboard at all. The
// desktop's own clipboard tool gets the text too.
func TestDragAlsoCopiesWithTheDesktopClipboard(t *testing.T) {
	clipboardCatcher(t)
	var got []string
	prev := localClipboard
	localClipboard = func(s string) { got = append(got, s) }
	t.Cleanup(func() { localClipboard = prev })
	m := selectPanes(t, []string{"hello world"}, []string{"x"})

	w := find(t, m, "world")
	m = drag(m, w, [2]int{w[0] + 4, w[1]})

	if !reflect.DeepEqual(got, []string{"world"}) {
		t.Errorf("desktop clipboard got %q, want the selection", got)
	}
}

// The tool is the one the session can reach: wl-copy on Wayland, xclip or
// xsel on X (which also reaches a Wayland desktop through XWayland), nothing
// over SSH — there the desktop is the remote machine's, and OSC 52 is what
// reaches the one in front of the user.
func TestClipboardCommandPicksTheToolTheSessionCanReach(t *testing.T) {
	cases := []struct {
		name  string
		env   map[string]string
		tools []string
		want  []string
	}{
		{"wayland with wl-copy", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, []string{"wl-copy", "xclip"}, []string{"wl-copy"}},
		{"wayland with only xclip", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, []string{"xclip"}, []string{"xclip", "-selection", "clipboard"}},
		{"x with only xsel", map[string]string{"DISPLAY": ":0"}, []string{"xsel"}, []string{"xsel", "--clipboard", "--input"}},
		{"over ssh", map[string]string{"DISPLAY": ":0", "SSH_CONNECTION": "1.2.3.4 5 6.7.8.9 22"}, []string{"xclip"}, nil},
		{"no display", map[string]string{}, []string{"xclip"}, nil},
		{"no tool", map[string]string{"DISPLAY": ":0"}, nil, nil},
	}
	for _, c := range cases {
		look := func(name string) (string, error) {
			for _, tool := range c.tools {
				if tool == name {
					return "/usr/bin/" + name, nil
				}
			}
			return "", errors.New("not found")
		}
		got := clipboardCommand(func(k string) string { return c.env[k] }, look)
		if c.want == nil {
			if got != nil {
				t.Errorf("%s: got %q, want no tool", c.name, got)
			}
			continue
		}
		want := append([]string{"/usr/bin/" + c.want[0]}, c.want[1:]...)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %q, want %q", c.name, got, want)
		}
	}
}

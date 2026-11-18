package ui

// Server mode: the model attached to a tm server that owns the terminals.
//
// Everything about projects, layouts and the state file stays here; the
// server only holds sessions. What a reattach needs to put the screen back
// - the state snapshot plus which running terminals were hidden, which the
// state file does not record - travels to the server as an opaque blob on
// every persist and comes back in the Welcome.

import (
	"encoding/json"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Vonnue-ShijilCG/tm/internal/server"
	"github.com/Vonnue-ShijilCG/tm/internal/state"
	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

var _ workspace.Session = (*server.RemoteSession)(nil)

// Options attach a model to a tm server. The zero value is no server:
// exactly the behaviour of New.
type Options struct {
	Client  *server.Client // nil: terminals run in this process
	Welcome server.Welcome // what Client's Attach returned
	Forget  []string       // refs from server.TakeExited, forgotten after the restore
}

// uiBlob is what the server keeps for the next attach. Selected is the
// project being shown, so the reattached window opens on it.
type uiBlob struct {
	State    state.File
	Hidden   []string // refs of running terminals that had no pane
	Selected string   `json:",omitempty"`
}

// spawnMeta is recorded with each session the server starts, so a session
// the blob does not mention (its write was lost) can still be put back in
// its project.
type spawnMeta struct {
	Project  string
	Terminal workspace.SavedTerminal
}

// serverLink is the model's side of an attached server. It is shared by
// every copy of the Model, which Bubble Tea only ever updates from one
// goroutine.
type serverLink struct {
	client  *server.Client
	live    map[string]server.SessionInfo // running at attach, by ref
	adopted map[string]bool               // refs handed to a terminal: each at most once

	// Until the first window size there is no area to lay panes out in;
	// these wait for it.
	hidden   map[*workspace.Terminal]bool
	selected string
	arranged bool

	gone bool   // the server let this client go; nothing may be written now
	exit string // what to print once the program has ended
}

// serverGoneMsg is the server dropping this client: evicted by another
// window, stopped, or lost.
type serverGoneMsg struct{ reason string }

// remoteSpawner starts sessions on the server. The workspace calls
// spawnFor instead when it is set; this is only the plain fallback.
func remoteSpawner(c *server.Client) workspace.Spawner {
	return func(dir string, cols, rows int) (workspace.Session, error) {
		rs, err := c.Spawn(dir, cols, rows, nil)
		if err != nil {
			return nil, err // never a typed nil in the interface
		}
		return rs, nil
	}
}

func (l *serverLink) spawnFor(p *workspace.Project, def workspace.SavedTerminal, cols, rows int) (workspace.Session, error) {
	meta, err := json.Marshal(spawnMeta{Project: p.Name, Terminal: def})
	if err != nil {
		return nil, err
	}
	dir := p.Path
	if def.Worktree != "" {
		dir = def.Worktree
	}
	rs, err := l.client.Spawn(dir, cols, rows, meta)
	if err != nil {
		return nil, err
	}
	return rs, nil
}

// adopt is the workspace's Adopt: the running session ref names, once.
func (l *serverLink) adopt(ref string) workspace.Session {
	info, ok := l.live[ref]
	if !ok || l.adopted[ref] {
		return nil
	}
	l.adopted[ref] = true
	return l.client.Adopt(info)
}

// attach restores the workspace from the server's blob, or from the state
// file when the server has none yet, and adopts every session still
// running: definitions that name one get it back instead of a new shell,
// sessions that exited while nobody was attached are forgotten, and live
// sessions the restored state does not account for are attached hidden, so
// nothing running is ever out of reach. Panes are laid out on the first
// window size (see arrangeAttached). The error is a state that could not be
// read; the workspace is usable either way.
func (m *Model) attach(o Options) error {
	l := &serverLink{
		client:  o.Client,
		live:    map[string]server.SessionInfo{},
		adopted: map[string]bool{},
		hidden:  map[*workspace.Terminal]bool{},
	}
	m.srv = l
	m.ws.SpawnFor = l.spawnFor

	var hidden []string
	var err error
	fromBlob := false
	if len(o.Welcome.Blob) > 0 {
		b, derr := decodeBlob(o.Welcome.Blob)
		if derr == nil {
			m.ws.RestoreFrom(b.State)
			hidden, l.selected, fromBlob = b.Hidden, b.Selected, true
		} else {
			err = errors.Join(fmt.Errorf("the tm server's copy: %w", derr), m.ws.Restore())
		}
	} else {
		err = m.ws.Restore()
	}

	forget := append([]string(nil), o.Forget...)
	live := map[string]bool{}
	for _, s := range o.Welcome.Sessions {
		if s.Exited {
			forget = append(forget, s.Ref)
			continue
		}
		l.live[s.Ref], live[s.Ref] = s, true
	}
	m.ws.Forget(forget)
	m.ws.Forget(settleRefs(m.ws, live, fromBlob))

	m.ws.Adopt = l.adopt
	cols, rows := m.terminalSize()
	for _, p := range m.ws.Projects {
		for _, s := range p.Saved {
			if live[s.Ref] {
				m.ws.OpenLive(p, live, cols, rows)
				break
			}
		}
	}
	for _, s := range o.Welcome.Sessions { // whatever is left over, in the server's order
		if s.Exited || l.adopted[s.Ref] {
			continue
		}
		p, def := m.orphanHome(s)
		if p == nil {
			continue // its project is not in this workspace any more
		}
		l.adopted[s.Ref] = true
		l.hidden[m.ws.AttachRunning(p, def, o.Client.Adopt(s))] = true
	}

	byRef := map[string]*workspace.Terminal{}
	for _, p := range m.ws.Projects {
		for _, t := range p.Terminals {
			byRef[terminalRef(t)] = t
		}
	}
	for _, ref := range hidden {
		if t := byRef[ref]; t != nil {
			l.hidden[t] = true
		}
	}
	for _, s := range o.Welcome.Sessions {
		if t := byRef[s.Ref]; t != nil && s.Changed {
			m.unseen[t] = true
		}
	}
	return err
}

// decodeBlob reads the blob through the state package, so its refs and
// layouts are sanitised exactly as the state file's are.
func decodeBlob(b []byte) (uiBlob, error) {
	var raw struct {
		State    json.RawMessage
		Hidden   []string
		Selected string
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return uiBlob{}, err
	}
	f, err := state.Decode(raw.State)
	if err != nil {
		return uiBlob{}, err
	}
	return uiBlob{State: f, Hidden: raw.Hidden, Selected: raw.Selected}, nil
}

// settleRefs leaves a ref only on a definition whose session is running,
// and on the first such definition only: a session runs one terminal, and a
// hand-edited file naming it twice must not make the second a copy of the
// first. A ref naming no running session means one of two things. In the
// server's own blob it is a session that ended - its Exit went to a client
// that was leaving - and the returned refs are to be forgotten like any
// other that exited while nobody was looking. From the state file, with a
// server that has no blob yet, it is a session of an earlier server, gone
// with it (a reboot): the definition stays, naming no session.
func settleRefs(ws *workspace.Workspace, live map[string]bool, fromBlob bool) (ended []string) {
	seen := map[string]bool{}
	for _, p := range ws.Projects {
		for i, s := range p.Saved {
			switch {
			case s.Ref == "":
			case !live[s.Ref] && fromBlob:
				ended = append(ended, s.Ref)
			case !live[s.Ref] || seen[s.Ref]:
				p.Saved[i].Ref = ""
			default:
				seen[s.Ref] = true
			}
		}
	}
	return ended
}

// orphanHome finds where a live session that nothing adopted belongs: the
// saved definition naming it, which OpenLive could not open (its worktree
// has gone from git), or else the project its spawn recorded. It returns a
// nil project when neither says.
func (m *Model) orphanHome(s server.SessionInfo) (*workspace.Project, workspace.SavedTerminal) {
	for _, p := range m.ws.Projects {
		for _, def := range p.Saved {
			if def.Ref == s.Ref {
				return p, def
			}
		}
	}
	var meta spawnMeta
	if json.Unmarshal(s.Meta, &meta) != nil {
		return nil, workspace.SavedTerminal{}
	}
	for _, p := range m.ws.Projects {
		if p.Name == meta.Project {
			def := meta.Terminal
			def.Ref = s.Ref
			return p, def
		}
	}
	return nil, workspace.SavedTerminal{}
}

func terminalRef(t *workspace.Terminal) string {
	if r, ok := t.Session.(interface{ Ref() string }); ok {
		return r.Ref()
	}
	return ""
}

// arrangeAttached lays out the adopted terminals once there is a window to
// lay them out in: the saved arrangement rebuilt from the visible ones,
// anything it does not cover placed as usual, the saved focus restored, the
// project that was shown shown again. The caller resizes the panes
// straight after, before any frame is drawn, so no screen sized for the
// previous window lands in a smaller card.
func (m *Model) arrangeAttached() {
	l := m.srv
	if l == nil || l.arranged {
		return
	}
	l.arranged = true
	first := -1
	for i, p := range m.ws.Projects {
		var visible []*workspace.Terminal
		for _, t := range p.Terminals {
			if !l.hidden[t] {
				visible = append(visible, t)
			}
		}
		if len(visible) == 0 {
			continue
		}
		m.layouts[p] = paneFromLayout(p.Layout, visible, map[*workspace.Terminal]bool{})
		for _, t := range visible {
			if !m.layouts[p].contains(t) {
				m.placeBalanced(p, t)
			}
		}
		m.enforceMinimums(p)
		focused := m.visibleActive(p)
		for _, t := range p.Terminals {
			if p.Focus != "" && t.Key() == p.Focus && m.layouts[p].contains(t) {
				focused = t
				break
			}
		}
		if focused != nil {
			selectTerminal(p, focused)
		}
		if first < 0 {
			first = i
		}
	}
	m.selected = first // the first project with something to show, unless the blob names one
	for i, p := range m.ws.Projects {
		if l.selected != "" && p.Name == l.selected {
			m.selected = i
		}
	}
	if m.selected >= 0 {
		m.cursor = m.selected
	}
	if _, t := m.activeTerminal(); t != nil && m.layouts[m.selectedProject()] != nil {
		m.focus = focusMain
	}
	l.hidden = nil
	m.persist()
}

// sendBlob hands the server what the next attach needs. Called by persist.
func (m *Model) sendBlob() {
	if m.srv == nil {
		return
	}
	b := uiBlob{State: m.ws.Snapshot()}
	if p := m.selectedProject(); p != nil {
		b.Selected = p.Name
	}
	for _, p := range m.ws.Projects {
		for _, t := range p.Terminals {
			if !m.layouts[p].contains(t) {
				b.Hidden = append(b.Hidden, terminalRef(t))
			}
		}
	}
	if data, err := json.Marshal(b); err == nil {
		m.srv.client.SetBlob(data)
	}
}

// released reports whether the server has let this client go. From then on
// the new owner writes the state, and this window must not.
func (m Model) released() bool { return m.srv != nil && m.srv.gone }

// lostWith reports whether an exit is really the connection to the server
// ending, which says nothing about the process; it marks the client
// released, since Done can close before Gone is read.
func (m Model) lostWith(err error) bool {
	if m.srv == nil || !errors.Is(err, server.ErrGone) {
		return false
	}
	m.srv.gone = true
	return true
}

// serverInit waits for the server to let go and watches every adopted
// terminal; nil without a server.
func (m Model) serverInit() tea.Cmd {
	if m.srv == nil {
		return nil
	}
	c := m.srv.client
	cmds := []tea.Cmd{func() tea.Msg {
		if reason, ok := <-c.Gone(); ok {
			return serverGoneMsg{reason: reason}
		}
		return nil
	}}
	for _, p := range m.ws.Projects {
		for _, t := range p.Terminals {
			cmds = append(cmds, watchTerminal(t))
		}
	}
	return tea.Batch(cmds...)
}

// serverGone ends the program with the reason, writing nothing.
func (m Model) serverGone(msg serverGoneMsg) (tea.Model, tea.Cmd) {
	m.srv.gone, m.srv.exit, m.quitting = true, "tm: "+msg.reason, true
	return m, tea.Quit
}

// leave is q with a server: the state is saved and the terminals keep
// running for the next attach.
func (m Model) leave() (tea.Model, tea.Cmd) {
	m.quitting = true
	m.persist()
	m.srv.client.Close()
	return m, tea.Quit
}

// stopAll is Q: every terminal ends and so does the server.
func (m Model) stopAll() (tea.Model, tea.Cmd) {
	m.quitting = true
	m.persist()
	m.ws.CloseAll()
	m.srv.client.Stop()
	return m, tea.Quit
}

// ExitMessage is what to print after the program ends: why the server let
// this window go, or "" when it was the user's own choice.
func (m Model) ExitMessage() string {
	if m.srv == nil {
		return ""
	}
	return m.srv.exit
}

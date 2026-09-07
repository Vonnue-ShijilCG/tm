package workspace

import "testing"

func TestCreatingATerminalWithACommandRunsIt(t *testing.T) {
	ws, spawned := newTestWorkspace()
	p := ws.Projects[0]

	if _, err := ws.CreateTerminal(p, Options{Command: "npm run dev"}, 80, 24); err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}

	s := (*spawned)[0]
	if len(s.text) != 1 || s.text[0] != "npm run dev\n" {
		t.Errorf("session received %q, want [\"npm run dev\\n\"]", s.text)
	}
}

func TestATerminalWithoutACommandIsSentNothing(t *testing.T) {
	ws, spawned := newTestWorkspace()

	if _, err := ws.NewTerminal(ws.Projects[0], 80, 24); err != nil {
		t.Fatalf("NewTerminal: %v", err)
	}

	if got := (*spawned)[0].text; len(got) != 0 {
		t.Errorf("session received %q, want nothing", got)
	}
}

func TestReopeningASavedDefinitionRunsItsCommandAgain(t *testing.T) {
	ws, spawned := newTestWorkspace()
	p := ws.Projects[0]

	if _, err := ws.OpenSaved(p, SavedTerminal{Command: "go test ./..."}, 80, 24); err != nil {
		t.Fatalf("OpenSaved: %v", err)
	}

	s := (*spawned)[0]
	if len(s.text) != 1 || s.text[0] != "go test ./...\n" {
		t.Errorf("session received %q, want the saved command", s.text)
	}
}

func TestDefinitionsAreDistinguishedByCommand(t *testing.T) {
	p := &Project{}
	p.remember(SavedTerminal{Command: "npm run dev"})
	p.remember(SavedTerminal{Command: "go test ./..."})

	p.forget("", "npm run dev", "")

	if len(p.Saved) != 1 || p.Saved[0].Command != "go test ./..." {
		t.Fatalf("saved = %+v, want only the go test definition", p.Saved)
	}
	if a, b := (SavedTerminal{Command: "a"}).key(), (SavedTerminal{Command: "b"}).key(); a == b {
		t.Error("definitions differing only by command must have different keys")
	}
}

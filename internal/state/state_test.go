package state

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sample() File {
	return File{Projects: []Project{
		{Name: "app", Terminals: []Terminal{
			{Name: "Plain"},
			{Name: "Login", Branch: "feature/login", Worktree: ".terminal-worktrees/app/login"},
		}},
		{Name: "other", Terminals: nil},
	}}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, sample()); err != nil {
		t.Fatal(err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	want := sample()
	want.Version = Version
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load() = %+v\nwant %+v", got, want)
	}
	data, _ := os.ReadFile(filepath.Join(root, FileName))
	for _, forbidden := range []string{"PATH=", "TOKEN", root} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("state file must not contain %q:\n%s", forbidden, data)
		}
	}
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Errorf("temp files should be cleaned up, dir has %d entries", len(entries))
	}
}

func TestLoadMissingAndEmptyFile(t *testing.T) {
	root := t.TempDir()
	f, err := Load(root)
	if err != nil || len(f.Projects) != 0 || f.Version != Version {
		t.Fatalf("missing file: got %+v, %v", f, err)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), []byte("  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err = Load(root)
	if err != nil || len(f.Projects) != 0 {
		t.Fatalf("empty file: got %+v, %v", f, err)
	}
}

func TestLoadCorruptFileReturnsErrorAndEmptyState(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, FileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(root)
	if err == nil {
		t.Fatal("corrupt file should report an error")
	}
	if len(f.Projects) != 0 {
		t.Errorf("corrupt file should yield empty state, got %+v", f)
	}
}

func TestLoadIgnoresUnknownFieldsAndInvalidEntries(t *testing.T) {
	root := t.TempDir()
	raw := `{
	  "version": 7, "future": {"x": 1},
	  "projects": [
	    {"name": "app", "color": "red", "terminals": [
	      {"name": "Ok", "worktree": ".terminal-worktrees/app/ok", "pid": 123},
	      {"name": "Abs", "worktree": "/etc"},
	      {"name": "Escape", "worktree": "../../etc"},
	      {"name": "Dup", "worktree": ".terminal-worktrees/app/dup"},
	      {"name": "Dup", "worktree": ".terminal-worktrees/app/dup"},
	      {"name": "Pair", "command": "claude", "worktree": ".terminal-worktrees/app/pair"},
	      {"name": "Pair", "command": "npm run dev", "worktree": ".terminal-worktrees/app/pair"},
	      {}, {}
	    ]},
	    {"name": "../evil", "terminals": [{"name": "x"}]},
	    {"name": "a/b", "terminals": []}
	  ]
	}`
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != 7 {
		t.Errorf("version should be preserved, got %d", f.Version)
	}
	if len(f.Projects) != 1 || f.Projects[0].Name != "app" {
		t.Fatalf("projects = %+v, want only app", f.Projects)
	}
	got := f.Projects[0].Terminals
	want := []Terminal{
		{Name: "Ok", Worktree: ".terminal-worktrees/app/ok"},
		{Name: "Dup", Worktree: ".terminal-worktrees/app/dup"}, // an exact duplicate still collapses to one
		{Name: "Pair", Command: "claude", Worktree: ".terminal-worktrees/app/pair"},
		{Name: "Pair", Command: "npm run dev", Worktree: ".terminal-worktrees/app/pair"}, // different command: both kept, a worktree may hold several terminals
		{}, {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("terminals = %+v, want %+v", got, want)
	}
}

func TestSaveFailureKeepsExistingFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks do not apply to root")
	}
	root := t.TempDir()
	if err := Save(root, sample()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(root, FileName))
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })
	if err := Save(root, File{}); err == nil {
		t.Fatal("Save into a read-only directory should fail")
	}
	_ = os.Chmod(root, 0o700)
	after, _ := os.ReadFile(filepath.Join(root, FileName))
	if string(before) != string(after) {
		t.Error("a failed save must leave the previous file untouched")
	}
}

func TestValidators(t *testing.T) {
	for name, ok := range map[string]bool{"app": true, "my-app.v2": true, "": false, ".": false, "..": false, "a/b": false, `a\b`: false} {
		if ValidName(name) != ok {
			t.Errorf("ValidName(%q) = %v, want %v", name, !ok, ok)
		}
	}
	for p, ok := range map[string]bool{".terminal-worktrees/app/x": true, "x": true, "": false, "/abs": false, "../x": false, "a/../../x": false} {
		if ValidRelPath(p) != ok {
			t.Errorf("ValidRelPath(%q) = %v, want %v", p, !ok, ok)
		}
	}
}

func TestTerminalCommandSurvivesARoundTrip(t *testing.T) {
	root := t.TempDir()
	in := File{Version: Version, Projects: []Project{{
		Name:      "api",
		Terminals: []Terminal{{Command: "npm run dev"}, {Name: "shell"}},
	}}}
	if err := Save(root, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := out.Projects[0].Terminals[0].Command; got != "npm run dev" {
		t.Errorf("command = %q, want %q", got, "npm run dev")
	}
	if got := out.Projects[0].Terminals[1].Command; got != "" {
		t.Errorf("a terminal with no command loaded %q, want empty", got)
	}
}

func TestLoadStripsNewlinesFromCommand(t *testing.T) {
	root := t.TempDir()
	body := `{"version":3,"projects":[{"name":"api","terminals":[{"command":"npm run dev\nrm -rf build"}]}]}`
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := out.Projects[0].Terminals[0].Command
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("command = %q, should have no newlines", got)
	}
	if strings.Contains(got, "devrm") {
		t.Errorf("command = %q, must not silently weld the two lines together", got)
	}
	if got != "npm run dev rm -rf build" {
		t.Errorf("command = %q, want %q", got, "npm run dev rm -rf build")
	}
}

func TestVersion2FileLoadsWithNoCommand(t *testing.T) {
	root := t.TempDir()
	body := `{"version":2,"projects":[{"name":"api","terminals":[{"name":"old"}]}]}`
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := out.Projects[0].Terminals[0].Command; got != "" {
		t.Errorf("command = %q, want empty for a v2 file", got)
	}
}

package workspace

import (
	"reflect"
	"testing"

	"github.com/Vonnue-ShijilCG/tm/internal/project"
)

func names(ps []*Project) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

// A repository cloned while tm runs joins the sidebar in name order, and the
// projects already there keep their identity: the UI holds pointers to them.
func TestRediscoverAddsNewProjectsInNameOrder(t *testing.T) {
	ws := New("/ws", []project.Project{{Name: "b", Path: "/ws/b"}, {Name: "d", Path: "/ws/d"}}, nil)
	b := ws.Projects[0]

	added, removed := ws.Rediscover([]project.Project{
		{Name: "a", Path: "/ws/a"}, {Name: "b", Path: "/ws/b"}, {Name: "c", Path: "/ws/c"}, {Name: "d", Path: "/ws/d"},
	})

	if !reflect.DeepEqual(added, []string{"a", "c"}) || removed != nil {
		t.Errorf("added %v removed %v, want [a c] and nothing", added, removed)
	}
	if got := names(ws.Projects); !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("projects = %v, want name order", got)
	}
	if ws.Projects[1] != b {
		t.Error("an existing project must be kept, not replaced")
	}
	if ws.Projects[0].ActiveTerminal() != nil || ws.Projects[0].ActiveIndex() != -1 {
		t.Error("a new project starts with no terminals")
	}
}

// A rescan may drop a project whose directory is gone, but never one with a
// terminal running or saved in it: forgetting a terminal is not its job.
func TestRediscoverDropsOnlyEmptyProjectsThatAreGone(t *testing.T) {
	ws, _ := newTestWorkspace()
	ws.Projects = append(ws.Projects, &Project{Project: project.Project{Name: "mobile", Path: "/ws/mobile"}, active: -1})
	running, saved, empty := ws.Projects[0], ws.Projects[1], ws.Projects[2]
	if _, err := ws.NewTerminal(running, 80, 24); err != nil {
		t.Fatal(err)
	}
	saved.Saved = []SavedTerminal{{Command: "npm test"}}

	_, removed := ws.Rediscover(nil) // every directory gone

	if !reflect.DeepEqual(removed, []string{empty.Name}) {
		t.Errorf("removed %v, want only %q", removed, empty.Name)
	}
	if got := names(ws.Projects); !reflect.DeepEqual(got, []string{running.Name, saved.Name}) {
		t.Errorf("projects = %v, want the running and the saved one kept", got)
	}
}

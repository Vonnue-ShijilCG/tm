package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func idx(i int) *int { return &i }

func nested() *Layout {
	return &Layout{Direction: DirSide, Ratio: 0.6, First: &Layout{Terminal: idx(0)},
		Second: &Layout{Direction: DirStack, Ratio: 0.3, First: &Layout{Terminal: idx(1)}, Second: &Layout{Terminal: idx(2)}}}
}

func TestLayoutRoundTrip(t *testing.T) {
	root := t.TempDir()
	f := File{Projects: []Project{{Name: "app", Terminals: []Terminal{{Name: "A"}, {Name: "B", Branch: "b", Worktree: ".terminal-worktrees/app/b"}, {}},
		Layout: nested(), Focused: idx(1)}}}
	if err := Save(root, f); err != nil {
		t.Fatal(err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != Version || len(got.Projects) != 1 {
		t.Fatalf("got %+v", got)
	}
	if !reflect.DeepEqual(got.Projects[0].Layout, nested()) {
		t.Errorf("layout = %s\nwant %s", dump(got.Projects[0].Layout), dump(nested()))
	}
	if got.Projects[0].Focused == nil || *got.Projects[0].Focused != 1 {
		t.Errorf("focused = %v, want 1", got.Projects[0].Focused)
	}
}

func TestVersion1FileWithoutLayoutStillLoads(t *testing.T) {
	root := t.TempDir()
	raw := `{"version":1,"projects":[{"name":"app","terminals":[{"name":"Plain"},{"name":"L","branch":"b","worktree":".terminal-worktrees/app/l"}]}]}`
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != 1 || len(f.Projects) != 1 || len(f.Projects[0].Terminals) != 2 || f.Projects[0].Layout != nil || f.Projects[0].Focused != nil {
		t.Errorf("old file should load with no layout: %+v", f)
	}
	if err := Save(root, f); err != nil { // re-saving upgrades the version, nothing else changes
		t.Fatal(err)
	}
	f2, _ := Load(root)
	if f2.Version != Version || !reflect.DeepEqual(f2.Projects[0].Terminals, f.Projects[0].Terminals) {
		t.Errorf("upgraded file = %+v", f2)
	}
}

func TestLayoutSanitisation(t *testing.T) {
	cases := map[string]struct {
		in   *Layout
		want *Layout
	}{
		"bad direction dropped":             {&Layout{Direction: "diagonal", First: &Layout{Terminal: idx(0)}, Second: &Layout{Terminal: idx(1)}}, nil},
		"out of range leaf collapses split": {&Layout{Direction: DirSide, Ratio: 0.5, First: &Layout{Terminal: idx(0)}, Second: &Layout{Terminal: idx(9)}}, &Layout{Terminal: idx(0)}},
		"duplicate leaf collapses":          {&Layout{Direction: DirSide, Ratio: 0.5, First: &Layout{Terminal: idx(1)}, Second: &Layout{Terminal: idx(1)}}, &Layout{Terminal: idx(1)}},
		"ratio clamped to default":          {&Layout{Direction: DirStack, Ratio: 7, First: &Layout{Terminal: idx(0)}, Second: &Layout{Terminal: idx(1)}}, &Layout{Direction: DirStack, Ratio: 0.5, First: &Layout{Terminal: idx(0)}, Second: &Layout{Terminal: idx(1)}}},
		"negative leaf dropped":             {&Layout{Terminal: idx(-1)}, nil},
		"empty node dropped":                {&Layout{}, nil},
		"nil":                               {nil, nil},
	}
	remap := map[int]int{0: 0, 1: 1, 2: 2}
	for name, tc := range cases {
		got := sanitizeLayout(tc.in, remap, map[int]bool{}, 0)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %s, want %s", name, dump(got), dump(tc.want))
		}
	}

	// Depth is bounded.
	deep := &Layout{Terminal: idx(0)}
	for i := 0; i < MaxLayoutDepth+5; i++ {
		deep = &Layout{Direction: DirSide, Ratio: 0.5, First: deep, Second: &Layout{Terminal: idx(1)}}
	}
	if got := sanitizeLayout(deep, remap, map[int]bool{}, 0); got == nil {
		t.Error("deep tree should degrade, not vanish entirely")
	} else if got.Terminal != nil && *got.Terminal == 0 {
		t.Error("the deepest leaf should have been cut")
	}
}

func TestLayoutIndexesRemapWhenTerminalsAreFiltered(t *testing.T) {
	root := t.TempDir()
	// Terminal 1 has an absolute worktree path and is dropped on load; the
	// layout referencing 0 and 2 must be remapped to 0 and 1, the leaf for 1 removed.
	raw := `{"version":2,"projects":[{"name":"app","terminals":[{"name":"A"},{"name":"BAD","worktree":"/etc"},{"name":"C"}],
	  "layout":{"direction":"side","ratio":0.4,"first":{"terminal":0},"second":{"direction":"stack","ratio":0.5,"first":{"terminal":1},"second":{"terminal":2}}},
	  "focused":2}]}`
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	p := f.Projects[0]
	want := &Layout{Direction: DirSide, Ratio: 0.4, First: &Layout{Terminal: idx(0)}, Second: &Layout{Terminal: idx(1)}}
	if !reflect.DeepEqual(p.Layout, want) {
		t.Errorf("layout = %s, want %s", dump(p.Layout), dump(want))
	}
	if p.Focused == nil || *p.Focused != 1 {
		t.Errorf("focused = %v, want 1 (remapped from 2)", p.Focused)
	}
}

func TestMalformedLayoutIsIgnoredNotFatal(t *testing.T) {
	root := t.TempDir()
	for _, raw := range []string{
		`{"version":2,"projects":[{"name":"app","terminals":[{"name":"A"}],"layout":5}]}`,
		`{"version":2,"projects":[{"name":"app","terminals":[{"name":"A"}],"layout":{"direction":["x"],"first":"nope"}}]}`,
		`{"version":2,"projects":[{"name":"app","terminals":[{"name":"A"}],"layout":{"terminal":"zero"},"focused":"A"}]}`,
	} {
		if err := os.WriteFile(filepath.Join(root, FileName), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		f, err := Load(root)
		if raw == `{"version":2,"projects":[{"name":"app","terminals":[{"name":"A"}],"layout":{"terminal":"zero"},"focused":"A"}]}` {
			// "focused" of the wrong type is a plain field: json reports it, and tm falls back to empty state.
			if err == nil {
				t.Error("type error on a plain field should be reported")
			}
			continue
		}
		if err != nil {
			t.Errorf("malformed layout must not fail the load: %v (%s)", err, raw)
			continue
		}
		if len(f.Projects) != 1 || len(f.Projects[0].Terminals) != 1 || f.Projects[0].Layout != nil {
			t.Errorf("terminals should survive and the bad layout be dropped: %+v", f.Projects)
		}
	}
}

func dump(l *Layout) string { b, _ := json.Marshal(l); return string(b) }

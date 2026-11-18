package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMissingFileGivesDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope", "config.toml"))
	if err != nil || c.MaxTerminals != 6 {
		t.Fatalf("missing file: %+v, %v", c, err)
	}
}

func TestMissingKeyGivesDefault(t *testing.T) {
	c, err := Load(write(t, "# just a comment\nother = 3\n"))
	if err != nil || c.MaxTerminals != 6 {
		t.Fatalf("missing key: %+v, %v", c, err)
	}
}

func TestCustomValue(t *testing.T) {
	for _, content := range []string{
		"max_terminals = 10\n",
		"max_terminals=10",
		"  max_terminals   =   10   # six is not enough\n",
		"# Maximum number of terminals allowed per project.\n# Lowering this value does not delete existing terminals.\nmax_terminals = 10\n",
		"[ignored]\nmax_terminals = 10\n",
		"name = \"x # not a comment\"\nmax_terminals = 10\nflag = true\n",
	} {
		c, err := Load(write(t, content))
		if err != nil || c.MaxTerminals != 10 {
			t.Errorf("%q: got %+v, %v", content, c, err)
		}
	}
}

func TestInvalidValuesFallBackAndReport(t *testing.T) {
	for _, content := range []string{
		"max_terminals = 0\n",
		"max_terminals = -3\n",
		"max_terminals = many\n",
		"max_terminals = 6.5\n",
		"max_terminals = \"6\"\n",
		"max_terminals =\n",
		"max_terminals\n",
	} {
		path := write(t, content)
		c, err := Load(path)
		if c.MaxTerminals != 6 {
			t.Errorf("%q: max = %d, want fallback 6", content, c.MaxTerminals)
		}
		if err == nil {
			t.Errorf("%q: expected an error describing the problem", content)
			continue
		}
		if !strings.Contains(err.Error(), "config.toml:1") {
			t.Errorf("%q: error should name the file and line: %v", content, err)
		}
	}
}

func TestValidValueSurvivesOtherBrokenLines(t *testing.T) {
	c, err := Load(write(t, "garbage line\nmax_terminals = 8\n"))
	if c.MaxTerminals != 8 {
		t.Errorf("valid setting should be kept, got %d", c.MaxTerminals)
	}
	if err == nil || !strings.Contains(err.Error(), "config.toml:1") {
		t.Errorf("malformed line should be reported: %v", err)
	}
}

func TestPathHonoursXDGAndFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := Path(); got != "/xdg/tm/config.toml" {
		t.Errorf("XDG path = %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/someone")
	if got := Path(); got != "/home/someone/.config/tm/config.toml" {
		t.Errorf("home path = %q", got)
	}
	if got := Display(Path()); got != "~/.config/tm/config.toml" {
		t.Errorf("display = %q", got)
	}
	if got := Display("/etc/tm.toml"); got != "/etc/tm.toml" {
		t.Errorf("display outside home = %q", got)
	}
}

func TestDefaultPathIsReadable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "tm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("max_terminals = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(Path())
	if err != nil || c.MaxTerminals != 3 {
		t.Fatalf("default path: %+v, %v", c, err)
	}
}

func TestServerFlagParses(t *testing.T) {
	for content, want := range map[string]bool{
		"server = true\n":                  true,
		"server=true # keep terminals\n":   true,
		"server = false\n":                 false,
		"max_terminals = 4\n":              false,
		"max_terminals = 4\nserver = true": true,
	} {
		c, err := Load(write(t, content))
		if err != nil || c.Server != want {
			t.Errorf("%q: got %+v, %v; want Server = %v", content, c, err, want)
		}
	}
	if Default().Server {
		t.Error("Default() should leave the server off")
	}
}

func TestInvalidServerFlagFallsBackAndReports(t *testing.T) {
	for _, content := range []string{"server = yes\n", "server = 1\n", "server = \"true\"\n", "server = True\n", "server =\n"} {
		c, err := Load(write(t, content))
		if c.Server {
			t.Errorf("%q: Server = true, want fallback false", content)
		}
		if err == nil {
			t.Errorf("%q: expected an error describing the problem", content)
			continue
		}
		if !strings.Contains(err.Error(), "server") || !strings.Contains(err.Error(), "config.toml:1") {
			t.Errorf("%q: error should name the key, file and line: %v", content, err)
		}
	}
}

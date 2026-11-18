// Package config reads tm's user configuration: a small TOML-style file at
// $XDG_CONFIG_HOME/tm/config.toml (~/.config/tm/config.toml by default).
//
// Only the subset of TOML needed for a flat list of settings is understood:
// blank lines, # comments, and key = value lines with integer, string or
// boolean values. Unknown keys and [tables] are ignored so the file can grow
// without breaking older versions. Invalid values never stop tm: the
// default is used and the problem is reported.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultMaxTerminals is the per-project terminal limit when the file or the
// setting is missing or invalid.
const DefaultMaxTerminals = 6

// Config holds every setting with defaults already applied.
type Config struct {
	MaxTerminals int  // max_terminals: running terminals allowed per project
	Server       bool // server: run terminals in a background server that outlives the window
}

// Default is the configuration used when nothing is configured.
func Default() Config { return Config{MaxTerminals: DefaultMaxTerminals} }

// Path returns the configuration file location:
// $XDG_CONFIG_HOME/tm/config.toml, or ~/.config/tm/config.toml.
func Path() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "tm", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "tm", "config.toml")
}

// Display shortens path for messages, replacing the home directory with ~.
func Display(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return path
}

// Load reads the file at path. A missing file is not an error. Anything the
// file gets wrong is returned as an error that says what is wrong first and
// names the file and line last (status bars are narrow), and the returned
// Config still carries usable values: defaults for whatever was invalid, so
// callers can always proceed.
func Load(path string) (Config, error) {
	c := Default()
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
		return c, fmt.Errorf("%s: %w", Display(path), err)
	}
	defer f.Close()

	var problems []string
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") { // tables are not used; skip the header
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			problems = append(problems, fmt.Sprintf("expected key = value (%s:%d)", Display(path), n))
			continue
		}
		key, value = strings.TrimSpace(key), stripComment(strings.TrimSpace(value))
		switch key {
		case "max_terminals":
			v, err := strconv.Atoi(value)
			if err != nil || v < 1 {
				problems = append(problems, fmt.Sprintf("max_terminals %q is not a positive integer; using %d (%s:%d)", value, DefaultMaxTerminals, Display(path), n))
				continue
			}
			c.MaxTerminals = v
		case "server":
			switch value {
			case "true":
				c.Server = true
			case "false":
				c.Server = false
			default:
				problems = append(problems, fmt.Sprintf("server %q is not true or false; using false (%s:%d)", value, Display(path), n))
				c.Server = false
			}
		}
	}
	if err := sc.Err(); err != nil {
		problems = append(problems, fmt.Sprintf("%s: %v", Display(path), err))
	}
	if len(problems) > 0 {
		return c, errors.New(strings.Join(problems, "; "))
	}
	return c, nil
}

// stripComment drops a trailing # comment from an unquoted value.
func stripComment(v string) string {
	if strings.HasPrefix(v, `"`) || strings.HasPrefix(v, `'`) {
		return v
	}
	if i := strings.Index(v, "#"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Paths are the files that belong to one workspace's server. They share a
// stem derived from the workspace root, so every tm started in the same
// workspace finds the same server.
type Paths struct {
	Dir    string // private to the user, mode 0700
	Socket string // the server listens here
	Lock   string // flock'd while a client checks for, or starts, the server
	Exited string // refs of sessions that ended with no client attached
	Log    string // the server's stdout and stderr
}

// maxSocket is the longest path a Unix socket can be bound to: sun_path is
// 108 bytes on Linux, including the terminating NUL.
const maxSocket = 107

// fallbackBase is where the short directory goes when the state directory is
// too deep for a socket path. A variable so a test can point it elsewhere.
var fallbackBase = "/tmp"

// PathsFor returns the server files for the workspace at root, creating
// their directory. The stem is the first 16 hex characters of the sha256 of
// the absolute, symlink-resolved root.
//
// The directory is $XDG_STATE_HOME/tm (default ~/.local/state/tm). It is
// never $XDG_RUNTIME_DIR: logind deletes that on the user's last logout,
// which is exactly when a server is meant to keep running. When the socket
// path there would not fit in sun_path, all four files move to
// /tmp/tm-<uid>, which must be a real directory owned by this user with mode
// 0700.
func PathsFor(root string) (Paths, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Paths{}, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Paths{}, err
	}
	sum := sha256.Sum256([]byte(resolved))
	stem := hex.EncodeToString(sum[:])[:16]

	dir, err := stateDir()
	if err != nil {
		return Paths{}, err
	}
	if len(filepath.Join(dir, stem+".sock")) > maxSocket {
		dir = filepath.Join(fallbackBase, fmt.Sprintf("tm-%d", os.Getuid()))
		if err := privateFallback(dir); err != nil {
			return Paths{}, err
		}
	} else if err := privateStateDir(dir); err != nil {
		return Paths{}, err
	}

	return Paths{
		Dir:    dir,
		Socket: filepath.Join(dir, stem+".sock"),
		Lock:   filepath.Join(dir, stem+".lock"),
		Exited: filepath.Join(dir, stem+".exited"),
		Log:    filepath.Join(dir, stem+".log"),
	}, nil
}

// stateDir is $XDG_STATE_HOME/tm, or ~/.local/state/tm when that is unset or
// not absolute (the XDG spec says to ignore a relative value).
func stateDir() (string, error) {
	if s := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(s) {
		return filepath.Join(s, "tm"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding the tm state directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "tm"), nil
}

// privateStateDir creates dir and makes it 0700. It lives under the user's
// home, so it is the user's to set.
func privateStateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// privateFallback creates dir in a world-writable directory and then checks,
// without following a symlink, that it is a directory this user owns with
// mode 0700. Anyone could have made it first; tm refuses rather than put its
// socket where another user can reach it.
func privateFallback(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(st.Uid) != os.Getuid() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("%s must be a directory owned by you with mode 0700", dir)
	}
	return nil
}

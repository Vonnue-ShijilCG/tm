// Package git wraps the handful of Git CLI operations tm needs: repository
// detection, branch listing and worktree management. It shells out to the
// git binary and never imports UI code.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrNotRepository is returned by Open for directories outside any Git repo.
var ErrNotRepository = errors.New("not a git repository")

// Error is a failed git command with the message git printed.
type Error struct {
	Args   []string // git arguments, without the leading "-C dir"
	Stderr string   // trimmed stderr, usually a single "fatal: ..." line
}

func (e *Error) Error() string {
	msg := e.Stderr
	// git prints progress lines before the actual failure; keep the failure.
	if i := strings.LastIndex(msg, "fatal: "); i >= 0 {
		msg = msg[i+len("fatal: "):]
	} else if i := strings.LastIndex(msg, "error: "); i >= 0 {
		msg = msg[i+len("error: "):]
	}
	// Name the command ("worktree add") but not its arguments, which can be
	// long paths the message itself usually repeats.
	cmd := e.Args[:0]
	for _, a := range e.Args {
		if strings.HasPrefix(a, "-") || strings.ContainsAny(a, "/.") || len(cmd) == 2 {
			break
		}
		cmd = append(cmd, a)
	}
	return fmt.Sprintf("git %s: %s", strings.Join(cmd, " "), msg)
}

// Worktree is one checkout of a repository.
type Worktree struct {
	Path   string // absolute path of the checkout
	Branch string // short branch name, "" when detached
	IsMain bool   // the primary checkout rather than a linked worktree
}

// Repo is an opened repository. Dir is the top-level directory.
type Repo struct {
	Dir string
}

// IsRepository reports whether dir is inside a Git repository.
func IsRepository(dir string) bool {
	_, err := Open(dir)
	return err == nil
}

// Open locates the repository containing dir.
func Open(dir string) (*Repo, error) {
	out, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		var gitErr *Error
		if errors.As(err, &gitErr) && (strings.Contains(gitErr.Stderr, "not a git repository") || strings.Contains(gitErr.Stderr, "cannot change to")) {
			return nil, fmt.Errorf("%w: %s", ErrNotRepository, dir) // includes directories that do not exist
		}
		return nil, err
	}
	return &Repo{Dir: out}, nil
}

// CurrentBranch returns the checked-out branch, or "" when HEAD is detached.
func (r *Repo) CurrentBranch() (string, error) {
	return run(r.Dir, "branch", "--show-current")
}

// Branches lists local branch names.
func (r *Repo) Branches() ([]string, error) {
	out, err := run(r.Dir, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// HasBranch reports whether a local branch with that name exists.
func (r *Repo) HasBranch(name string) bool {
	_, err := run(r.Dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// Worktrees lists all checkouts of the repository, the main one first.
func (r *Repo) Worktrees() ([]Worktree, error) {
	out, err := run(r.Dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []Worktree
	for _, block := range strings.Split(out, "\n\n") {
		var wt Worktree
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				wt.Path = strings.TrimPrefix(line, "worktree ")
			case strings.HasPrefix(line, "branch "):
				wt.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
			}
		}
		if wt.Path != "" {
			wt.IsMain = len(list) == 0
			list = append(list, wt)
		}
	}
	return list, nil
}

// AddWorktree checks out an existing branch into a new worktree at path.
// Git refuses if the branch is already checked out elsewhere.
func (r *Repo) AddWorktree(path, branch string) error {
	_, err := run(r.Dir, "worktree", "add", path, branch)
	return err
}

// AddWorktreeNewBranch creates branch at base and checks it out into a new
// worktree at path. An empty base means the repository's current HEAD.
func (r *Repo) AddWorktreeNewBranch(path, branch, base string) error {
	args := []string{"worktree", "add", "-b", branch, path}
	if base != "" {
		// "--" stops base from ever being parsed as an option, even if a
		// future change makes something dash-prefixed reach this point.
		args = append(args, "--", base)
	}
	_, err := run(r.Dir, args...)
	return err
}

// DefaultBranch is the branch new work should start from: main, else master,
// else whatever the repository currently has checked out.
func (r *Repo) DefaultBranch() string {
	for _, name := range []string{"main", "master"} {
		if r.HasBranch(name) {
			return name
		}
	}
	branch, err := r.CurrentBranch()
	if err != nil {
		return ""
	}
	return branch
}

// HasRef reports whether ref resolves in this repository. Unlike HasBranch it
// also accepts remote branches such as origin/main, tags and commit ids.
//
// Deliberately no "--" before ref: "git rev-parse --verify" requires exactly
// one revision parameter, and inserting "--" makes it see two ("--" and ref),
// so it fails with "Needed a single revision" even for a valid ref. In
// practice this call already rejects every "-"-prefixed value (rev-parse
// treats it as an option), so there is nothing for "--" to protect against
// here.
func (r *Repo) HasRef(ref string) bool {
	_, err := run(r.Dir, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// RemoveWorktree deletes the worktree at path. Without force git refuses
// when the worktree has uncommitted changes. The branch itself is kept.
func (r *Repo) RemoveWorktree(path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	_, err := run(r.Dir, append(args, path)...)
	return err
}

// Prune drops registrations of worktrees whose directories no longer exist.
func (r *Repo) Prune() error {
	_, err := run(r.Dir, "worktree", "prune")
	return err
}

// IsDirty reports whether the checkout at dir has uncommitted changes,
// including untracked files.
func IsDirty(dir string) (bool, error) {
	out, err := run(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// run executes git in dir and returns trimmed stdout.
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return "", fmt.Errorf("run git: %w", err) // git binary missing etc.
		}
		return "", &Error{Args: args, Stderr: strings.TrimSpace(stderr.String())}
	}
	return strings.TrimSpace(stdout.String()), nil
}

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Running tm inside a project is the first thing anybody tries, because every
// other developer tool is run there. The message has to say what tm wanted
// instead, and — since it can see them — that the repositories are one
// directory up.
func TestInsideARepositoryTheMessagePointsAtTheDirectoryAbove(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	for _, name := range []string{"api", "web"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	err := explainNoProjects(filepath.Join(root, "api"))

	if err == nil {
		t.Fatal("a directory with no repositories in it should be explained")
	}
	msg := err.Error()
	for _, want := range []string{"api is a repository", "holds 2", "cd .. && tm"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

// A directory that is neither a repository nor holds any gets the shape tm
// expects, since there is nothing better to point at.
func TestAnEmptyDirectoryGetsTheLayoutTmExpects(t *testing.T) {
	err := explainNoProjects(t.TempDir())

	if err == nil {
		t.Fatal("an empty directory should be explained")
	}
	msg := err.Error()
	for _, want := range []string{"no Git repositories in", "run tm here", "a repository"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "cd ..") {
		t.Errorf("there are no repositories above to suggest:\n%s", msg)
	}
}

// A lone repository still has somewhere to point: its own directory holds one
// repository — itself — and tm there shows a sidebar of one. The count is
// written out, so it has to read properly at one as well as at six.
func TestTheCountInTheSuggestionReadsProperlyAtOne(t *testing.T) {
	root := t.TempDir()
	lone := filepath.Join(root, "lone")
	if err := os.MkdirAll(filepath.Join(lone, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	msg := explainNoProjects(lone).Error()

	if !strings.Contains(msg, "holds 1 repository:") {
		t.Errorf("want \"holds 1 repository\", not a plural:\n%s", msg)
	}
	if !strings.Contains(msg, "cd .. && tm") {
		t.Errorf("going up would show that one repository, so it should be suggested:\n%s", msg)
	}
}

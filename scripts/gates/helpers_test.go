package main

import (
	"os"
	"os/exec"
	"testing"
)

// mustModuleRoot is moduleRoot for a test, which wants a fatal rather
// than an error.
func mustModuleRoot(t *testing.T) string {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// initRepo makes an empty git repository in a temporary directory, on
// branch main, and moves the test into it. Git, the gates' own calls
// included, reads none of the machine's configuration for the rest of
// the test, so a personal excludes file or hook cannot change what a
// gate sees, and a commit has an author.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "Fixture", "GIT_AUTHOR_EMAIL": "fixture@example.com",
		"GIT_COMMITTER_NAME": "Fixture", "GIT_COMMITTER_EMAIL": "fixture@example.com"} {
		t.Setenv(k, v)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	if _, err := gitOutput("init", "--quiet", "--initial-branch=main"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// mustGit runs git for a test, which wants a fatal rather than an error.
func mustGit(t *testing.T, args ...string) {
	t.Helper()
	if _, err := gitOutput(args...); err != nil {
		t.Fatal(err)
	}
}

// putAll writes each file's body to its path under root.
func putAll(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		put(t, root, rel, body)
	}
}

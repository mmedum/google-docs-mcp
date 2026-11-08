package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// isolateGit keeps the machine's git configuration from every git the
// test runs, the gates' own included, so a personal excludes file or hook
// cannot change what a gate sees, and gives a commit an author.
//
// It also turns automatic maintenance off. A commit starts it detached, and
// it holds .git/objects/maintenance.lock after git returns, so a copy of the
// repository races it (https://github.com/git/git/blob/v2.51.0/builtin/gc.c#L1663).
func isolateGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
		"XDG_CONFIG_HOME": t.TempDir(), "GIT_AUTHOR_NAME": "Fixture", "GIT_AUTHOR_EMAIL": "fixture@example.com",
		"GIT_COMMITTER_NAME": "Fixture", "GIT_COMMITTER_EMAIL": "fixture@example.com",
		// Command-scope pairs, which every config file yields to:
		// https://git-scm.com/docs/git-config#Documentation/git-config.txt-GITCONFIGCOUNT
		"GIT_CONFIG_COUNT": "2",
		// No command starts git maintenance run --auto after a write:
		// https://git-scm.com/docs/git-maintenance#Documentation/git-maintenance.txt-maintenanceauto
		"GIT_CONFIG_KEY_0": "maintenance.auto", "GIT_CONFIG_VALUE_0": "false",
		// A porcelain command that runs git gc --auto itself finds no work:
		// https://git-scm.com/docs/git-gc#Documentation/git-gc.txt-gcauto
		"GIT_CONFIG_KEY_1": "gc.auto", "GIT_CONFIG_VALUE_1": "0"} {
		t.Setenv(k, v)
	}
}

// initRepo makes an empty git repository in a temporary directory, on
// branch main, and moves the test into it, git isolated as isolateGit
// isolates it.
func initRepo(t *testing.T) string {
	t.Helper()
	isolateGit(t)
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

// A fixture's git leaves nothing running when it returns: a commit starts
// no maintenance, and a git gc --auto with more packs than its limit packs
// nothing.
func TestIsolatedGitRunsNoAutomaticMaintenance(t *testing.T) {
	dir := initRepo(t)
	commit := exec.Command("git", "commit", "--allow-empty", "-q", "-m", "first")
	commit.Env = append(os.Environ(), "GIT_TRACE=1")
	out, err := commit.CombinedOutput()
	if err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	// Before git 2.29 a commit starts git gc --auto itself, in the
	// foreground, and gc.auto=0 returns it before it can detach
	// (https://github.com/git/git/blob/v2.28.0/builtin/gc.c#L343):
	// the pack count below holds that, so the trace may show it.
	if trace := string(out); !strings.Contains(trace, "built-in: git commit") {
		t.Fatalf("GIT_TRACE=1 git commit printed %q, which traces no commit", trace)
	} else if strings.Contains(trace, "git maintenance run") {
		t.Errorf("git commit traced %q, want no git maintenance run started", trace)
	}
	for _, msg := range []string{"second", "third"} {
		mustGit(t, "commit", "--allow-empty", "-q", "-m", msg)
		mustGit(t, "repack", "-q")
	}
	mustGit(t, "-c", "gc.autoPackLimit=1", "-c", "gc.autoDetach=false", "gc", "--auto", "--quiet")
	packs, err := filepath.Glob(filepath.Join(dir, ".git", "objects", "pack", "*.pack"))
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 2 {
		t.Errorf("git gc --auto over 2 packs with gc.autoPackLimit=1 left %d, want the 2 untouched", len(packs))
	}
}

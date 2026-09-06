package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// schemaDiff compares the current build's tool schemas with the last
// tag's. A removed tool, a removed field or a newly required field
// breaks a caller, and this is what makes the version promise in the
// README enforceable rather than stated.
//
// It was a shell script. The reason it is not one any more is the whole
// of §1: this builds an old revision in a worktree, parses two JSON
// documents and compares them, and every one of those steps in bash is a
// place where a quote ends up inside a string. It also has to run on the
// Windows runner, where bash is a dependency rather than a given.
func schemaDiff(w io.Writer, args []string) error {
	bin := binOr(args)

	current, err := dumpSchemas(bin)
	if err != nil {
		return err
	}
	if err := os.WriteFile("schemas.json", current, 0o644); err != nil {
		return fmt.Errorf("write schemas.json: %w", err)
	}

	last := lastTag()
	if last == "" {
		_, err := fmt.Fprintln(w, "no previous tag; schemas.json written")
		return err
	}

	old, err := schemasAtTag(last)
	if err != nil {
		return err
	}
	oldDump, err := parseSchemas(old)
	if err != nil {
		return fmt.Errorf("%s: %w", last, err)
	}
	newDump, err := parseSchemas(current)
	if err != nil {
		return err
	}
	if breaking := diff(w, oldDump, newDump); breaking {
		return fmt.Errorf("the tool surface changed in a way that breaks a caller since %s", last)
	}
	return nil
}

// schemasAtTag builds the binary as it was at a tag and asks it for its
// schemas. The worktree is removed whether or not the build succeeds:
// leaving one behind makes every later `git worktree add` fail with a
// message about the path already existing, which is a confusing way to
// learn that a gate crashed an hour ago.
func schemasAtTag(tag string) (out []byte, err error) {
	tmp, err := os.MkdirTemp("", "gates-schema-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	src := filepath.Join(tmp, "src")
	if err := runCmd("git", "worktree", "add", "-q", src, tag); err != nil {
		return nil, fmt.Errorf("worktree for %s: %w", tag, err)
	}
	defer func() {
		if rmErr := runCmd("git", "worktree", "remove", "-f", src); rmErr != nil && err == nil {
			err = fmt.Errorf("remove worktree: %w", rmErr)
		}
	}()

	oldBin := filepath.Join(tmp, "old")
	build := exec.Command("go", "build", "-o", oldBin, "./cmd/google-docs-mcp")
	build.Dir = src
	if b, buildErr := build.CombinedOutput(); buildErr != nil {
		return nil, fmt.Errorf("build %s: %w: %s", tag, buildErr, b)
	}
	return dumpSchemas(oldBin)
}

func dumpSchemas(bin string) ([]byte, error) {
	out, err := exec.Command(bin, "--dump-schemas").Output()
	if err != nil {
		return nil, fmt.Errorf("%s --dump-schemas: %w", bin, err)
	}
	return out, nil
}

func lastTag() string {
	out, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		return "" // no tags yet, or not a checkout
	}
	return trimLine(string(out))
}

func runCmd(name string, args ...string) error {
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, out)
	}
	return nil
}

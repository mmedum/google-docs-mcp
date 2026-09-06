package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The gate needs its own tests for the reason the gates keep teaching:
// one that reads nothing passes, and one whose arithmetic is wrong is
// invisible until it lets something through. The shell version this
// replaces had neither.
func TestCoverageProfileArithmetic(t *testing.T) {
	const module = "example.test/mod"
	write := func(t *testing.T, lines ...string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "cov.out")
		body := "mode: atomic\n" + strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("half the statements", func(t *testing.T) {
		p := write(t,
			module+"/internal/a/x.go:1.1,2.2 3 1",
			module+"/internal/a/x.go:3.1,4.2 3 0")
		s, err := readProfile(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.percent(module + "/internal/a/"); got != 50 {
			t.Errorf("percent = %.1f, want 50", got)
		}
	})

	t.Run("a block reported by two binaries counts once", func(t *testing.T) {
		// -coverpkg makes every test binary report every block. Counting
		// them twice would quietly halve the weight of a package's own
		// tests against another package's incidental coverage.
		p := write(t,
			module+"/internal/a/x.go:1.1,2.2 4 0",
			module+"/internal/a/x.go:1.1,2.2 4 7",
			module+"/internal/a/x.go:3.1,4.2 4 0")
		s, err := readProfile(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.percent(module + "/internal/a/"); got != 50 {
			t.Errorf("percent = %.1f, want 50 (8 statements, 4 covered)", got)
		}
	})

	t.Run("a package with no blocks is zero, not a hundred", func(t *testing.T) {
		p := write(t, module+"/internal/a/x.go:1.1,2.2 3 1")
		s, err := readProfile(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.percent(module + "/internal/absent/"); got != 0 {
			t.Errorf("an unmeasured package should be 0%%, got %.1f", got)
		}
	})

	t.Run("a prefix is not a package", func(t *testing.T) {
		// internal/doc must not absorb internal/doctest's statements.
		p := write(t,
			module+"/internal/doc/x.go:1.1,2.2 2 1",
			module+"/internal/doctest/y.go:1.1,2.2 2 0")
		s, err := readProfile(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.percent(module + "/internal/doc/"); got != 100 {
			t.Errorf("percent = %.1f, want 100; the trailing slash is what keeps doctest out", got)
		}
	})

	t.Run("an empty profile is an error, not a pass", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cov.out")
		if err := os.WriteFile(path, []byte("mode: atomic\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readProfile(path); err == nil {
			t.Error("a profile with no blocks should fail: it cannot tell zero coverage from zero measurement")
		}
	})

	t.Run("a missing profile is an error", func(t *testing.T) {
		if _, err := readProfile(filepath.Join(t.TempDir(), "nothing.out")); err == nil {
			t.Error("a missing profile should fail")
		}
	})
}

// Every exemption carries a reason, because the reason is the only thing
// that makes an exemption reviewable.
// A reason is not enough: the package has to exist in the list the floor
// is derived from, or the exemption is a claim about nothing. Both stale
// entries this caught had a perfectly good reason written beside them.
func TestEveryExemptionNamesARealPackage(t *testing.T) {
	module, err := moduleName()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	// Not Skipf. A check that skips when it cannot read its input
	// reports the same green as a check that read everything and found
	// nothing, which is the failure this whole file exists to avoid.
	pkgs, err := internalPackages(module, mustModuleRoot(t))
	if err != nil {
		t.Fatalf("go list ./internal/...: %v", err)
	}
	if len(pkgs) < 5 {
		t.Fatalf("only %d packages listed; the check is not reading go list", len(pkgs))
	}
	listed := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		listed[p] = true
	}
	for pkg := range exemptFromFloor {
		if !listed[pkg] {
			t.Errorf("%s is exempted from the coverage floor but is not in `go list ./internal/...`, "+
				"so the exemption applies to nothing", pkg)
		}
	}
}

func TestEveryExemptionHasAReason(t *testing.T) {
	if len(exemptFromFloor) == 0 {
		t.Fatal("no exemptions listed; the map is not being read")
	}
	for pkg, why := range exemptFromFloor {
		if len(strings.Fields(why)) < 4 {
			t.Errorf("%s: %q is not a reason", pkg, why)
		}
		if !strings.HasPrefix(pkg, "internal/") {
			t.Errorf("%s: exemptions name packages under internal/", pkg)
		}
	}
}

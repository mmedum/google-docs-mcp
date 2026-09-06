package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// exemptFromFloor are the packages the coverage floor does not apply to,
// each with the reason. The list of packages it does apply to is derived
// from `go list ./internal/...`, so a package added under internal/ is
// under the floor from its first commit; an exemption has to be written
// here, where a reviewer sees it.
//
// An entry naming a package the list does not contain is worse than no
// entry: it reads as a considered exemption and exempts nothing.
// `TestEveryExemptionNamesARealPackage` is what stops that, and it was
// written after two of these — `internal/livecheck` and `internal/evals`
// — had gone stale, one of them because a build tag was removed and its
// stated reason ("absent from an untagged go list") quietly stopped
// being true.
var exemptFromFloor = map[string]string{
	"internal/devcheck":    "a build-gate helper with no runtime path; the gates that use it are what exercise it",
	"internal/doc/doctest": "fixtures for other packages' tests",
	"internal/gdocs":       "wire types: struct tags, no logic",
	"internal/leakcheck":   "a test-only package: it scans the repository and has no statements of its own",
}

// coverageFloor enforces a statement-coverage floor per package. The
// profile comes from -coverpkg=./internal/..., so a package's statements
// appear once per test binary that linked it and are de-duplicated here.
//
// It was a shell script until the list of packages was hand-written and
// a new package went uncovered without anyone noticing. Being Go, it
// runs on every platform CI uses rather than only on Linux, and it has
// tests of its own — a gate that has never been watched fail is not yet
// a gate.
func coverageFloor(profile string, min float64) error {
	module, err := moduleName()
	if err != nil {
		return err
	}
	pkgs, err := internalPackages(module, "")
	if err != nil {
		return err
	}
	if len(pkgs) < 5 {
		return fmt.Errorf("only %d packages under internal/; the list is not being read", len(pkgs))
	}
	covered, err := readProfile(profile)
	if err != nil {
		return err
	}

	var below []string
	for _, pkg := range pkgs {
		if _, ok := exemptFromFloor[pkg]; ok {
			continue
		}
		pct := covered.percent(module + "/" + pkg + "/")
		fmt.Printf("%-22s %6.1f%%\n", pkg, pct)
		if pct < min {
			below = append(below, fmt.Sprintf("%s (%.1f%%)", pkg, pct))
		}
	}
	if len(below) > 0 {
		return fmt.Errorf("coverage below %.0f%% in: %s", min, strings.Join(below, ", "))
	}
	return nil
}

// statements counts covered and total statements per block, keyed by the
// block's position so the same block reported by several test binaries
// counts once.
type statements struct {
	total map[string]int
	hit   map[string]bool
}

func (s statements) percent(prefix string) float64 {
	var total, cov int
	for block, n := range s.total {
		if !strings.HasPrefix(block, prefix) {
			continue
		}
		total += n
		if s.hit[block] {
			cov += n
		}
	}
	if total == 0 {
		return 0
	}
	return 100 * float64(cov) / float64(total)
}

func readProfile(path string) (statements, error) {
	f, err := os.Open(path)
	if err != nil {
		return statements{}, fmt.Errorf("coverage profile: %w", err)
	}
	defer func() { _ = f.Close() }()

	s := statements{total: map[string]int{}, hit: map[string]bool{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first { // "mode: atomic"
			first = false
			continue
		}
		// path/to/file.go:1.2,3.4 <numStatements> <count>
		block, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		numStr, countStr, ok := strings.Cut(rest, " ")
		if !ok {
			continue
		}
		num, err1 := strconv.Atoi(numStr)
		count, err2 := strconv.Atoi(countStr)
		if err1 != nil || err2 != nil {
			continue
		}
		if _, seen := s.total[block]; !seen {
			s.total[block] = num
		}
		if count > 0 {
			s.hit[block] = true
		}
	}
	if err := sc.Err(); err != nil {
		return statements{}, err
	}
	if len(s.total) == 0 {
		return statements{}, fmt.Errorf("%s has no coverage blocks; the run that produced it covered nothing", path)
	}
	return s, nil
}

func moduleName() (string, error) {
	out, err := exec.Command("go", "list", "-m").Output()
	if err != nil {
		return "", fmt.Errorf("go list -m: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// dir is where `go list` runs. The gate passes "" and runs at the module
// root; a test has to name the root, because ./internal/... does not
// resolve from inside a package and the command exits 1 there — which a
// check that skipped on error would have reported as success.
func internalPackages(module, dir string) ([]string, error) {
	cmd := exec.Command("go", "list", "./internal/...")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list ./internal/...: %w", err)
	}
	var pkgs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		pkgs = append(pkgs, strings.TrimPrefix(line, module+"/"))
	}
	sort.Strings(pkgs)
	return pkgs, nil
}

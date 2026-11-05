package main

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/mmedum/google-docs-mcp/v3/scripts/internal/livecover"
)

// The docs gate checks references, not claims. Decision IDs, every
// citation of one, every link to a heading, the codemap, the paths the
// docs name, and the live steps and tests the code names all have to
// resolve.
// Whether the cited text is true is for review and the live run.
//
// Evidence has no IDs. What was verified upstream is stated at the line
// that depends on it, with its source link, so an E- citation fails.

// The files the docs gate reads besides the records and the changelog.
const (
	architectureDoc = "docs/architecture.md"
	docsAgentFile   = "CLAUDE.md"
	docsAllowlist   = "testdata/citation-allowlist.txt"
	docsMakefile    = "Makefile"
	docsLintConfig  = ".golangci.yml"
	docsWorkflowDir = ".github/workflows"
)

// A decision ID is D- and two or more digits, zero-padded.
const docsDecisionPattern = "D-NN"

var (
	docsDecisionRef = regexp.MustCompile(`\bD-\d+\b`)
	// docsDecisionHeading finds an entry, with its title.
	docsDecisionHeading = regexp.MustCompile(`^### (D-\S*)(?:[ \t]+(.*))?$`)
	docsEvidenceRef     = regexp.MustCompile(`\bE-\d+\b`)
)

// docsDecisionNumber is the number in a canonical decision ID, or -1 when
// id is not one.
func docsDecisionNumber(id string) int {
	digits, ok := strings.CutPrefix(id, "D-")
	if !ok {
		return -1
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 1 || docsDecisionText(n) != id {
		return -1
	}
	return n
}

func docsDecisionText(n int) string { return fmt.Sprintf("D-%02d", n) }

var (
	// A section number names nothing a reader can follow unless it says
	// which document it numbers: an RFC named right before it, or a URL
	// it sits in.
	docsBareSection   = regexp.MustCompile(`§\s?\d+`)
	docsSectionSource = regexp.MustCompile(`RFC \d+ ?§|https?://[^\s)>\]]+`)
	// A pointer like client.go:238 or client.go:238:5 names the file.
	docsLineSuffix = regexp.MustCompile(`:\d+(?::\d+)?$`)
	// A comment cites live steps by their quoted names, listed with commas,
	// "and" or "or".
	docsLiveStepCite = regexp.MustCompile(`\blive steps? ((?:"[^"]*"(?:,? and |,? or |, )?)+)`)
	docsQuotedName   = regexp.MustCompile(`"([^"]*)"`)
	// A test is cited by its kind and its name, the names listed the same
	// way, each in a code span or not: "the integration tests
	// Test<One> and Test<Two>". The kind is the build tag its tests need.
	docsTestCite = regexp.MustCompile("\\b(integration|live) tests? ((?:`?" + docsTestNamePattern + "`?(?:,? and |,? or |, )?)+)")
	docsTestName = regexp.MustCompile(docsTestNamePattern)
	// A live test is named for what it is, so its name alone cites one.
	docsLiveTestName = regexp.MustCompile(`\bTestLive\w*`)
	// YAML and TOML end a line with a comment after white space.
	docsTrailingComment = regexp.MustCompile(`\s#`)
	docsTOMLMultiline   = regexp.MustCompile(`'''|"""`)

	docsMDLink = regexp.MustCompile(`\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`)
	// A footnote, [^label]:, is not a link definition, though its text may
	// hold links.
	docsMDLinkDef  = regexp.MustCompile(`^\s{0,3}\[[^\]^][^\]]*\]:\s*<?([^\s>]+)`)
	docsATXHeading = regexp.MustCompile(`^#{1,6}[ \t]+(.*?)(?:[ \t]+#+)?$`)
	// Go has no code spans or links for the path check to read, so a doc
	// reference in a comment or a string is found by its shape: a relative
	// path ending in .md, with an optional anchor. A match never starts
	// right after a slash or a dot; one inside a URL is docsInURL's to drop.
	docsGoDocRef = regexp.MustCompile(`(?:^|[^\w/.-])(?:\./)?((?:[\w.-]+/)*[\w.-]+\.md)(?:#([\p{L}\p{N}_-]+))?`)
	// The root documents are named in capitals. A lowercase name in a
	// comment is a file the code writes, which need not exist here.
	docsRootDocName = regexp.MustCompile(`^[A-Z][A-Z0-9_-]*\.md$`)
	// A root file or a source path is held even when its first segment is
	// gone. .txt and .json are left out: checksums.txt and manifest.json
	// name files in a release archive and a bundle.
	docsPathShape = regexp.MustCompile(`^(?:(?:cmd|internal|scripts|docs|testdata|packaging|\.github)/|[\w.-]+\.(?:md|ya?ml|toml|mod|sum)$|Makefile$|LICENSE$)`)
	// An entry runs to the next heading at its own level or above.
	docsEntryEnd = regexp.MustCompile(`^#{1,3} `)
	// A package directory under internal/ or cmd/, as a code span in the
	// codemap names it.
	docsPackageName = regexp.MustCompile(`^((?:internal|cmd)(?:/[^/\s]+)+)/?$`)

	docsSeparatorRow = regexp.MustCompile(`^\|(\s*:?-+:?\s*\|)+$`)
	docsGatesCommand = regexp.MustCompile(`(?:^|\s|\./)scripts/gates\s+(\S+)`)
	// A rule line: targets, a colon, and not the := of an assignment.
	docsMakeRule = regexp.MustCompile(`(?m)^([^\s:#=]+(?:[ \t]+[^\s:#=]+)*)[ \t]*:(?:[^=]|$)`)
)

const docsSupersededBy = "superseded by "

var (
	docsIndexHeader = []string{"ID", "Decision", "Status", "Held by"}
	// The last status is a pattern; docsValidStatus reads it.
	docsStatuses    = []string{"accepted", "rejected", "deprecated", docsSupersededBy + docsDecisionPattern}
	docsEntryFields = []string{"Rejected", "Cost"}
)

// docsTestNamePattern is a test's name, or a placeholder for one such as
// Test<Name>, which docsFollowable leaves unchecked.
const docsTestNamePattern = `Test[\w<>…]*`

// docsUnfollowable are the characters that mark a word as a glob or a
// placeholder.
const docsUnfollowable = "*?[]{}<>$…"

// docsFollowable reports whether a word can name something that exists:
// it holds none of docsUnfollowable and no "...", Go's package wildcard.
func docsFollowable(word string) bool {
	return !strings.ContainsAny(word, docsUnfollowable) && !strings.Contains(word, "...")
}

// docsFloors are the fewest links, code-span paths and package
// directories the gate may read. Live steps need no floor: a driver read
// as naming none leaves every citation of one undefined.
type docsFloors struct{ links, spanPaths, packages int }

// docsLimits are the repository's floors, each well under what it holds.
// A pass below its floor has stopped reading what it checks.
var docsLimits = docsFloors{links: 25, spanPaths: 80, packages: 15}

// docs runs the gate over the checkout the gates run in.
func docs(out io.Writer, _ []string) error {
	root, err := moduleRoot()
	if err != nil {
		return couldNotRun{err}
	}
	return docsGateAt(root, docsLimits, out)
}

// docsGateAt runs every check over the checkout at root and reports every
// problem. A checkout git cannot list is a gate that could not run.
func docsGateAt(root string, floors docsFloors, out io.Writer) error {
	files, err := docsFiles(root)
	if err != nil {
		return couldNotRun{err}
	}
	g := newDocsCheck(root, files, floors)
	g.run()
	return g.verdict(out)
}

// verdict is every problem the checks found, or nil after printing what
// they read.
func (g *docsCheck) verdict(out io.Writer) error {
	if len(g.problems) > 0 {
		return fmt.Errorf("%d problem(s):\n  %s", len(g.problems), strings.Join(g.problems, "\n  "))
	}
	_, _ = fmt.Fprintf(out, "%d decisions, %d packages, %d live steps, %d links and %d code-span paths, in %d files\n",
		len(g.decisions), g.packages, len(g.liveSteps), g.links, g.spanPaths, len(g.files))
	return nil
}

// docsFiles is every file git tracks or would commit under root, as it is
// on disk, sorted. A new doc is untracked until the commit that adds it,
// and the gate reads it before that commit as CI will after it. A file
// deleted in the worktree stays listed until the deletion is staged, and
// has nothing to read.
func docsFiles(root string) ([]string, error) {
	listed, err := lsFiles(root)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range listed {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(f))); err == nil {
			files = append(files, f)
		}
	}
	slices.Sort(files)
	return files, nil
}

// run runs every check. Later checks need what earlier ones read: the
// test names and live steps from the Go pass, and the decisions the
// citations resolve to.
func (g *docsCheck) run() {
	g.readGo()
	g.readVocabulary()
	g.readDecisions()
	g.readAllowlist()
	g.checkCitations()
	g.checkCodemap()
}

// docsInTestdata reports whether a file is a fixture. Go ignores testdata
// directories, and the gate reads neither their code nor their prose.
func docsInTestdata(file string) bool {
	return slices.Contains(strings.Split(path.Dir(file), "/"), "testdata")
}

// docsCheck is what the checks share: the checkout, what they read from
// it, and the problems they found.
type docsCheck struct {
	root    string
	files   []string
	present map[string]bool
	dirs    map[string]bool

	problems []string
	reported map[string]bool

	rawOf    map[string]string
	proseOf  map[string][]string
	slugs    map[string]map[string]bool
	comments map[string][]docsSourceLine
	literals map[string][]docsSourceLine

	tests       map[string]bool
	makeTargets map[string]bool
	linters     map[string]bool
	// liveSteps is where each live step is named, by its name.
	liveSteps map[string]token.Position
	// taggedTests is the test and fuzz functions of the files that build
	// only with a tag, by the tag.
	taggedTests map[string]map[string]bool

	// decisions stays nil when the architecture document cannot be read,
	// so a citation of one is not also reported as unknown.
	decisions map[string]*docsDecision

	allowlist map[string]docsAllowance
	// bare counts each allowlisted file's bare section citations.
	bare map[string]int

	// links and spanPaths count what the Markdown pass checked, and
	// packages the directories the codemap is held to, each held to its
	// floor.
	links, spanPaths, packages int
	floors                     docsFloors
}

// docsSourceLine is one line of a Go comment or string literal.
type docsSourceLine struct {
	line int
	text string
}

func newDocsCheck(root string, files []string, floors docsFloors) *docsCheck {
	g := &docsCheck{
		root:      root,
		files:     files,
		present:   map[string]bool{},
		dirs:      map[string]bool{},
		reported:  map[string]bool{},
		rawOf:     map[string]string{},
		proseOf:   map[string][]string{},
		slugs:     map[string]map[string]bool{},
		comments:  map[string][]docsSourceLine{},
		literals:  map[string][]docsSourceLine{},
		tests:     map[string]bool{},
		liveSteps: map[string]token.Position{},
		taggedTests: map[string]map[string]bool{
			livecover.DriverTag: {}, "integration": {},
		},
		bare:   map[string]int{},
		floors: floors,
	}
	for _, f := range files {
		g.present[f] = true
		for d := path.Dir(f); d != "."; d = path.Dir(d) {
			g.dirs[d] = true
		}
	}
	return g
}

// exists reports whether rel is a file or a directory of the checkout.
func (g *docsCheck) exists(rel string) bool { return g.present[rel] || g.dirs[rel] }

// fail records a problem once. A line that cites one ID twice is one
// problem, not two.
func (g *docsCheck) fail(format string, a ...any) {
	if p := fmt.Sprintf(format, a...); !g.reported[p] {
		g.reported[p] = true
		g.problems = append(g.problems, p)
	}
}

// at reports a problem at a line of a file, or at the file when line is 0.
func (g *docsCheck) at(file string, line int, format string, a ...any) {
	where := file
	if line > 0 {
		where += ":" + strconv.Itoa(line)
	}
	g.fail("%s: %s", where, fmt.Sprintf(format, a...))
}

// malformed reports a decision ID not written with two or more digits,
// naming the ID it should be. A heading or index row is read twice, by its
// reader and the citation scan, and the one wording lets fail report it once.
func (g *docsCheck) malformed(file string, line int, id string) {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "D-"))
	if err != nil || n < 1 {
		n = 1
	}
	g.at(file, line, "%q is not written as a decision ID like %s", id, docsDecisionText(n))
}

// source is a checkout file's bytes as they are on disk.
func (g *docsCheck) source(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(g.root, filepath.FromSlash(rel)))
}

// readFile is a checkout file's text with CRLF line ends folded, read
// once. A file that cannot be read is tried again by the next caller, and
// fail drops the repeat.
func (g *docsCheck) readFile(rel string) (string, bool) {
	if text, ok := g.rawOf[rel]; ok {
		return text, true
	}
	if !g.present[rel] {
		g.fail("%s is missing", rel)
		return "", false
	}
	raw, err := g.source(rel)
	if err != nil {
		g.fail("gates cannot read %s: %v", rel, err)
		return "", false
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	g.rawOf[rel] = text
	return text, true
}

// lines is a Markdown file's prose lines, read once.
func (g *docsCheck) lines(rel string) ([]string, bool) {
	if l, ok := g.proseOf[rel]; ok {
		return l, true
	}
	raw, ok := g.readFile(rel)
	if !ok {
		return nil, false
	}
	l := docsProse(raw)
	g.proseOf[rel] = l
	return l, true
}

// docsProse is a Markdown document's lines with fenced code blanked, so
// an example in a fence is neither a heading nor a citation. Blanking
// rather than dropping keeps the line numbers.
func docsProse(doc string) []string {
	lines := strings.Split(doc, "\n")
	code := docsCodeLines(lines)
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
		if code[i] {
			lines[i] = ""
		}
	}
	return lines
}

// docsCodeLines reports, per line, whether it opens, closes or sits inside
// a fence. release-notes reads fences with it too, so a line the docs gate
// reads as code is never lifted into a heading.
func docsCodeLines(lines []string) []bool {
	code := make([]bool, len(lines))
	fence := ""
	for i, line := range lines {
		unindented := strings.TrimLeft(strings.TrimRight(line, " \t\r"), " ")
		marker := docsFenceMarker(unindented)
		switch {
		case fence == "" && marker != "":
			fence = marker
			code[i] = true
		case fence != "":
			if strings.HasPrefix(marker, fence) && strings.TrimLeft(unindented, marker[:1]) == "" {
				fence = ""
			}
			code[i] = true
		}
	}
	return code
}

// docsFenceMarker is the run of three or more backticks or tildes a line
// opens a fence with, or empty. A backtick fence's info string holds no
// backtick, so ```x``` is a code span
// (https://spec.commonmark.org/0.31.2/#fenced-code-blocks).
func docsFenceMarker(line string) string {
	for _, c := range []string{"`", "~"} {
		n := len(line) - len(strings.TrimLeft(line, c))
		if n >= 3 && (c == "~" || !strings.Contains(line[n:], "`")) {
			return line[:n]
		}
	}
	return ""
}

// headingSlugs is every anchor GitHub gives a Markdown file's headings.
func (g *docsCheck) headingSlugs(rel string) map[string]bool {
	if s, ok := g.slugs[rel]; ok {
		return s
	}
	lines, _ := g.lines(rel)
	s := map[string]bool{}
	for _, line := range lines {
		m := docsATXHeading.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// A repeated heading gets -1, -2 and so on, skipping any suffix a
		// real heading already took, the way GitHub numbers them.
		base := docsGitHubSlug(m[1])
		slug := base
		for n := 1; s[slug]; n++ {
			slug = base + "-" + strconv.Itoa(n)
		}
		s[slug] = true
	}
	g.slugs[rel] = s
	return s
}

// docsGitHubSlug is the anchor GitHub gives a heading: lowercase, with
// everything but letters, digits, spaces, hyphens and underscores dropped,
// and spaces turned into hyphens.
func docsGitHubSlug(heading string) string {
	heading = docsMDLink.ReplaceAllString(heading, "")
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// readGo collects every comment line, every test and fuzz function name,
// and the string literals outside tests and test-helper packages. A
// string is read for doc paths only, as a message can send a user to a
// doc; a test's strings are fixtures. It also reads the live steps from
// the driver's own files.
func (g *docsCheck) readGo() {
	fset := token.NewFileSet()
	read := 0
	var driver []*ast.File
	for _, file := range g.files {
		if !strings.HasSuffix(file, ".go") || docsInTestdata(file) {
			continue
		}
		src, err := g.source(file)
		if err != nil {
			g.fail("gates cannot read %s: %v", file, err)
			continue
		}
		f, err := parser.ParseFile(fset, file, src, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			g.fail("gates cannot parse %s: %v", file, err)
			continue
		}
		read++
		for _, group := range f.Comments {
			for _, c := range group.List {
				start := fset.Position(c.Slash).Line
				for i, text := range strings.Split(c.Text, "\n") {
					g.comments[file] = append(g.comments[file], docsSourceLine{start + i, text})
				}
			}
		}
		tag := docsBuildTag(f)
		// The driver is the files under its tag. A file beside them that
		// builds without it is a unit test, and its calls are fakes.
		if path.Dir(file) == livecover.DriverDir && tag == livecover.DriverTag {
			driver = append(driver, f)
		}
		if !strings.HasSuffix(file, "_test.go") {
			if !docsIsTestHelper(f) {
				g.literals[file] = docsStringLines(fset, f)
			}
			continue
		}
		g.readTests(f, tag)
	}
	if read == 0 {
		g.fail("no .go files read: this gate would be looking at nothing")
	}
	// A comment cites a live step by its name, which the driver passes as
	// a string literal. The gate parses the driver's files as text, so
	// nothing has to build them. Two steps of one name would make a
	// citation of it ambiguous.
	for _, step := range livecover.Steps(driver) {
		at := fset.Position(step.Pos)
		if first, ok := g.liveSteps[step.Name]; ok {
			g.at(at.Filename, at.Line, "names the live step %q, which %s:%d names too; a step's name is its own",
				step.Name, first.Filename, first.Line)
			continue
		}
		g.liveSteps[step.Name] = at
	}
}

// readTests reads a _test.go file's test and fuzz functions, and files
// them under tag, the build tag the file needs, when citations of that
// tag's tests are checked.
func (g *docsCheck) readTests(f *ast.File, tag string) {
	tagged := g.taggedTests[tag]
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !docsRunsUnderGoTest(fn.Name.Name) {
			continue
		}
		g.tests[fn.Name.Name] = true
		if tagged != nil {
			tagged[fn.Name.Name] = true
		}
	}
}

// docsBuildTag is the build tag a file needs: the first tag its
// //go:build line holds with alone. A file with no such line, or one
// whose line holds with no tag set, needs none.
func docsBuildTag(f *ast.File) string {
	for _, group := range f.Comments {
		if group.Pos() > f.Package {
			break
		}
		for _, c := range group.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil || expr.Eval(func(string) bool { return false }) {
				return ""
			}
			// Eval asks about every tag in the line, whatever it answers.
			var tags []string
			expr.Eval(func(t string) bool { tags = append(tags, t); return false })
			for _, tag := range tags {
				if expr.Eval(func(t string) bool { return t == tag }) {
					return tag
				}
			}
			return ""
		}
	}
	return ""
}

// docsIsTestHelper reports whether a file belongs to a test-helper
// package, named for what it fakes with a test suffix as httptest is. Its
// strings are fixture content no user reads, so a doc path in one is
// data; its comments are read as any other.
func docsIsTestHelper(f *ast.File) bool {
	return strings.HasSuffix(f.Name.Name, "test")
}

// docsRunsUnderGoTest reports whether a _test.go function is one `go
// test` runs, and so can hold a decision: a test, or a fuzz target, whose
// seed corpus runs as a test
// (https://go.dev/doc/security/fuzz/#running-fuzz-tests). A benchmark
// runs only under -bench, and an example with no output comment is
// compiled but not executed (`go help testfunc`), so neither holds
// anything by its name.
func docsRunsUnderGoTest(name string) bool {
	return strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Fuzz")
}

// docsStringLines is the value of every string literal in a file, by
// source line. Only a raw string spans lines; an escaped newline stays on
// one.
func docsStringLines(fset *token.FileSet, f *ast.File) []docsSourceLine {
	var out []docsSourceLine
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		parts := []string{value}
		if lit.Value[0] == '`' {
			parts = strings.Split(value, "\n")
		}
		start := fset.Position(lit.Pos()).Line
		for i, text := range parts {
			out = append(out, docsSourceLine{start + i, text})
		}
		return true
	})
	return out
}

// readVocabulary reads the Makefile targets and enabled linters a
// "Held by" item may name.
func (g *docsCheck) readVocabulary() {
	g.makeTargets = map[string]bool{}
	if mk, ok := g.readFile(docsMakefile); ok {
		for _, m := range docsMakeRule.FindAllStringSubmatch(mk, -1) {
			for _, target := range strings.Fields(m[1]) {
				if !strings.HasPrefix(target, ".") {
					g.makeTargets[target] = true
				}
			}
		}
		if len(g.makeTargets) == 0 {
			g.at(docsMakefile, 0, "no targets read: a Held by naming one would never match")
		}
	}
	if cfg, ok := g.readFile(docsLintConfig); ok {
		g.linters = docsEnabledLinters(cfg)
		if len(g.linters) == 0 {
			g.at(docsLintConfig, 0, "no enabled linters read: a Held by naming one would never match")
		}
	}
}

// docsEnabledLinters is the linters.enable list of a golangci-lint
// config, written as a block list. It reads the indentation, so an
// enable: nested under one linter's own settings is not taken for the
// list. A flow list reads as none, and the floor on it says so.
func docsEnabledLinters(cfg string) map[string]bool {
	out := map[string]bool{}
	inLinters, inEnable, childIndent := false, false, -1
	for _, raw := range strings.Split(cfg, "\n") {
		line := withoutComment(raw)
		if line == "" {
			continue
		}
		at := indentOf(raw)
		if at == 0 {
			inLinters, inEnable, childIndent = line == "linters:", false, -1
			continue
		}
		if !inLinters {
			continue
		}
		if item, ok := strings.CutPrefix(line, "- "); ok && inEnable {
			out[strings.Trim(strings.TrimSpace(item), `"'`)] = true
			continue
		}
		if childIndent < 0 {
			childIndent = at
		}
		inEnable = at == childIndent && line == "enable:"
	}
	return out
}

// docsIsGatesCommand reports whether name is a command of this program,
// whatever pipeline it runs in.
func docsIsGatesCommand(name string) bool {
	_, ok := commands[name]
	return ok
}

// holds reports whether a "Held by" item names something that exists.
func (g *docsCheck) holds(item string) bool {
	return item == "live" || item == "review" || docsIsGatesCommand(item) ||
		g.makeTargets[item] || g.linters[item] || g.tests[item]
}

// docsListItems splits a comma-separated cell. A closing period and code
// quotes around an item are not part of it.
func docsListItems(s string) []string {
	parts := strings.Split(strings.TrimSuffix(strings.TrimSpace(s), "."), ",")
	out := make([]string, 0, len(parts))
	for _, item := range parts {
		if item = strings.Trim(strings.TrimSpace(item), "`"); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// docsOrList is items the way a sentence lists them: "a, b or c".
func docsOrList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// docsHasField reports whether the entry headed by lines[heading] has a
// "name:" line.
func docsHasField(lines []string, heading int, name string) bool {
	for j := heading + 1; j < len(lines) && !docsEntryEnd.MatchString(lines[j]); j++ {
		if strings.HasPrefix(strings.TrimSpace(lines[j]), name+":") {
			return true
		}
	}
	return false
}

// docsDecision is one D-ID as its index row and its entry give it. A zero
// line means that half is missing: line is the row's, entryLine the
// entry's heading.
type docsDecision struct {
	id                    string
	line                  int
	title, status, heldBy string
	entryLine             int
	entryTitle            string
}

// lapsed reports whether a decision has stopped holding. Only the
// architecture document may cite it.
func (d *docsDecision) lapsed() bool {
	return d.status == "deprecated" || strings.HasPrefix(d.status, docsSupersededBy)
}

// docsValidStatus reports whether s is one of docsStatuses. Whether a
// successor exists is the citation check's, which reads the index too.
func docsValidStatus(s string) bool {
	if successor, ok := strings.CutPrefix(s, docsSupersededBy); ok {
		return docsDecisionNumber(successor) > 0
	}
	return slices.Contains(docsStatuses, s)
}

func (g *docsCheck) decision(id string) *docsDecision {
	if g.decisions[id] == nil {
		g.decisions[id] = &docsDecision{id: id}
	}
	return g.decisions[id]
}

func (g *docsCheck) readDecisions() {
	lines, ok := g.lines(architectureDoc)
	if !ok {
		return
	}
	g.decisions = map[string]*docsDecision{}
	g.readDecisionIndex(lines)
	g.readDecisionEntries(lines)
	rows := 0
	for _, d := range g.decisions {
		if d.line != 0 {
			rows++
		}
	}
	if rows == 0 {
		g.at(architectureDoc, 0, "no decisions in the index: this gate would be looking at nothing")
	}

	last := len(lines) - 1
	for last >= 0 && lines[last] == "" {
		last--
	}
	next := 0
	if last >= 0 {
		if id, ok := strings.CutPrefix(lines[last], "Next ID: "); ok {
			next = max(docsDecisionNumber(id), 0)
		}
	}
	if next == 0 {
		g.at(architectureDoc, last+1, `the last line is not "Next ID: %s"`, docsDecisionPattern)
	}
	ids := slices.Collect(maps.Keys(g.decisions))
	slices.SortFunc(ids, func(a, b string) int { return cmp.Compare(docsDecisionNumber(a), docsDecisionNumber(b)) })
	for _, id := range ids {
		g.checkDecision(g.decisions[id])
	}
	if next > 0 {
		g.checkNextID(ids, next, last+1)
	}
}

// checkNextID holds the decisions to the Next ID line: each ID is below
// it, and each number below it has a decision, so no ID returns.
func (g *docsCheck) checkNextID(ids []string, next, nextLine int) {
	for _, id := range ids {
		if docsDecisionNumber(id) >= next {
			d := g.decisions[id]
			g.at(architectureDoc, cmp.Or(d.line, d.entryLine), "%s is not below Next ID %s", id, docsDecisionText(next))
		}
	}
	for n := 1; n < next; n++ {
		if id := docsDecisionText(n); g.decisions[id] == nil {
			g.at(architectureDoc, nextLine, "%s is below Next ID %s and is missing; an ID is never deleted", id, docsDecisionText(next))
		}
	}
}

func (g *docsCheck) readDecisionIndex(lines []string) {
	start := slices.Index(lines, "## Decisions")
	if start < 0 {
		g.at(architectureDoc, 0, `no "## Decisions" heading`)
		return
	}
	i := start + 1
	for i < len(lines) && lines[i] == "" {
		i++
	}
	if i+1 >= len(lines) || !strings.HasPrefix(lines[i], "|") || !docsSeparatorRow.MatchString(lines[i+1]) {
		g.at(architectureDoc, start+1, `the decision index is not a table directly under "## Decisions"`)
		return
	}
	if header := docsTableCells(lines[i]); !slices.Equal(header, docsIndexHeader) {
		g.at(architectureDoc, i+1, "the decision index has the columns %q, want %q", header, docsIndexHeader)
		return
	}
	for i += 2; i < len(lines) && strings.HasPrefix(lines[i], "|"); i++ {
		g.readIndexRow(i+1, docsTableCells(lines[i]))
	}
}

// docsTableCells splits a table row at each pipe a backslash does not
// escape, even inside a code span, and unescapes the rest
// (https://github.github.com/gfm/#tables-extension-).
func docsTableCells(row string) []string {
	row = strings.TrimPrefix(strings.TrimSpace(row), "|")
	if !strings.HasSuffix(row, `\|`) {
		row = strings.TrimSuffix(row, "|")
	}
	var cells []string
	var cell strings.Builder
	for i := 0; i < len(row); i++ {
		switch {
		case strings.HasPrefix(row[i:], `\|`):
			cell.WriteByte('|')
			i++
		case row[i] == '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(row[i])
		}
	}
	return append(cells, strings.TrimSpace(cell.String()))
}

func (g *docsCheck) readIndexRow(line int, row []string) {
	if len(row) != len(docsIndexHeader) {
		g.at(architectureDoc, line, "an index row has %d cells, want %d", len(row), len(docsIndexHeader))
		return
	}
	id := row[0]
	if docsDecisionNumber(id) < 0 {
		g.malformed(architectureDoc, line, id)
		return
	}
	d := g.decision(id)
	if d.line != 0 {
		g.at(architectureDoc, line, "%s is in the index twice", id)
		return
	}
	d.line, d.title, d.status, d.heldBy = line, row[1], row[2], row[3]
}

func (g *docsCheck) readDecisionEntries(lines []string) {
	for i, line := range lines {
		m := docsDecisionHeading.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		id := m[1]
		if docsDecisionNumber(id) < 0 {
			g.malformed(architectureDoc, i+1, id)
			continue
		}
		d := g.decision(id)
		if d.entryLine != 0 {
			g.at(architectureDoc, i+1, "%s has two entries", id)
			continue
		}
		d.entryLine, d.entryTitle = i+1, m[2]
		for _, name := range docsEntryFields {
			if !docsHasField(lines, i, name) {
				g.at(architectureDoc, i+1, "%s has no %q line", id, name+":")
			}
		}
	}
}

func (g *docsCheck) checkDecision(d *docsDecision) {
	switch {
	case d.line == 0:
		g.at(architectureDoc, d.entryLine, "%s has no index row", d.id)
		return
	case d.entryLine == 0:
		g.at(architectureDoc, d.line, "%s has no entry", d.id)
	case d.entryTitle != d.title:
		g.at(architectureDoc, d.entryLine, "%s's entry is titled %q and its index row %q", d.id, d.entryTitle, d.title)
	}
	if !docsValidStatus(d.status) {
		g.at(architectureDoc, d.line, "%s has status %q, want %s", d.id, d.status, docsOrList(docsStatuses))
	}
	switch holders := docsListItems(d.heldBy); {
	case slices.Equal(holders, []string{"—"}):
		if d.status == "accepted" {
			g.at(architectureDoc, d.line, "%s is accepted and held by —; an accepted decision names what holds it", d.id)
		}
	case len(holders) == 0:
		g.at(architectureDoc, d.line, "%s is held by nothing", d.id)
	default:
		for _, h := range holders {
			if !g.holds(h) {
				g.at(architectureDoc, d.line, "%s is held by %q, which is not live, review, a make target, "+
					"a gates command, an enabled linter or a test or fuzz function", d.id, h)
			}
		}
	}
}

// docsAllowance is an allowlist line: a file may keep count bare section
// citations, and the count may only fall.
type docsAllowance struct{ line, count int }

func (g *docsCheck) readAllowlist() {
	g.allowlist = map[string]docsAllowance{}
	if !g.present[docsAllowlist] {
		return
	}
	raw, ok := g.readFile(docsAllowlist)
	if !ok {
		return
	}
	for i, line := range strings.Split(raw, "\n") {
		line, _, _ = strings.Cut(line, "#")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		count := 0
		if len(fields) == 2 {
			count, _ = strconv.Atoi(fields[1])
		}
		if count < 1 {
			g.at(docsAllowlist, i+1, "%q is not a path and a count above zero", strings.TrimSpace(line))
			continue
		}
		g.allowlist[fields[0]] = docsAllowance{i + 1, count}
	}
}

// docsIsTrailer reports whether a line is the decisions' Next ID line,
// which names an ID that does not exist.
func docsIsTrailer(file, line string) bool {
	return file == architectureDoc && strings.HasPrefix(line, "Next ID:")
}

// scanned is the range of a Markdown file's lines the citation scan
// reads. A changelog's released sections were true when written; its
// [Unreleased] section becomes a release note, and the scan reads the
// lines release-notes lifts, from the file as it is rather than with its
// fences blanked.
func (g *docsCheck) scanned(file string, lines []string) (from, to int) {
	if file != changelogPath {
		return 0, len(lines)
	}
	raw, _ := g.readFile(file)
	return changelogSection(strings.Split(raw, "\n"), "Unreleased")
}

func (g *docsCheck) checkCitations() {
	// The agent file is one of the documents whose paths this holds.
	if !g.present[docsAgentFile] {
		g.fail("%s is missing", docsAgentFile)
	}
	for _, file := range g.files {
		switch {
		case docsIsRecord(file):
			// A record's reasons cite decisions and live steps, as a
			// comment does.
			if raw, ok := g.readFile(file); ok {
				var rows []docsSourceLine
				for i, line := range strings.Split(raw, "\n") {
					g.checkComment(file, i+1, line)
					rows = append(rows, docsSourceLine{i + 1, line})
				}
				g.checkRuns(file, rows)
			}
		case docsInTestdata(file):
		case strings.HasSuffix(file, ".go"):
			for _, c := range g.comments[file] {
				g.checkComment(file, c.line, c.text)
			}
			for _, s := range g.literals[file] {
				g.checkGoDocRefs(file, s.line, s.text, false)
			}
			g.checkRuns(file, g.comments[file])
		case strings.HasSuffix(file, ".md"):
			g.checkMarkdown(file)
		case docsIsConfigFile(file):
			if raw, ok := g.readFile(file); ok {
				for _, c := range docsConfigComments(file, raw) {
					g.checkComment(file, c.line, c.text)
				}
			}
		}
	}
	if g.links < g.floors.links {
		g.fail("%d Markdown links checked, fewer than %d: the links are not being read", g.links, g.floors.links)
	}
	if g.spanPaths < g.floors.spanPaths {
		g.fail("%d code-span paths checked, fewer than %d: the code spans are not being read", g.spanPaths, g.floors.spanPaths)
	}
	g.checkAllowlist()
}

// checkComment resolves what a comment line cites: decision IDs, section
// numbers and docs.
func (g *docsCheck) checkComment(file string, n int, text string) {
	g.checkLine(file, n, text)
	g.checkGoDocRefs(file, n, text, true)
}

// docsIsRecord reports whether a file is a hand-written TSV record: the
// API coverage and field verdicts. Each is read whole although it sits in
// testdata, since nothing generates it. The snapshots beside them and the
// golden files are written by a command, and any other table there is a
// fixture.
func docsIsRecord(file string) bool {
	return file == apiCoverageFile || file == apiFieldVerdict
}

// docsIsConfigFile reports whether a file holds # comments the citation
// scan reads as it reads a Go comment.
func docsIsConfigFile(file string) bool {
	base := path.Base(file)
	return base == "Makefile" || base == ".gitignore" || docsHasTrailingComments(file)
}

// docsHasTrailingComments reports whether a file's format ends a line
// with a comment, as YAML and TOML do.
func docsHasTrailingComments(file string) bool {
	return strings.HasSuffix(file, ".yml") || strings.HasSuffix(file, ".yaml") || strings.HasSuffix(file, ".toml")
}

// docsConfigComments is each # comment in a config file, by line: a line
// that starts with #, and in YAML or TOML the end of a line after white
// space and #. A " #" in a one-line quoted string is read too, which can
// only add a check; a TOML multi-line string is left out, as its # is
// text.
func docsConfigComments(file, raw string) []docsSourceLine {
	var out []docsSourceLine
	trailing := docsHasTrailingComments(file)
	toml, open := strings.HasSuffix(file, ".toml"), ""
	for i, line := range strings.Split(raw, "\n") {
		if toml {
			line, open = docsOutsideTOMLStrings(line, open)
		}
		if text := strings.TrimLeft(line, " \t"); strings.HasPrefix(text, "#") {
			out = append(out, docsSourceLine{i + 1, text})
		} else if at := docsTrailingComment.FindStringIndex(line); trailing && at != nil {
			out = append(out, docsSourceLine{i + 1, line[at[1]-1:]})
		}
	}
	return out
}

// docsOutsideTOMLStrings is a TOML line less its multi-line strings, each
// between three single or three double quotes
// (https://toml.io/en/v1.0.0#string). open is the delimiter of the string
// the line starts in, and the result's that of the one it leaves open. A
// delimiter after a # is in a comment and opens nothing.
func docsOutsideTOMLStrings(line, open string) (string, string) {
	var out strings.Builder
	for {
		if open != "" {
			end := strings.Index(line, open)
			if end < 0 {
				return out.String(), open
			}
			line = line[end+len(open):]
		}
		start := docsTOMLMultiline.FindStringIndex(line)
		if start == nil || strings.Contains(line[:start[0]], "#") {
			out.WriteString(line)
			return out.String(), ""
		}
		out.WriteString(line[:start[0]])
		line, open = line[start[1]:], line[start[0]:start[1]]
	}
}

// checkRuns holds what a file's comments or prose cite by name, the
// live steps and the tests, to what exists. A name wraps across lines, so
// adjacent lines are read as one text.
func (g *docsCheck) checkRuns(file string, lines []docsSourceLine) {
	for start := 0; start < len(lines); {
		end := start + 1
		for end < len(lines) && lines[end].line == lines[end-1].line+1 {
			end++
		}
		g.checkRun(file, lines[start:end])
		start = end
	}
}

// checkRun checks one run of adjacent lines, reporting an unknown name at
// the line it starts on.
func (g *docsCheck) checkRun(file string, run []docsSourceLine) {
	var text strings.Builder
	starts := make([]int, len(run))
	for i, c := range run {
		starts[i] = text.Len()
		text.WriteString(strings.TrimSpace(strings.TrimPrefix(c.text, "//")))
		text.WriteByte(' ')
	}
	joined := text.String()
	lineAt := func(at int) int {
		i, _ := slices.BinarySearch(starts, at+1)
		return run[i-1].line
	}
	if strings.Contains(joined, "live step") {
		for _, cite := range docsLiveStepCite.FindAllStringSubmatchIndex(joined, -1) {
			for _, q := range docsQuotedName.FindAllStringSubmatchIndex(joined[cite[2]:cite[3]], -1) {
				name := strings.Join(strings.Fields(joined[cite[2]+q[2]:cite[2]+q[3]]), " ")
				// A placeholder such as "<name>" shows the form; it names no step.
				if _, ok := g.liveSteps[name]; !ok && docsFollowable(name) {
					g.at(file, lineAt(cite[2]+q[0]), "names the live step %q, which %s does not define", name, livecover.DriverDir)
				}
			}
		}
	}
	if strings.Contains(joined, "Test") {
		g.checkTestNames(file, joined, lineAt)
	}
}

// checkTestNames holds the tests a text cites by kind to the build tag
// that kind runs under, as in "the integration test Test<Name>", and a
// live test named on its own, whose name starts TestLive.
func (g *docsCheck) checkTestNames(file, joined string, lineAt func(int) int) {
	cited := func(at int, name, kind string) {
		if docsFollowable(name) && !g.taggedTests[kind][name] {
			g.at(file, lineAt(at), "names the %s test %s, which no test file built only with -tags=%s declares", kind, name, kind)
		}
	}
	for _, cite := range docsTestCite.FindAllStringSubmatchIndex(joined, -1) {
		kind, list := joined[cite[2]:cite[3]], joined[cite[4]:cite[5]]
		for _, n := range docsTestName.FindAllStringIndex(list, -1) {
			cited(cite[4]+n[0], list[n[0]:n[1]], kind)
		}
	}
	for _, n := range docsLiveTestName.FindAllStringIndex(joined, -1) {
		cited(n[0], joined[n[0]:n[1]], livecover.DriverTag)
	}
}

func (g *docsCheck) checkMarkdown(file string) {
	lines, ok := g.lines(file)
	if !ok {
		return
	}
	from, to := g.scanned(file, lines)
	// A paragraph can cite a live step across a line break, as a comment
	// can, so the prose is checked in runs of adjacent lines.
	var prose []docsSourceLine
	for i := from; i < to; i++ {
		line := lines[i]
		if line != "" {
			prose = append(prose, docsSourceLine{i + 1, line})
		}
		if docsIsTrailer(file, line) {
			continue
		}
		g.checkLine(file, i+1, line)
		g.checkLinks(file, i+1, line)
		if strings.Contains(line, "`") {
			for _, span := range codeSpans(line) {
				g.checkSpan(file, i+1, span)
			}
		}
	}
	g.checkRuns(file, prose)
}

// checkLine resolves the IDs and section numbers a line cites. Each regex
// runs only on a line holding the literal it needs.
func (g *docsCheck) checkLine(file string, n int, text string) {
	if strings.Contains(text, "D-") {
		for _, id := range docsDecisionRef.FindAllString(text, -1) {
			g.citeDecision(file, n, id)
		}
	}
	if strings.Contains(text, "E-") {
		for _, id := range docsEvidenceRef.FindAllString(text, -1) {
			g.at(file, n, "cites %s; evidence has no IDs: state the behavior at the line that depends on it, with its source link", id)
		}
	}
	if !strings.Contains(text, "§") {
		return
	}
	var bare []string
	sources := docsSectionSource.FindAllStringIndex(text, -1)
	for _, at := range docsBareSection.FindAllStringIndex(text, -1) {
		if !slices.ContainsFunc(sources, func(s []int) bool { return s[0] <= at[0] && at[0] < s[1] }) {
			bare = append(bare, text[at[0]:at[1]])
		}
	}
	switch _, listed := g.allowlist[file]; {
	case len(bare) == 0:
	case listed:
		g.bare[file] += len(bare)
	default:
		g.at(file, n, "cites a section by number (%s); cite a D-ID, docs/<file>.md#<slug> or a source link", bare[0])
	}
}

func (g *docsCheck) citeDecision(file string, n int, id string) {
	d := g.decisions[id]
	switch {
	case docsDecisionNumber(id) < 0:
		g.malformed(file, n, id)
	case g.decisions == nil:
	case d == nil:
		g.at(file, n, "cites %s, which is not a decision", id)
	case file != architectureDoc && d.lapsed():
		g.at(file, n, "cites %s, which is %s", id, d.status)
	}
}

// checkAllowlist holds each allowlisted file's count to what it has, so
// the count can only fall and a file leaves the list when it reaches zero.
func (g *docsCheck) checkAllowlist() {
	paths := slices.Collect(maps.Keys(g.allowlist))
	slices.SortFunc(paths, func(a, b string) int { return cmp.Compare(g.allowlist[a].line, g.allowlist[b].line) })
	for _, p := range paths {
		a, have := g.allowlist[p], g.bare[p]
		switch {
		case !g.present[p]:
			g.at(docsAllowlist, a.line, "lists %s, which does not exist", p)
		case have == 0:
			g.at(docsAllowlist, a.line, "lists %s, which has no bare section citation left; remove it", p)
		case have < a.count:
			g.at(docsAllowlist, a.line, "lists %s at %d bare section citations, and it has %d; lower the count", p, a.count, have)
		case have > a.count:
			g.at(docsAllowlist, a.line, "lists %s at %d bare section citations, and it has %d; "+
				"cite a D-ID, docs/<file>.md#<slug> or a source link", p, a.count, have)
		}
	}
}

// checkGoDocRefs resolves the docs a line of Go names. A string names one
// only by docs/ path or anchor, from the root: a bare file name in a
// string is data. A path inside a URL names nothing here.
func (g *docsCheck) checkGoDocRefs(file string, n int, text string, inComment bool) {
	if !strings.Contains(text, ".md") {
		return
	}
	for _, m := range docsGoDocRef.FindAllStringSubmatchIndex(text, -1) {
		if docsInURL(text, m[2]) {
			continue
		}
		target, slug := text[m[2]:m[3]], ""
		if m[4] >= 0 {
			slug = text[m[4]:m[5]]
		}
		switch {
		case inComment:
			if resolved, ok := g.commentDoc(file, target, slug); ok {
				g.checkTarget(file, n, "cites", resolved, slug)
			}
		case strings.HasPrefix(target, "docs/") || slug != "":
			g.checkTarget(file, n, "names", target, slug)
		}
	}
}

// docsInURL reports whether text[at:] sits inside a URL or a query
// string: the word it is in has :// or ? before it.
func docsInURL(text string, at int) bool {
	word := text[strings.LastIndexFunc(text[:at], unicode.IsSpace)+1 : at]
	return strings.Contains(word, "://") || strings.Contains(word, "?")
}

// commentDoc is the file a .md path in a comment names, read from the
// comment's directory first and then from the root, as a link is. A
// missing file is still a reference when the path has an anchor, starts
// at something in either place or has a path's shape, or is a root
// document's name, in capitals. A path out of the checkout never is.
func (g *docsCheck) commentDoc(file, target, slug string) (string, bool) {
	dir := path.Dir(file)
	local := path.Join(dir, target)
	first, _, nested := strings.Cut(target, "/")
	switch {
	case local == ".." || strings.HasPrefix(local, "../"):
		return "", false
	case g.exists(local):
		return local, true
	case g.exists(target):
		return target, true
	case nested && g.exists(path.Join(dir, first)):
		return local, true
	case slug != "":
		return target, true
	case !nested:
		return target, docsRootDocName.MatchString(target)
	}
	return target, g.exists(first) || docsPathShape.MatchString(target)
}

// checkLinks resolves a Markdown link relative to the file it is in. A
// link written in a code span is an example, not a link.
func (g *docsCheck) checkLinks(file string, n int, text string) {
	if !strings.Contains(text, "](") && !strings.Contains(text, "]:") {
		return
	}
	text = codeSpan.ReplaceAllStringFunc(text, func(span string) string { return strings.Repeat(" ", len(span)) })
	if m := docsMDLinkDef.FindStringSubmatch(text); m != nil {
		g.checkLinkTarget(file, n, m[1])
		return
	}
	for _, m := range docsMDLink.FindAllStringSubmatch(text, -1) {
		g.checkLinkTarget(file, n, m[1])
	}
}

func (g *docsCheck) checkLinkTarget(file string, n int, target string) {
	p, slug, _ := strings.Cut(target, "#")
	if strings.Contains(p, ":") {
		return // A URL, which is not this gate's to fetch.
	}
	resolved := path.Join(path.Dir(file), p)
	switch {
	case p == "":
		resolved = file
	case strings.HasPrefix(p, "/"):
		resolved = strings.TrimPrefix(path.Clean(p), "/")
	case resolved == ".." || strings.HasPrefix(resolved, "../"):
		return // Out of the checkout, as GitHub's ../../issues/51 is: nothing here to check.
	}
	g.links++
	g.checkTarget(file, n, "links to", resolved, slug)
}

// checkTarget holds a reference to a file or directory of the checkout,
// and its slug to a heading when it names a Markdown file.
func (g *docsCheck) checkTarget(file string, n int, verb, target, slug string) {
	if !g.exists(target) {
		g.at(file, n, "%s %s, which does not exist", verb, target)
		return
	}
	g.checkSlug(file, n, verb, target, slug)
}

func (g *docsCheck) checkSlug(file string, n int, verb, target, slug string) {
	if slug != "" && strings.HasSuffix(target, ".md") && !g.headingSlugs(target)[slug] {
		g.at(file, n, "%s %s#%s, and %s has no heading with that slug", verb, target, slug, target)
	}
}

// docsQuoted is a quoted argument in a shell command.
var docsQuoted = regexp.MustCompile(`"[^"]*"|'[^']*'`)

// checkSpan holds the make targets, gates commands and repository paths a
// code span names to what exists. A placeholder such as <name> names
// none.
func (g *docsCheck) checkSpan(file string, n int, span string) {
	fields := strings.Fields(span)
	if len(fields) > 1 && fields[0] == "make" {
		// A quoted value is one argument, spaces and all.
		for _, target := range strings.Fields(docsQuoted.ReplaceAllString(span, `""`))[1:] {
			if !strings.HasPrefix(target, "-") && !strings.Contains(target, "=") &&
				docsFollowable(target) && !g.makeTargets[target] {
				g.at(file, n, "names make %s, which is not a Makefile target", target)
			}
		}
	}
	if m := docsGatesCommand.FindStringSubmatch(span); m != nil && docsFollowable(m[1]) && !docsIsGatesCommand(m[1]) {
		g.at(file, n, "names scripts/gates %s, which is not a gates command", m[1])
	}
	for _, f := range fields {
		g.checkPath(file, n, f)
	}
}

// checkPath holds a word to the tree when it starts at a top-level entry
// of the checkout, or has a path's shape. Anything else is a name, not a
// path: a package, a function, a resource id. A glob or a placeholder is
// not followable. Sentence punctuation after a word and a :line pointer
// are not part of the path.
func (g *docsCheck) checkPath(file string, n int, word string) {
	if !docsFollowable(word) {
		return
	}
	word = strings.TrimRight(word, ".,;)")
	p, slug, _ := strings.Cut(strings.TrimPrefix(word, "./"), "#")
	if p = docsLineSuffix.ReplaceAllString(p, ""); p == "" {
		return
	}
	if first, _, _ := strings.Cut(p, "/"); !g.exists(first) && !docsPathShape.MatchString(p) {
		return
	}
	g.spanPaths++
	clean := strings.TrimSuffix(p, "/")
	target, ok := g.spanTarget(file, clean)
	switch {
	case !ok && !g.dirs[docsGoPackage(clean)]:
		g.at(file, n, "names %s, which does not exist", word)
	case !ok:
		// A Go name in a package that exists.
	case strings.HasSuffix(p, "/") && g.present[target]:
		g.at(file, n, "names %s, which is not a directory", word)
	default:
		g.checkSlug(file, n, "names", target, slug)
	}
}

// spanTarget is the file or directory a code span's path names. A bare
// name is read as a link is, from the naming file's directory, then from
// the root, then as a workflow named the way a person says it: ci.yml.
func (g *docsCheck) spanTarget(file, p string) (string, bool) {
	tries := []string{p}
	if !strings.Contains(p, "/") {
		tries = []string{path.Join(path.Dir(file), p), p, path.Join(docsWorkflowDir, p)}
	}
	for _, t := range tries {
		if g.exists(t) {
			return t, true
		}
	}
	return "", false
}

// docsGoPackage is the package directory of a Go name written
// pkg/path.Ident or pkg/path.Type.Method: everything before the first dot
// after the last slash, or empty. The segment after that dot has to be a
// Go identifier and not a file extension, so a missing
// internal/service/approver.go is not excused by its directory.
func docsGoPackage(p string) string {
	slash := strings.LastIndex(p, "/")
	dot := strings.Index(p[slash+1:], ".")
	if slash < 0 || dot < 0 {
		return ""
	}
	name, _, _ := strings.Cut(p[slash+1+dot+1:], ".")
	if !token.IsIdentifier(name) || docsFileExtensions[name] {
		return ""
	}
	return p[:slash+1+dot]
}

// docsFileExtensions are what docsGoPackage reads as a file's extension
// rather than a Go name.
var docsFileExtensions = map[string]bool{
	"go": true, "md": true, "yml": true, "yaml": true, "json": true, "toml": true,
	"tsv": true, "txt": true, "mod": true, "sum": true, "sh": true,
}

// checkCodemap holds the codemap to the tree: every package under
// internal/ and cmd/, at any depth, is named, and every directory named
// is one. A name that does not exist is the path check's to report.
func (g *docsCheck) checkCodemap() {
	lines, ok := g.lines(architectureDoc)
	if !ok {
		return
	}
	start := slices.Index(lines, "## Codemap")
	if start < 0 {
		g.at(architectureDoc, 0, `no "## Codemap" heading`)
		return
	}
	named := map[string]int{}
	for i := start + 1; i < len(lines) && !strings.HasPrefix(lines[i], "## "); i++ {
		for _, span := range codeSpans(lines[i]) {
			if m := docsPackageName.FindStringSubmatch(span); m != nil && named[m[1]] == 0 {
				named[m[1]] = i + 1
			}
		}
	}
	tree := docsPackageDirs(g.files)
	if g.packages = len(tree); g.packages < g.floors.packages {
		g.fail("%d package directories under internal/ and cmd/, fewer than %d: the tree is not being read",
			g.packages, g.floors.packages)
		return
	}
	for _, pkg := range tree {
		if named[pkg] == 0 {
			g.at(architectureDoc, start+1, `"## Codemap" does not name %s`, pkg)
		}
	}
	for _, pkg := range slices.Sorted(maps.Keys(named)) {
		if g.dirs[pkg] && !slices.Contains(tree, pkg) {
			g.at(architectureDoc, named[pkg], `"## Codemap" names %s, which holds no .go file`, pkg)
		}
	}
}

// docsPackageDirs is every directory under internal/ or cmd/, at any
// depth, that holds a .go file of its own outside testdata, sorted.
func docsPackageDirs(files []string) []string {
	var out []string
	for _, f := range files {
		top, _, _ := strings.Cut(f, "/")
		if dir := path.Dir(f); (top == "internal" || top == "cmd") && dir != top &&
			strings.HasSuffix(f, ".go") && !docsInTestdata(f) {
			out = append(out, dir)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// One reader for a GitHub workflow, shared by the pins and parity gates.
// It reads the block style these workflows are written in, a key and its
// value on one line, and refuses by name a shape it does not read, so a
// file written another way fails rather than yielding nothing to check.
// It is not a YAML parser, and is not grown into one.

const workflowDir = ".github/workflows"

// workflowFile is what the reader takes from one workflow.
type workflowFile struct {
	// path is slash-separated, for messages.
	path string
	text string
	// steps are the steps that use an action, and each job that calls a
	// reusable workflow, in file order.
	steps []workflowStep
	// env is every key under an env: block, at any level.
	env []workflowKey
	// images are each job's container image and each service's.
	images []workflowJobKey
	runs   []workflowRun
	// shell is the workflow-level `defaults: run: shell:`, or "".
	shell string
	// shells are every shell a run step can take, in file order: the
	// workflow's, each job's `defaults: run: shell:` and each step's.
	shells []workflowJobKey
	// refused are the shapes this reader does not read.
	refused []string
}

// workflowStep is one `uses:`, with the step's own with: and env: keys.
// A reusable one is a job that calls a reusable workflow, which has no
// keys of a step.
type workflowStep struct {
	job      string
	line     int
	uses     string
	comment  string
	with     map[string]workflowKey
	env      map[string]workflowKey
	reusable bool
}

// action is the step's action without its ref: "actions/checkout".
func (s workflowStep) action() string {
	a, _, _ := strings.Cut(s.uses, "@")
	return a
}

// workflowKey is one key and its value, unquoted, with its line. A block
// scalar's value is its lines, trimmed and joined by newlines.
type workflowKey struct {
	key, value string
	line       int
}

// workflowJobKey is a key that sets something a job runs, such as its
// container image or its shell, and what sets it.
type workflowJobKey struct {
	job, what string
	workflowKey
}

// workflowRun is one command line of a run: block, its comments and
// continuation breaks removed.
type workflowRun struct {
	job  string
	line int
	text string
}

var (
	// workflowKeyLine is a block mapping's plain key, with an optional
	// sequence dash before it and its value after it.
	workflowKeyLine = regexp.MustCompile(`^(-[ \t]+)?([^\s"'#{\[&*!|>%@` + "`" + `-][^:#]*?)[ \t]*:(?:[ \t]+(.*))?$`)
	// workflowQuotedKey is a key line whose key is quoted, which the reader
	// refuses rather than reads.
	workflowQuotedKey = regexp.MustCompile(`^(?:-[ \t]+)?("[^"]*"|'[^']*')[ \t]*:(?:[ \t]|$)`)
	// workflowBlockScalar is the indicator of a literal or folded scalar.
	workflowBlockScalar = regexp.MustCompile(`^[|>][-+0-9]*$`)
	// workflowUsesKey is a uses: key anywhere on a line, as a flow mapping
	// or a quoted key writes it.
	workflowUsesKey = regexp.MustCompile(`(?:^|[\s{,\[])["']?uses["']?[ \t]*:`)
	// workflowVersionKey is a key that names a tool's version: an input
	// such as cosign-release or syft-version, an env value such as
	// GITLEAKS_VERSION or COSIGN_RELEASE, or a Makefile variable.
	workflowVersionKey = regexp.MustCompile(`(?:version|release|_VERSION|_RELEASE)$`)
)

// workflowMergeKey refuses a merge key, as a key or inside a flow mapping.
const workflowMergeKey = "GitHub Actions does not document merge keys; write the mapping out"

// workflowReadKeys are the keys whose mapping or sequence the gates read.
var workflowReadKeys = map[string]bool{
	"jobs": true, "steps": true, "with": true, "env": true, "container": true, "services": true,
	"defaults": true, "run": true, "uses": true,
}

// workflowHeld reports whether the value of the key path ends at is one a
// gate holds, so it has to sit on the key's line: an action, an image, or
// a version under with: or env:.
func workflowHeld(path []string) bool {
	key, parent := workflowKeyParent(path)
	return key == "uses" || key == "image" || key == "container" ||
		(parent == "with" || parent == "env") && workflowVersionKey.MatchString(key)
}

// workflowKeyParent is a path's last key and the one before it, each ""
// where the path is too short.
func workflowKeyParent(path []string) (key, parent string) {
	if len(path) > 0 {
		key = path[len(path)-1]
	}
	if len(path) > 1 {
		parent = path[len(path)-2]
	}
	return key, parent
}

// readWorkflows reads every workflow, sorted by path.
func readWorkflows() ([]workflowFile, error) {
	var paths []string
	for _, pattern := range []string{"*.yml", "*.yaml"} {
		found, err := filepath.Glob(filepath.Join(filepath.FromSlash(workflowDir), pattern))
		if err != nil {
			return nil, err
		}
		paths = append(paths, found...)
	}
	slices.Sort(paths)
	out := make([]workflowFile, 0, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p) //nolint:gosec // a path under .github/workflows
		if err != nil {
			return nil, err
		}
		out = append(out, parseWorkflow(filepath.ToSlash(p), string(data)))
	}
	return out, nil
}

// workflowNamed finds one workflow by its base name.
func workflowNamed(flows []workflowFile, base string) (workflowFile, bool) {
	for _, w := range flows {
		if w.path == base || strings.HasSuffix(w.path, "/"+base) {
			return w, true
		}
	}
	return workflowFile{}, false
}

// workflowFrame is one open mapping key or sequence item, by the column
// it starts at. An item's key is "-", and an item of a job's steps: holds
// the index of its step. A key whose value was refused takes the lines
// under it with it. open is a key with no value on its line, whose value
// is the block under it.
type workflowFrame struct {
	indent  int
	key     string
	step    int
	refused bool
	open    bool
}

// workflowParser is parseWorkflow's state: the open frames, and in file
// order every step item read, with an action or without, and every job
// that calls a reusable workflow.
type workflowParser struct {
	w     workflowFile
	stack []workflowFrame
	items []workflowStep
}

// parseWorkflow reads one workflow's text.
func parseWorkflow(path, text string) workflowFile {
	p := workflowParser{w: workflowFile{path: path, text: text}}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" {
			continue
		}
		m := workflowKeyLine.FindStringSubmatch(trimmed)
		if m == nil {
			p.unread(i+1, lines[i])
			continue
		}
		i = p.key(lines, i, m)
	}
	for _, s := range p.items {
		if s.line > 0 {
			p.w.steps = append(p.w.steps, s)
		}
	}
	return p.w
}

// key reads the key line lines[i], whose match is m, and returns the index
// of the last line it took: a block scalar takes the lines under it.
func (p *workflowParser) key(lines []string, i int, m []string) int {
	n, col := i+1, indent(lines[i])
	p.pop(col, m[1] != "")
	keyCol := col
	if m[1] != "" {
		item := workflowFrame{indent: col, key: "-", step: -1}
		if path := workflowPath(p.stack); len(path) == 3 && path[0] == "jobs" && path[2] == "steps" {
			item.step = len(p.items)
			p.items = append(p.items, workflowStep{job: path[1], with: map[string]workflowKey{}, env: map[string]workflowKey{}})
		}
		p.stack = append(p.stack, item)
		keyCol = col + len(m[1])
	}
	key, raw := m[2], strings.TrimSpace(m[3])
	path := append(workflowPath(p.stack), key)
	var step *workflowStep
	if len(p.stack) >= 4 && path[0] == "jobs" && path[2] == "steps" && p.stack[3].step >= 0 {
		step = &p.items[p.stack[3].step]
	}
	value, comment := workflowValue(raw)
	p.stack = append(p.stack, workflowFrame{indent: keyCol, key: key, step: -1, open: value == ""})
	if p.refused(n, path, raw, value) {
		p.stack[len(p.stack)-1].refused = true
		return i
	}
	if workflowBlockScalar.MatchString(value) {
		// The body is indented past the key, not the dash before it, so a
		// step's next key ends it.
		body, end := workflowScalarBody(lines, i, keyCol)
		switch {
		case key == "run":
			p.w.addRuns(workflowJob(path), n+1, body, strings.HasPrefix(value, ">"))
		case workflowHeld(path):
			p.w.refuse(n, "%s: a block scalar; write the value on the key's line", key)
		case strings.HasPrefix(value, ">"):
			// A folded scalar's lines are one line, joined by spaces.
			p.read(path, step, workflowKey{key, strings.ReplaceAll(workflowJoin(body), "\n", " "), n}, "")
		default:
			p.read(path, step, workflowKey{key, workflowJoin(body), n}, "")
		}
		return end
	}
	p.read(path, step, workflowKey{key, value, n}, comment)
	return i
}

// read records one key the gates ask about.
func (p *workflowParser) read(path []string, step *workflowStep, k workflowKey, comment string) {
	// A step's keys sit at depth 5: jobs, the job, steps, the item, the key.
	depth, job, inStep := len(path), workflowJob(path), step != nil
	switch {
	case inStep && depth == 5 && k.key == "uses":
		step.line, step.uses, step.comment = k.line, k.value, comment
	case job != "" && depth == 3 && k.key == "uses":
		p.items = append(p.items, workflowStep{job: job, line: k.line, uses: k.value, comment: comment, reusable: true})
	case inStep && depth == 5 && k.key == "run" && k.value != "":
		p.w.addRuns(job, k.line, []string{k.value}, false)
	case depth >= 2 && path[depth-2] == "env":
		p.w.env = append(p.w.env, k)
		if inStep && depth == 6 {
			step.env[k.key] = k
		}
	case inStep && depth == 6 && path[4] == "with":
		step.with[k.key] = k
	case job != "":
		p.readJob(path, job, k)
	case slices.Equal(path, []string{"defaults", "run", "shell"}):
		p.w.shell = k.value
		p.w.shells = append(p.w.shells, workflowJobKey{"", "the workflow", k})
	}
}

// readJob records a job's container image or a service's, and a job's
// shell or a step's.
func (p *workflowParser) readJob(path []string, job string, k workflowKey) {
	switch depth := len(path); {
	case depth == 3 && k.key == "container" && k.value != "", depth == 4 && path[2] == "container" && k.key == "image":
		p.w.images = append(p.w.images, workflowJobKey{job, "the container", k})
	case depth == 5 && path[2] == "services" && k.key == "image":
		p.w.images = append(p.w.images, workflowJobKey{job, "the service " + path[3], k})
	case depth == 5 && path[2] == "defaults" && path[3] == "run" && k.key == "shell":
		p.w.shells = append(p.w.shells, workflowJobKey{job, "job " + job, k})
	case depth == 5 && path[2] == "steps" && k.key == "shell":
		p.w.shells = append(p.w.shells, workflowJobKey{job, "a step of job " + job, k})
	}
}

// refused refuses a value this reader would misread: a merge key, an
// anchor, an alias or a tag, a quoted value with an escape in it, a flow
// collection in or under a key it reads, and a held value on another line.
func (p *workflowParser) refused(n int, path []string, raw, value string) bool {
	key, parent := workflowKeyParent(path)
	switch {
	case key == "<<":
		p.w.refuse(n, workflowMergeKey)
	case raw != "" && strings.ContainsRune("&*!", rune(raw[0])):
		p.w.refuse(n, "%s: an anchor, an alias or a tag; write the value out", key)
	case workflowQuoteEscape(raw):
		p.w.refuse(n, "%s: a quoted value with an escape in it; write it unquoted or as a block", key)
	case raw != "" && strings.ContainsRune("{[", rune(raw[0])) && (workflowReadKeys[key] || workflowReadKeys[parent]):
		p.w.refuse(n, "%s: a flow collection; write it as a block, one key a line", key)
	case value == "" && workflowHeld(path) && key != "container":
		p.w.refuse(n, "%s: has its value on another line; write it on the key's line", key)
	default:
		return false
	}
	return true
}

// unread refuses a line that is not a plain block key where a value a gate
// reads could hide: a quoted key anywhere, a step written as a flow mapping,
// an anchored or empty item, or a held value continued from the line above.
func (p *workflowParser) unread(n int, line string) {
	trimmed := strings.TrimSpace(line)
	item := trimmed == "-" || strings.HasPrefix(trimmed, "- ")
	p.pop(indent(line), item)
	path := workflowPath(p.stack)
	last, _ := workflowKeyParent(path)
	quoted := workflowQuotedKey.FindStringSubmatch(trimmed)
	switch {
	case len(p.stack) > 0 && p.stack[len(p.stack)-1].refused:
	case quoted != nil:
		p.w.refuse(n, "%s: a quoted key; write the key plain", quoted[1])
	case strings.Contains(trimmed, "<<:"):
		p.w.refuse(n, workflowMergeKey)
	case workflowUsesKey.MatchString(trimmed):
		p.w.refuse(n, "uses: is not a block key on its own line; write the step in block style")
	case item && workflowReadKeys[last]:
		p.w.refuse(n, "an item of %s: is not a block mapping; write it one key a line", last)
	case !strings.HasPrefix(trimmed, "- ") && (workflowHeld(path) || last == "run"):
		p.w.refuse(n, "%s: a value continued on the next line; write it on one line or as a block scalar", last)
	}
}

// pop closes the frames a line at col ends: each at col or deeper, but an
// open key at col when the line is an item of it. A sequence may sit at its
// key's own column, the dash counted as indentation
// (https://yaml.org/spec/1.2.2/#823-block-nodes).
func (p *workflowParser) pop(col int, item bool) {
	for n := len(p.stack); n > 0; n = len(p.stack) {
		if top := p.stack[n-1]; top.indent < col || item && top.indent == col && top.open {
			return
		}
		p.stack = p.stack[:n-1]
	}
}

// workflowPath is the keys of the open frames, items included as "-".
func workflowPath(stack []workflowFrame) []string {
	out := make([]string, 0, len(stack)+1)
	for _, f := range stack {
		out = append(out, f.key)
	}
	return out
}

// workflowJob is the job a key's path sits in, or "".
func workflowJob(path []string) string {
	if len(path) > 1 && path[0] == "jobs" {
		return path[1]
	}
	return ""
}

// workflowQuoteEscape reports whether a quoted value holds an escape that
// workflowValue does not read: a doubled single quote in single quotes, or
// a backslash in double quotes (https://yaml.org/spec/1.2.2/#73-flow-scalar-styles).
func workflowQuoteEscape(raw string) bool {
	if len(raw) < 2 || raw[0] != '"' && raw[0] != '\'' {
		return false
	}
	end := strings.IndexByte(raw[1:], raw[0]) + 1
	if raw[0] == '"' {
		return end > 0 && strings.Contains(raw[1:end], `\`)
	}
	return end > 0 && strings.HasPrefix(raw[end+1:], "'")
}

// workflowValue is a value without its quotes and the comment after it.
func workflowValue(s string) (value, comment string) {
	if len(s) > 1 && (s[0] == '"' || s[0] == '\'') {
		if end := strings.IndexByte(s[1:], s[0]); end >= 0 {
			_, comment := yamlCutComment(strings.TrimSpace(s[end+2:]))
			return s[1 : end+1], comment
		}
	}
	return yamlCutComment(s)
}

// yamlCutComment is a YAML line or value without the comment after it, and
// the comment's text. A comment starts at a # that begins the text or
// follows white space (https://yaml.org/spec/1.2.2/#66-comments).
func yamlCutComment(s string) (value, comment string) {
	if rest, ok := strings.CutPrefix(s, "#"); ok {
		return "", strings.TrimSpace(rest)
	}
	if at := docsTrailingComment.FindStringIndex(s); at != nil {
		return strings.TrimSpace(s[:at[0]]), strings.TrimSpace(s[at[1]:])
	}
	return strings.TrimSpace(s), ""
}

// workflowScalarBody is a block scalar's lines after the key at line i,
// indented past col, and the index of its last line.
func workflowScalarBody(lines []string, i, col int) ([]string, int) {
	end := i
	for j := i + 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) != "" && indent(lines[j]) <= col {
			break
		}
		end = j
	}
	return lines[i+1 : end+1], end
}

// workflowJoin is a block scalar's lines, each trimmed, the blank ones
// dropped, joined by newlines.
func workflowJoin(body []string) string {
	var kept []string
	for _, l := range body {
		if t := strings.TrimSpace(l); t != "" {
			kept = append(kept, t)
		}
	}
	return strings.Join(kept, "\n")
}

// addRuns adds a run: block's command lines from first on. A folded
// scalar is one line; a literal one is a script, a trailing backslash
// joining a line to the next.
func (w *workflowFile) addRuns(job string, first int, body []string, folded bool) {
	if folded {
		body = []string{strings.ReplaceAll(workflowJoin(body), "\n", " ")}
	}
	pending, start := "", first
	for i, l := range body {
		t := strings.TrimSpace(l)
		if pending == "" && (t == "" || strings.HasPrefix(t, "#")) {
			continue
		}
		if pending == "" {
			start = first + i
		}
		if cont, ok := strings.CutSuffix(t, "\\"); ok {
			pending += strings.TrimRight(cont, " \t") + " "
			continue
		}
		w.runs = append(w.runs, workflowRun{job, start, strings.TrimSpace(pending + t)})
		pending = ""
	}
	if t := strings.TrimSpace(pending); t != "" {
		w.runs = append(w.runs, workflowRun{job, start, t})
	}
}

func (w *workflowFile) refuse(n int, format string, a ...any) {
	w.refused = append(w.refused, fmt.Sprintf("%s:%d: %s", w.path, n, fmt.Sprintf(format, a...)))
}

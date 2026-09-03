package render

import (
	"fmt"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// DiffStats count the changed lines of a diff.
type DiffStats struct {
	Added   int `json:"added_lines"`
	Removed int `json:"removed_lines"`
	Hunks   int `json:"hunks"`
}

// DiffResult is a rendered unified diff.
type DiffResult struct {
	Text      string
	Stats     DiffStats
	Truncated bool
}

type diffLine struct {
	op   byte // ' ', '-', '+'
	text string
}

// UnifiedDiff renders the line-level differences between two texts in
// unified-diff form with the given lines of context, cut at a hunk
// boundary when the output would exceed maxChars (0 = no limit).
func UnifiedDiff(oldText, newText string, context, maxChars int) DiffResult {
	if context < 0 {
		context = 0
	}
	lines := lineDiff(oldText, newText)
	var res DiffResult
	var sb strings.Builder
	oldNo, newNo := 1, 1
	i := 0
	for i < len(lines) {
		if lines[i].op == ' ' {
			oldNo++
			newNo++
			i++
			continue
		}
		// A hunk: from `context` lines before this change until `context`
		// lines after the last change with no gap larger than 2*context.
		start := max(i-context, 0)
		end := i
		for end < len(lines) {
			if lines[end].op != ' ' {
				end++
				continue
			}
			gap := end
			for gap < len(lines) && lines[gap].op == ' ' && gap-end < 2*context+1 {
				gap++
			}
			if gap == len(lines) || lines[gap].op == ' ' {
				end = min(end+context, len(lines))
				break
			}
			end = gap
		}
		// Line numbers at hunk start.
		hOld, hNew := oldNo, newNo
		for j := i - 1; j >= start; j-- {
			hOld--
			hNew--
		}
		var oldCount, newCount, removed, added int
		var hunk strings.Builder
		for j := start; j < end; j++ {
			l := lines[j]
			switch l.op {
			case ' ':
				oldCount++
				newCount++
			case '-':
				oldCount++
				removed++
			case '+':
				newCount++
				added++
			}
			hunk.WriteByte(l.op)
			hunk.WriteString(l.text)
			hunk.WriteByte('\n')
		}
		header := fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", hOld, oldCount, hNew, newCount)
		if maxChars > 0 && res.Stats.Hunks > 0 && sb.Len()+len(header)+hunk.Len() > maxChars {
			res.Truncated = true
			break
		}
		sb.WriteString(header)
		sb.WriteString(hunk.String())
		res.Stats.Hunks++
		res.Stats.Removed += removed
		res.Stats.Added += added
		// Advance counters over the hunk body past i.
		for j := i; j < end; j++ {
			switch lines[j].op {
			case ' ':
				oldNo++
				newNo++
			case '-':
				oldNo++
			case '+':
				newNo++
			}
		}
		i = end
	}
	res.Text = strings.TrimRight(sb.String(), "\n")
	return res
}

// lineDiff computes a line-level diff as a flat list of context, removed
// and added lines in output order.
func lineDiff(oldText, newText string) []diffLine {
	dmp := diffmatchpatch.New()
	a, b, arr := dmp.DiffLinesToChars(oldText, newText)
	diffs := dmp.DiffCharsToLines(dmp.DiffMain(a, b, false), arr)
	var out []diffLine
	for _, d := range diffs {
		op := byte(' ')
		switch d.Type {
		case diffmatchpatch.DiffDelete:
			op = '-'
		case diffmatchpatch.DiffInsert:
			op = '+'
		}
		text := strings.TrimSuffix(d.Text, "\n")
		if text == "" && d.Text == "" {
			continue
		}
		for _, line := range strings.Split(text, "\n") {
			out = append(out, diffLine{op: op, text: line})
		}
	}
	return out
}

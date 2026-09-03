package render

import (
	"strings"
	"testing"
)

func TestUnifiedDiff(t *testing.T) {
	oldText := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n"
	newText := "a\nb\nc\nD\ne\nf\ng\nh\ni\nJ\n"
	res := UnifiedDiff(oldText, newText, 1, 0)
	want := "@@ -3,3 +3,3 @@\n c\n-d\n+D\n e\n@@ -9,2 +9,2 @@\n i\n-j\n+J"
	if res.Text != want {
		t.Fatalf("diff:\n%s\nwant:\n%s", res.Text, want)
	}
	if res.Stats.Added != 2 || res.Stats.Removed != 2 || res.Stats.Hunks != 2 || res.Truncated {
		t.Fatalf("stats %+v", res.Stats)
	}
	// Close changes merge into one hunk; equal texts produce nothing.
	res = UnifiedDiff(oldText, "a\nB\nc\nD\ne\nf\ng\nh\ni\nj\n", 1, 0)
	if res.Stats.Hunks != 1 || !strings.HasPrefix(res.Text, "@@ -1,5 +1,5 @@") {
		t.Fatalf("merged hunk: %+v\n%s", res.Stats, res.Text)
	}
	if res := UnifiedDiff(oldText, oldText, 3, 0); res.Text != "" || res.Stats.Hunks != 0 {
		t.Fatalf("identical: %+v", res)
	}
	// Budget cuts at a hunk boundary.
	res = UnifiedDiff(oldText, newText, 0, 20)
	if !res.Truncated || res.Stats.Hunks != 1 || strings.Count(res.Text, "@@") != 2 {
		t.Fatalf("budget: %+v\n%s", res, res.Text)
	}
	// Pure additions and removals at the edges.
	res = UnifiedDiff("x\n", "x\ny\nz\n", 0, 0)
	if res.Text != "@@ -2,0 +2,2 @@\n+y\n+z" || res.Stats.Added != 2 {
		t.Fatalf("append: %q %+v", res.Text, res.Stats)
	}
	res = UnifiedDiff("p\nq\n", "", 0, 0)
	if res.Text != "@@ -1,2 +1,0 @@\n-p\n-q" {
		t.Fatalf("delete all: %q", res.Text)
	}
}

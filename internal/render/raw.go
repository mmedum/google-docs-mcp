package render

import (
	"encoding/json"
	"fmt"

	"github.com/mmedum/google-docs-mcp/internal/doc"
)

// Raw renders blocks [from, to) as the API's structural elements: one
// JSON array, cut at element boundaries when over budget.
func Raw(seg *doc.Segment, from, to, maxChars int) (Result, error) {
	var err error
	first := true
	res := budgeted(seg.Blocks, from, to, maxChars, func(b *doc.Block) (string, bool) {
		data, e := json.Marshal(b.Wire)
		if e != nil && err == nil {
			err = fmt.Errorf("encode %s: %w", b.Handle, e)
		}
		return string(data), true
	}, func(*doc.Block, string) string {
		if first {
			first = false
			return ""
		}
		return ",\n"
	})
	if err != nil {
		return Result{}, err
	}
	res.Text = "[" + res.Text + "]"
	res.Chars = len(res.Text)
	return res, nil
}

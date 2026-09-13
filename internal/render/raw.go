package render

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/mmedum/google-docs-mcp/internal/doc"
)

// Raw renders blocks [from, to) as the API's structural elements: one
// JSON array, cut at element boundaries when over budget.
//
// The bytes the API sent, where the element still has them, rather than
// a re-encoding of the wire types. Marshalling those can only return the
// fields they model, which made this — the one read whose whole job is
// to show what Google said — the least trustworthy read on the server:
// #46 was filed on a raw read that had silently dropped the field
// proving the write it was reporting as broken had worked. Nine types
// model fewer fields than the API publishes, so the gap outlived that
// fix; keeping the bytes closes it for all of them at once.
func Raw(seg *doc.Segment, from, to, maxChars int) (Result, error) {
	var err error
	first := true
	res := budgeted(seg.Blocks, from, to, maxChars, func(b *doc.Block) (string, bool) {
		data, e := elementJSON(b)
		if e != nil && err == nil {
			err = fmt.Errorf("encode %s: %w", b.Handle, e)
		}
		return data, true
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

// elementJSON is one block as the API sent it, compacted, falling back
// to encoding the wire types for a block that never came from a
// response. Compacted because the budget is in characters and Google
// pretty-prints by default: the same element indented is two to three
// times the size, which would silently shrink how much of a document a
// raw read can return.
func elementJSON(b *doc.Block) (string, error) {
	if raw := b.Wire.RawJSON(); len(raw) > 0 {
		var out bytes.Buffer
		if err := json.Compact(&out, raw); err != nil {
			return "", err
		}
		return out.String(), nil
	}
	data, err := json.Marshal(b.Wire)
	return string(data), err
}

package render

import (
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/doc"
)

// Plain renders blocks [from, to) as plain text: no markdown markers,
// headings and list items still recognisable, tables tab-separated.
func Plain(seg *doc.Segment, from, to int, o Options) Result {
	first := true
	marks := marksFor(o.Marks, seg)
	res := budgeted(seg.Blocks, from, to, o.MaxChars, func(b *doc.Block) (string, bool) {
		return plainBlock(b, o, marks), true
	}, func(*doc.Block, string) string {
		if first {
			first = false
			return ""
		}
		return "\n"
	})
	return withFooter(seg, from, res, o)
}

func plainBlock(b *doc.Block, o Options, marks []Mark) string {
	prefix := handlePrefix(b, o)
	view := o.view()
	switch {
	case b.Paragraph != nil:
		p := b.Paragraph
		text := textWithMarks(p, view, marks)
		if p.Bullet != nil {
			text = strings.Repeat("  ", p.Bullet.Nesting) + listMarker(b) + text
		}
		if o.WithHandles && p.HeadingID != "" {
			text += " {" + p.HeadingID + "}"
		}
		return strings.TrimRight(prefix+text, " ")
	case b.Table != nil, b.TOC != nil:
		return prefix + b.Text(view)
	}
	return strings.TrimSpace(prefix)
}

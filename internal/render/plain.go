package render

import (
	"strconv"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/doc"
)

// Plain renders blocks [from, to) as plain text: no markdown markers,
// headings and list items still recognisable, tables tab-separated.
func Plain(seg *doc.Segment, from, to int, o Options) Result {
	blocks := seg.Blocks
	if from < 0 {
		from = 0
	}
	if to > len(blocks) {
		to = len(blocks)
	}
	view := o.view()
	var out strings.Builder
	var res Result
	for i := from; i < to; i++ {
		b := blocks[i]
		line := plainBlock(seg, b, view, o)
		if res.Blocks > 0 && o.MaxChars > 0 && out.Len()+1+len(line) > o.MaxChars {
			res.Truncated, res.ContinueFrom = true, b.Handle
			break
		}
		if res.Blocks > 0 {
			out.WriteString("\n")
		}
		out.WriteString(line)
		res.Blocks++
	}
	res.Text = out.String()
	res.Chars = len(res.Text)
	return res
}

func plainBlock(seg *doc.Segment, b *doc.Block, view doc.View, o Options) string {
	prefix := ""
	if o.WithHandles {
		prefix = "[" + b.Handle + "] "
	}
	switch {
	case b.Paragraph != nil:
		p := b.Paragraph
		text := p.Text(view)
		if p.Bullet != nil {
			marker := "- "
			if p.Bullet.Ordered {
				marker = strconv.Itoa(ListNumber(seg, b)) + ". "
			}
			text = strings.Repeat("  ", p.Bullet.Nesting) + marker + text
		}
		if o.WithHandles && p.HeadingID != "" {
			text += " {" + p.HeadingID + "}"
		}
		return strings.TrimRight(prefix+text, " ")
	case b.Table != nil:
		return prefix + b.Text(view)
	case b.TOC != nil:
		return prefix + b.Text(view)
	case b.SectionBreak != nil:
		if o.WithHandles {
			return strings.TrimSpace(prefix)
		}
		return ""
	}
	return ""
}

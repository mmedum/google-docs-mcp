package render

import (
	"fmt"
	"strings"

	"github.com/mmedum/google-docs-mcp/internal/doc"
)

// Thread is a comment thread. It is shown twice: as one line in the
// footer of a read that marked it inline, and in full with its replies
// for list_comments. A view sets only the fields it wants shown — the
// footer leaves Created and Quote empty, because the read already shows
// the marked text where it sits.
type Thread struct {
	ID       string
	Handle   string
	Author   string
	Created  string
	Quote    string
	Content  string
	Resolved bool
	Deleted  bool
	Replies  []Reply
}

// Reply is one post in a thread after the first.
type Reply struct {
	Author  string
	Content string
	Created string
	Action  string // resolve or reopen
	Deleted bool
}

// CommentThreads lists whole threads, each with its replies under it.
// The caller writes the count line above.
func CommentThreads(threads []Thread) string {
	var sb strings.Builder
	for _, t := range threads {
		threadLine(&sb, t, "")
		sb.WriteString("\n")
		for _, r := range t.Replies {
			sb.WriteString("    ↳ ")
			if r.Author != "" {
				sb.WriteString(r.Author)
			} else {
				sb.WriteString("someone")
			}
			if r.Action != "" {
				sb.WriteString(" " + r.Action + "d")
			}
			if r.Created != "" {
				fmt.Fprintf(&sb, " (%s)", r.Created)
			}
			if r.Deleted {
				sb.WriteString(" [deleted]")
			}
			if r.Content != "" {
				sb.WriteString(": " + doc.OneLine(r.Content))
			}
			sb.WriteString("\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// threadLine writes a thread as one bullet: the thread, its handle, its
// author, when it was written, its state, the text it sits on and its
// first post — each part only when the view filled it in. prefix matches
// the inline marker where the read made one.
func threadLine(sb *strings.Builder, t Thread, prefix string) {
	fmt.Fprintf(sb, "- %s%s", prefix, t.ID)
	if t.Handle != "" {
		fmt.Fprintf(sb, " [%s]", t.Handle)
	}
	if t.Author != "" {
		sb.WriteString(" by " + t.Author)
	}
	if t.Created != "" {
		fmt.Fprintf(sb, " (%s)", t.Created)
	}
	switch {
	case t.Deleted:
		sb.WriteString(" [deleted]")
	case t.Resolved:
		sb.WriteString(" [resolved]")
	}
	if t.Quote != "" {
		fmt.Fprintf(sb, " on “%s”", doc.Clip(t.Quote, 60))
	}
	sb.WriteString(": " + doc.OneLine(t.Content))
}

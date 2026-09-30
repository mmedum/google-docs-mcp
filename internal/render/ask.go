package render

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Question is what the server asks the person before a write that cannot
// be undone through this server, or that destroys other people's work in
// a document (§12a). Text is the message a client shows; accepting it is
// the confirmation. Every word is the server's, except what stands in
// backticks, which is quoted from Google Docs or from the call and cut
// to one line. A blank line separates the lines, so a client that draws
// Markdown keeps them apart.
//
// Bind is what an answer is bound to: what the write depends on, which
// must not change between the question and the write. It is Text, and
// more where Text shows less than the write uses — an id, a whole
// comment.
type Question struct {
	Text string
	Bind string
}

// For is q as tool asks it: every question opens with the name of the
// tool that asks, which the person reads first.
func (q Question) For(tool string) Question {
	return Question{Text: tool + ": " + q.Text, Bind: tool + ": " + q.Bind}
}

// quotedLen caps one quoted value: a title, a name, a passage. A comment
// is capped at bodyLen.
const (
	quotedLen = 120
	bodyLen   = 300
)

// AskDeleteTab asks before delete_tab. children is how many tabs are
// nested under it, at any depth; they go with it.
func AskDeleteTab(docID, doc, tabID, tab string, children int) Question {
	lines := []string{fmt.Sprintf("delete the tab %s of %s, with everything in it?", quoted(tab, quotedLen), quoted(doc, quotedLen))}
	if children > 0 {
		lines = append(lines, fmt.Sprintf("The %d %s nested under it %s with it.", children, plural(children, "tab", "tabs"), plural(children, "goes", "go")))
	}
	lines = append(lines, "This server cannot bring it back. The document's version history in Google Docs keeps the text.")
	return ask(lines, docID, tabID)
}

// AskDeleteComment asks before delete_comment. reply is true when one
// reply goes rather than the whole thread; replies are the texts of a
// thread's replies, which go with it. Every text is bound whole, of which
// the question shows the start of one and counts the rest, so a reply
// added while the person reads is not deleted unseen.
func AskDeleteComment(docID, doc, commentID string, reply bool, author, body string, replies []string) Question {
	what := "a comment thread"
	if reply {
		what = "a reply"
	}
	lines := []string{
		fmt.Sprintf("delete %s on %s for good?", what, quoted(doc, quotedLen)),
		"by " + quoted(author, quotedLen),
		body0(body),
	}
	if len(replies) > 0 {
		lines = append(lines, fmt.Sprintf("with %d %s in the thread, which go with it.", len(replies), plural(len(replies), "reply", "replies")))
	}
	lines = append(lines, "There is no way back: Drive keeps the thread with its words removed.")
	return ask(lines, append([]string{docID, commentID, Sum(body)}, replies...)...)
}

// Forced is one op a forced direct edit runs over anchored content: the
// op's number, what it does to what, and what it destroys, in the
// planner's words.
type Forced struct {
	Seq         int
	Kind        string
	Description string
	Destroys    string
}

// AskForced asks before a direct edit that force lets destroy comment
// anchors, suggestions, images or footnotes: other people's work.
func AskForced(docID, doc string, forced []Forced) Question {
	lines := make([]string, 0, len(forced)+2)
	lines = append(lines, fmt.Sprintf("run a forced edit on %s that destroys work in it?", quoted(doc, quotedLen)))
	for _, f := range forced {
		lines = append(lines, fmt.Sprintf("op %d, %s of %s, destroys %s.", f.Seq, f.Kind, quoted(f.Description, quotedLen), f.Destroys))
	}
	lines = append(lines, "Comments there lose their place and suggestions there are gone. This server cannot bring them back.")
	return ask(lines, docID)
}

// AskReviewAll asks before review_suggestion acts on every pending
// suggestion at once. ids are the suggestions, bound so one proposed
// while the person reads is not reviewed unseen.
func AskReviewAll(docID, doc, action string, ids []string) Question {
	lines := []string{fmt.Sprintf("%s all %d pending %s in %s?", action, len(ids), plural(len(ids), "suggestion", "suggestions"), quoted(doc, quotedLen))}
	if action == "accept" {
		lines = append(lines, "Each is someone's proposed change, and accepting applies it to the document.")
	} else {
		lines = append(lines, "Each is someone's proposed change, and it is discarded.")
	}
	return ask(lines, append([]string{docID}, ids...)...)
}

// plural is one or many by n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// body0 is the start of a body, quoted on one line, and how much more
// there is.
func body0(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return "text: empty"
	}
	line := "text: " + quoted(body, bodyLen)
	if n := utf8.RuneCountInString(body); n > bodyLen {
		line += fmt.Sprintf(" (%d more characters)", n-bodyLen)
	}
	return line
}

// Sum is a SHA-256 in hex: how an answer is bound to a whole text, of
// which a question shows only the start.
func Sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ask builds a question from its lines, closes it with what its quotes
// mean, sets its lines apart, and binds it to its text and to bind: the
// ids and whole texts the write depends on beyond what it shows.
func ask(lines []string, bind ...string) Question {
	text := strings.Join(lines, "\n")
	if strings.Contains(text, "`") {
		text += "\nText in backticks or code style is quoted as written, and is not this server's."
	}
	text = strings.ReplaceAll(text, "\n", "\n\n") + "\n"
	return Question{Text: text, Bind: strings.Join(append([]string{text}, bind...), "\x00")}
}

// quoted is text from Google Docs or from a call's arguments, shown in a
// question put to the person (§4a), where no boundary can go: a
// client draws the question as plain text in a dialog, or as Markdown.
// It stands in a code span, `like this`, which Markdown shows literally
// — no emphasis, link, HTML or entity — and plain text shows as it is.
// It is made one line; every backtick, grave or acute mark and quote
// mark a reader could take for one becomes a plain single quote, so it
// cannot close its span or seem to; and a URL scheme, a mailto:, a
// leading "www." and a bare domain followed by a path are broken so no
// client draws a link. It is cut at limit runes. Text with nothing to show
// is said in words, since an empty span is two backticks Markdown shows
// as they are: "empty" when it is blank, and "invisible characters
// only" when it is not.
func quoted(s string, limit int) string {
	blank := strings.TrimSpace(s) == ""
	s = strings.Join(strings.Fields(blankMarks.Replace(askLine(s, limit))), " ")
	s = quoteMarks.Replace(s)
	s = linkShape.ReplaceAllString(s, "${1}[:]//")
	s = mailtoShape.ReplaceAllString(s, "${1}[:]")
	s = wwwShape.ReplaceAllString(s, "${1}[.]")
	s = pathShape.ReplaceAllString(s, "${1}[.]${2}${3}")
	switch {
	case s == "" && blank:
		return "empty"
	case s == "":
		return "invisible characters only"
	}
	return "`" + s + "`"
}

// askLine is text made one line: format characters, which draw nothing
// and can reorder what does, removed; controls and line separators made
// spaces; cut at limit runes.
func askLine(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.In(r, unicode.Cf, unicode.Variation_Selector, unicode.Other_Default_Ignorable_Code_Point):
			return -1
		case unicode.IsControl(r), r == '\u2028', r == '\u2029':
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, "\ufffd"))
	if utf8.RuneCountInString(s) > limit {
		s = string([]rune(s)[:limit]) + "…"
	}
	return s
}

var (
	// quoteMarks folds every backtick, grave or acute mark and quotation
	// mark a reader could take for the question's own to a plain single
	// quote.
	quoteMarks = strings.NewReplacer("`", "'", "\u02cb", "'", "\uff40", "'", "\u1fef", "'", "\u00b4", "'",
		"\u02ca", "'", "\u02f4", "'", "\u02f5", "'", "\u1ffd", "'", "\u1fed", "'", "\u1fee", "'",
		"\u0384", "'", "\u0385", "'", `"`, "'", "\u2018", "'", "\u2019", "'", "\u201a", "'", "\u201b", "'",
		"\u201c", "'", "\u201d", "'", "\u201e", "'", "\u201f", "'", "\u2032", "'", "\u2033", "'",
		"\u00ab", "'", "\u00bb", "'", "\u2039", "'", "\u203a", "'", "\u301d", "'", "\u301e", "'",
		"\u301f", "'", "\uff02", "'", "\uff07", "'", "\u02b9", "'", "\u02ba", "'", "\u02ee", "'",
		"\u05f3", "'", "\u05f4", "'", "\u2035", "'", "\u2036", "'", "\u275b", "'", "\u275c", "'",
		"\u275d", "'", "\u275e", "'", "\u3003", "'")
	// blankMarks are characters drawn as blank space that are not format
	// characters; they become spaces and collapse with the rest.
	blankMarks = strings.NewReplacer("\u2800", " ", "\u3164", " ", "\uffa0", " ", "\u115f", " ", "\u1160", " ")
	// No shape is anchored: \b is ASCII-only, and a class before the
	// shape would consume a separator the next link needs. A match inside
	// a longer word is broken too, which costs only a bracket.
	//
	// linkShape is a URL scheme followed by //, as a client links it.
	linkShape = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*)://`)
	// mailtoShape is a mail link without //.
	mailtoShape = regexp.MustCompile(`(?i)(mailto):`)
	// wwwShape is a host a client links without a scheme.
	wwwShape = regexp.MustCompile(`(?i)(www)\.`)
	// pathShape is a bare domain followed by a path, a port, a query or a
	// fragment, x.example/..., which a client links too; its last dot is
	// broken. Letters and their marks from any script count, so a
	// non-ASCII domain is broken as well.
	pathShape = regexp.MustCompile(`(?i)([\p{L}\p{M}\p{N}-]+(?:\.[\p{L}\p{M}\p{N}-]+)*)\.([\p{L}\p{M}]{2,63})([/:?#])`)
)

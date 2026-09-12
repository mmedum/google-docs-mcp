package doc

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/mmedum/google-docs-mcp/internal/gdocs"
)

// StyleChange is one pending suggested formatting change on a run, a
// paragraph or a cell: the suggestion's id and the properties it sets.
//
// Props are named as the API names them — bold, weightedFontFamily,
// foregroundColor — so a suggestion and an updateTextStyle fields mask
// can be compared without a translation table between them. They are
// read off the `json` tags for that reason: deriving them from the Go
// field names instead put `headingID` and `listID` where the API says
// `headingId` and `listId`, which is thirteen properties a fields mask
// would never have matched. PropList turns them into words.
type StyleChange struct {
	ID    string
	Props []string
}

// Props of every change in cs, flattened and de-duplicated. It answers
// "what does any pending suggestion already set here".
func Props(cs []StyleChange) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cs {
		for _, p := range c.Props {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// propWords are the API property names whose spelling says nothing to a
// person. Everything else is turned from camelCase into words, which
// covers the rest of the hundred and forty-odd of them: spaceAbove
// reads as "space above", keepLinesTogether as "keep lines together".
// An unmapped property keeps its API spelling rather than disappearing.
//
// The wording matches render.styleAnnotation, which prints the same
// properties for the committed style; a change to either should change
// both, or one passage of a read will call a thing by two names.
var propWords = map[string]string{
	"weightedFontFamily": "font",
	"foregroundColor":    "color",
	"backgroundColor":    "background",
	"fontSize":           "size",
	"baselineOffset":     "baseline",
	"namedStyleType":     "named style",
}

// PropList is props as a person reads them, comma-separated. Every
// caller wants the sentence rather than the slice, so the separator has
// one spelling.
func PropList(props []string) string {
	words := make([]string, 0, len(props))
	for _, p := range props {
		words = append(words, propLabel(p))
	}
	return strings.Join(words, ", ")
}

// propLabel is one property name as a person reads it. A nested path
// keeps its parts, so shading.backgroundColor reads "shading background".
func propLabel(prop string) string {
	parts := strings.Split(prop, ".")
	words := make([]string, 0, len(parts))
	for _, p := range parts {
		switch {
		case propWords[p] != "":
			words = append(words, propWords[p])
		default:
			if _, err := strconv.Atoi(p); err == nil {
				// An index from a repeated state: "nesting levels 0 …".
				words = append(words, p)
				continue
			}
			words = append(words, spaced(p))
		}
	}
	return strings.Join(words, " ")
}

// spaced turns camelCase into lower-case words.
func spaced(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte(' ')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// FormatSuggestion is one pending suggested formatting change, in
// document order — whether or not the same suggestion also inserts or
// deletes text, which one batch in suggest mode can do.
//
// It exists because a restyling on its own adds and removes nothing: it
// hangs off the run it restyles as a suggestedTextStyleChanges entry, so
// a walk over inserted and deleted ids — which is how suggestions were
// collected until #46 — sees no trace of it and reports a document
// holding one as holding none.
type FormatSuggestion struct {
	// ID is the suggestion id, the same id review_suggestion takes.
	ID string
	// Target is what the suggestion restyles, for a person: "text",
	// "paragraph", "bullet", "table cell", "table row", "list",
	// "inline object", "positioned object", "document style" or
	// "named styles".
	Target string
	// Props are the properties it sets, API-named as in StyleChange.
	Props []string
	// Handle is the block it sits in, and Text the restyled text, both
	// empty for a suggestion that belongs to no block (a list's
	// properties, the document's own style).
	Handle string
	Text   string
}

// textChanges reads the suggested text-style changes off any inline
// element. Every one of the ten that can carry them embeds
// gdocs.SuggestedStyle, so one signature covers all of them.
func textChanges(s gdocs.SuggestedStyle) []StyleChange {
	return changesOf(s.SuggestedTextStyleChanges)
}

// changesOf turns one of the API's suggestion maps into the model's
// list, in suggestion-id order. A map has no order and two people can
// suggest a change to the same run, so sorting is what keeps a read of
// the same revision identical twice running.
func changesOf[T any](m map[string]T) []StyleChange {
	if len(m) == 0 {
		return nil
	}
	out := make([]StyleChange, 0, len(m))
	for _, id := range slices.Sorted(maps.Keys(m)) {
		out = append(out, StyleChange{ID: id, Props: suggestedProps(m[id])})
	}
	return out
}

// suggestedProps lists the properties one entry of a suggestion map
// marks as part of the suggestion: boldSuggested becomes bold, and a
// nested state is prefixed with the thing it describes, as
// textStyle.bold.
//
// Every entry is a pair — the style, and the state naming which of its
// fields the suggestion sets — and this reads the state. The API reports
// the style as it would be once the suggestion is accepted, with every
// inherited property filled in, so a suggestion to turn on bold arrives
// carrying italic, underline, a size and a colour as well; the state is
// the only field that says which of them the person asked for.
//
// Reflection rather than a switch over the twenty-five state types and
// the hundred and forty-seven booleans between them: a list of field
// names written out by hand is a list that drifts from the API the first
// time Google adds a property, and drifts silently, because a property
// nobody mapped looks exactly like a property nobody suggested. What it
// relies on is the API's own naming convention, which the discovery
// document holds to without exception — every marked property is a bool
// named <property>Suggested, every nested one a <thing>SuggestionState.
// Finding the state field by that convention is also what lets the
// eleven call sites pass the pair and name nothing: an accessor per map
// could name the wrong field and compile, and a suggestion that reported
// no properties is the failure this whole change is about.
func suggestedProps(pair any) []string {
	v := reflect.ValueOf(pair)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	t := v.Type()
	for i := range t.NumField() {
		if strings.HasSuffix(t.Field(i).Name, "SuggestionState") {
			collect(v.Field(i), "", &out)
		}
	}
	return out
}

func collect(v reflect.Value, prefix string, out *[]string) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := range t.NumField() {
		f, fv := t.Field(i), v.Field(i)
		// The API's name for the property, not Go's name for the field.
		name := apiName(f)
		switch {
		case name == "":
		case f.Type.Kind() == reflect.Bool && strings.HasSuffix(name, "Suggested"):
			if fv.Bool() {
				*out = append(*out, prefix+strings.TrimSuffix(name, "Suggested"))
			}
		case f.Type.Kind() == reflect.Slice && strings.HasSuffix(name, "SuggestionStates"):
			// A repeated state: one per nesting level of a list, one per
			// named style. The index goes in the name because the state
			// gives its entries no other identity — a nesting level is
			// only ever known by its position.
			stem := prefix + strings.TrimSuffix(name, "SuggestionStates")
			for j := range fv.Len() {
				collect(fv.Index(j), fmt.Sprintf("%s.%d.", stem, j), out)
			}
		case strings.HasSuffix(name, "SuggestionState"):
			collect(fv, prefix+strings.TrimSuffix(name, "SuggestionState")+".", out)
		}
	}
}

// apiName is the field's name on the wire, or "" for a field the API
// does not send.
func apiName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if i := strings.IndexByte(tag, ','); i >= 0 {
		tag = tag[:i]
	}
	if tag == "-" {
		return ""
	}
	return tag
}

// formatSuggestions collects every pending formatting suggestion in the
// document, in document order, from the model for anything that sits in
// a block and from the wire response for the collections that do not:
// lists, objects and the document-wide styles are keyed by id at the top
// of the response, with no block to hang from.
func (d *Document) formatSuggestions(wire *gdocs.Document) []FormatSuggestion {
	var out []FormatSuggestion
	add := func(target, handle, text string, cs []StyleChange) {
		for _, c := range cs {
			if len(c.Props) == 0 {
				// A state with nothing set is a suggestion to change
				// nothing, and reporting one would be worse than missing it.
				continue
			}
			out = append(out, FormatSuggestion{ID: c.ID, Target: target, Props: c.Props, Handle: handle, Text: text})
		}
	}
	for _, b := range d.AllBlocks() {
		if p := b.Paragraph; p != nil {
			// Behind the check, not in the argument list: building the
			// paragraph's text walks every run, and doing it for every
			// paragraph of every document — which is what passing it as an
			// argument did — cost 46% of the time and 44% of the memory of
			// a parse, on documents with no suggestion in them at all.
			if len(p.StyleChanges) > 0 || len(p.BulletChanges) > 0 {
				text := OneLine(Clip(p.Text(ViewInline), 60))
				add("paragraph", b.Handle, text, p.StyleChanges)
				add("bullet", b.Handle, text, p.BulletChanges)
			}
			for _, r := range p.Runs {
				if len(r.StyleChanges) > 0 {
					add("text", b.Handle, OneLine(Clip(r.Text, 60)), r.StyleChanges)
				}
			}
		}
		if b.Table == nil {
			continue
		}
		for _, row := range b.Table.Cells {
			for _, c := range row {
				add("table cell", c.Handle, "", c.StyleChanges)
				add("table row", c.Handle, "", c.RowChanges)
			}
		}
	}
	if wire == nil {
		return out
	}
	for _, t := range gdocs.DocumentTabs(wire) {
		for _, id := range suggestedIn(t.Lists, func(v gdocs.List) int { return len(v.SuggestedListPropertiesChanges) }) {
			add("list", "", "", changesOf(t.Lists[id].SuggestedListPropertiesChanges))
		}
		for _, id := range suggestedIn(t.InlineObjects, func(v gdocs.InlineObject) int {
			return len(v.SuggestedInlineObjectPropertiesChanges)
		}) {
			add("inline object", "", "", changesOf(t.InlineObjects[id].SuggestedInlineObjectPropertiesChanges))
		}
		for _, id := range suggestedIn(t.PositionedObjects, func(v gdocs.PositionedObject) int {
			return len(v.SuggestedPositionedObjectPropertiesChanges)
		}) {
			add("positioned object", "", "", changesOf(t.PositionedObjects[id].SuggestedPositionedObjectPropertiesChanges))
		}
		add("document style", "", "", changesOf(t.SuggestedDocumentStyleChanges))
		add("named styles", "", "", changesOf(t.SuggestedNamedStylesChanges))
	}
	// A response without tabs content carries the same two on the document.
	add("document style", "", "", changesOf(wire.SuggestedDocumentStyleChanges))
	add("named styles", "", "", changesOf(wire.SuggestedNamedStylesChanges))
	return out
}

// suggestedIn is the sorted ids of the entries of m that carry a
// suggestion, counted by n. A document with five hundred images has
// five hundred entries and usually no suggestions, so the sort is over
// what was suggested rather than over what exists.
func suggestedIn[T any](m map[string]T, n func(T) int) []string {
	var ids []string
	for id, v := range m {
		if n(v) > 0 {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

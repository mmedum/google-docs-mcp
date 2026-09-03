package plan

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Named-range ops. A named range is the one anchor that survives an
// edit: Google moves it with the text it covers, where a handle is only
// valid for the revision it came from.
const (
	OpCreateNamedRange  OpKind = "create_named_range"
	OpDeleteNamedRange  OpKind = "delete_named_range"
	OpReplaceNamedRange OpKind = "replace_named_range"
)

// MaxNamedRangeName is the API's limit on a name.
const MaxNamedRangeName = 256

// NamedRangeParams name a range to create, delete or fill.
type NamedRangeParams struct {
	// Name is the range's name. Several ranges may share one, and every
	// one of them is deleted or filled together.
	Name string
	// ID names exactly one range instead, as a read reports it.
	ID string
	// Text is the plain text replace_named_range writes over the range.
	Text string
}

// validateNamedRange checks a named-range op on its own.
func validateNamedRange(op *Op) error {
	p := op.NamedRange
	switch op.Kind {
	case OpCreateNamedRange:
		if p.Name == "" {
			return fmt.Errorf("op %d: create_named_range needs a name", op.Seq)
		}
		if p.ID != "" {
			return fmt.Errorf("op %d: an id names an existing range; create_named_range takes only a name", op.Seq)
		}
	case OpDeleteNamedRange, OpReplaceNamedRange:
		if (p.Name == "") == (p.ID == "") {
			return fmt.Errorf("op %d: name the range by name or by id, not both", op.Seq)
		}
	}
	if n := utf8.RuneCountInString(p.Name); n > MaxNamedRangeName {
		return fmt.Errorf("op %d: a name is at most %d characters, got %d", op.Seq, MaxNamedRangeName, n)
	}
	if op.Kind == OpReplaceNamedRange && strings.ContainsAny(p.Text, "\n\v") {
		return fmt.Errorf("op %d: replace_named_range writes one run of text, so it cannot contain a newline", op.Seq)
	}
	return nil
}

// CreateNamedRange names a range so later calls can find it again.
func CreateNamedRange(name string, r Rng) json.RawMessage {
	return raw(map[string]any{"createNamedRange": map[string]any{"name": name, "range": r.json()}})
}

// DeleteNamedRange forgets a named range. The text it covered stays.
func DeleteNamedRange(p NamedRangeParams, tabID string) json.RawMessage {
	req := map[string]any{}
	if p.ID != "" {
		req["namedRangeId"] = p.ID
	} else {
		req["name"] = p.Name
	}
	// Without tabsCriteria a named-range request reaches every tab.
	if tabID != "" {
		req["tabsCriteria"] = map[string]any{"tabIds": []string{tabID}}
	}
	return raw(map[string]any{"deleteNamedRange": req})
}

// ReplaceNamedRangeContent writes text over every range of the name.
func ReplaceNamedRangeContent(p NamedRangeParams, tabID string) json.RawMessage {
	req := map[string]any{"text": p.Text}
	if p.ID != "" {
		req["namedRangeId"] = p.ID
	} else {
		req["namedRangeName"] = p.Name
	}
	if tabID != "" {
		req["tabsCriteria"] = map[string]any{"tabIds": []string{tabID}}
	}
	return raw(map[string]any{"replaceNamedRangeContent": req})
}

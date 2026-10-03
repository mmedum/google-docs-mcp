// Package doctest loads the synthetic document fixture for tests in
// several packages. The fixture is hand-written; it contains no content
// from any real document.
package doctest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mmedum/google-docs-mcp/v2/internal/doc"
	"github.com/mmedum/google-docs-mcp/v2/internal/gdocs"
)

// FixturePath returns the absolute path of testdata/sample.json.
func FixturePath(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate fixture")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "sample.json")
}

// RawFixture returns the fixture bytes.
func RawFixture(t testing.TB) []byte {
	t.Helper()
	data, err := os.ReadFile(FixturePath(t))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

// WireFixture decodes the fixture into wire types.
func WireFixture(t testing.TB) *gdocs.Document {
	t.Helper()
	var d gdocs.Document
	if err := json.Unmarshal(RawFixture(t), &d); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return &d
}

// Fixture parses the fixture into the model.
func Fixture(t testing.TB) *doc.Document {
	t.Helper()
	d, err := doc.Parse(WireFixture(t))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return d
}

// InsertionReset is the state Google files under a suggested insertion's
// own id when the inserted text's style is reset: every text property
// marked except link (seen live 2026-10-03). Paired with an empty style
// it means the text takes the inherited style.
func InsertionReset() *gdocs.TextStyleSuggestionState {
	return &gdocs.TextStyleSuggestionState{BoldSuggested: true, ItalicSuggested: true, UnderlineSuggested: true,
		StrikethroughSuggested: true, SmallCapsSuggested: true, BackgroundColorSuggested: true,
		ForegroundColorSuggested: true, FontSizeSuggested: true, WeightedFontFamilySuggested: true,
		BaselineOffsetSuggested: true}
}

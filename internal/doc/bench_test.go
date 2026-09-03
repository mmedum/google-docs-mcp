package doc_test

import (
	"encoding/json"
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/doc/doctest"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
)

func BenchmarkDecodeLarge(b *testing.B) {
	data := doctest.LargeJSON(doctest.DefaultLarge)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var d gdocs.Document
		if err := json.Unmarshal(data, &d); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseLarge(b *testing.B) {
	w := doctest.Large(doctest.DefaultLarge)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := doc.Parse(w); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStatsLarge(b *testing.B) {
	d, _ := doc.Parse(doctest.Large(doctest.DefaultLarge))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		d.Stats()
	}
}

func BenchmarkSectionsLarge(b *testing.B) {
	d, _ := doc.Parse(doctest.Large(doctest.DefaultLarge))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		d.Tabs[0].Body.Sections()
	}
}

func BenchmarkHeadingByIDLarge(b *testing.B) {
	d, _ := doc.Parse(doctest.Large(doctest.DefaultLarge))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, ok := d.HeadingByID("h.t1.s50.8"); !ok {
			b.Fatal("missing")
		}
	}
}

func BenchmarkAllBlocksLarge(b *testing.B) {
	d, _ := doc.Parse(doctest.Large(doctest.DefaultLarge))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		d.AllBlocks()
	}
}

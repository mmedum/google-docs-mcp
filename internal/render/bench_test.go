package render_test

import (
	"testing"

	"github.com/mmedum/google-docs-mcp/internal/doc/doctest"
	"github.com/mmedum/google-docs-mcp/internal/render"
)

func BenchmarkMarkdownWholeLarge(b *testing.B) {
	seg := doctest.LargeDoc(b, doctest.DefaultLarge).Tabs[0].Body
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		render.Markdown(seg, 0, len(seg.Blocks), render.Options{})
	}
}

func BenchmarkMarkdownBudgetedLarge(b *testing.B) {
	seg := doctest.LargeDoc(b, doctest.DefaultLarge).Tabs[0].Body
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		render.Markdown(seg, 0, len(seg.Blocks), render.Options{MaxChars: 20000, WithHandles: true})
	}
}

func BenchmarkMarkdownTailLarge(b *testing.B) {
	seg := doctest.LargeDoc(b, doctest.DefaultLarge).Tabs[0].Body
	from := len(seg.Blocks) - 200
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		render.Markdown(seg, from, len(seg.Blocks), render.Options{MaxChars: 20000, WithHandles: true, WithStyles: true, Suggestions: true})
	}
}

func BenchmarkPlainWholeLarge(b *testing.B) {
	seg := doctest.LargeDoc(b, doctest.DefaultLarge).Tabs[0].Body
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		render.Plain(seg, 0, len(seg.Blocks), render.Options{})
	}
}

func BenchmarkOutlineLarge(b *testing.B) {
	d := doctest.LargeDoc(b, doctest.DefaultLarge)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		render.Outline(d, render.OutlineData(d, nil))
	}
}

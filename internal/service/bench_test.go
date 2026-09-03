package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-docs-mcp/internal/doc"
	"github.com/mmedum/google-docs-mcp/internal/doc/doctest"
	"github.com/mmedum/google-docs-mcp/internal/gapi"
	"github.com/mmedum/google-docs-mcp/internal/gdocs"
	"github.com/mmedum/google-docs-mcp/internal/plan"
	"github.com/mmedum/google-docs-mcp/internal/render"
)

// largeAPI serves one pre-built wire document without decoding.
type largeAPI struct {
	fakeAPI
	wire *gdocs.Document
}

func (l *largeAPI) GetDocument(context.Context, string, gapi.GetOptions) (*gapi.DocumentResult, error) {
	return &gapi.DocumentResult{Document: l.wire}, nil
}

// largeService is a preview service over the large fixture whose clock
// never advances, so the cache serves every read after the first.
func largeService(b *testing.B, spec doctest.LargeSpec) (*Service, *Fetched) {
	b.Helper()
	svc := New(&largeAPI{wire: doctest.Large(spec)}, Options{Preview: true})
	fixed := time.Unix(1000, 0)
	svc.now = func() time.Time { return fixed }
	f, err := svc.Fetch(context.Background(), doctest.Large(doctest.LargeSpec{}).DocumentID)
	if err != nil {
		b.Fatal(err)
	}
	return svc, f
}

// uniqueSentence is the first sentence of a paragraph in the middle of
// the body, which the generator makes unique.
func uniqueSentence(f *Fetched) string {
	blocks := f.Doc.Tabs[0].Body.Blocks
	for i := len(blocks) / 2; i < len(blocks); i++ {
		if b := blocks[i]; b.Paragraph != nil && b.Paragraph.Level == 0 && b.Paragraph.Bullet == nil {
			return strings.SplitN(b.Text(doc.ViewCurrent), ". ", 2)[0] + "."
		}
	}
	return ""
}

func BenchmarkFetchFreshLarge(b *testing.B) {
	svc, f := largeService(b, doctest.DefaultLarge)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := svc.FetchFresh(context.Background(), f.Doc.ID); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRememberLarge(b *testing.B) {
	svc, f := largeService(b, doctest.DefaultLarge)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		svc.Remember(f)
	}
}

func BenchmarkReadSectionLarge(b *testing.B) {
	svc, f := largeService(b, doctest.DefaultLarge)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, err := svc.Read(context.Background(), ReadRequest{Document: f.Doc.ID, Scope: ReadScope{HeadingID: "h.t1.s25.4"}, Options: render.Options{WithHandles: true, MaxChars: DefaultMaxChars}})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadWithCommentsLarge(b *testing.B) {
	svc, f := largeService(b, doctest.DefaultLarge)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, err := svc.Read(context.Background(), ReadRequest{Document: f.Doc.ID, IncludeComments: true, Options: render.Options{MaxChars: DefaultMaxChars}})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLocateCommentsAnchoredLarge(b *testing.B) {
	svc, _ := largeService(b, doctest.DefaultLarge)
	benchLocate(b, svc)
}

func BenchmarkLocateCommentsQuotedLarge(b *testing.B) {
	spec := doctest.DefaultLarge
	spec.Anchored = false
	svc, _ := largeService(b, spec)
	benchLocate(b, svc)
}

func benchLocate(b *testing.B, svc *Service) {
	b.Helper()
	id := doctest.Large(doctest.LargeSpec{}).DocumentID
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		f, err := svc.FetchFresh(context.Background(), id)
		if err != nil {
			b.Fatal(err)
		}
		threads, err := svc.comments(context.Background(), f)
		if err != nil {
			b.Fatal(err)
		}
		located := 0
		for _, t := range threads {
			if t.Handle != "" {
				located++
			}
		}
		if located != len(threads) || located == 0 {
			b.Fatalf("located %d of %d", located, len(threads))
		}
	}
}

func BenchmarkResolveTextLarge(b *testing.B) {
	svc, f := largeService(b, doctest.DefaultLarge)
	needle := uniqueSentence(f)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := svc.ResolveTarget(f, Target{Text: needle}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFindLarge(b *testing.B) {
	svc, f := largeService(b, doctest.DefaultLarge)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := svc.Find(context.Background(), FindRequest{Document: f.Doc.ID, Query: "consectetur", Limit: 50}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFindRegexLarge(b *testing.B) {
	svc, f := largeService(b, doctest.DefaultLarge)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := svc.Find(context.Background(), FindRequest{Document: f.Doc.ID, Query: `Paragraph \d+7 `, Regex: true, Limit: 50}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAnchorsInLarge(b *testing.B) {
	svc, f := largeService(b, doctest.DefaultLarge)
	threads, _ := svc.comments(context.Background(), f)
	seg := f.Doc.Tabs[0].Body
	mid := seg.Blocks[len(seg.Blocks)/2]
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		f.anchorsIn(seg, mid.Start, mid.End+500, threads)
	}
}

func BenchmarkEditDryRunLarge(b *testing.B) {
	svc, f := largeService(b, doctest.DefaultLarge)
	needle := uniqueSentence(f)
	req := EditRequest{Document: f.Doc.ID, Mode: "direct", DryRun: true, Ops: []EditOp{
		{Kind: plan.OpReplace, Target: &Target{Text: needle}, Content: "A replacement sentence."},
		{Kind: plan.OpInsert, Location: &Location{At: "after", Of: &Target{HeadingID: "h.t1.s10.2", IncludeHeading: new(true)}}, Content: "Inserted paragraph."},
	}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := svc.Edit(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}

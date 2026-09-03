package tools

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-docs-mcp/internal/plan"
	"github.com/mmedum/google-docs-mcp/internal/service"
)

// ObjectInput is the insert_object call.
type ObjectInput struct {
	Document       string        `json:"document" jsonschema:"document id or any docs.google.com URL"`
	Kind           string        `json:"kind" jsonschema:"image, person, rich_link, or date"`
	Location       LocationInput `json:"location" jsonschema:"where the object goes: after or before a block, start or end of a paragraph, or end of the body"`
	URL            string        `json:"url,omitempty" jsonschema:"image: a publicly fetchable PNG, JPEG or GIF URL (under 50 MB, 25 megapixels); rich_link: the URL of a Google Drive file, Calendar event or YouTube video"`
	WidthPt        float64       `json:"width_pt,omitempty" jsonschema:"image: width in points; with only one dimension the aspect ratio is kept"`
	HeightPt       float64       `json:"height_pt,omitempty" jsonschema:"image: height in points"`
	Email          string        `json:"email,omitempty" jsonschema:"person: the email address of the person chip"`
	Name           string        `json:"name,omitempty" jsonschema:"person: display name shown on the chip"`
	Title          string        `json:"title,omitempty" jsonschema:"rich_link: display title (Google fills it from the resource when omitted)"`
	Date           string        `json:"date,omitempty" jsonschema:"date: the date as YYYY-MM-DD or an RFC 3339 timestamp"`
	TimeZone       string        `json:"time_zone,omitempty" jsonschema:"date: IANA time zone id such as Europe/Copenhagen; default UTC"`
	DateFormat     string        `json:"date_format,omitempty" jsonschema:"date: iso (2026-09-03), full (September 3, 2026), abbreviated (Sep 3, 2026, default), or month_day (Sep 3)"`
	WithTime       bool          `json:"with_time,omitempty" jsonschema:"date: also show the time"`
	Mode           string        `json:"mode,omitempty" jsonschema:"suggest, direct or comment; default from get_document capabilities"`
	DryRun         bool          `json:"dry_run,omitempty"`
	ExpectRevision string        `json:"expect_revision,omitempty"`
}

var dateFormats = map[string]string{
	"": "", "iso": "DATE_FORMAT_ISO8601", "full": "DATE_FORMAT_MONTH_DAY_FULL", "abbreviated": "DATE_FORMAT_MONTH_DAY_YEAR_ABBREVIATED", "month_day": "DATE_FORMAT_MONTH_DAY_ABBREVIATED",
}

func registerObjects(s *mcp.Server, d Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "insert_object",
		Description: "Insert a non-text object into a Google Doc at a location: an image from a public URL (optionally " +
			"sized in points), a person chip (email), a rich-link chip to a Google resource (Drive file, Calendar event, " +
			"YouTube video), or a date chip. Objects go inline in a paragraph; use edit_document to add surrounding " +
			"text. Same mode, dry_run and expect_revision semantics as edit_document; suggestion mode tracks the insertion.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ObjectInput) (*mcp.CallToolResult, *service.EditResult, error) {
		obj := &plan.ObjectParams{Kind: strings.ToLower(strings.TrimSpace(in.Kind)), URL: strings.TrimSpace(in.URL), WidthPt: in.WidthPt, HeightPt: in.HeightPt,
			Name: strings.TrimSpace(in.Name), Email: strings.TrimSpace(in.Email), Title: strings.TrimSpace(in.Title)}
		if obj.Kind == "date" {
			ts, err := parseDate(in.Date)
			if err != nil {
				return nil, nil, fail(err)
			}
			format, ok := dateFormats[strings.ToLower(strings.TrimSpace(in.DateFormat))]
			if !ok {
				return nil, nil, fail(service.Errorf("invalid", "date_format %q; use iso, full, abbreviated or month_day", in.DateFormat))
			}
			obj.Date = plan.DateSpec{Timestamp: ts, TimeZoneID: strings.TrimSpace(in.TimeZone), DateFormat: format}
			if in.WithTime {
				obj.Date.TimeFormat = "TIME_FORMAT_HOUR_MINUTE"
			}
		}
		op := service.EditOp{Kind: plan.OpInsertObject, Object: obj, Location: &service.Location{At: in.Location.At, Of: in.Location.Of.target()}}
		res, err := d.Service.Edit(ctx, service.EditRequest{Document: in.Document, Ops: []service.EditOp{op}, Mode: in.Mode, DryRun: in.DryRun, ExpectRevision: in.ExpectRevision})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(editText(res)), res, nil
	})
}

// parseDate accepts YYYY-MM-DD or RFC 3339 and returns RFC 3339 in UTC.
func parseDate(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", service.Errorf("invalid", "date is empty; pass YYYY-MM-DD or an RFC 3339 timestamp")
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.RFC3339), nil
		}
	}
	return "", service.Errorf("invalid", "date %q; use YYYY-MM-DD or an RFC 3339 timestamp", s)
}

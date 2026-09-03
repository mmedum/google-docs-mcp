package tools

import (
	"context"
	"strings"

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

func registerObjects(s *mcp.Server, d Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "insert_object",
		Description: "Insert a non-text object into a Google Doc at a location: an image from a public URL (optionally " +
			"sized in points), a person chip (email), a rich-link chip to a Google resource (Drive file, Calendar event, " +
			"YouTube video), or a date chip. Objects go inline in a paragraph; use edit_document to add surrounding " +
			"text. Same mode, dry_run and expect_revision semantics as edit_document; suggestion mode tracks the insertion.",
		Annotations: writeSafe,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ObjectInput) (*mcp.CallToolResult, *service.EditResult, error) {
		obj := &plan.ObjectParams{Kind: strings.ToLower(strings.TrimSpace(in.Kind)), URL: strings.TrimSpace(in.URL), WidthPt: in.WidthPt, HeightPt: in.HeightPt,
			Name: strings.TrimSpace(in.Name), Email: strings.TrimSpace(in.Email), Title: strings.TrimSpace(in.Title)}
		if obj.Kind == "date" {
			if strings.TrimSpace(in.Date) == "" {
				return nil, nil, fail(service.Errorf("invalid", "date is empty; pass YYYY-MM-DD or an RFC 3339 timestamp"))
			}
			ts, err := service.ParseTime(in.Date)
			if err != nil {
				return nil, nil, fail(service.Errorf("invalid", "date %q; use YYYY-MM-DD or an RFC 3339 timestamp", in.Date))
			}
			// The friendly format name is validated by the planner.
			obj.Date = plan.DateSpec{Timestamp: ts, TimeZoneID: strings.TrimSpace(in.TimeZone), DateFormat: strings.ToLower(strings.TrimSpace(in.DateFormat))}
			if in.WithTime {
				obj.Date.TimeFormat = "TIME_FORMAT_HOUR_MINUTE"
			}
		}
		op := service.EditOp{Kind: plan.OpInsertObject, Object: obj, Location: in.Location.location()}
		res, err := d.Service.Edit(ctx, service.EditRequest{Document: in.Document, Ops: []service.EditOp{op}, Mode: in.Mode, DryRun: in.DryRun, ExpectRevision: in.ExpectRevision})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(res.Text), res, nil
	})
}

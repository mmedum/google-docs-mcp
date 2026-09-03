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
	Action         string        `json:"action,omitempty" jsonschema:"insert (default), replace to swap an image's source in place, or delete to remove an object"`
	Kind           string        `json:"kind,omitempty" jsonschema:"insert: image, person, rich_link, or date"`
	Object         string        `json:"object,omitempty" jsonschema:"replace and delete: the object's id as a read shows it, e.g. kix.img1"`
	Crop           bool          `json:"crop,omitempty" jsonschema:"replace: centre-crop the new image into the old one's frame instead of resizing the frame to it"`
	Tab            string        `json:"tab,omitempty" jsonschema:"replace and delete: tab id, title or number; default the first tab"`
	Location       LocationInput `json:"location,omitempty" jsonschema:"insert: where the object goes — after or before a block, start or end of a paragraph, or end of the body"`
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
	Force          bool          `json:"force,omitempty" jsonschema:"delete in direct mode: remove an inline object even though a comment or suggestion sits on it; ask the person first"`
}

func registerObjects(s *mcp.Server, d Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "insert_object",
		Description: "Insert, replace or delete a non-text object in a Google Doc. action insert (default) adds an " +
			"image from a public URL (optionally sized in points), a person chip (email), a rich-link chip to a Google " +
			"resource (Drive file, Calendar event, YouTube video), or a date chip at a location; objects go inline in a " +
			"paragraph, so use edit_document for the surrounding text. action replace swaps an existing image's source " +
			"while it keeps its place and size (crop centre-crops the new image instead of resizing the frame). action " +
			"delete removes an object, including a floating image, which no text range covers and no edit_document " +
			"delete can reach. Both name the object by the id a read shows, like kix.img1. Same mode, dry_run, " +
			"expect_revision and force semantics as edit_document; deleting an inline object that also carries a " +
			"comment or a suggestion is refused in direct mode unless forced. Suggestion mode tracks the insertion.",
		Annotations: writeSafe,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ObjectInput) (*mcp.CallToolResult, *service.EditResult, error) {
		obj := &plan.ObjectParams{Kind: strings.ToLower(strings.TrimSpace(in.Kind)), URL: strings.TrimSpace(in.URL), WidthPt: in.WidthPt, HeightPt: in.HeightPt,
			Name: strings.TrimSpace(in.Name), Email: strings.TrimSpace(in.Email), Title: strings.TrimSpace(in.Title),
			ID: strings.TrimSpace(in.Object), Crop: in.Crop}
		switch strings.ToLower(strings.TrimSpace(in.Action)) {
		case "", "insert":
		case "replace", "delete":
			kind := plan.OpReplaceImage
			if strings.EqualFold(strings.TrimSpace(in.Action), "delete") {
				kind = plan.OpDeleteObject
			}
			op := service.EditOp{Kind: kind, Object: obj, Target: &service.Target{Tab: in.Tab}}
			res, err := d.Service.Edit(ctx, service.EditRequest{Document: in.Document, Ops: []service.EditOp{op}, Mode: in.Mode,
				DryRun: in.DryRun, ExpectRevision: in.ExpectRevision, Force: in.Force})
			if err != nil {
				return nil, nil, fail(err)
			}
			return text(res.Text), res, nil
		default:
			return nil, nil, fail(service.Errorf("invalid", "action %q; use insert, replace or delete", in.Action))
		}
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

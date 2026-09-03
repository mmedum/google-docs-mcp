package doc

// Stats are document-wide counts, computed over every tab's body.
type Stats struct {
	Tabs          int `json:"tabs"`
	Paragraphs    int `json:"paragraphs"`
	Headings      int `json:"headings"`
	Tables        int `json:"tables"`
	InlineObjects int `json:"inline_objects"`
	Footnotes     int `json:"footnotes"`
	Words         int `json:"words"`
	Chars         int `json:"chars"`
	Suggestions   int `json:"pending_suggestions"`
}

// Stats counts the document's content in the committed view.
func (d *Document) Stats() Stats {
	var st Stats
	st.Tabs = len(d.Tabs)
	suggestions := map[string]bool{}
	for _, t := range d.Tabs {
		st.Footnotes += len(t.Footnotes)
		for _, b := range t.Body.AllBlocks() {
			for _, id := range b.Inserted {
				suggestions[id] = true
			}
			for _, id := range b.Deleted {
				suggestions[id] = true
			}
			switch {
			case b.Paragraph != nil:
				st.Paragraphs++
				if b.IsHeading() {
					st.Headings++
				}
				words, chars := b.Paragraph.Count(ViewCurrent)
				st.Words += words
				st.Chars += chars
				for _, r := range b.Paragraph.Runs {
					if r.Kind == RunInlineObject && !r.IsSuggestedInsertion() {
						st.InlineObjects++
					}
					for _, id := range r.Inserted {
						suggestions[id] = true
					}
					for _, id := range r.Deleted {
						suggestions[id] = true
					}
				}
			case b.Table != nil && b.Cell == nil:
				st.Tables++
			}
		}
	}
	st.Suggestions = len(suggestions)
	return st
}

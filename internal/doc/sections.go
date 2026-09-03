package doc

// Section is a heading and the blocks it owns: everything up to the next
// heading of the same or a higher level. Indices are into Segment.Blocks;
// From is the heading itself, To is exclusive. A Section with a nil
// Heading is the preamble before the first heading.
type Section struct {
	Heading *Block
	Level   int
	From    int
	To      int
}

// Sections derives the heading tree of a segment. Only top-level
// paragraphs count; headings inside tables and tables of contents do not.
func (s *Segment) Sections() []Section {
	var out []Section
	blocks := s.Blocks
	for i, b := range blocks {
		if !b.IsHeading() {
			continue
		}
		level := b.Paragraph.Level
		to := len(blocks)
		for j := i + 1; j < len(blocks); j++ {
			if blocks[j].IsHeading() && blocks[j].Paragraph.Level <= level {
				to = j
				break
			}
		}
		out = append(out, Section{Heading: b, Level: level, From: i, To: to})
	}
	return out
}

// Preamble is the block range before the first heading.
func (s *Segment) Preamble() Section {
	for i, b := range s.Blocks {
		if b.IsHeading() {
			return Section{From: 0, To: i}
		}
	}
	return Section{From: 0, To: len(s.Blocks)}
}

// SectionByHeadingID finds a section by Google's stable heading id.
func (s *Segment) SectionByHeadingID(id string) (Section, bool) {
	if id == "" {
		return Section{}, false
	}
	for _, sec := range s.Sections() {
		if sec.Heading.Paragraph.HeadingID == id {
			return sec, true
		}
	}
	return Section{}, false
}

// SectionsByHeading finds sections whose heading text matches after
// normalisation, optionally restricted to a level (0 = any).
func (s *Segment) SectionsByHeading(text string, level int) []Section {
	want := Normalize(text)
	var out []Section
	for _, sec := range s.Sections() {
		if level > 0 && sec.Level != level {
			continue
		}
		if equalFold(Normalize(sec.Heading.Paragraph.Text(ViewInline)), want) {
			out = append(out, sec)
		}
	}
	return out
}

// HeadingByID searches every tab's body for a heading id.
func (d *Document) HeadingByID(id string) (*Tab, Section, bool) {
	for _, t := range d.Tabs {
		if sec, ok := t.Body.SectionByHeadingID(id); ok {
			return t, sec, true
		}
	}
	return nil, Section{}, false
}

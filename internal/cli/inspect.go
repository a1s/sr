package cli

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/a1s/sr/printout"
)

// cmdInspect dumps a printout as text.
func cmdInspect(env Env, args []string) error {
	var (
		pages   string
		summary bool
	)
	set := newFlags("inspect")
	stringFlag(set, &pages, "pages", "", "pages to dump, as 1,4-6,10-")
	boolFlag(set, &summary, "summary", "", "the header only")

	rest, err := parse(env, set, args)
	if err != nil {
		return err
	}
	switch {
	case len(rest) == 0:
		return usagef("inspect", "a printout file is required")
	case len(rest) > 1:
		return usagef("inspect", "one printout at a time; %q is a second", rest[1])
	case rest[0] == "-":
		return usagef("inspect",
			"a printout is read from a file, not from standard input: "+
				"the paths inside it resolve against the directory it came from")
	}
	wanted, err := parsePages(pages)
	if err != nil {
		return usageError{command: "inspect", message: err.Error()}
	}

	doc, err := printout.ReadFile(rest[0])
	if err != nil {
		return err
	}
	buffered := bufio.NewWriter(env.Out)
	dmp := &dumper{out: newStream(buffered)}
	dmp.header(rest[0], doc)
	if !summary {
		for _, page := range doc.Pages {
			if !wanted.matches(page.Number) {
				continue
			}
			dmp.page(doc, page)
		}
	}
	if dmp.out.err != nil {
		return dmp.out.err
	}
	if err := buffered.Flush(); err != nil {
		return err
	}
	// An inspector that read a broken printout without saying so would be
	// the wrong tool, so the invariants are checked whatever was dumped.
	if err := doc.Validate(); err != nil {
		return err
	}
	return nil
}

// pageSet is a parsed --pages list. An empty one matches every page.
type pageSet []pageRange

// pageRange is one item of a --pages list.
// A zero lo means "from the first page", a zero hi "to the last".
type pageRange struct {
	lo, hi int
}

// matches reports whether a page number was asked for.
func (set pageSet) matches(number int) bool {
	if len(set) == 0 {
		return true
	}
	for _, item := range set {
		if (item.lo == 0 || number >= item.lo) && (item.hi == 0 || number <= item.hi) {
			return true
		}
	}
	return false
}

// parsePages reads a --pages list: comma-separated numbers and ranges,
// where an open end means the first or the last page.
func parsePages(text string) (pageSet, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	var set pageSet
	for _, item := range strings.Split(text, ",") {
		item = strings.TrimSpace(item)
		lo, hi, dash := strings.Cut(item, "-")
		bounds := pageRange{}
		var err error
		if bounds.lo, err = pageNumber(lo); err != nil {
			return nil, fmt.Errorf("--pages %q: %w", item, err)
		}
		if !dash {
			if bounds.lo == 0 {
				return nil, fmt.Errorf(
					"--pages %q is not a page number or a range", item)
			}
			bounds.hi = bounds.lo
		} else if bounds.hi, err = pageNumber(hi); err != nil {
			return nil, fmt.Errorf("--pages %q: %w", item, err)
		}
		if bounds.lo != 0 && bounds.hi != 0 && bounds.hi < bounds.lo {
			return nil, fmt.Errorf("--pages %q counts backwards", item)
		}
		set = append(set, bounds)
	}
	return set, nil
}

// pageNumber reads one end of a range. An empty end is open, and reads 0.
func pageNumber(text string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	number, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", text)
	}
	if number < 1 {
		return 0, fmt.Errorf("pages count from 1, and %d does not", number)
	}
	return number, nil
}

// dumper writes the report of doc/cli.md.
type dumper struct {
	out *stream
}

// line writes one indented line.
func (dmp *dumper) line(indent, format string, args ...any) {
	dmp.out.line("%s%s", indent, fmt.Sprintf(format, args...))
}

// rows writes a padded table under the current heading.
func (dmp *dumper) rows(items [][]string) {
	for _, line := range rows(items) {
		dmp.line("  ", "%s", line)
	}
}

// header writes everything before the first page.
func (dmp *dumper) header(path string, doc *printout.Printout) {
	head := doc.Header
	dmp.line("", "printout %s", path)

	made := []string{
		fmt.Sprintf("format %d", head.SR),
		fmt.Sprintf("engine %q", head.Engine),
		"built " + head.Built,
	}
	if head.StrictFonts {
		made = append(made, "strict fonts")
	}
	dmp.line("  ", "%s", strings.Join(made, ", "))

	if head.Report != nil {
		about := []string{}
		if head.Report.Name != "" {
			about = append(about, "report "+strconv.Quote(head.Report.Name))
		}
		if head.Report.Version != "" {
			about = append(about, "version "+head.Report.Version)
		}
		if head.Report.Author != "" {
			about = append(about, "by "+head.Report.Author)
		}
		if len(about) > 0 {
			dmp.line("  ", "%s", strings.Join(about, " "))
		}
		if head.Report.Description != "" {
			dmp.line("  ", "%s", head.Report.Description)
		}
	}
	dmp.line("  ", "%s", describeGeometry(head.Page))

	counts := []string{count(len(doc.Pages), "page"), count(len(head.Fonts), "font")}
	if len(head.Data) > 0 {
		counts = append(counts, count(len(head.Data), "data blob"))
	}
	dmp.line("  ", "%s", strings.Join(counts, ", "))

	if len(head.GroupRuns) > 0 {
		dmp.line("", "groups")
		items := [][]string{}
		for _, name := range sortedKeys(head.GroupRuns) {
			items = append(items, []string{name,
				count(head.GroupRuns[name], "run"),
				count(head.GroupKeys[name], "key")})
		}
		dmp.rows(items)
	}
	if len(head.Fonts) > 0 {
		dmp.line("", "fonts")
		items := make([][]string, 0, len(head.Fonts))
		for _, entry := range head.Fonts {
			items = append(items, fontFields(entry))
		}
		dmp.rows(items)
	}
	if len(head.Data) > 0 {
		dmp.line("", "data")
		items := make([][]string, 0, len(head.Data))
		for _, name := range sortedKeys(head.Data) {
			items = append(items, append([]string{name}, blobFields(head.Data[name])...))
		}
		dmp.rows(items)
	}
	if len(head.Warnings) > 0 {
		dmp.line("", "warnings")
		for _, warning := range head.Warnings {
			dmp.line("  ", "%s", describeWarning(warning))
		}
	}
}

// blobFields renders a header data entry: how it is encoded and how big it is.
func blobFields(blob *printout.Blob) []string {
	if blob.Encoding == "base64" {
		raw, err := base64.StdEncoding.DecodeString(blob.Content)
		if err != nil {
			return []string{"base64",
				count(len(blob.Content), "character") + ", and it does not decode"}
		}
		return []string{"base64", count(len(raw), "byte")}
	}
	return []string{"text", count(len(blob.Content), "byte")}
}

// page writes a page and its marks.
//
// A page that runs at a geometry of its own -- which a subreport paginating
// itself produces -- says so in full, because a reader comparing a mark against
// the printable area needs the numbers that page was laid out against.
func (dmp *dumper) page(doc *printout.Printout, page *printout.Page) {
	geometry := page.Geometry(doc.Header.Page)
	if geometry != doc.Header.Page {
		dmp.line("", "page %d  %s  %s",
			page.Number, describeSize(geometry), count(len(page.Marks), "mark"))
	} else {
		dmp.line("", "page %d  %s x %s  %s",
			page.Number, num(geometry.Width), num(geometry.Height),
			count(len(page.Marks), "mark"))
	}
	for _, mark := range page.Marks {
		dmp.mark(mark, "  ")
	}
}

// mark writes one mark, and an xref's children under it.
//
// An xref is the only nesting a printout has, and its children carry page
// coordinates, so the indentation says what contains what and nothing else.
func (dmp *dumper) mark(mark printout.Mark, indent string) {
	switch typed := mark.(type) {
	case *printout.Text:
		parts := []string{
			"text", "box " + describeBox(typed.Box),
			"font " + typed.Font, "color " + typed.Color,
			"align " + typed.Align, "leading " + num(typed.Leading),
		}
		if typed.LastLineJustified {
			parts = append(parts, "last line justified")
		}
		dmp.line(indent, "%s", strings.Join(parts, "  "))
		for _, text := range typed.Lines {
			dmp.line(indent+"  ", "%s", strconv.Quote(text))
		}
	case *printout.Line:
		parts := []string{
			"line", "box " + describeBox(typed.Box),
			"width " + num(typed.Width), "dash " + typed.Dash,
			"color " + typed.Color,
		}
		if typed.Backslant {
			parts = append(parts, "backslant")
		}
		dmp.line(indent, "%s", strings.Join(parts, "  "))
	case *printout.Rectangle:
		parts := []string{"rectangle", "box " + describeBox(typed.Box)}
		if typed.Stroke != nil {
			parts = append(parts,
				"stroke "+*typed.Stroke, "width "+num(typed.Width), "dash "+typed.Dash)
		}
		if typed.Fill != nil {
			parts = append(parts, "fill "+*typed.Fill)
		}
		if typed.Radius != 0 {
			parts = append(parts, "radius "+num(typed.Radius))
		}
		dmp.line(indent, "%s", strings.Join(parts, "  "))
	case *printout.Image:
		parts := []string{"image", "box " + describeBox(typed.Box), typed.Type}
		if typed.Data != "" {
			parts = append(parts, "data "+typed.Data)
		}
		if typed.File != "" {
			parts = append(parts, "file "+typed.File)
		}
		if typed.Crop != nil {
			parts = append(parts, "crop "+describeBox(*typed.Crop))
		}
		dmp.line(indent, "%s", strings.Join(parts, "  "))
	case *printout.Barcode:
		parts := []string{
			"barcode", "box " + describeBox(typed.Box),
			typed.Type, strconv.Quote(typed.Value), "module " + num(typed.Module),
		}
		if typed.Vertical {
			parts = append(parts, "vertical")
		}
		if typed.Rows != nil {
			parts = append(parts, count(len(typed.Rows), "row"))
		} else {
			parts = append(parts, count(len(typed.Stripes), "stripe"))
		}
		dmp.line(indent, "%s", strings.Join(parts, "  "))
	case *printout.Outline:
		parts := []string{
			"outline", fmt.Sprintf("at %s,%s", num(typed.Left), num(typed.Top)),
			fmt.Sprintf("level %d", typed.Level), strconv.Quote(typed.Title),
		}
		if typed.Name != "" {
			parts = append(parts, "name "+typed.Name)
		}
		if typed.Closed {
			parts = append(parts, "closed")
		}
		dmp.line(indent, "%s", strings.Join(parts, "  "))
	case *printout.Xref:
		parts := []string{
			"xref", "box " + describeBox(typed.Box),
			typed.Type, strconv.Quote(typed.Target),
		}
		if typed.Caption != "" {
			parts = append(parts, "caption "+strconv.Quote(typed.Caption))
		}
		dmp.line(indent, "%s", strings.Join(parts, "  "))
		for _, child := range typed.Marks {
			dmp.mark(child, indent+"  ")
		}
	default:
		dmp.line(indent, "%s", mark.MarkKind())
	}
}

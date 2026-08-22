// Package printout is the document an engine produces and a renderer consumes:
// pages of absolutely-positioned marks in paint order.
//
// Nothing in a printout is evaluable. Every expression has been evaluated,
// every box resolved to absolute coordinates, every string wrapped to lines,
// every barcode encoded to stripe widths, every font resolved to a specific
// file. A renderer needs no data, no template, and no expression evaluator.
package printout

// Version is the format version this package reads and writes.
const Version = 1

// Printout is a whole document.
//
// The in-memory structure is the primary artifact: the engine hands it
// to a renderer directly, and serializes only when asked.
type Printout struct {
	Header Header
	Pages  []*Page
}

// Header is the printout's first line.
type Header struct {
	SR          int              `json:"sr"`
	Kind        string           `json:"kind"`
	Report      *ReportMeta      `json:"report,omitempty"`
	Built       string           `json:"built"`
	Engine      string           `json:"engine"`
	StrictFonts bool             `json:"strictFonts"`
	Pages       int              `json:"pages"`
	GroupRuns   map[string]int   `json:"groupRuns,omitempty"`
	GroupKeys   map[string]int   `json:"groupKeys,omitempty"`
	Page        PageGeometry     `json:"page"`
	Fonts       []FontEntry      `json:"fonts"`
	Data        map[string]*Blob `json:"data"`
	Warnings    []Warning        `json:"warnings,omitempty"`
}

// ReportMeta is what the template's report node carried.
// Omitted fields are absent, not null.
type ReportMeta struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	Author      string `json:"author,omitempty"`
}

// PageGeometry is a page's size and margins, in points.
type PageGeometry struct {
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
	LeftMargin   float64 `json:"leftMargin"`
	RightMargin  float64 `json:"rightMargin"`
	TopMargin    float64 `json:"topMargin"`
	BottomMargin float64 `json:"bottomMargin"`
}

// FontEntry records a font that was actually measured, and how it was found.
type FontEntry struct {
	Name      string `json:"name"`
	Size      int    `json:"size"`
	Bold      bool   `json:"bold"`
	Italic    bool   `json:"italic"`
	Underline bool   `json:"underline"`
	// Requested is the template's typeface. A font node that named
	// a file or data instead has no typeface to record, so this is
	// absent and ResolvedBy is "explicit".
	Requested string `json:"requested,omitempty"`
	// ResolvedFile is relative to the printout when the template named it,
	// and absolute -- as opened -- when the engine found it on the host.
	ResolvedFile string `json:"resolvedFile,omitempty"`
	// ResolvedData names a header data entry, for an embedded font.
	ResolvedData string `json:"resolvedData,omitempty"`
	ResolvedFace string `json:"resolvedFace"`
	ResolvedBy   string `json:"resolvedBy"`

	// absFile is the path as the engine resolved it, kept so that
	// serialization can rewrite it relative to wherever the printout
	// is being written. It is not part of the document.
	absFile string
}

// SetResolvedPath records where a template-named font was found. Provenance
// decides what the writer emits: a path the template named travels with
// the printout, one the engine discovered is written as it was opened.
func (entry *FontEntry) SetResolvedPath(abs string, templateNamed bool) {
	entry.ResolvedFile = abs
	if templateNamed {
		entry.absFile = abs
	}
}

// Blob is a shared piece of content, stored once however many marks read it.
type Blob struct {
	// Encoding is "base64" for binary, absent for literal text.
	Encoding string `json:"encoding,omitempty"`
	Content  string `json:"content"`
}

// Warning is a diagnostic about the document, recorded in the header
// so an affected printout is identifiable from the artifact.
type Warning struct {
	// Kind is "overflow", "glyph", or "font".
	Kind    string `json:"kind"`
	Node    string `json:"node,omitempty"`
	Record  int    `json:"record,omitempty"`
	Message string `json:"message"`
}

// Warning kinds.
const (
	WarnOverflow = "overflow"
	WarnGlyph    = "glyph"
	WarnFont     = "font"
)

// Page is one page of marks.
type Page struct {
	Kind   string  `json:"kind"`
	Number int     `json:"number"`
	Width  float64 `json:"width,omitempty"`
	Height float64 `json:"height,omitempty"`
	Marks  []Mark  `json:"marks"`
}

// Box is an absolute rectangle, with the top-left corner at x, y
// measured from the top-left of the page. Y grows downward.
type Box struct {
	Left   float64 `json:"x"`
	Top    float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Mark is one drawn thing.
type Mark interface {
	// MarkKind names the mark, matching its "kind" field.
	MarkKind() string
}

// Text is wrapped text. A renderer must not re-wrap it.
type Text struct {
	Kind              string   `json:"kind"`
	Box               Box      `json:"box"`
	Font              string   `json:"font"`
	Color             string   `json:"color"`
	Align             string   `json:"align"`
	Leading           float64  `json:"leading"`
	Lines             []string `json:"lines"`
	LastLineJustified bool     `json:"lastLineJustified,omitempty"`
}

// MarkKind names the mark.
func (text *Text) MarkKind() string { return "text" }

// Line runs from the box's top-left corner to its bottom-right,
// or bottom-left to top-right when Backslant is set.
type Line struct {
	Kind      string  `json:"kind"`
	Box       Box     `json:"box"`
	Width     float64 `json:"width"`
	Dash      string  `json:"dash"`
	Color     string  `json:"color"`
	Backslant bool    `json:"backslant"`
}

// MarkKind names the mark.
func (line *Line) MarkKind() string { return "line" }

// Rectangle is a box, stroked and filled independently.
// An absent Stroke means no outline is drawn regardless of Width;
// an absent Fill leaves the interior untouched.
type Rectangle struct {
	Kind   string  `json:"kind"`
	Box    Box     `json:"box"`
	Width  float64 `json:"width"`
	Dash   string  `json:"dash"`
	Stroke *string `json:"stroke,omitempty"`
	Fill   *string `json:"fill,omitempty"`
	Radius float64 `json:"radius,omitempty"`
}

// MarkKind names the mark.
func (rect *Rectangle) MarkKind() string { return "rectangle" }

// Image is a bitmap. Box is the final drawn rectangle and Crop names
// the source pixels that fill it, so a renderer needs no notion of fitting.
type Image struct {
	Kind string `json:"kind"`
	Box  Box    `json:"box"`
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	// File appears only for embed=#false.
	// The template named it, so it is written relative to the printout.
	File string `json:"file,omitempty"`
	Crop *Box   `json:"crop,omitempty"`

	// absFile is the path as the engine resolved it; not part of the document.
	absFile string
}

// SetFile records a referenced image's resolved location.
func (image *Image) SetFile(abs string) { image.absFile = abs; image.File = abs }

// MarkKind names the mark.
func (image *Image) MarkKind() string { return "image" }

// Barcode is encoded stripe geometry, in modules.
type Barcode struct {
	Kind     string  `json:"kind"`
	Box      Box     `json:"box"`
	Type     string  `json:"type"`
	Value    string  `json:"value"`
	Module   float64 `json:"module"`
	Vertical bool    `json:"vertical"`
	// Stripes is a flat array for a 1-D type;
	// Rows is an array of rows for a 2-D one.
	// Exactly one is present.
	Stripes []int   `json:"stripes,omitempty"`
	Rows    [][]int `json:"rows,omitempty"`
}

// MarkKind names the mark.
func (barcode *Barcode) MarkKind() string { return "barcode" }

// Outline is a document outline entry. It names a scroll position
// rather than a region, so it carries a point instead of a box.
type Outline struct {
	Kind   string  `json:"kind"`
	Title  string  `json:"title"`
	Level  int     `json:"level"`
	Name   string  `json:"name,omitempty"`
	Closed bool    `json:"closed"`
	Left   float64 `json:"x"`
	Top    float64 `json:"y"`
}

// MarkKind names the mark.
func (outline *Outline) MarkKind() string { return "outline" }

// Xref is a link region. Its nested marks use page coordinates,
// so a renderer can flatten them recursively and draw in one pass;
// the box is purely a hit region.
type Xref struct {
	Kind    string `json:"kind"`
	Box     Box    `json:"box"`
	Type    string `json:"type"`
	Target  string `json:"target"`
	Caption string `json:"caption,omitempty"`
	Marks   []Mark `json:"marks"`
}

// MarkKind names the mark.
func (xref *Xref) MarkKind() string { return "xref" }

// NewText builds a text mark with its kind set.
func NewText() *Text { return &Text{Kind: "text"} }

// NewLine builds a line mark with its kind set.
func NewLine() *Line { return &Line{Kind: "line"} }

// NewRectangle builds a rectangle mark with its kind set.
func NewRectangle() *Rectangle { return &Rectangle{Kind: "rectangle"} }

// NewImage builds an image mark with its kind set.
func NewImage() *Image { return &Image{Kind: "image"} }

// NewBarcode builds a barcode mark with its kind set.
func NewBarcode() *Barcode { return &Barcode{Kind: "barcode"} }

// NewOutline builds an outline mark with its kind set.
func NewOutline() *Outline { return &Outline{Kind: "outline"} }

// NewXref builds an xref mark with its kind set.
func NewXref() *Xref { return &Xref{Kind: "xref"} }

// AddWarning records a document warning in the header.
func (doc *Printout) AddWarning(kind, node string, record int, message string) {
	doc.Header.Warnings = append(doc.Header.Warnings, Warning{
		Kind: kind, Node: node, Record: record, Message: message,
	})
}

// HasWarning reports whether the header carries a warning of a kind.
func (doc *Printout) HasWarning(kind string) bool {
	for _, warning := range doc.Header.Warnings {
		if warning.Kind == kind {
			return true
		}
	}
	return false
}

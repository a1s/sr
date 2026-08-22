package fontres

import (
	"bytes"
	"fmt"

	"github.com/a1s/sr/internal/geom"
	gofont "github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
)

// LeadingRatio is the baseline-to-baseline distance
// as a multiple of the font size.
//
// A constant multiplier is predictable and font-independent,
// which is what a paginating engine wants: line spacing must
// not change when a typeface is substituted, because that changes
// what fits on a page.
//
// The font's own suggestion -- hhea ascent + descent + lineGap --
// is per-face and does exactly that.
const LeadingRatio = 1.2

// ResolvedBy names the step of the resolution chain that produced a face.
type ResolvedBy string

// Resolution steps, as the printout spells them.
const (
	ByExplicit   ResolvedBy = "explicit"
	ByHost       ResolvedBy = "host"
	ByAlias      ResolvedBy = "alias"
	BySubstitute ResolvedBy = "substitute"
)

// Face is a resolved font at a size,
// and the only thing the engine measures with.
type Face struct {
	// Name is the template's name for this font.
	Name string
	Size int

	Bold      bool
	Italic    bool
	Underline bool

	// Requested is the template's typeface,
	// absent when the template pinned a file or a data blob.
	Requested string

	ResolvedFile string
	ResolvedData string
	ResolvedFace string
	ResolvedBy   ResolvedBy

	font    *gofont.Font
	face    *gofont.Face
	upem    float64
	widths  map[rune]float64
	missing map[rune]bool
}

// newFace wraps a parsed font at a size.
func newFace(ft *gofont.Font) *Face {
	return &Face{
		font:    ft,
		face:    gofont.NewFace(ft),
		upem:    float64(ft.Upem()),
		widths:  map[rune]float64{},
		missing: map[rune]bool{},
	}
}

// parseFaceBytes reads a single face, or the first face of a collection.
func parseFaceBytes(data []byte) (*FaceInfo, error) {
	loaders, err := ot.NewLoaders(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if len(loaders) == 0 {
		return nil, fmt.Errorf("no faces in font")
	}
	return describe(loaders[0], "", 0)
}

// Advance returns a rune's advance width in points at the face's size.
//
// Advance means hmtx throughout: the engine does not kern and does not shape,
// so this is the only notion of advance it has, and a renderer must be told
// to match. A rune the font lacks measures the .notdef glyph, which is the
// visible empty box the specification asks for, and is recorded as missing.
func (face *Face) Advance(char rune) float64 {
	if width, ok := face.widths[char]; ok {
		return width
	}
	gid, ok := face.face.NominalGlyph(char)
	if !ok {
		face.missing[char] = true
		gid = 0
	}
	width := float64(face.face.HorizontalAdvance(gid)) * float64(face.Size) / face.upem
	face.widths[char] = width
	return width
}

// AdvanceUnits returns a rune's advance in font units,
// which is what a monospace check compares.
func (face *Face) AdvanceUnits(char rune) (float64, bool) {
	gid, ok := face.face.NominalGlyph(char)
	if !ok {
		return 0, false
	}
	return float64(face.face.HorizontalAdvance(gid)), true
}

// Width measures a string in points.
func (face *Face) Width(text string) float64 {
	var total float64
	for _, char := range text {
		total += face.Advance(char)
	}
	return geom.Round(total)
}

// Leading is the baseline-to-baseline distance.
func (face *Face) Leading() float64 { return geom.Round(LeadingRatio * float64(face.Size)) }

// MissingRunes lists the characters this face was asked for and does not have.
func (face *Face) MissingRunes() []rune {
	out := make([]rune, 0, len(face.missing))
	for char := range face.missing {
		out = append(out, char)
	}
	return out
}

// Key identifies a face for the printout's font table.
func (face *Face) Key() string {
	return fmt.Sprintf("%s/%d/%t/%t/%t",
		face.Name, face.Size, face.Bold, face.Italic, face.Underline)
}

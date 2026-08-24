package fontres

import (
	"bytes"
	"fmt"
	"sort"

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

	// ascender and descender are the face's own hhea metrics,
	// in font units, the descender positive downward.
	ascender, descender float64

	// index is the face's position inside the file it was parsed from.
	// It reaches the printout, so that a renderer opens the same face
	// out of a collection the engine measured.
	index int
}

// newFace wraps a described face.
func newFace(info *FaceInfo) *Face {
	return &Face{
		font:      info.Font,
		face:      gofont.NewFace(info.Font),
		upem:      float64(info.Font.Upem()),
		widths:    map[rune]float64{},
		missing:   map[rune]bool{},
		ascender:  info.Ascender,
		descender: info.Descender,
		index:     info.Index,
	}
}

// pickFace chooses a face inside a font file.
//
// One file usually holds one face and there is nothing to choose.
// A collection holds several, and then the declared style picks among them:
// the first face carrying exactly those bits, or the first face of all when
// none does, which leaves the mismatch for the caller to report.
//
// Style, not position. Face 0 of Menlo.ttc is the regular one, but that is
// not a rule collections keep, so asking for the regular face by index is
// asking for whichever face the file happens to begin with.
func pickFace(raw []byte, want Style) (*FaceInfo, error) {
	loaders, err := ot.NewLoaders(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if len(loaders) == 0 {
		return nil, fmt.Errorf("no faces in font")
	}
	var first *FaceInfo
	var firstErr error
	for at, ld := range loaders {
		info, err := describe(ld, "", at)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if first == nil {
			first = info
		}
		if info.Style == want {
			return info, nil
		}
	}
	if first == nil {
		return nil, firstErr
	}
	return first, nil
}

// Load parses a font file for measuring, outside the resolution chain.
//
// A renderer takes this path: the printout already names the file and the
// face inside it, so there is nothing left to resolve, and measuring
// through the same code as the engine is what keeps the two agreeing.
func Load(raw []byte, index int, size int) (*Face, error) {
	loaders, err := ot.NewLoaders(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(loaders) {
		return nil, fmt.Errorf("face %d of a font holding %d", index, len(loaders))
	}
	info, err := describe(loaders[index], "", index)
	if err != nil {
		return nil, err
	}
	face := newFace(info)
	face.Size = size
	face.ResolvedFace = info.Family
	return face, nil
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
// The order is by code point rather than the map's, so that a font missing
// more than one glyph produces the same warning sequence on every run.
func (face *Face) MissingRunes() []rune {
	out := make([]rune, 0, len(face.missing))
	for char := range face.missing {
		out = append(out, char)
	}
	sort.Slice(out, func(one, two int) bool { return out[one] < out[two] })
	return out
}

// Key identifies a face for the printout's font table.
func (face *Face) Key() string {
	return fmt.Sprintf("%s/%d/%t/%t/%t",
		face.Name, face.Size, face.Bold, face.Italic, face.Underline)
}

// FaceIndex is the face's position inside the file it was parsed from,
// which is 0 for every file that is not a collection.
//
// The printout carries it so that a renderer, handed nothing
// but the document, opens the same face out of a collection
// that the engine measured with.
func (face *Face) FaceIndex() int { return face.index }

// Upem is the face's units per em, the denominator every font-unit
// metric below is expressed in.
func (face *Face) Upem() float64 { return face.upem }

// Glyph maps a rune to a glyph index, reporting whether the font has it.
//
// The mapping comes from the same cmap the engine measured through,
// which is what keeps a renderer's glyph choice and the printout's
// metrics describing one font.
func (face *Face) Glyph(char rune) (uint16, bool) {
	gid, ok := face.face.NominalGlyph(char)
	return uint16(gid), ok
}

// GlyphAdvance returns a glyph's advance in font units.
func (face *Face) GlyphAdvance(gid uint16) float64 {
	return float64(face.face.HorizontalAdvance(gofont.GID(gid)))
}

// VerticalMetrics returns the ascender and descender in font units,
// the descender positive downward.
//
// They position the baseline inside the leading the printout carries;
// they do not decide the leading, which is a constant multiple of the size.
func (face *Face) VerticalMetrics() (ascender, descender float64) {
	if face.ascender+face.descender > 0 {
		return face.ascender, face.descender
	}
	// A face without an hhea table is pathological; split the em the way
	// the common Latin face does rather than pile everything above the
	// baseline.
	return 0.8 * face.upem, 0.2 * face.upem
}

// UnderlineMetrics returns the underline's offset below the baseline
// and its thickness, in font units.
func (face *Face) UnderlineMetrics() (offset, thickness float64) {
	offset = -float64(face.face.LineMetric(gofont.UnderlinePosition))
	thickness = float64(face.face.LineMetric(gofont.UnderlineThickness))
	if thickness <= 0 {
		thickness = face.upem / 20
	}
	if offset <= 0 {
		offset = face.upem / 10
	}
	return offset, thickness
}

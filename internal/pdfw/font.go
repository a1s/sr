package pdfw

import (
	"fmt"
	"hash/fnv"
	"strings"
	"unicode/utf16"

	"github.com/a1s/sr/internal/sfnt"
)

// Embedded is one font as it will appear in the file:
// a font program, the glyphs the document actually uses,
// and how a reader is to read them back.
//
// Glyphs holds original glyph indices, and a glyph's position in the
// slice is its index in the embedded subset, which under Identity-H
// encoding is also the two-byte code a content stream shows. Position 0
// must be glyph 0, the empty box a font draws for a character it lacks.
type Embedded struct {
	Program *sfnt.Font
	Glyphs  []uint16
	// Advances are the glyphs' advance widths in font units,
	// parallel to Glyphs. They are supplied rather than read
	// back out of the program so that the widths a reader
	// advances the pen by are the widths the engine measured.
	Advances []float64
	// Runes maps a subset glyph index to the characters it stands for,
	// which is what makes the text selectable, searchable, and readable
	// back out of the file.
	Runes map[uint16][]rune

	Italic bool
}

// WriteFont writes a font's five objects and returns the number of the
// Type0 dictionary, which is what a page's resource dictionary names.
func (doc *Doc) WriteFont(embedded *Embedded) (int, error) {
	if len(embedded.Glyphs) == 0 || embedded.Glyphs[0] != 0 {
		return 0, fmt.Errorf("a subset must open with glyph 0")
	}
	if len(embedded.Advances) != len(embedded.Glyphs) {
		return 0, fmt.Errorf("%d glyphs against %d advances",
			len(embedded.Glyphs), len(embedded.Advances))
	}
	program := embedded.Program
	raw, err := program.Subset(embedded.Glyphs)
	if err != nil {
		return 0, err
	}

	base := subsetTag(program.PostScriptName, embedded.Glyphs) +
		"+" + program.PostScriptName

	type0 := doc.Alloc()
	cidFont := doc.Alloc()
	descriptor := doc.Alloc()
	fontFile := doc.Alloc()
	toUnicode := doc.Alloc()

	doc.Object(type0, fmt.Sprintf(
		"<</Type /Font /Subtype /Type0 /BaseFont /%s /Encoding /Identity-H"+
			" /DescendantFonts [%s] /ToUnicode %s>>",
		base, Ref(cidFont), Ref(toUnicode)))

	doc.Object(cidFont, fmt.Sprintf(
		"<</Type /Font /Subtype /CIDFontType2 /BaseFont /%s"+
			" /CIDSystemInfo <</Registry (Adobe) /Ordering (Identity) /Supplement 0>>"+
			" /FontDescriptor %s /DW 0 /W [0 [%s]] /CIDToGIDMap /Identity>>",
		base, Ref(descriptor), embedded.widthArray()))

	doc.Object(descriptor, embedded.descriptor(base, fontFile))
	doc.Stream(fontFile, fmt.Sprintf("/Length1 %d", len(raw)), raw)
	doc.Stream(toUnicode, "", []byte(embedded.toUnicodeCMap()))
	return type0, nil
}

// widthArray formats the advances in the thousandths of an em
// a PDF font dictionary measures them in.
//
// Three decimals rather than the customary whole number: the values are
// then exact for the usual units per em, and the pen inside a shown
// string lands where the engine measured it to well under a thousandth
// of a point. Rounding to whole thousandths of an em, which is what the
// writers surveyed in doc/decisions.md all do, is what leaves a
// hundredth-of-a-point drift across a long line.
func (embedded *Embedded) widthArray() string {
	scale := 1000 / embedded.Program.Upem
	parts := make([]string, len(embedded.Advances))
	for index, advance := range embedded.Advances {
		parts[index] = Num(advance * scale)
	}
	return strings.Join(parts, " ")
}

// descriptor writes the font descriptor, its metrics scaled
// to the thousandths of an em PDF expresses them in.
func (embedded *Embedded) descriptor(base string, fontFile int) string {
	program := embedded.Program
	scale := 1000 / program.Upem

	capHeight := float64(program.CapHeight)
	if capHeight <= 0 {
		capHeight = float64(program.Ascender)
	}

	// Symbolic, because an Identity-H subset carries
	// no standard encoding for a reader to reason about.
	flags := 4
	if program.FixedPitch {
		flags |= 1
	}
	if embedded.Italic || program.ItalicAngle != 0 {
		flags |= 1 << 6
	}
	// StemV has no table to come from, and a reader uses it
	// only when it has to substitute the font entirely.
	// The two values are the conventional nominal ones.
	stemV := 80
	if program.WeightClass >= 600 {
		stemV = 120
	}

	return fmt.Sprintf(
		"<</Type /FontDescriptor /FontName /%s /Flags %d"+
			" /FontBBox [%s %s %s %s] /ItalicAngle %s"+
			" /Ascent %s /Descent %s /CapHeight %s /StemV %d /FontFile2 %s>>",
		base, flags,
		Num(float64(program.XMin)*scale), Num(float64(program.YMin)*scale),
		Num(float64(program.XMax)*scale), Num(float64(program.YMax)*scale),
		Num(program.ItalicAngle),
		Num(float64(program.Ascender)*scale), Num(float64(program.Descender)*scale),
		Num(capHeight*scale), stemV, Ref(fontFile))
}

// subsetTag builds the six upper-case letters PDF requires
// in front of an embedded subset's font name.
//
// It is derived from the face and the glyphs it carries,
// so two subsets of one font get different tags, and the
// same subset gets the same tag on every run.
func subsetTag(name string, glyphs []uint16) string {
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(name))
	for _, glyph := range glyphs {
		_, _ = sum.Write([]byte{byte(glyph >> 8), byte(glyph)})
	}
	value := sum.Sum64()
	tag := make([]byte, 6)
	for index := range tag {
		tag[index] = byte('A' + value%26)
		value /= 26
	}
	return string(tag)
}

// bfCharsPerBlock is the limit PDF puts on one bfchar block.
const bfCharsPerBlock = 100

// toUnicodeCMap maps each subset glyph index back to the characters
// it stands for, which is what lets the text be selected, searched,
// and read back out of the file.
func (embedded *Embedded) toUnicodeCMap() string {
	var entries []string
	for index := range embedded.Glyphs {
		cid := uint16(index)
		runes := embedded.Runes[cid]
		if len(runes) == 0 {
			continue
		}
		var units strings.Builder
		for _, unit := range utf16.Encode(runes) {
			fmt.Fprintf(&units, "%04X", unit)
		}
		entries = append(entries, fmt.Sprintf("<%04X> <%s>", cid, units.String()))
	}

	var out strings.Builder
	out.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n")
	out.WriteString("/CIDSystemInfo <</Registry (Adobe) /Ordering (UCS) /Supplement 0>> def\n")
	out.WriteString("/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n")
	out.WriteString("1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")
	for start := 0; start < len(entries); start += bfCharsPerBlock {
		end := start + bfCharsPerBlock
		if end > len(entries) {
			end = len(entries)
		}
		fmt.Fprintf(&out, "%d beginbfchar\n", end-start)
		for _, entry := range entries[start:end] {
			out.WriteString(entry)
			out.WriteString("\n")
		}
		out.WriteString("endbfchar\n")
	}
	out.WriteString("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")
	return out.String()
}

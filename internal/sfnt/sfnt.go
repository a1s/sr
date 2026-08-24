// Package sfnt reads the font tables an embedded PDF font needs
// and builds a subset containing only the glyphs a document uses.
//
// It reads tables and moves bytes. It does not map characters to glyphs:
// that mapping is the engine's, through the metrics it measured with,
// and a second implementation of it in the renderer is how a renderer
// comes to draw glyphs the printout never measured.
package sfnt

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// Font is a parsed font file: its tables, and the values
// a PDF font descriptor asks for.
type Font struct {
	// Tables holds the raw table data, by four-character tag.
	Tables map[string][]byte

	// TrueType reports whether the outlines are in a glyf table.
	// A font with PostScript outlines carries CFF instead and cannot
	// be embedded as the TrueType font program this package writes.
	TrueType bool

	Upem      float64
	NumGlyphs int

	XMin, YMin, XMax, YMax int16
	// Ascender is positive and Descender negative,
	// whichever way the face wrote them. See readHhea.
	Ascender, Descender int16
	CapHeight           int16
	ItalicAngle         float64
	FixedPitch          bool
	WeightClass         uint16

	// PostScriptName is name ID 6, which is the name a PDF
	// font dictionary carries, falling back to the family
	// with its spaces removed the way a PostScript name is formed.
	PostScriptName string

	indexToLocFormat int16
	// locations are the glyf offsets, NumGlyphs+1 of them,
	// already scaled out of the short format's two-byte units.
	locations []uint32
}

// Parse reads a font file, or one face of a collection.
func Parse(raw []byte, index int) (*Font, error) {
	offset, err := faceOffset(raw, index)
	if err != nil {
		return nil, err
	}
	tables, err := readTableDirectory(raw, offset)
	if err != nil {
		return nil, err
	}
	font := &Font{Tables: tables}
	_, hasGlyf := tables["glyf"]
	_, hasCFF := tables["CFF "]
	_, hasCFF2 := tables["CFF2"]
	font.TrueType = hasGlyf && !hasCFF && !hasCFF2

	if err := font.readHead(); err != nil {
		return nil, err
	}
	if err := font.readMaxp(); err != nil {
		return nil, err
	}
	font.readHhea()
	font.readOS2()
	font.readPost()
	font.readName()
	if font.TrueType {
		if err := font.readLoca(); err != nil {
			return nil, err
		}
	}
	return font, nil
}

// faceOffset finds a face's table directory
//
// A plain font has one at the start of the file, a collection an offset per face.
func faceOffset(raw []byte, index int) (uint32, error) {
	if len(raw) < 12 {
		return 0, fmt.Errorf("a font file of %d bytes", len(raw))
	}
	if string(raw[:4]) != "ttcf" {
		if index != 0 {
			return 0, fmt.Errorf("face %d of a file holding one face", index)
		}
		return 0, nil
	}
	count := int(binary.BigEndian.Uint32(raw[8:12]))
	if index < 0 || index >= count {
		return 0, fmt.Errorf("face %d of a collection holding %d", index, count)
	}
	at := 12 + 4*index
	if at+4 > len(raw) {
		return 0, fmt.Errorf("the collection's face table is truncated")
	}
	return binary.BigEndian.Uint32(raw[at : at+4]), nil
}

func readTableDirectory(raw []byte, offset uint32) (map[string][]byte, error) {
	base := int(offset)
	if base+12 > len(raw) {
		return nil, fmt.Errorf("the table directory lies past the end of the file")
	}
	count := int(binary.BigEndian.Uint16(raw[base+4 : base+6]))
	tables := make(map[string][]byte, count)
	for entry := 0; entry < count; entry++ {
		at := base + 12 + entry*16
		if at+16 > len(raw) {
			return nil, fmt.Errorf("the table directory is truncated")
		}
		tag := string(raw[at : at+4])
		start := int(binary.BigEndian.Uint32(raw[at+8 : at+12]))
		length := int(binary.BigEndian.Uint32(raw[at+12 : at+16]))
		if start < 0 || length < 0 || start > len(raw) {
			return nil, fmt.Errorf("table %q lies past the end of the file", tag)
		}
		end := start + length
		if end > len(raw) {
			// A last table padded past the end of the file is common
			// enough to accept rather than reject the whole font.
			end = len(raw)
		}
		tables[tag] = raw[start:end]
	}
	return tables, nil
}

func (font *Font) readHead() error {
	head := font.Tables["head"]
	if len(head) < 54 {
		return fmt.Errorf("head: a table of %d bytes", len(head))
	}
	font.Upem = float64(binary.BigEndian.Uint16(head[18:20]))
	if font.Upem == 0 {
		return fmt.Errorf("head: units per em is zero")
	}
	font.XMin = int16(binary.BigEndian.Uint16(head[36:38]))
	font.YMin = int16(binary.BigEndian.Uint16(head[38:40]))
	font.XMax = int16(binary.BigEndian.Uint16(head[40:42]))
	font.YMax = int16(binary.BigEndian.Uint16(head[42:44]))
	font.indexToLocFormat = int16(binary.BigEndian.Uint16(head[50:52]))
	return nil
}

func (font *Font) readMaxp() error {
	maxp := font.Tables["maxp"]
	if len(maxp) < 6 {
		return fmt.Errorf("maxp: a table of %d bytes", len(maxp))
	}
	font.NumGlyphs = int(binary.BigEndian.Uint16(maxp[4:6]))
	return nil
}

// readHhea reads the vertical extent, signed as PDF wants it: the
// ascender above the baseline positive and the descender below it
// negative, which is the descriptor's convention and the table's own.
//
// The signs are normalised rather than copied because files carry both
// conventions -- a descender stored positive is not rare -- and this pair
// goes straight into /Ascent and /Descent, where a positive descent puts
// the bottom of the face above its baseline. The other reader of this
// table normalises too, in fontres.hheaMetrics, to its own sign rule.
func (font *Font) readHhea() {
	hhea := font.Tables["hhea"]
	if len(hhea) < 36 {
		return
	}
	font.Ascender = absInt16(int16(binary.BigEndian.Uint16(hhea[4:6])))
	font.Descender = -absInt16(int16(binary.BigEndian.Uint16(hhea[6:8])))
}

// absInt16 is the magnitude of a metric, clamped where a face states the
// one value a signed 16-bit magnitude cannot hold.
func absInt16(value int16) int16 {
	switch {
	case value == math.MinInt16:
		return math.MaxInt16
	case value < 0:
		return -value
	}
	return value
}

func (font *Font) readOS2() {
	os2 := font.Tables["OS/2"]
	if len(os2) < 6 {
		return
	}
	font.WeightClass = binary.BigEndian.Uint16(os2[4:6])
	// sCapHeight arrives with version 2 of the table.
	if binary.BigEndian.Uint16(os2[0:2]) >= 2 && len(os2) >= 90 {
		font.CapHeight = int16(binary.BigEndian.Uint16(os2[88:90]))
	}
}

func (font *Font) readPost() {
	post := font.Tables["post"]
	if len(post) < 16 {
		return
	}
	// italicAngle is a 16.16 fixed point number.
	font.ItalicAngle = float64(int32(binary.BigEndian.Uint32(post[4:8]))) / 65536
	font.FixedPitch = binary.BigEndian.Uint32(post[12:16]) != 0
}

func (font *Font) readName() {
	names := ParseNames(font.Tables["name"])
	if name := names[6]; name != "" {
		font.PostScriptName = sanitize(name)
		return
	}
	family := names[16]
	if family == "" {
		family = names[1]
	}
	font.PostScriptName = sanitize(strings.ReplaceAll(family, " ", ""))
}

// sanitize keeps only the characters a PDF name may carry unescaped
//
// It never returns the empty string, since a font dictionary needs a name.
func sanitize(name string) string {
	var out strings.Builder
	for _, char := range name {
		if char <= ' ' || char > '~' {
			continue
		}
		switch char {
		case '/', '%', '(', ')', '<', '>', '[', ']', '{', '}', '#':
			continue
		}
		out.WriteRune(char)
	}
	if out.Len() == 0 {
		return "Unknown"
	}
	return out.String()
}

func (font *Font) readLoca() error {
	loca := font.Tables["loca"]
	count := font.NumGlyphs + 1
	font.locations = make([]uint32, count)
	if font.indexToLocFormat == 0 {
		if len(loca) < count*2 {
			return fmt.Errorf("loca: %d bytes for %d short entries", len(loca), count)
		}
		for index := 0; index < count; index++ {
			font.locations[index] = uint32(
				binary.BigEndian.Uint16(loca[index*2:index*2+2])) * 2
		}
		return nil
	}
	if len(loca) < count*4 {
		return fmt.Errorf("loca: %d bytes for %d long entries", len(loca), count)
	}
	for index := 0; index < count; index++ {
		font.locations[index] = binary.BigEndian.Uint32(loca[index*4 : index*4+4])
	}
	return nil
}

// GlyphData returns one glyph's outline data, empty for a blank glyph.
func (font *Font) GlyphData(gid uint16) []byte {
	index := int(gid)
	if index+1 >= len(font.locations) {
		return nil
	}
	start, end := font.locations[index], font.locations[index+1]
	glyf := font.Tables["glyf"]
	if end <= start || int(end) > len(glyf) {
		return nil
	}
	return glyf[start:end]
}

// Advance returns a glyph's advance width in font units, read from hmtx.
//
// The last entry of hmtx covers every glyph after it, which is how
// a font with a run of equal-width glyphs at the end saves space.
func (font *Font) Advance(gid uint16) uint16 {
	hmtx := font.Tables["hmtx"]
	metrics := font.numberOfHMetrics()
	if metrics == 0 || len(hmtx) < 4 {
		return 0
	}
	index := int(gid)
	if index >= metrics {
		index = metrics - 1
	}
	if (index+1)*4 > len(hmtx) {
		return 0
	}
	return binary.BigEndian.Uint16(hmtx[index*4 : index*4+2])
}

// SideBearing returns a glyph's left side bearing in font units.
func (font *Font) SideBearing(gid uint16) int16 {
	hmtx := font.Tables["hmtx"]
	metrics := font.numberOfHMetrics()
	index := int(gid)
	if index < metrics {
		if (index+1)*4 > len(hmtx) {
			return 0
		}
		return int16(binary.BigEndian.Uint16(hmtx[index*4+2 : index*4+4]))
	}
	// Past the paired entries the table holds bearings alone.
	at := metrics*4 + (index-metrics)*2
	if at+2 > len(hmtx) {
		return 0
	}
	return int16(binary.BigEndian.Uint16(hmtx[at : at+2]))
}

func (font *Font) numberOfHMetrics() int {
	hhea := font.Tables["hhea"]
	if len(hhea) < 36 {
		return 0
	}
	return int(binary.BigEndian.Uint16(hhea[34:36]))
}

// ParseNames returns the best string for each name ID in a name table.
//
// Windows records in English win, then any Windows record,
// then Macintosh, then Unicode. A face carrying several localised strings
// for one ID must not resolve to whichever happens to come last in the file.
func ParseNames(raw []byte) map[int]string {
	out := map[int]string{}
	if len(raw) < 6 {
		return out
	}
	count := int(binary.BigEndian.Uint16(raw[2:4]))
	storage := int(binary.BigEndian.Uint16(raw[4:6]))
	best := map[int]int{}

	for index := 0; index < count; index++ {
		at := 6 + index*12
		if at+12 > len(raw) {
			break
		}
		platform := int(binary.BigEndian.Uint16(raw[at : at+2]))
		encoding := int(binary.BigEndian.Uint16(raw[at+2 : at+4]))
		language := int(binary.BigEndian.Uint16(raw[at+4 : at+6]))
		nameID := int(binary.BigEndian.Uint16(raw[at+6 : at+8]))
		length := int(binary.BigEndian.Uint16(raw[at+8 : at+10]))
		strOff := int(binary.BigEndian.Uint16(raw[at+10 : at+12]))

		start := storage + strOff
		if start < 0 || start+length > len(raw) {
			continue
		}
		rank := 0
		switch {
		case platform == 3 && language == 0x0409:
			rank = 4
		case platform == 3:
			rank = 3
		case platform == 1:
			rank = 2
		case platform == 0:
			rank = 1
		}
		if rank == 0 || rank <= best[nameID] {
			continue
		}
		text := decodeName(platform, encoding, raw[start:start+length])
		if text == "" {
			continue
		}
		best[nameID] = rank
		out[nameID] = text
	}
	return out
}

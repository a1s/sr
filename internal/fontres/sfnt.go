package fontres

import (
	"encoding/binary"
	"math"
	"strings"
	"unicode/utf16"

	gofont "github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
)

// Style bits, read from the tables the specification ranks in order.
type Style struct {
	Bold   bool
	Italic bool
}

// FaceInfo describes one face inside a font file.
type FaceInfo struct {
	File  string
	Index int
	// Family is name ID 16 where the font has one, and name ID 1 otherwise.
	Family string
	// Subfamily is name ID 17 or 2, read only as a last resort.
	Subfamily string
	Style     Style
	Font      *gofont.Font

	// Ascender and Descender are the face's hhea metrics in font units,
	// the descender positive downward. Both are zero for a face without
	// an hhea table.
	Ascender, Descender float64
}

// tag builds a four-byte table tag.
func tag(text string) ot.Tag { return ot.MustNewTag(text) }

// describe reads a face's family and style.
//
// Family comes from a name record. Boldness and slant do not: they come from
// OS/2.fsSelection, else head.macStyle, else the subfamily string. That ranks
// sources, not answers — the first table the face has decides, and the string
// is read only when neither table is present, never because it disagrees.
func describe(ld *ot.Loader, file string, index int) (*FaceInfo, error) {
	ft, err := gofont.NewFont(ld)
	if err != nil {
		return nil, err
	}
	info := &FaceInfo{File: file, Index: index, Font: ft}
	info.Ascender, info.Descender = hheaMetrics(ld)

	nameTable, err := ld.RawTable(tag("name"))
	if err == nil {
		names := parseNameTable(nameTable)
		info.Family = firstNonEmpty(names[16], names[1])
		info.Subfamily = firstNonEmpty(names[17], names[2])
	}
	if info.Family == "" {
		info.Family = ft.Describe().Family
	}

	switch {
	case hasStyleFromOS2(ld, &info.Style):
	case hasStyleFromHead(ld, &info.Style):
	default:
		lower := strings.ToLower(info.Subfamily)
		info.Style.Bold = strings.Contains(lower, "bold")
		info.Style.Italic = strings.Contains(lower, "italic") ||
			strings.Contains(lower, "oblique")
	}
	return info, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// hheaMetrics reads the horizontal ascender and descender, in font units,
// the descender returned positive downward.
//
// hhea is read here rather than through the shaping library, which prefers
// OS/2's typographic metrics whenever the face sets USE_TYPO_METRICS.
// Those are a different pair of numbers, and a renderer places the baseline
// from these while writing the face's hhea values into the font descriptor:
// one table has to answer both, and the specification names this one.
func hheaMetrics(ld *ot.Loader) (ascender, descender float64) {
	raw, err := ld.RawTable(tag("hhea"))
	if err != nil || len(raw) < 8 {
		return 0, 0
	}
	ascender = float64(int16(binary.BigEndian.Uint16(raw[4:6])))
	descender = float64(int16(binary.BigEndian.Uint16(raw[6:8])))
	// Signs are as the table has them: an ascender above the baseline
	// is positive and a descender below it negative, but files carry
	// both conventions, so the magnitudes are what is kept.
	return math.Abs(ascender), math.Abs(descender)
}

// hasStyleFromOS2 reads fsSelection: bit 0 ITALIC, bit 5 BOLD, bit 9 OBLIQUE.
func hasStyleFromOS2(ld *ot.Loader, out *Style) bool {
	raw, err := ld.RawTable(tag("OS/2"))
	if err != nil || len(raw) < 64 {
		return false
	}
	sel := binary.BigEndian.Uint16(raw[62:64])
	out.Italic = sel&1 != 0 || sel&(1<<9) != 0
	out.Bold = sel&(1<<5) != 0
	return true
}

// hasStyleFromHead reads macStyle: bit 0 bold, bit 1 italic.
func hasStyleFromHead(ld *ot.Loader, out *Style) bool {
	raw, err := ld.RawTable(tag("head"))
	if err != nil || len(raw) < 46 {
		return false
	}
	mac := binary.BigEndian.Uint16(raw[44:46])
	out.Bold = mac&1 != 0
	out.Italic = mac&2 != 0
	return true
}

// parseNameTable returns the best string for each name ID.
//
// Windows records in English win, then any Windows record, then Macintosh,
// then Unicode. A face carrying several localised subfamily strings must
// not resolve to whichever happens to come last in the file.
func parseNameTable(raw []byte) map[int]string {
	out := map[int]string{}
	if len(raw) < 6 {
		return out
	}
	count := int(binary.BigEndian.Uint16(raw[2:4]))
	storage := int(binary.BigEndian.Uint16(raw[4:6]))
	best := map[int]int{} // name ID -> rank of the record chosen

	for index := 0; index < count; index++ {
		off := 6 + index*12
		if off+12 > len(raw) {
			break
		}
		platform := int(binary.BigEndian.Uint16(raw[off : off+2]))
		encoding := int(binary.BigEndian.Uint16(raw[off+2 : off+4]))
		language := int(binary.BigEndian.Uint16(raw[off+4 : off+6]))
		nameID := int(binary.BigEndian.Uint16(raw[off+6 : off+8]))
		length := int(binary.BigEndian.Uint16(raw[off+8 : off+10]))
		strOff := int(binary.BigEndian.Uint16(raw[off+10 : off+12]))

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
		text := decodeNameString(platform, encoding, raw[start:start+length])
		if text == "" {
			continue
		}
		best[nameID] = rank
		out[nameID] = text
	}
	return out
}

func decodeNameString(platform, encoding int, raw []byte) string {
	if platform == 1 {
		// Macintosh; the Roman encoding agrees with ASCII
		// over the range a family name uses.
		var buf strings.Builder
		for _, ch := range raw {
			buf.WriteRune(rune(ch))
		}
		return strings.TrimSpace(buf.String())
	}
	// Platform 0 and 3 are UTF-16BE.
	if len(raw)%2 != 0 {
		raw = raw[:len(raw)-1]
	}
	units := make([]uint16, 0, len(raw)/2)
	for index := 0; index+1 < len(raw); index += 2 {
		units = append(units, binary.BigEndian.Uint16(raw[index:index+2]))
	}
	_ = encoding
	return strings.TrimSpace(string(utf16.Decode(units)))
}

// looksLikeSFNT reports whether the first four bytes claim an sfnt
// or a collection. A file that does not is a format the engine
// does not support and is classified and skipped; one that does
// and then fails to parse is a warning.
func looksLikeSFNT(header []byte) bool {
	if len(header) < 4 {
		return false
	}
	switch string(header[:4]) {
	case "\x00\x01\x00\x00", "true", "ttcf", "OTTO", "typ1":
		return true
	}
	return false
}

package sfnt

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// Composite glyph component flags, from the glyf table's definition.
const (
	argsAreWords    = 0x0001
	haveScale       = 0x0008
	moreComponents  = 0x0020
	haveXAndYScale  = 0x0040
	haveTwoByTwo    = 0x0080
	compositeHeader = 10
)

// copiedTables are carried over verbatim. glyf, loca, hmtx, head, hhea
// and maxp are rebuilt, the hinting programs are copied when the font
// has them, and everything else -- cmap included -- is left out:
// an Identity-H composite font addresses glyphs directly, so nothing
// in the file reads a character map.
var copiedTables = []string{"cvt ", "fpgm", "prep"}

// Subset builds a font program holding only the given glyphs.
//
// A glyph's position in glyphs is its index in the result, which is what
// lets a PDF address it by that index through an identity CID to glyph
// map. Composite glyphs pull their components in and are rewritten to
// point at the components' new indices; those extra glyphs land after
// the requested ones, so no requested glyph moves.
func (font *Font) Subset(glyphs []uint16) ([]byte, error) {
	if !font.TrueType {
		return nil, fmt.Errorf(
			"the font has PostScript (CFF) outlines, which this renderer does not embed")
	}
	if len(glyphs) == 0 {
		return nil, fmt.Errorf("a subset needs at least the empty glyph")
	}

	order := append([]uint16(nil), glyphs...)
	position := make(map[uint16]uint16, len(order))
	for index, gid := range order {
		if int(gid) >= font.NumGlyphs {
			return nil, fmt.Errorf("glyph %d asked for, and the face holds %d",
				gid, font.NumGlyphs)
		}
		if _, seen := position[gid]; !seen {
			position[gid] = uint16(index)
		}
	}
	// The loop reads entries it appends, so a component of a component
	// is pulled in too.
	for index := 0; index < len(order); index++ {
		for _, component := range font.components(order[index]) {
			if int(component) >= font.NumGlyphs {
				// A component outside the face is a corrupt font, and
				// pulling in the blank it resolves to would drop an
				// accent rather than report anything.
				return nil, fmt.Errorf(
					"glyph %d names component glyph %d, and the face holds %d",
					order[index], component, font.NumGlyphs)
			}
			if _, seen := position[component]; seen {
				continue
			}
			if len(order) >= 0xFFFF {
				return nil, fmt.Errorf("a subset of more than 65535 glyphs")
			}
			position[component] = uint16(len(order))
			order = append(order, component)
		}
	}

	glyf := make([]byte, 0, 4096)
	locations := make([]byte, 0, 4*(len(order)+1))
	for _, gid := range order {
		locations = binary.BigEndian.AppendUint32(locations, uint32(len(glyf)))
		data := font.GlyphData(gid)
		if isComposite(data) {
			remapped, err := remapComponents(data, position)
			if err != nil {
				return nil, fmt.Errorf("glyph %d: %w", gid, err)
			}
			data = remapped
		}
		glyf = append(glyf, data...)
		for len(glyf)%4 != 0 {
			glyf = append(glyf, 0)
		}
	}
	locations = binary.BigEndian.AppendUint32(locations, uint32(len(glyf)))

	hmtx := make([]byte, 0, 4*len(order))
	for _, gid := range order {
		hmtx = binary.BigEndian.AppendUint16(hmtx, font.Advance(gid))
		hmtx = binary.BigEndian.AppendUint16(hmtx, uint16(font.SideBearing(gid)))
	}

	head, err := font.patchedHead()
	if err != nil {
		return nil, err
	}
	hhea, err := patch16(font.Tables["hhea"], 36, 34, uint16(len(order)))
	if err != nil {
		return nil, fmt.Errorf("hhea: %w", err)
	}
	maxp, err := patch16(font.Tables["maxp"], 6, 4, uint16(len(order)))
	if err != nil {
		return nil, fmt.Errorf("maxp: %w", err)
	}

	out := map[string][]byte{
		"head": head, "hhea": hhea, "maxp": maxp,
		"hmtx": hmtx, "loca": locations, "glyf": glyf,
	}
	for _, tag := range copiedTables {
		if data, ok := font.Tables[tag]; ok && len(data) > 0 {
			out[tag] = data
		}
	}
	return assemble(out), nil
}

// patchedHead copies the head table with the two fields a rebuilt font changes:
// the loca format, which the subset always writes long, and the file checksum,
// which assemble fills in once the file exists.
func (font *Font) patchedHead() ([]byte, error) {
	head, err := patch16(font.Tables["head"], 54, 50, 1)
	if err != nil {
		return nil, fmt.Errorf("head: %w", err)
	}
	binary.BigEndian.PutUint32(head[8:12], 0)
	return head, nil
}

// patch16 copies a table of at least a length and overwrites one two-byte field.
func patch16(table []byte, least, at int, value uint16) ([]byte, error) {
	if len(table) < least {
		return nil, fmt.Errorf("a table of %d bytes, %d wanted", len(table), least)
	}
	out := append([]byte(nil), table...)
	binary.BigEndian.PutUint16(out[at:at+2], value)
	return out, nil
}

func isComposite(data []byte) bool {
	if len(data) < compositeHeader {
		return false
	}
	return int16(binary.BigEndian.Uint16(data[0:2])) < 0
}

// components lists the glyphs a composite glyph is built from.
func (font *Font) components(gid uint16) []uint16 {
	data := font.GlyphData(gid)
	if !isComposite(data) {
		return nil
	}
	var out []uint16
	walkComponents(data, func(at int, flags, component uint16) {
		out = append(out, component)
	})
	return out
}

// remapComponents rewrites a composite glyph's component indices
// to the positions those components hold in the subset.
func remapComponents(data []byte, position map[uint16]uint16) ([]byte, error) {
	out := append([]byte(nil), data...)
	var missing uint16
	found := true
	walkComponents(out, func(at int, flags, component uint16) {
		mapped, ok := position[component]
		if !ok {
			missing, found = component, false
			return
		}
		binary.BigEndian.PutUint16(out[at+2:at+4], mapped)
	})
	if !found {
		return nil, fmt.Errorf("component glyph %d is not in the subset", missing)
	}
	return out, nil
}

// walkComponents visits each component record of a composite glyph,
// passing its offset, its flags, and the glyph it names.
func walkComponents(data []byte, visit func(at int, flags, component uint16)) {
	at := compositeHeader
	for at+4 <= len(data) {
		flags := binary.BigEndian.Uint16(data[at : at+2])
		component := binary.BigEndian.Uint16(data[at+2 : at+4])
		visit(at, flags, component)

		at += 4
		if flags&argsAreWords != 0 {
			at += 4
		} else {
			at += 2
		}
		switch {
		case flags&haveScale != 0:
			at += 2
		case flags&haveXAndYScale != 0:
			at += 4
		case flags&haveTwoByTwo != 0:
			at += 8
		}
		if flags&moreComponents == 0 {
			return
		}
	}
}

// assemble writes the sfnt container: the table directory, sorted
// by tag as the format requires, then the tables, each padded to
// a four-byte boundary, then the file checksum patched into head.
func assemble(tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for tag := range tables {
		tags = append(tags, tag)
	}
	sort.Strings(tags)

	count := len(tags)
	// The binary-search hints in the header are derived from the table
	// count; readers ignore them, and a wrong value is a conformance
	// defect rather than a harmless one.
	entrySelector := 0
	for 1<<(entrySelector+1) <= count {
		entrySelector++
	}
	searchRange := 16 << entrySelector

	out := make([]byte, 12+16*count)
	binary.BigEndian.PutUint32(out[0:4], 0x00010000)
	binary.BigEndian.PutUint16(out[4:6], uint16(count))
	binary.BigEndian.PutUint16(out[6:8], uint16(searchRange))
	binary.BigEndian.PutUint16(out[8:10], uint16(entrySelector))
	binary.BigEndian.PutUint16(out[10:12], uint16(16*count-searchRange))

	headEntry := -1
	for index, tag := range tags {
		data := tables[tag]
		at := 12 + index*16
		copy(out[at:at+4], tag)
		binary.BigEndian.PutUint32(out[at+4:at+8], checksum(data))
		binary.BigEndian.PutUint32(out[at+8:at+12], uint32(len(out)))
		binary.BigEndian.PutUint32(out[at+12:at+16], uint32(len(data)))
		if tag == "head" {
			headEntry = len(out) + 8
		}
		out = append(out, data...)
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
	}
	if headEntry >= 0 && headEntry+4 <= len(out) {
		binary.BigEndian.PutUint32(out[headEntry:headEntry+4], 0xB1B0AFBA-checksum(out))
	}
	return out
}

// checksum sums a table as big-endian 32-bit words, the trailing
// bytes treated as though the table were padded with zeros.
func checksum(data []byte) uint32 {
	var sum uint32
	index := 0
	for ; index+4 <= len(data); index += 4 {
		sum += binary.BigEndian.Uint32(data[index : index+4])
	}
	if index < len(data) {
		var last [4]byte
		copy(last[:], data[index:])
		sum += binary.BigEndian.Uint32(last[:])
	}
	return sum
}

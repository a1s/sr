package sfnt

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"

	ot "github.com/go-text/typesetting/font/opentype"
)

const goRegular = "../../example/fonts/Go-Regular.ttf"

func load(test *testing.T) *Font {
	test.Helper()
	raw, err := os.ReadFile(goRegular)
	if err != nil {
		test.Fatal(err)
	}
	font, err := Parse(raw, 0)
	if err != nil {
		test.Fatal(err)
	}
	return font
}

// The tables a font descriptor reads come out as the font states them.
func TestParse(test *testing.T) {
	font := load(test)
	if !font.TrueType {
		test.Error("the committed face has glyf outlines and did not read as TrueType")
	}
	if font.Upem != 2048 {
		test.Errorf("units per em = %v, want 2048", font.Upem)
	}
	if font.NumGlyphs < 100 {
		test.Errorf("glyphs = %d, want a whole face", font.NumGlyphs)
	}
	if font.PostScriptName != "GoRegular" {
		test.Errorf("PostScript name = %q", font.PostScriptName)
	}
	if font.Ascender <= 0 || font.Descender >= 0 {
		test.Errorf("ascender %d and descender %d, want the descender below the baseline",
			font.Ascender, font.Descender)
	}
	if font.XMax <= font.XMin || font.YMax <= font.YMin {
		test.Errorf("the bounding box is empty: %d %d %d %d",
			font.XMin, font.YMin, font.XMax, font.YMax)
	}
}

// Asking for a face a file does not have is an error
// rather than a silently wrong face.
func TestParseFaceIndex(test *testing.T) {
	raw, err := os.ReadFile(goRegular)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := Parse(raw, 1); err == nil {
		test.Error("face 1 of a single-face file parsed")
	}
	if _, err := Parse(raw[:40], 0); err == nil {
		test.Error("a truncated file parsed")
	}
}

// A subset carries the glyphs asked for, in the order asked for,
// with their own advances, and nothing else.
func TestSubset(test *testing.T) {
	font := load(test)
	// Glyph 0 is the empty box, and three arbitrary glyphs after it.
	wanted := []uint16{0, 40, 41, 60}
	raw, err := font.Subset(wanted)
	if err != nil {
		test.Fatal(err)
	}
	subset, err := Parse(raw, 0)
	if err != nil {
		test.Fatalf("the subset does not parse: %v", err)
	}
	if subset.NumGlyphs != len(wanted) {
		test.Fatalf("the subset holds %d glyphs, want %d", subset.NumGlyphs, len(wanted))
	}
	for index, gid := range wanted {
		if got, want := subset.Advance(uint16(index)), font.Advance(gid); got != want {
			test.Errorf("glyph %d: advance %d, want %d for original glyph %d",
				index, got, want, gid)
		}
		if got, want := subset.SideBearing(uint16(index)), font.SideBearing(gid); got != want {
			test.Errorf("glyph %d: side bearing %d, want %d", index, got, want)
		}
		if len(subset.GlyphData(uint16(index))) != len(font.GlyphData(gid)) {
			test.Errorf("glyph %d: outline of %d bytes, want %d",
				index, len(subset.GlyphData(uint16(index))), len(font.GlyphData(gid)))
		}
	}
	// The subset is a fraction of the face it came from.
	if len(raw) >= 10000 {
		test.Errorf("a four-glyph subset is %d bytes", len(raw))
	}
	// The tables an Identity-H composite font does not read are left out.
	if _, present := subset.Tables["cmap"]; present {
		test.Error("the subset carries a cmap, which nothing in the file reads")
	}
	if _, present := subset.Tables["name"]; present {
		test.Error("the subset carries a name table")
	}
}

// The head table's checksum covers the file the subset actually is,
// so a reader validating it agrees.
func TestSubsetChecksum(test *testing.T) {
	font := load(test)
	raw, err := font.Subset([]uint16{0, 40})
	if err != nil {
		test.Fatal(err)
	}
	subset, err := Parse(raw, 0)
	if err != nil {
		test.Fatal(err)
	}
	head := subset.Tables["head"]
	stated := binary.BigEndian.Uint32(head[8:12])

	// Recompute the way a validator does: zero the field, sum the file.
	copied := append([]byte(nil), raw...)
	at := indexOfTable(test, raw, "head")
	binary.BigEndian.PutUint32(copied[at+8:at+12], 0)
	if want := 0xB1B0AFBA - checksum(copied); stated != want {
		test.Errorf("checkSumAdjustment = %08X, want %08X", stated, want)
	}

	// And every table's own checksum.
	count := int(binary.BigEndian.Uint16(raw[4:6]))
	for entry := 0; entry < count; entry++ {
		record := 12 + entry*16
		tag := string(raw[record : record+4])
		stated := binary.BigEndian.Uint32(raw[record+4 : record+8])
		start := int(binary.BigEndian.Uint32(raw[record+8 : record+12]))
		length := int(binary.BigEndian.Uint32(raw[record+12 : record+16]))
		data := raw[start : start+length]
		if tag == "head" {
			data = append([]byte(nil), data...)
			binary.BigEndian.PutUint32(data[8:12], 0)
		}
		if got := checksum(data); got != stated {
			test.Errorf("table %q: checksum %08X, want %08X", tag, stated, got)
		}
	}
}

func indexOfTable(test *testing.T, raw []byte, tag string) int {
	test.Helper()
	count := int(binary.BigEndian.Uint16(raw[4:6]))
	for entry := 0; entry < count; entry++ {
		record := 12 + entry*16
		if string(raw[record:record+4]) == tag {
			return int(binary.BigEndian.Uint32(raw[record+8 : record+12]))
		}
	}
	test.Fatalf("the subset has no %q table", tag)
	return 0
}

// A subset must open with the empty glyph, since that is the code
// a missing character is drawn with.
func TestSubsetRequiresNotdef(test *testing.T) {
	font := load(test)
	if _, err := font.Subset([]uint16{}); err == nil {
		test.Error("an empty subset was built")
	}
}

// A name table resolves one string per identifier, preferring the
// English Windows record over the localised ones that follow it.
func TestParseNames(test *testing.T) {
	raw, err := os.ReadFile(goRegular)
	if err != nil {
		test.Fatal(err)
	}
	font, err := Parse(raw, 0)
	if err != nil {
		test.Fatal(err)
	}
	names := ParseNames(font.Tables["name"])
	if names[1] != "Go" {
		test.Errorf("family = %q, want Go", names[1])
	}
	if names[6] != "GoRegular" {
		test.Errorf("PostScript name = %q", names[6])
	}
	if got := ParseNames(nil); len(got) != 0 {
		test.Errorf("a missing name table produced %v", got)
	}
}

// compositeFont builds a three-glyph font whose last glyph
// is a composite referencing the second.
//
// It is built rather than committed because the Go faces have no
// composite glyph, and a subsetter that failed to remap components
// would pass every test that used only them -- while breaking the
// accented letters most European text is made of.
func compositeFont(test *testing.T) []byte {
	test.Helper()
	source := load(test)

	var simple uint16
	for gid := uint16(1); gid < uint16(source.NumGlyphs); gid++ {
		if len(source.GlyphData(gid)) > 10 {
			simple = gid
			break
		}
	}
	if simple == 0 {
		test.Fatal("the committed face has no glyph with an outline")
	}
	outline := append([]byte(nil), source.GlyphData(simple)...)
	for len(outline)%4 != 0 {
		outline = append(outline, 0)
	}

	// numberOfContours -1 marks a composite; the bounding box is
	// the component's, and the single component record names glyph 1
	// with a one-byte offset.
	composite := append([]byte(nil), outline[0:10]...)
	binary.BigEndian.PutUint16(composite[0:2], 0xFFFF)
	composite = binary.BigEndian.AppendUint16(composite, 0x0002)
	composite = binary.BigEndian.AppendUint16(composite, 1)
	composite = append(composite, 10, 0)
	for len(composite)%4 != 0 {
		composite = append(composite, 0)
	}

	glyf := append(append([]byte(nil), outline...), composite...)
	var loca []byte
	for _, offset := range []uint32{
		0, 0, uint32(len(outline)), uint32(len(glyf)),
	} {
		loca = binary.BigEndian.AppendUint32(loca, offset)
	}
	var hmtx []byte
	for _, gid := range []uint16{0, simple, simple} {
		hmtx = binary.BigEndian.AppendUint16(hmtx, source.Advance(gid))
		hmtx = binary.BigEndian.AppendUint16(hmtx, uint16(source.SideBearing(gid)))
	}

	head, err := source.patchedHead()
	if err != nil {
		test.Fatal(err)
	}
	hhea, err := patch16(source.Tables["hhea"], 36, 34, 3)
	if err != nil {
		test.Fatal(err)
	}
	maxp, err := patch16(source.Tables["maxp"], 6, 4, 3)
	if err != nil {
		test.Fatal(err)
	}
	return assemble(map[string][]byte{
		"head": head, "hhea": hhea, "maxp": maxp,
		"hmtx": hmtx, "loca": loca, "glyf": glyf,
	})
}

// A composite glyph pulls its components into the subset and is
// rewritten to name their new positions, since a component index
// still pointing into the original face would draw the wrong glyph.
func TestSubsetComposite(test *testing.T) {
	font, err := Parse(compositeFont(test), 0)
	if err != nil {
		test.Fatal(err)
	}
	if parts := font.components(2); len(parts) != 1 || parts[0] != 1 {
		test.Fatalf("the built font's composite names %v, want [1]", parts)
	}

	// Glyph 2 is asked for and glyph 1 is not, so the component
	// follows it in and lands after it.
	raw, err := font.Subset([]uint16{0, 2})
	if err != nil {
		test.Fatal(err)
	}
	subset, err := Parse(raw, 0)
	if err != nil {
		test.Fatalf("the subset does not parse: %v", err)
	}
	if subset.NumGlyphs != 3 {
		test.Fatalf("the subset holds %d glyphs, want the component too",
			subset.NumGlyphs)
	}
	parts := subset.components(1)
	if len(parts) != 1 {
		test.Fatalf("the composite lost its components: %v", parts)
	}
	if parts[0] != 2 {
		test.Errorf("the component names glyph %d, want 2, its position in the subset",
			parts[0])
	}
	if got, want := len(subset.GlyphData(2)), len(font.GlyphData(1)); got != want {
		test.Errorf("the component's outline is %d bytes, want %d", got, want)
	}
}

// A composite whose component cannot be found is an error
// rather than a glyph pointing at nothing.
func TestSubsetBrokenComposite(test *testing.T) {
	font, err := Parse(compositeFont(test), 0)
	if err != nil {
		test.Fatal(err)
	}
	// Point the component at a glyph the font does not have,
	// so nothing can be pulled in for it.
	glyf := font.Tables["glyf"]
	at := int(font.locations[2])
	binary.BigEndian.PutUint16(glyf[at+12:at+14], 99)
	if _, err := font.Subset([]uint16{0, 2}); err == nil {
		test.Error("a composite naming an absent component subsetted")
	}
}

// The subset's container is valid to an implementation that is
// not this one: an independent loader reads its table directory
// and hands back the tables at the offsets and lengths it states.
//
// It cannot go further than that. The subset deliberately omits cmap,
// which an Identity-H composite font never reads, and go-text -- like
// every library the Stage 1 spike measured against the same kind of
// file -- refuses a face without one. That refusal is correct on
// both sides, so the outlines are checked here through loca instead.
func TestSubsetContainerIsValid(test *testing.T) {
	font := load(test)
	wanted := []uint16{0, 40, 41}
	raw, err := font.Subset(wanted)
	if err != nil {
		test.Fatal(err)
	}
	loaders, err := ot.NewLoaders(bytes.NewReader(raw))
	if err != nil {
		test.Fatalf("an independent loader rejected the subset: %v", err)
	}
	if len(loaders) != 1 {
		test.Fatalf("faces = %d, want 1", len(loaders))
	}
	for _, tag := range []string{"head", "hhea", "maxp", "hmtx", "loca", "glyf"} {
		table, err := loaders[0].RawTable(ot.MustNewTag(tag))
		if err != nil {
			test.Errorf("table %q: %v", tag, err)
			continue
		}
		if len(table) == 0 {
			test.Errorf("table %q is empty", tag)
		}
	}
	if _, err := loaders[0].RawTable(ot.MustNewTag("cmap")); err == nil {
		test.Error("the subset carries a cmap after all")
	}
}

// A glyph outside the face is an error, whether it was asked for
// or pulled in as a component: silently substituting a blank
// would drop a character with nothing to say so.
func TestSubsetGlyphOutOfRange(test *testing.T) {
	font := load(test)
	if _, err := font.Subset([]uint16{0, uint16(font.NumGlyphs)}); err == nil {
		test.Error("a glyph past the end of the face subsetted")
	}
}

// hhea's signs are normalised, because a face may not use them.
//
// The ascender and descender go straight into the PDF font descriptor's
// /Ascent and /Descent, which are defined relative to the baseline:
// above it positive, below it negative. A face that stores its descender
// as a magnitude -- the wrong convention, and not a rare one -- would
// otherwise state a descent above its own baseline.
func TestHheaSignsAreNormalised(test *testing.T) {
	want := load(test)
	if want.Ascender <= 0 || want.Descender >= 0 {
		test.Fatalf("the fixture itself is signed oddly: %d %d",
			want.Ascender, want.Descender)
	}

	raw, err := os.ReadFile(goRegular)
	if err != nil {
		test.Fatal(err)
	}
	// Both metrics written as magnitudes, which is what the
	// descender convention this guards against looks like.
	patched := append([]byte(nil), raw...)
	at := tableOffset(test, patched, "hhea")
	binary.BigEndian.PutUint16(patched[at+4:at+6], uint16(abs16(test, want.Ascender)))
	binary.BigEndian.PutUint16(patched[at+6:at+8], uint16(abs16(test, want.Descender)))

	font, err := Parse(patched, 0)
	if err != nil {
		test.Fatal(err)
	}
	if font.Ascender != want.Ascender {
		test.Errorf("ascender = %d, want %d", font.Ascender, want.Ascender)
	}
	if font.Descender != want.Descender {
		test.Errorf("descender = %d, want %d, below the baseline",
			font.Descender, want.Descender)
	}
}

func abs16(test *testing.T, value int16) int16 {
	test.Helper()
	if value < 0 {
		return -value
	}
	return value
}

// tableOffset finds where a table's bytes start in an sfnt file.
func tableOffset(test *testing.T, raw []byte, want string) int {
	test.Helper()
	count := int(binary.BigEndian.Uint16(raw[4:6]))
	for index := 0; index < count; index++ {
		at := 12 + index*16
		if string(raw[at:at+4]) == want {
			return int(binary.BigEndian.Uint32(raw[at+8 : at+12]))
		}
	}
	test.Fatalf("the fixture has no %s table", want)
	return 0
}

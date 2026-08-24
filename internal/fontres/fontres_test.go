package fontres

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fontDir = "../../example/fonts"

func regular(test *testing.T, size int) *Face {
	test.Helper()
	resolver := NewResolver(fontDir, true)
	face, err := resolver.Resolve(
		Request{Name: "body", File: "Go-Regular.ttf", Size: size})
	if err != nil {
		test.Fatal(err)
	}
	return face
}

func TestExplicitResolution(test *testing.T) {
	face := regular(test, 8)
	if face.ResolvedBy != ByExplicit {
		test.Errorf("resolvedBy = %q, want explicit", face.ResolvedBy)
	}
	if face.Requested != "" {
		test.Errorf(
			"requested = %q; a pinned font has no typeface to record",
			face.Requested)
	}
	if face.ResolvedFace != "Go" {
		test.Errorf("resolvedFace = %q, want Go", face.ResolvedFace)
	}
	if !strings.HasSuffix(face.ResolvedFile, "Go-Regular.ttf") {
		test.Errorf("resolvedFile = %q", face.ResolvedFile)
	}
}

// A single-face file selects its face outright, so bold and italic decide
// nothing there -- unlike a collection, which has faces to choose between.
// Declaring a style the face does not carry is not an error, but the printout
// would then describe the face wrongly, so resolution warns.
func TestDeclaredStyleAgainstTheFile(test *testing.T) {
	resolver := NewResolver(fontDir, true)
	_, err := resolver.Resolve(Request{Name: "body", File: "Go-Regular.ttf", Size: 9, Bold: true})
	if err != nil {
		test.Fatal(err)
	}
	if len(resolver.Warnings) != 1 {
		test.Fatalf("want one warning, got %v", resolver.Warnings)
	}
	if !strings.Contains(resolver.Warnings[0], `"body"`) || !strings.Contains(resolver.Warnings[0], "bold") {
		test.Errorf("warning = %q, want it to name the font and the style", resolver.Warnings[0])
	}

	// The face carries what was declared: nothing to report.
	resolver = NewResolver(fontDir, true)
	_, err = resolver.Resolve(Request{Name: "heading", File: "Go-Bold.ttf", Size: 9, Bold: true})
	if err != nil {
		test.Fatal(err)
	}
	if len(resolver.Warnings) != 0 {
		test.Errorf("a bold file declared bold must not warn: %v", resolver.Warnings)
	}

	// Nor does the other direction. Naming a bold file without the flag is
	// ordinary use — the flag would only be redundant — so it is not a claim
	// about the face and there is nothing to contradict.
	resolver = NewResolver(fontDir, true)
	_, err = resolver.Resolve(Request{Name: "heading", File: "Go-Bold.ttf", Size: 9})
	if err != nil {
		test.Fatal(err)
	}
	if len(resolver.Warnings) != 0 {
		test.Errorf("an undeclared bold file must not warn: %v", resolver.Warnings)
	}
}

// Strict mode stops after step 1 and fails with the typeface named.
// Tests run this way so that no outcome depends on what is installed.
func TestStrictModeRefusesATypeface(test *testing.T) {
	resolver := NewResolver(fontDir, true)
	_, err := resolver.Resolve(Request{Name: "body", Typeface: "Helvetica", Size: 9})
	if err == nil {
		test.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "Helvetica") {
		test.Errorf("the diagnostic must name the unresolved typeface: %v", err)
	}
}

func TestDataBlobResolution(test *testing.T) {
	raw := mustReadFont(test)
	resolver := NewResolver(fontDir, true)
	resolver.Blob = func(name string) ([]byte, bool) {
		if name == "embedded" {
			return raw, true
		}
		return nil, false
	}
	face, err := resolver.Resolve(Request{Name: "body", Data: "embedded", Size: 9})
	if err != nil {
		test.Fatal(err)
	}
	if face.ResolvedBy != ByExplicit || face.ResolvedData != "embedded" || face.ResolvedFile != "" {
		test.Errorf("an embedded font names a data entry: %+v", face)
	}
	if _, err := resolver.Resolve(Request{Name: "x", Data: "nonesuch", Size: 9}); err == nil {
		test.Error("want an error for an unknown data blob")
	}
}

func mustReadFont(test *testing.T) []byte {
	test.Helper()
	raw, err := readFile(fontDir + "/Go-Regular.ttf")
	if err != nil {
		test.Fatal(err)
	}
	return raw
}

// Advances come from hmtx at the font's own units per em,
// so a measurement is one multiply and no fixed-point quantisation.
func TestAdvances(test *testing.T) {
	face := regular(test, 8)
	// Go Regular: 'A' advances 1366 units of 2048 per em.
	want := 1366.0 * 8 / 2048
	if got := face.Advance('A'); math.Abs(got-want) > 1e-9 {
		test.Errorf("Advance('A') = %v, want %v", got, want)
	}
	if got, ok := face.AdvanceUnits('A'); !ok || got != 1366 {
		test.Errorf("AdvanceUnits('A') = %v, %v", got, ok)
	}
	// Scaling is linear in the size.
	big := regular(test, 16)
	if math.Abs(big.Advance('A')-2*face.Advance('A')) > 1e-9 {
		test.Error("advance must scale with the size")
	}
}

func TestWidthIsTheSumOfAdvances(test *testing.T) {
	face := regular(test, 8)
	var sum float64
	for _, char := range "AVWil" {
		sum += face.Advance(char)
	}
	if got := face.Width("AVWil"); math.Abs(got-sum) > 0.001 {
		test.Errorf("Width = %v, want the hmtx sum %v", got, sum)
	}
}

func TestMissingGlyphMeasuresNotdef(test *testing.T) {
	face := regular(test, 9)
	// U+4E2D is outside the Go fonts' coverage.
	width := face.Advance('中')
	if width <= 0 {
		test.Error("a missing glyph keeps metrics: .notdef has an advance")
	}
	missing := face.MissingRunes()
	if len(missing) != 1 || missing[0] != '中' {
		test.Errorf("missing runes = %q, want one entry", missing)
	}
}

func TestLeading(test *testing.T) {
	// printout.md's example carries leading 10.8 for a 9 pt font.
	if got := regular(test, 9).Leading(); got != 10.8 {
		test.Errorf("Leading = %v, want 10.8", got)
	}
}

func TestWrap(test *testing.T) {
	face := regular(test, 8)
	text := "the quick brown fox jumps over the lazy dog"
	// Wide enough for everything: one line.
	if got := Wrap(face, text, 1000); len(got) != 1 || got[0] != text {
		test.Errorf("wide box: got %q", got)
	}
	// Narrow: several lines, each within the box, and no text lost.
	lines := Wrap(face, text, 60)
	if len(lines) < 3 {
		test.Errorf("want several lines, got %q", lines)
	}
	for _, line := range lines {
		if width := face.Width(line); width > 60.001 {
			test.Errorf("line %q measures %v, over the 60 pt box", line, width)
		}
	}
	if got := strings.Join(strings.Fields(strings.Join(lines, " ")), " "); got != text {
		test.Errorf("wrapping lost or changed text:\n got %q\nwant %q", got, text)
	}
}

func TestWrapBreaksAnOverlongWord(test *testing.T) {
	face := regular(test, 8)
	lines := Wrap(face, "supercalifragilisticexpialidocious", 30)
	if len(lines) < 2 {
		test.Fatalf("want the word broken, got %q", lines)
	}
	if got := strings.Join(lines, ""); got != "supercalifragilisticexpialidocious" {
		test.Errorf("breaking lost characters: %q", got)
	}
}

func TestWrapHonoursNewlines(test *testing.T) {
	face := regular(test, 8)
	lines := Wrap(face, "one\ntwo", 1000)
	if len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		test.Errorf("got %q", lines)
	}
}

func TestWrapEmptyText(test *testing.T) {
	face := regular(test, 8)
	if got := Wrap(face, "", 100); len(got) != 1 || got[0] != "" {
		test.Errorf("a text mark always has at least one line: got %q", got)
	}
}

// The exact break positions are hand-computable from the advance table,
// so this pins one case rather than trusting the wrapper against itself.
func TestWrapAgainstHandComputedWidths(test *testing.T) {
	face := regular(test, 10)
	// Go Regular at 10 pt: 'i' is 505/2048*10 = 2.4658 pt, 'm' 1735/2048*10 =
	// 8.4717, ' ' 569/2048*10 = 2.7783.
	widthOf := func(text string) float64 { return face.Width(text) }
	iiii := widthOf("iiii")
	// A box one point narrower than two such words plus a space breaks them.
	box := 2*iiii + widthOf(" ") - 1
	lines := Wrap(face, "iiii iiii", box)
	if len(lines) != 2 {
		test.Errorf("box %.4f: want two lines, got %q", box, lines)
	}
	// One point wider, and they share a line.
	lines = Wrap(face, "iiii iiii", box+2)
	if len(lines) != 1 {
		test.Errorf("box %.4f: want one line, got %q", box+2, lines)
	}
}

func TestTextHeight(test *testing.T) {
	face := regular(test, 9)
	if got := TextHeight(face, 3); got != 32.4 {
		test.Errorf("TextHeight(3) = %v, want 32.4", got)
	}
	if got := TextHeight(face, 0); got != 0 {
		test.Errorf("TextHeight(0) = %v, want 0", got)
	}
}

// The resolution chain, exercised against a fixture directory
// rather than the machine, and deliberately not in strict mode.
// Strict mode is what the rest of the suite runs under;
// these are the tests for what strict mode disables.
func fixtureResolver() *Resolver {
	resolver := NewResolver(fontDir, false)
	resolver.Dirs = []string{fontDir}
	return resolver
}

func TestHostEnumerationFindsTheCommittedFamily(test *testing.T) {
	resolver := fixtureResolver()
	families := resolver.HostFamilies()
	found := false
	for _, family := range families {
		if family == "go" {
			found = true
		}
	}
	if !found {
		test.Fatalf("enumeration missed the fixture family: %v", families)
	}

	// Step 2: matched on the host, case-insensitively.
	face, err := resolver.Resolve(Request{Name: "body", Typeface: "go", Size: 9})
	if err != nil {
		test.Fatal(err)
	}
	if face.ResolvedBy != ByHost {
		test.Errorf("resolvedBy = %q, want host", face.ResolvedBy)
	}
	if face.Requested != "go" {
		test.Errorf("requested = %q; a typeface that went through the chain is recorded",
			face.Requested)
	}

	// The bold face is a separate entry, matched on the style bit.
	other, err := resolver.Resolve(Request{Name: "bold", Typeface: "Go", Size: 9, Bold: true})
	if err != nil {
		test.Fatal(err)
	}
	if !strings.Contains(other.ResolvedFile, "Bold") {
		test.Errorf("bold resolved to %q", other.ResolvedFile)
	}
}

func TestAliasIsConsultedAfterTheHost(test *testing.T) {
	resolver := fixtureResolver()
	resolver.Dirs = []string{fontDir}
	// The fixture directory has no Helvetica and no Arial, so Helvetica falls
	// past the alias table to the substitute rather than resolving.
	face, err := resolver.Resolve(Request{Name: "body", Typeface: "Helvetica", Size: 9})
	if err != nil {
		// A machine with no substitute face at all is a legitimate outcome
		// and the diagnostic must name what was tried.
		if !strings.Contains(err.Error(), "Helvetica") {
			test.Errorf("the diagnostic must name the typeface: %v", err)
		}
		return
	}
	if face.ResolvedBy != BySubstitute {
		test.Errorf("resolvedBy = %q, want substitute", face.ResolvedBy)
	}
	if face.Requested != "Helvetica" {
		test.Errorf("requested = %q", face.Requested)
	}
	// A substitute is a guess, so the dependable signal is the field plus a
	// warning naming the typeface.
	joined := strings.Join(resolver.Warnings, "\n")
	if !strings.Contains(joined, "Helvetica") {
		test.Errorf("want a warning naming the typeface, got %q", joined)
	}
}

// The substitute is chosen for a bound that rests on a uniform advance,
// so the one property worth verifying is the one being relied on.
// A proportional face must be caught.
func TestMonospaceCheckCatchesAProportionalFace(test *testing.T) {
	resolver := fixtureResolver()
	face := regular(test, 10)
	face.ResolvedFace = "Go"
	resolver.checkMonospaced(face)
	if len(resolver.Warnings) == 0 {
		test.Error("Go Regular is proportional; the check must warn")
	}
	if !strings.Contains(resolver.Warnings[0], "not monospaced") {
		test.Errorf("warning = %q", resolver.Warnings[0])
	}
}

func TestEnumerationDiagnosticsAreNotWarnings(test *testing.T) {
	resolver := fixtureResolver()
	resolver.scan()
	// The fixture directory holds a LICENSE and a README, neither an sfnt.
	// Both are classified and skipped, and neither is a document warning.
	if len(resolver.Diagnostics) == 0 {
		test.Error("want the non-font files recorded as enumeration diagnostics")
	}
	for _, diag := range resolver.Diagnostics {
		if !strings.Contains(diag, "not an sfnt font") {
			test.Errorf("unexpected diagnostic: %s", diag)
		}
	}
	if len(resolver.Warnings) != 0 {
		test.Errorf(
			"a file on the machine that this report did not use is not a report warning: %v",
			resolver.Warnings)
	}
}

// Missing glyphs come back in code-point order.
//
// They become printout warnings in the order they are read,
// and a map's order varies per run, so a font lacking more than one glyph
// would otherwise break the byte-identical guarantee that WithBuildTime
// and StrictFonts are there to give.
func TestMissingRunesAreOrdered(test *testing.T) {
	face := regular(test, 10)
	for _, char := range []rune{'\u2603', '\u00e6', '\u4e2d', '\u2014'} {
		face.Advance(char)
	}
	got := face.MissingRunes()
	if len(got) < 2 {
		test.Skipf("the fixture face covers these; nothing to order: %q", got)
	}
	for index := 1; index < len(got); index++ {
		if got[index-1] >= got[index] {
			test.Fatalf("MissingRunes is not ordered: %q", got)
		}
	}
}

// tableRange finds a table in an sfnt file: where its bytes start,
// and how many there are.
func tableRange(test *testing.T, raw []byte, want string) (start, length int) {
	test.Helper()
	if len(raw) < 12 {
		test.Fatal("not an sfnt")
	}
	count := int(binary.BigEndian.Uint16(raw[4:6]))
	for index := 0; index < count; index++ {
		at := 12 + index*16
		if at+16 > len(raw) {
			break
		}
		if string(raw[at:at+4]) != want {
			continue
		}
		start = int(binary.BigEndian.Uint32(raw[at+8 : at+12]))
		length = int(binary.BigEndian.Uint32(raw[at+12 : at+16]))
		return start, length
	}
	test.Fatalf("the fixture has no %s table", want)
	return 0, 0
}

// A face's vertical metrics are hhea's, and stay hhea's when the face
// asks readers to prefer OS/2's typographic pair instead.
//
// The shaping library this package parses with honours that request, and
// the two pairs differ by a point or so in the faces that set the bit.
// The renderer places its baselines from these numbers and writes the
// face's hhea values into the PDF font descriptor, so one table has to
// answer both -- and doc/render.md names hhea.
func TestVerticalMetricsComeFromHhea(test *testing.T) {
	raw := mustReadFont(test)

	hheaAt, _ := tableRange(test, raw, "hhea")
	wantAscender := float64(int16(binary.BigEndian.Uint16(raw[hheaAt+4 : hheaAt+6])))
	wantDescender := -float64(int16(binary.BigEndian.Uint16(raw[hheaAt+6 : hheaAt+8])))

	// USE_TYPO_METRICS, fsSelection bit 7, with a typographic pair a
	// point clear of hhea's so that reading the wrong one shows.
	patched := append([]byte(nil), raw...)
	os2At, os2Len := tableRange(test, patched, "OS/2")
	if os2Len < 78 {
		test.Fatalf("OS/2 is %d bytes, too short to carry the typographic metrics", os2Len)
	}
	selection := binary.BigEndian.Uint16(patched[os2At+62 : os2At+64])
	binary.BigEndian.PutUint16(patched[os2At+62:os2At+64], selection|0x80)
	typoAscender := int16(wantAscender) - 300
	typoDescender := int16(-wantDescender) + 300
	binary.BigEndian.PutUint16(patched[os2At+68:os2At+70], uint16(typoAscender))
	binary.BigEndian.PutUint16(patched[os2At+70:os2At+72], uint16(typoDescender))
	if float64(typoAscender) == wantAscender {
		test.Fatal("the patched metrics match hhea, so this proves nothing")
	}

	// The renderer's path.
	face, err := Load(patched, 0, 10)
	if err != nil {
		test.Fatal(err)
	}
	ascender, descender := face.VerticalMetrics()
	if ascender != wantAscender || descender != wantDescender {
		test.Errorf("Load: ascender, descender = %v, %v; want hhea's %v, %v",
			ascender, descender, wantAscender, wantDescender)
	}

	// The engine's path, through the resolution chain.
	dir := test.TempDir()
	path := filepath.Join(dir, "Patched.ttf")
	if err := os.WriteFile(path, patched, 0o644); err != nil {
		test.Fatal(err)
	}
	resolved, err := NewResolver(dir, true).Resolve(
		Request{Name: "body", File: "Patched.ttf", Size: 10})
	if err != nil {
		test.Fatal(err)
	}
	ascender, descender = resolved.VerticalMetrics()
	if ascender != wantAscender || descender != wantDescender {
		test.Errorf("resolved: ascender, descender = %v, %v; want hhea's %v, %v",
			ascender, descender, wantAscender, wantDescender)
	}
}

// buildCollection packs whole font files into one ttcf collection.
//
// A real collection shares tables between its faces. This one does not,
// which the format allows: each face keeps its own table directory, with
// every offset in it shifted to wherever the face landed in the file.
func buildCollection(test *testing.T, names ...string) []byte {
	test.Helper()
	out := make([]byte, 12+4*len(names))
	copy(out, "ttcf")
	binary.BigEndian.PutUint32(out[4:8], 0x00010000)
	binary.BigEndian.PutUint32(out[8:12], uint32(len(names)))

	for index, name := range names {
		face, err := readFile(filepath.Join(fontDir, name))
		if err != nil {
			test.Fatal(err)
		}
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
		base := len(out)
		binary.BigEndian.PutUint32(out[12+4*index:16+4*index], uint32(base))
		out = append(out, face...)

		count := int(binary.BigEndian.Uint16(out[base+4 : base+6]))
		for entry := 0; entry < count; entry++ {
			at := base + 12 + entry*16
			was := binary.BigEndian.Uint32(out[at+8 : at+12])
			binary.BigEndian.PutUint32(out[at+8:at+12], was+uint32(base))
		}
	}
	return out
}

// A collection holds several faces, so a template naming one by file has
// something to choose after all, and the declared style is what chooses.
//
// The face index reaches the printout, and a renderer opens the file
// by it, so picking the wrong one here sets the whole report in the
// wrong weight.
func TestCollectionFaceIsChosenByStyle(test *testing.T) {
	dir := test.TempDir()
	path := filepath.Join(dir, "Go.ttc")
	err := os.WriteFile(path, buildCollection(test, "Go-Regular.ttf", "Go-Bold.ttf"), 0o644)
	if err != nil {
		test.Fatal(err)
	}

	for _, want := range []struct {
		bold  bool
		index int
	}{{false, 0}, {true, 1}} {
		resolver := NewResolver(dir, true)
		face, err := resolver.Resolve(
			Request{Name: "body", File: "Go.ttc", Size: 9, Bold: want.bold})
		if err != nil {
			test.Fatalf("bold=%t: %v", want.bold, err)
		}
		if face.FaceIndex() != want.index {
			test.Errorf("bold=%t: face index %d, want %d",
				want.bold, face.FaceIndex(), want.index)
		}
		if len(resolver.Warnings) != 0 {
			test.Errorf("bold=%t: the chosen face carries the declared style,"+
				" so there is nothing to warn about: %v", want.bold, resolver.Warnings)
		}
		// Bold is wider than regular in this family; measuring proves the
		// index reached the face rather than only the printout.
		width := face.Width("Payment")
		other := 0.0
		if want.bold {
			other = regular(test, 9).Width("Payment")
		}
		if want.bold && width <= other {
			test.Errorf("the bold face measures %v, no wider than regular's %v",
				width, other)
		}
	}
}

// A collection asked for a style none of its faces has falls back to the
// first face, and the mismatch is reported as it is for a single file.
func TestCollectionWithoutTheDeclaredStyle(test *testing.T) {
	dir := test.TempDir()
	path := filepath.Join(dir, "Go.ttc")
	err := os.WriteFile(path, buildCollection(test, "Go-Regular.ttf", "Go-Bold.ttf"), 0o644)
	if err != nil {
		test.Fatal(err)
	}
	resolver := NewResolver(dir, true)
	face, err := resolver.Resolve(
		Request{Name: "body", File: "Go.ttc", Size: 9, Italic: true})
	if err != nil {
		test.Fatal(err)
	}
	if face.FaceIndex() != 0 {
		test.Errorf("face index %d, want the first face", face.FaceIndex())
	}
	if len(resolver.Warnings) != 1 {
		test.Fatalf("want one warning about the declared style, got %v", resolver.Warnings)
	}
}

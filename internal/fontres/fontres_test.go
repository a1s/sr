package fontres

import (
	"math"
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

// A font named by file selects its face outright, so bold and italic
// decide nothing there. Declaring one the face does not carry is not an error,
// but the printout would then describe the face wrongly, so resolution warns.
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

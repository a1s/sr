package pdf

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a1s/sr/internal/pdfscan"
	"github.com/a1s/sr/internal/sfnt"
	"github.com/a1s/sr/printout"
)

// pngBlob builds a solid image of a size, as a base64 blob
// the way the engine embeds one.
func pngBlob(test *testing.T, width, height int, shade color.NRGBA) *printout.Blob {
	test.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for top := 0; top < height; top++ {
		for left := 0; left < width; left++ {
			img.Set(left, top, shade)
		}
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, img); err != nil {
		test.Fatal(err)
	}
	return &printout.Blob{
		Encoding: "base64",
		Content:  base64.StdEncoding.EncodeToString(raw.Bytes()),
	}
}

// An image with no crop fills its box; one with a crop is scaled so the
// crop fills the box, and clipped to it.
func TestImagePlacement(test *testing.T) {
	doc := document()
	doc.Header.Data["logo"] = pngBlob(test, 20, 10, color.NRGBA{200, 100, 50, 255})

	whole := printout.NewImage()
	whole.Type = "png"
	whole.Data = "logo"
	whole.Box = printout.Box{Left: 10, Top: 10, Width: 40, Height: 20}

	cropped := printout.NewImage()
	cropped.Type = "png"
	cropped.Data = "logo"
	cropped.Box = printout.Box{Left: 100, Top: 10, Width: 8, Height: 6}
	cropped.Crop = &printout.Box{Left: 4, Top: 2, Width: 8, Height: 6}

	doc.Pages[0].Marks = []printout.Mark{whole, cropped}
	pages, _ := render(test, doc)

	if len(pages[0].Draws) != 2 {
		test.Fatalf("draws = %d, want 2", len(pages[0].Draws))
	}
	// One XObject, drawn twice: two marks reading one blob share it.
	if pages[0].Draws[0].Name != pages[0].Draws[1].Name {
		test.Errorf("two marks on one blob drew two objects: %q and %q",
			pages[0].Draws[0].Name, pages[0].Draws[1].Name)
	}

	full := pages[0].Draws[0]
	near(test, "left", full.Left, 10)
	near(test, "top", full.Top, 10)
	near(test, "width", full.Width, 40)
	near(test, "height", full.Height, 20)

	// The crop is at scale 1 -- a pixel per point -- so the whole image
	// is placed at its natural size, shifted so that the crop's own
	// corner lands on the box's.
	cut := pages[0].Draws[1]
	near(test, "cropped width", cut.Width, 20)
	near(test, "cropped height", cut.Height, 10)
	near(test, "cropped left", cut.Left, 100-4)
	near(test, "cropped top", cut.Top, 10-2)
	// And the box itself was clipped, or the rest of the image would show.
	clipped := false
	for _, rect := range pages[0].Rects {
		if rect.Painted == "clip" {
			clipped = true
		}
	}
	if !clipped {
		test.Errorf("a cropped image was drawn without a clip")
	}
}

// An image with transparency carries a soft mask, and an opaque one does not.
func TestImageTransparency(test *testing.T) {
	doc := document()
	doc.Header.Data["clear"] = pngBlob(test, 4, 4, color.NRGBA{255, 0, 0, 128})
	mark := printout.NewImage()
	mark.Type = "png"
	mark.Data = "clear"
	mark.Box = printout.Box{Left: 10, Top: 10, Width: 8, Height: 8}
	doc.Pages[0].Marks = []printout.Mark{mark}

	raw := renderBytes(test, doc, Uncompressed())
	if !bytes.Contains(raw, []byte("/SMask")) {
		test.Errorf("a half-transparent image was embedded without a soft mask")
	}

	doc.Header.Data["clear"] = pngBlob(test, 4, 4, color.NRGBA{255, 0, 0, 255})
	raw = renderBytes(test, doc, Uncompressed())
	if bytes.Contains(raw, []byte("/SMask")) {
		test.Errorf("an opaque image was given a soft mask")
	}
}

// An image the printout names by file is read from beside the printout.
func TestImageFromFile(test *testing.T) {
	dir := test.TempDir()
	path := filepath.Join(dir, "spot.png")
	blob := pngBlob(test, 6, 6, color.NRGBA{0, 0, 255, 255})
	decoded, err := base64.StdEncoding.DecodeString(blob.Content)
	if err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(path, decoded, 0o600); err != nil {
		test.Fatal(err)
	}

	mark := printout.NewImage()
	mark.Type = "png"
	mark.SetFile(path)
	mark.Box = printout.Box{Left: 10, Top: 10, Width: 12, Height: 12}

	pages, _ := render(test, document(mark))
	if len(pages[0].Draws) != 1 {
		test.Fatalf("draws = %d, want 1", len(pages[0].Draws))
	}

	mark.SetFile(filepath.Join(dir, "absent.png"))
	var out bytes.Buffer
	err = Write(document(mark), &out)
	if err == nil {
		test.Fatal("a missing image file rendered without complaint")
	}
	if !strings.Contains(err.Error(), "absent.png") {
		test.Errorf("the error does not name the file: %v", err)
	}
}

// The outline entries become a tree, a closed entry states its count negated,
// and each entry's destination is the point it named.
func TestOutlineTree(test *testing.T) {
	top := printout.NewOutline()
	top.Title = "Report"
	top.Level = 1
	top.Name = "top"
	top.Left, top.Top = 15, 25

	branch := printout.NewOutline()
	branch.Title = "Closed branch"
	branch.Level = 2
	branch.Closed = true
	branch.Left, branch.Top = 15, 40

	leaf := printout.NewOutline()
	leaf.Title = "Leaf"
	leaf.Level = 3
	leaf.Left, leaf.Top = 15, 55

	second := printout.NewOutline()
	second.Title = "Открытая ветвь"
	second.Level = 2
	second.Left, second.Top = 15, 70

	doc := document(top, branch, leaf, second)
	raw := renderBytes(test, doc, Uncompressed())
	file, err := pdfscan.Read(raw)
	if err != nil {
		test.Fatal(err)
	}

	root, ok := file.Get(file.Catalog(), "Outlines").(pdfscan.Dict)
	if !ok {
		test.Fatal("the catalog has no outline")
	}
	if mode, _ := file.Get(file.Catalog(), "PageMode").(pdfscan.Name); mode != "UseOutlines" {
		test.Errorf("PageMode = %q, want UseOutlines", mode)
	}
	// One root with two children, one of them closed,
	// so three entries are visible when the root is open.
	if count, _ := file.Number(root, "Count"); count != 3 {
		test.Errorf("the root's Count = %v, want 3", count)
	}

	first, ok := file.Get(root, "First").(pdfscan.Dict)
	if !ok {
		test.Fatal("the outline root has no first entry")
	}
	if title, _ := file.Get(first, "Title").(pdfscan.String); string(title) != "Report" {
		test.Errorf("the first title is %q", title)
	}
	if count, _ := file.Number(first, "Count"); count != 2 {
		test.Errorf("Report's Count = %v, want 2: one open child and one closed", count)
	}

	closed, ok := file.Get(first, "First").(pdfscan.Dict)
	if !ok {
		test.Fatal("Report has no children")
	}
	if count, _ := file.Number(closed, "Count"); count != -1 {
		test.Errorf("a closed entry's Count = %v, want -1", count)
	}
	// The destination keeps the entry's own x, and turns its y
	// the right way up for the page it names.
	dest, ok := file.Get(closed, "Dest").(pdfscan.Array)
	if !ok || len(dest) != 5 {
		test.Fatalf("the destination is %s", pdfscan.Text(file.Get(closed, "Dest")))
	}
	if left, _ := dest[2].(float64); left != 15 {
		test.Errorf("the destination x = %v, want the entry's own 15", left)
	}
	if top, _ := dest[3].(float64); top != 200-40 {
		test.Errorf("the destination y = %v, want %v", top, 200-40)
	}

	// A title outside ASCII survives as UTF-16.
	open, ok := file.Get(first, "Last").(pdfscan.Dict)
	if !ok {
		test.Fatal("Report has no last child")
	}
	if title, _ := file.Get(open, "Title").(pdfscan.String); len(title) < 2 ||
		title[0] != 0xFE || title[1] != 0xFF {
		test.Errorf("a non-ASCII title was not written as UTF-16: %q", title)
	}
}

// A printout with no outline entries gets no outline object
// and no page mode telling a reader to show one.
func TestNoOutline(test *testing.T) {
	doc := document(text(printout.Box{Left: 10, Top: 10, Width: 80, Height: 12},
		"left", "plain"))
	raw := renderBytes(test, doc, Uncompressed())
	if bytes.Contains(raw, []byte("/Outlines")) {
		test.Errorf("a document without outline entries carries an outline")
	}
}

// An xref becomes a link annotation over its box, and its body is drawn
// as ordinary marks.
func TestXrefAnnotations(test *testing.T) {
	entry := printout.NewOutline()
	entry.Title = "Target"
	entry.Level = 1
	entry.Name = "target"
	entry.Left, entry.Top = 5, 60

	url := printout.NewXref()
	url.Type = "url"
	url.Target = "https://example.invalid/docs"
	url.Caption = "Documentation"
	url.Box = printout.Box{Left: 10, Top: 10, Width: 100, Height: 20}
	url.Marks = []printout.Mark{
		text(printout.Box{Left: 10, Top: 10, Width: 100, Height: 12}, "left", "Docs"),
	}

	inner := printout.NewXref()
	inner.Type = "outline"
	inner.Target = "target"
	inner.Box = printout.Box{Left: 10, Top: 40, Width: 60, Height: 15}

	pages, _ := render(test, document(entry, url, inner))
	if len(pages[0].Annots) != 2 {
		test.Fatalf("annotations = %d, want 2", len(pages[0].Annots))
	}
	if len(pages[0].Runs) != 1 || pages[0].Runs[0].Text != "Docs" {
		test.Errorf("the xref's body was not drawn: %v", pages[0].Runs)
	}

	link := pages[0].Annots[0]
	near(test, "link left", link.Left, 10)
	near(test, "link top", link.Top, 10)
	near(test, "link width", link.Width, 100)
	near(test, "link height", link.Height, 20)
	if link.URI != url.Target {
		test.Errorf("URI = %q, want %q", link.URI, url.Target)
	}
	if link.Contents != "Documentation" {
		test.Errorf("the caption = %q", link.Contents)
	}

	internal := pages[0].Annots[1]
	if internal.DestPage != 0 {
		test.Errorf("the destination page = %d, want 0", internal.DestPage)
	}
	near(test, "destination x", internal.DestLeft, 5)
	near(test, "destination y", internal.DestTop, 60)
}

// An xref naming an outline no entry claims is an error, since
// silently dropping the link would leave a region that looks clickable.
func TestXrefUnknownTarget(test *testing.T) {
	mark := printout.NewXref()
	mark.Type = "outline"
	mark.Target = "nowhere"
	mark.Box = printout.Box{Left: 10, Top: 10, Width: 50, Height: 10}

	var out bytes.Buffer
	err := Write(document(mark), &out)
	if err == nil {
		test.Fatal("an unresolved outline link rendered without complaint")
	}
	if !strings.Contains(err.Error(), "nowhere") {
		test.Errorf("the error does not name the target: %v", err)
	}
}

// A page states its own size when it differs from the header's default,
// and a destination on such a page is measured against that page.
func TestPageSizes(test *testing.T) {
	doc := document(text(printout.Box{Left: 10, Top: 10, Width: 50, Height: 12},
		"left", "one"))
	entry := printout.NewOutline()
	entry.Title = "On the wide page"
	entry.Level = 1
	entry.Left, entry.Top = 10, 30
	doc.Pages = append(doc.Pages, &printout.Page{
		Kind: "page", Number: 2, Width: 500, Height: 400,
		Marks: []printout.Mark{entry},
	})

	pages, file := render(test, doc)
	if len(pages) != 2 {
		test.Fatalf("pages = %d, want 2", len(pages))
	}
	if pages[0].Width != 300 || pages[0].Height != 200 {
		test.Errorf("page 1 is %gx%g, want the header's default",
			pages[0].Width, pages[0].Height)
	}
	if pages[1].Width != 500 || pages[1].Height != 400 {
		test.Errorf("page 2 is %gx%g, want its own size",
			pages[1].Width, pages[1].Height)
	}

	root, _ := file.Get(file.Catalog(), "Outlines").(pdfscan.Dict)
	first, _ := file.Get(root, "First").(pdfscan.Dict)
	dest, ok := file.Get(first, "Dest").(pdfscan.Array)
	if !ok || len(dest) != 5 {
		test.Fatalf("the destination is %s", pdfscan.Text(file.Get(first, "Dest")))
	}
	if top, _ := dest[3].(float64); top != 400-30 {
		test.Errorf("the destination y = %v, want %v: the second page's own height",
			top, 400-30)
	}
}

// The document information dictionary comes from the printout's header,
// including its build time, so rendering says nothing about when it ran.
func TestDocumentInformation(test *testing.T) {
	doc := document(text(printout.Box{Left: 10, Top: 10, Width: 50, Height: 12},
		"left", "one"))
	doc.Header.Report = &printout.ReportMeta{
		Name: "Quarterly", Author: "als", Description: "Sales by region",
	}
	_, file := render(test, doc)
	info := file.Info()
	if info == nil {
		test.Fatal("there is no information dictionary")
	}
	for key, want := range map[pdfscan.Name]string{
		"Title":        "Quarterly",
		"Author":       "als",
		"Subject":      "Sales by region",
		"Producer":     "sr test",
		"CreationDate": "D:20260804091244+00'00'",
	} {
		got, _ := file.Get(info, key).(pdfscan.String)
		if string(got) != want {
			test.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// Rendering is a pure function of the printout: the same document twice
// is the same bytes twice, and nothing about the machine or run time gets in.
func TestReproducible(test *testing.T) {
	doc := document(
		text(printout.Box{Left: 10, Top: 10, Width: 100, Height: 24},
			"justified", "one two three", "four"),
	)
	doc.Header.Data["logo"] = pngBlob(test, 8, 4, color.NRGBA{10, 20, 30, 255})
	mark := printout.NewImage()
	mark.Type = "png"
	mark.Data = "logo"
	mark.Box = printout.Box{Left: 10, Top: 40, Width: 16, Height: 8}
	doc.Pages[0].Marks = append(doc.Pages[0].Marks, mark)

	one := renderBytes(test, doc)
	two := renderBytes(test, doc)
	if !bytes.Equal(one, two) {
		test.Errorf("two renders of one printout differ: %d and %d bytes",
			len(one), len(two))
	}
}

// The embedded font is a subset: it carries the glyphs the document used
// and not the rest of the face, its name carries the six-letter tag PDF
// requires, and the widths it states are the widths the engine measured.
func TestFontSubset(test *testing.T) {
	const line = "Subset"
	doc := document(text(printout.Box{Left: 10, Top: 10, Width: 200, Height: 12},
		"left", line))
	pages, file := render(test, doc)

	fonts, err := file.Fonts(file.Pages()[0])
	if err != nil {
		test.Fatal(err)
	}
	if len(fonts) != 1 {
		test.Fatalf("fonts = %d, want 1", len(fonts))
	}
	var embedded *pdfscan.Font
	for _, font := range fonts {
		embedded = font
	}

	tag, name, found := strings.Cut(embedded.BaseFont, "+")
	if !found || len(tag) != 6 || strings.ToUpper(tag) != tag {
		test.Errorf("BaseFont = %q, want six upper-case letters and a plus",
			embedded.BaseFont)
	}
	if name != "GoRegular" {
		test.Errorf("the subset names the face %q, want its PostScript name", name)
	}

	// One glyph per distinct character, plus the empty glyph at code 0.
	distinct := map[rune]bool{}
	for _, char := range line {
		distinct[char] = true
	}
	subset, err := sfnt.Parse(embedded.FontFile, 0)
	if err != nil {
		test.Fatalf("the embedded subset does not parse: %v", err)
	}
	if subset.NumGlyphs != len(distinct)+1 {
		test.Errorf("the subset holds %d glyphs, want %d", subset.NumGlyphs, len(distinct)+1)
	}
	original := face(test, goRegular, 10)
	if subset.NumGlyphs >= 100 {
		test.Errorf("the whole face was embedded: %d glyphs", subset.NumGlyphs)
	}

	// Every code's width, and every subset glyph's own advance, agree
	// with the face the engine measured.
	for code, char := range []rune{'S', 'b', 'e', 's', 't', 'u'} {
		gid, ok := original.Glyph(char)
		if !ok {
			test.Fatalf("the committed face lacks %q", char)
		}
		want := original.GlyphAdvance(gid) * 1000 / original.Upem()
		got := embedded.Widths[uint16(code+1)]
		if got-want > 0.001 || want-got > 0.001 {
			test.Errorf("the width of %q is %v, want %v", char, got, want)
		}
		got = float64(subset.Advance(uint16(code + 1)))
		if got != original.GlyphAdvance(gid) {
			test.Errorf("the subset's advance for %q is %v, want %v",
				char, got, original.GlyphAdvance(gid))
		}
	}
	if got := pages[0].Runs[0].Text; got != line {
		test.Errorf("the text read back as %q, want %q", got, line)
	}
}

// A character the resolved face does not have draws the empty box
// and still reads back as itself, so the text is searchable
// even where the glyph is missing.
func TestMissingGlyph(test *testing.T) {
	const line = "a中b"
	doc := document(text(printout.Box{Left: 10, Top: 10, Width: 200, Height: 12},
		"left", line))
	pages, _ := render(test, doc)
	if got := pages[0].Runs[0].Text; got != line {
		test.Errorf("the text read back as %q, want %q", got, line)
	}
	metrics := face(test, goRegular, 10)
	near(test, "width with a missing glyph", pages[0].Runs[0].Width, metrics.Width(line))
}

// A text mark naming a font the header does not carry is an error
// rather than a page with a gap in it.
func TestUnknownFont(test *testing.T) {
	mark := text(printout.Box{Left: 10, Top: 10, Width: 50, Height: 12}, "left", "x")
	mark.Font = "nowhere"
	var out bytes.Buffer
	err := Write(document(mark), &out)
	if err == nil {
		test.Fatal("a text mark on an undeclared font rendered without complaint")
	}
	if !strings.Contains(err.Error(), "nowhere") {
		test.Errorf("the error does not name the font: %v", err)
	}
}

// A font file the printout names and the machine does not have
// is an error naming the file.
func TestMissingFontFile(test *testing.T) {
	doc := document(text(printout.Box{Left: 10, Top: 10, Width: 50, Height: 12},
		"left", "x"))
	doc.Header.Fonts[0].ResolvedFile = "../example/fonts/absent.ttf"
	var out bytes.Buffer
	err := Write(doc, &out)
	if err == nil {
		test.Fatal("a missing font file rendered without complaint")
	}
	if !strings.Contains(err.Error(), "absent.ttf") ||
		!strings.Contains(err.Error(), "body") {
		test.Errorf("the error names neither the file nor the font: %v", err)
	}
}

// A font the printout embedded as a data blob is read out of the header
// rather than off the disk.
func TestFontFromBlob(test *testing.T) {
	raw, err := os.ReadFile(goBold)
	if err != nil {
		test.Fatal(err)
	}
	doc := document(text(printout.Box{Left: 10, Top: 10, Width: 100, Height: 12},
		"left", "Embedded"))
	doc.Header.Data["bold"] = &printout.Blob{
		Encoding: "base64",
		Content:  base64.StdEncoding.EncodeToString(raw),
	}
	doc.Header.Fonts[0] = printout.FontEntry{
		Name: "body", Size: 10, Bold: true,
		ResolvedData: "bold", ResolvedFace: "Go", ResolvedBy: "explicit",
	}

	pages, file := render(test, doc)
	if got := pages[0].Runs[0].Text; got != "Embedded" {
		test.Errorf("text = %q", got)
	}
	fonts, err := file.Fonts(file.Pages()[0])
	if err != nil {
		test.Fatal(err)
	}
	for _, font := range fonts {
		if !strings.HasSuffix(font.BaseFont, "+Go-Bold") {
			test.Errorf("BaseFont = %q, want the bold face", font.BaseFont)
		}
	}
}

// A printout with no pages is not a document,
// and inventing a blank page for it would hide whatever produced it.
func TestNoPages(test *testing.T) {
	doc := document()
	doc.Pages = nil
	var out bytes.Buffer
	if err := Write(doc, &out); err == nil {
		test.Fatal("an empty printout rendered")
	}
}

// A failed render leaves the file that was there alone.
//
// Rendering reads the font files the printout resolved, which are
// not part of the document and can have moved since it was written.
// Opening the output first would turn that into a lost report: the
// previous PDF truncated to nothing, and an error explaining a font.
func TestWriteFileKeepsThePreviousFileOnFailure(test *testing.T) {
	path := filepath.Join(test.TempDir(), "report.pdf")
	if err := WriteFile(document(), path); err != nil {
		test.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		test.Fatal(err)
	}
	if len(before) == 0 {
		test.Fatal("the first render wrote nothing")
	}

	broken := document()
	broken.Header.Fonts[0].ResolvedFile = "no/such/font.ttf"
	if err := WriteFile(broken, path); err == nil {
		test.Fatal("a printout naming a font that is not there rendered")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		test.Fatalf("the earlier report is gone: %v", err)
	}
	if !bytes.Equal(before, after) {
		test.Errorf("the file changed: %d bytes before, %d after", len(before), len(after))
	}
}

// The font descriptor states the face's extent the way PDF defines it:
// /Ascent above the baseline, /Descent below it.
//
// The face here writes both of its hhea metrics as magnitudes,
// which some do. Copying that into /Descent would put the bottom
// of the face above its own baseline, and a reader substituting
// the font would lay the text out from it.
func TestDescriptorSignsTheExtent(test *testing.T) {
	raw, err := os.ReadFile(goRegular)
	if err != nil {
		test.Fatal(err)
	}
	patched := append([]byte(nil), raw...)
	count := int(binary.BigEndian.Uint16(patched[4:6]))
	found := false
	for index := 0; index < count; index++ {
		at := 12 + index*16
		if string(patched[at:at+4]) != "hhea" {
			continue
		}
		hhea := int(binary.BigEndian.Uint32(patched[at+8 : at+12]))
		descender := int16(binary.BigEndian.Uint16(patched[hhea+6 : hhea+8]))
		if descender < 0 {
			descender = -descender
		}
		binary.BigEndian.PutUint16(patched[hhea+6:hhea+8], uint16(descender))
		found = true
	}
	if !found {
		test.Fatal("the fixture has no hhea table")
	}

	path := filepath.Join(test.TempDir(), "Magnitudes.ttf")
	if err := os.WriteFile(path, patched, 0o644); err != nil {
		test.Fatal(err)
	}
	doc := document(text(printout.Box{Left: 10, Top: 10, Width: 200, Height: 12},
		"left", "Descent"))
	doc.Header.Fonts[0].ResolvedFile = path

	_, file := render(test, doc)
	fonts, err := file.Fonts(file.Pages()[0])
	if err != nil {
		test.Fatal(err)
	}
	if len(fonts) != 1 {
		test.Fatalf("fonts = %d, want one", len(fonts))
	}
	for _, font := range fonts {
		ascent, _ := file.Get(font.Descriptor, "Ascent").(float64)
		descent, _ := file.Get(font.Descriptor, "Descent").(float64)
		if ascent <= 0 {
			test.Errorf("/Ascent = %v, want it above the baseline", ascent)
		}
		if descent >= 0 {
			test.Errorf("/Descent = %v, want it below the baseline", descent)
		}
	}
}

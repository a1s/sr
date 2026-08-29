package sr

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a1s/sr/printout"
)

func TestNDJSONRoundTrip(test *testing.T) {
	out := buildString(test, minimal, rowsOf(1, 2, 3))

	dir := test.TempDir()
	path := filepath.Join(dir, "report.srp.jsonl")
	if err := out.WriteFile(path); err != nil {
		test.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		test.Fatal(err)
	}
	// The first line is the header, and every line after it is a page.
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		test.Fatalf("lines = %d, want a header and one page", len(lines))
	}
	if !strings.HasPrefix(lines[0], `{"sr":1,"kind":"header"`) {
		test.Errorf("header line begins %q", lines[0][:40])
	}
	if !strings.Contains(lines[1], `"kind":"page"`) {
		test.Errorf("page line = %q", lines[1][:40])
	}
	// Numbers serialize in the shortest form that round-trips, so an integral
	// value carries no fractional part.
	if !strings.Contains(lines[1], `"width":200`) {
		test.Errorf("want an integral width written without a fractional part:\n%s", lines[1])
	}

	back, err := printout.ReadFile(path)
	if err != nil {
		test.Fatal(err)
	}
	if err := back.Validate(); err != nil {
		test.Fatalf("%v", err)
	}
	if len(back.Pages) != len(out.Pages) {
		test.Fatalf("pages = %d, want %d", len(back.Pages), len(out.Pages))
	}
	if got, want := joined(linesOf(back.Pages[0])), joined(linesOf(out.Pages[0])); got != want {
		test.Errorf("round trip changed the marks:\n got %q\nwant %q", got, want)
	}
	if back.Header.Built != out.Header.Built || back.Header.Engine != out.Header.Engine {
		test.Errorf("header did not round-trip: %+v", back.Header)
	}
}

func linesOf(page *printout.Page) []string {
	var out []string
	for _, mark := range page.Marks {
		if text, ok := mark.(*printout.Text); ok {
			out = append(out, strings.Join(text.Lines, "|"))
		}
	}
	return out
}

func TestCBORRoundTrip(test *testing.T) {
	out := buildString(test, minimal, rowsOf(1, 2, 3))
	dir := test.TempDir()
	path := filepath.Join(dir, "report.srp.cbor")
	if err := out.WriteFile(path); err != nil {
		test.Fatal(err)
	}
	back, err := printout.ReadFile(path)
	if err != nil {
		test.Fatal(err)
	}
	if len(back.Pages) != 1 {
		test.Fatalf("pages = %d", len(back.Pages))
	}
	if got, want := joined(linesOf(back.Pages[0])), joined(linesOf(out.Pages[0])); got != want {
		test.Errorf("CBOR round trip changed the marks: %q vs %q", got, want)
	}
	// The CBOR encoding is smaller than the NDJSON one, which is why it exists.
	jsonPath := filepath.Join(dir, "report.srp.jsonl")
	if err := out.WriteFile(jsonPath); err != nil {
		test.Fatal(err)
	}
	cborInfo, _ := os.Stat(path)
	jsonInfo, _ := os.Stat(jsonPath)
	test.Logf("cbor %d bytes, ndjson %d bytes", cborInfo.Size(), jsonInfo.Size())
}

// A path the template named travels with the printout
//
// Check that it is written relative to wherever the printout is being written;
// and one printout serialized to two directories carries two different values, both right.
func TestFontPathIsRelativeToThePrintout(test *testing.T) {
	out := buildString(test, minimal, rowsOf(1))

	dir := test.TempDir()
	deep := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		test.Fatal(err)
	}

	pathOf := func(target string) string {
		var buf bytes.Buffer
		if err := out.WriteNDJSON(&buf, target); err != nil {
			test.Fatal(err)
		}
		back, err := printout.ReadNDJSON(bytes.NewReader(buf.Bytes()), target)
		if err != nil {
			test.Fatal(err)
		}
		return back.Header.Fonts[0].ResolvedFile
	}

	shallow := pathOf(dir)
	nested := pathOf(deep)
	if shallow == nested {
		test.Errorf("two destinations must give two paths, both %q", shallow)
	}
	for _, path := range []string{shallow, nested} {
		if filepath.IsAbs(path) {
			test.Errorf("a template-named font is written relative, got %q", path)
		}
		if strings.Contains(path, "\\") {
			test.Errorf("separators are forward slashes on every platform, got %q", path)
		}
		if !strings.HasSuffix(path, "Go-Regular.ttf") {
			test.Errorf("path = %q", path)
		}
	}
	// The rewrite is only a rewrite: writing a printout creates one file
	// and nothing beside it.
	entries, err := os.ReadDir(dir)
	if err != nil {
		test.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "a" {
		test.Errorf("writing a printout must not put files beside it: %v", entries)
	}
}

// The same template over the same data produces a byte-identical printout
// when the build time is fixed and fonts are strict.
func TestReproducibility(test *testing.T) {
	render := func() []byte {
		out := buildString(test, minimal, rowsOf(1, 2, 3))
		var buf bytes.Buffer
		if err := out.WriteNDJSON(&buf, "."); err != nil {
			test.Fatal(err)
		}
		return buf.Bytes()
	}
	if first, second := render(), render(); !bytes.Equal(first, second) {
		test.Error("two builds of the same template over the same data differ")
	}
}

// Without a fixed build time the run timestamp differs,
// which is the one thing reproducibility asks the caller to pin.
func TestBuildTimeIsWhatVaries(test *testing.T) {
	tpl, err := ParseTemplate("example/fonts/test.kdl", minimal)
	if err != nil {
		test.Fatal(err)
	}
	one, err := tpl.Build(rowsOf(1), StrictFonts(), WithBuildTime(time.Unix(0, 0).UTC()))
	if err != nil {
		test.Fatal(err)
	}
	two, err := tpl.Build(rowsOf(1), StrictFonts(), WithBuildTime(time.Unix(86400, 0).UTC()))
	if err != nil {
		test.Fatal(err)
	}
	if one.Header.Built == two.Header.Built {
		test.Error("the header records the run's BUILD_TIME")
	}
}

// An embedded image's bytes go into the header's data table
// under a generated name, and two images from one source share one entry.
func TestEmbeddedImageSharesOneDataEntry(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=8
  data "swatch" encoding="base64" {
    content """
      iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAIAAACQkWg2AAAAI0lEQVR42mNgiGSAo89v
      n8MRTvFBqIEoRUjig1HDaDwMCg0ArOyQEA2r1rMAAAAASUVORK5CYII=
      """
  }
  layout width=300 height=300 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    detail height=40 {
      image data="swatch" scale="fill" left=0 width=20 height=20
      image data="swatch" scale="fill" left=30 width=20 height=20
    }
  }
}`
	out := buildString(test, src, rowsOf(1))
	if len(out.Header.Data) != 1 {
		test.Errorf("data entries = %d, want one shared by both marks: %v",
			len(out.Header.Data), out.Header.Data)
	}
	blob := out.Header.Data["swatch"]
	if blob == nil || blob.Encoding != "base64" || blob.Content == "" {
		test.Fatalf("data entry = %+v", blob)
	}
	count := 0
	for _, mark := range out.Pages[0].Marks {
		if im, ok := mark.(*printout.Image); ok {
			count++
			if im.Data != "swatch" || im.File != "" {
				test.Errorf("image mark = %+v", im)
			}
			if im.Type != "png" {
				test.Errorf("type = %q, want png sniffed from the content", im.Type)
			}
		}
	}
	if count != 2 {
		test.Errorf("image marks = %d, want 2", count)
	}
}

// A page that runs at a size and inset of its own -- which only a subreport
// that paginates itself produces -- carries the difference and reads it back.
//
// Zero is a real margin, so the fields are pointers: a page flush to the paper
// edge under a header that insets has to be able to say so.
func TestPageGeometryOverrideRoundTrips(test *testing.T) {
	doc := buildString(test, minimal, rowsOf(1))
	flush := 0.0
	doc.Pages[0].Width, doc.Pages[0].Height = 400, 500
	doc.Pages[0].LeftMargin = &flush

	dir := test.TempDir()
	path := filepath.Join(dir, "report.srp.jsonl")
	if err := doc.WriteFile(path); err != nil {
		test.Fatal(err)
	}
	back, err := printout.ReadFile(path)
	if err != nil {
		test.Fatal(err)
	}
	page := back.Pages[0]
	if page.Width != 400 || page.Height != 500 {
		test.Errorf("size = %g x %g", page.Width, page.Height)
	}
	if page.LeftMargin == nil || *page.LeftMargin != 0 {
		test.Fatalf("left margin = %v, want a recorded zero", page.LeftMargin)
	}
	if page.RightMargin != nil {
		test.Errorf("right margin = %v, want the header's", *page.RightMargin)
	}
	geom := page.Geometry(back.Header.Page)
	if geom.LeftMargin != 0 || geom.RightMargin != back.Header.Page.RightMargin {
		test.Errorf("resolved geometry = %+v", geom)
	}

	// CBOR carries it too.
	var binary bytes.Buffer
	if err := doc.WriteCBOR(&binary, dir); err != nil {
		test.Fatal(err)
	}
	viaCBOR, err := printout.ReadCBOR(&binary, dir)
	if err != nil {
		test.Fatal(err)
	}
	page = viaCBOR.Pages[0]
	if page.Width != 400 || page.LeftMargin == nil || *page.LeftMargin != 0 {
		test.Errorf("cbor page = %+v", page)
	}
}

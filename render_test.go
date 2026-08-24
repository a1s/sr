package sr

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a1s/sr/internal/fontres"
	"github.com/a1s/sr/internal/pdfscan"
	"github.com/a1s/sr/pdf"
	"github.com/a1s/sr/printout"
)

// renderScan renders a printout and reads the file back.
func renderScan(test *testing.T, doc *Printout) ([]*pdfscan.Page, []byte) {
	test.Helper()
	var raw bytes.Buffer
	if err := pdf.Write(doc, &raw); err != nil {
		test.Fatalf("rendering: %v", err)
	}
	file, err := pdfscan.Read(raw.Bytes())
	if err != nil {
		test.Fatalf("reading the rendered file: %v", err)
	}
	pages, err := file.ReadPages()
	if err != nil {
		test.Fatalf("replaying the content streams: %v", err)
	}
	return pages, raw.Bytes()
}

// faces loads every font a printout resolved, the way the renderer does,
// so that a test can say where a line ought to have landed.
func faces(test *testing.T, doc *Printout) map[string]*fontres.Face {
	test.Helper()
	out := map[string]*fontres.Face{}
	for _, entry := range doc.Header.Fonts {
		raw, err := os.ReadFile(entry.ResolvedPath())
		if err != nil {
			test.Fatalf("font %q: %v", entry.Name, err)
		}
		face, err := fontres.Load(raw, entry.ResolvedIndex, entry.Size)
		if err != nil {
			test.Fatalf("font %q: %v", entry.Name, err)
		}
		out[entry.Name] = face
	}
	return out
}

// textMarks collects a page's text marks in paint order,
// flattening xrefs, which is the order the renderer draws them in.
func textMarks(page *printout.Page) []*printout.Text {
	var out []*printout.Text
	var walk func([]printout.Mark)
	walk = func(marks []printout.Mark) {
		for _, mark := range marks {
			switch typed := mark.(type) {
			case *printout.Text:
				out = append(out, typed)
			case *printout.Xref:
				walk(typed.Marks)
			}
		}
	}
	walk(page.Marks)
	return out
}

// checkRendered compares a rendered document against the printout it came
// from, line by line: the text, where the line starts, and its baseline.
//
// The expectations are computed from the rules in doc/render.md rather
// than from the renderer, so the two agreeing means the file says what
// the document does.
func checkRendered(test *testing.T, doc *Printout, pages []*pdfscan.Page) {
	test.Helper()
	const slack = 0.002
	loaded := faces(test, doc)
	sizes := map[string]int{}
	for _, entry := range doc.Header.Fonts {
		sizes[entry.Name] = entry.Size
	}

	if len(pages) != len(doc.Pages) {
		test.Fatalf("rendered pages = %d, want %d", len(pages), len(doc.Pages))
	}
	lines := 0
	for index, source := range doc.Pages {
		rendered := pages[index]
		runs := rendered.Runs
		at := 0
		for _, mark := range textMarks(source) {
			face := loaded[mark.Font]
			size := float64(sizes[mark.Font])
			ascender, descender := face.VerticalMetrics()
			scale := size / face.Upem()
			first := mark.Box.Top +
				(mark.Leading-(ascender*scale+descender*scale))/2 + ascender*scale

			for lineIndex, line := range mark.Lines {
				if line == "" {
					// An empty line draws no glyphs, so the file holds
					// nothing to compare it against.
					continue
				}
				if at >= len(runs) {
					test.Fatalf("page %d: the file ran out of runs at line %q",
						source.Number, line)
				}
				baseline := first + float64(lineIndex)*mark.Leading
				// A justified line arrives as one run per word; every
				// other line as one run.
				group := []pdfscan.Run{runs[at]}
				at++
				for at < len(runs) && math.Abs(runs[at].Baseline-baseline) <= slack &&
					strings.HasPrefix(line, joinRuns(group)+runs[at].Text) {
					group = append(group, runs[at])
					at++
				}
				lines++

				if got := joinRuns(group); got != line {
					test.Errorf("page %d: line %d of %q read back as %q",
						source.Number, lineIndex, line, got)
					continue
				}
				if math.Abs(group[0].Baseline-baseline) > slack {
					test.Errorf("page %d: %q sits on baseline %.3f, want %.3f",
						source.Number, line, group[0].Baseline, baseline)
				}
				width := face.Width(line)
				want := mark.Box.Left
				switch mark.Align {
				case "center":
					want = mark.Box.Left + (mark.Box.Width-width)/2
				case "right":
					want = mark.Box.Left + mark.Box.Width - width
				}
				if math.Abs(group[0].Left-want) > slack {
					test.Errorf("page %d: %q starts at %.3f, want %.3f",
						source.Number, line, group[0].Left, want)
				}
				if len(group) > 1 {
					// The only reason a line comes in pieces is
					// justification, which ends at the box's far edge.
					tail := group[len(group)-1]
					got := tail.Left + tail.Width
					if math.Abs(got-(mark.Box.Left+mark.Box.Width)) > slack {
						test.Errorf("page %d: justified %q ends at %.3f, want %.3f",
							source.Number, line, got, mark.Box.Left+mark.Box.Width)
					}
				} else if math.Abs(group[0].Width-width) > slack {
					test.Errorf("page %d: %q measures %.3f in the file, want %.3f",
						source.Number, line, group[0].Width, width)
				}
			}
		}
		if at != len(runs) {
			test.Errorf("page %d: the file holds %d runs, the printout accounts for %d",
				source.Number, len(runs), at)
		}
	}
	if lines == 0 {
		test.Fatal("no lines were compared")
	}
	test.Logf("compared %d lines over %d pages", lines, len(pages))
}

func joinRuns(runs []pdfscan.Run) string {
	var out strings.Builder
	for _, run := range runs {
		out.WriteString(run.Text)
	}
	return out.String()
}

// The reference report renders, and every line in the file is
// the line the printout says, in the place the printout puts it.
func TestSakilaRendersFaithfully(test *testing.T) {
	doc := buildSakila(test)
	pages, _ := renderScan(test, doc)
	checkRendered(test, doc, pages)

	// The features the reference template exercises reach the file.
	var barcodes, images, links int
	for _, page := range pages {
		images += len(page.Draws)
		links += len(page.Annots)
		barcodes += len(page.Rects)
	}
	if images < 2 {
		test.Errorf("images drawn = %d, want the referenced one and the embedded one", images)
	}
	if links < 2 {
		test.Errorf("links = %d, want a url and an outline link", links)
	}
	if barcodes < 100 {
		test.Errorf("filled rectangles = %d, want the six barcodes' bars", barcodes)
	}
}

// A report over several pages renders faithfully too, with its page
// header and footer on each of them and a deferred page count resolved.
//
// The second reference template is not used here: it needs subreports,
// which are staged after this, so it does not build yet.
func TestMultiPageRendersFaithfully(test *testing.T) {
	const paged = `report name="Paged" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=10
  font "note" file="Go-Bold.ttf" size=9 underline=#true
  layout pagesize="A5" leftmargin=20 rightmargin=20 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    header height=20 {
      field text="Rows" left=0 width=100 height=12 { style font="note" }
      field expr="'page %d of %s' % (PAGE_NUMBER, FINAL.PAGE_COUNT)" evaltime="report" text="page 9 of 9" align="right" right=0 width=120 height=12
      line left=0 right=0 top=16 height=0 width=0.5
    }
    detail height=12 {
      field expr="n" format="row %d" left=0 width=120 height=12
      field expr="'%s' % (n * 7)" align="right" right=0 width=60 height=12
    }
    footer height=14 {
      field align="justified" left=0 right=0 height=24 stretch=#true expr="'a justified footnote that spans the whole frame width and wraps across two lines of it'"
    }
  }
}`
	rows := make([]any, 60)
	for index := range rows {
		rows[index] = index + 1
	}
	doc := buildString(test, paged, rowsOf(rows...))
	if len(doc.Pages) < 2 {
		test.Fatalf("pages = %d, want a report that breaks", len(doc.Pages))
	}
	pages, _ := renderScan(test, doc)
	checkRendered(test, doc, pages)
}

// A printout written out and read back renders to the same bytes
// as the one it was built from. That is what makes a printout worth
// archiving: the file is the document, not a stage on the way to one.
func TestRenderFromFileMatches(test *testing.T) {
	doc := buildSakila(test)

	var direct bytes.Buffer
	if err := pdf.Write(doc, &direct); err != nil {
		test.Fatal(err)
	}

	dir := test.TempDir()
	path := filepath.Join(dir, "sakila.srp.jsonl")
	if err := doc.WriteFile(path); err != nil {
		test.Fatal(err)
	}
	back, err := printout.ReadFile(path)
	if err != nil {
		test.Fatal(err)
	}
	var reread bytes.Buffer
	if err := pdf.Write(back, &reread); err != nil {
		test.Fatal(err)
	}
	if !bytes.Equal(direct.Bytes(), reread.Bytes()) {
		test.Errorf("rendering a printout read back gave %d bytes against %d",
			reread.Len(), direct.Len())
	}
}

// WritePDF is the same rendering, to a file.
func TestWritePDF(test *testing.T) {
	doc := buildSakila(test)
	path := filepath.Join(test.TempDir(), "sakila.pdf")
	if err := WritePDF(doc, path); err != nil {
		test.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		test.Fatal(err)
	}
	var direct bytes.Buffer
	if err := pdf.Write(doc, &direct); err != nil {
		test.Fatal(err)
	}
	if !bytes.Equal(raw, direct.Bytes()) {
		test.Errorf("WritePDF wrote %d bytes, Write %d", len(raw), direct.Len())
	}
}

// collectionFile writes a two-face ttcf collection into dir.
//
// The faces are the two committed fonts, whole, with each
// table directory's offsets shifted to where its face landed.
// Building the fixture rather than committing one keeps a binary
// out of the tree; the format is explained where the resolver's
// own collection test builds the same thing.
func collectionFile(test *testing.T, dir string) string {
	test.Helper()
	out := make([]byte, 20)
	copy(out, "ttcf")
	binary.BigEndian.PutUint32(out[4:8], 0x00010000)
	binary.BigEndian.PutUint32(out[8:12], 2)

	for index, name := range []string{"Go-Regular.ttf", "Go-Bold.ttf"} {
		face, err := os.ReadFile(filepath.Join("example/fonts", name))
		if err != nil {
			test.Fatal(err)
		}
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
		base := len(out)
		binary.BigEndian.PutUint32(out[12+4*index:16+4*index], uint32(base))
		out = append(out, face...)
		for entry := 0; entry < int(binary.BigEndian.Uint16(out[base+4:base+6])); entry++ {
			at := base + 12 + entry*16
			binary.BigEndian.PutUint32(out[at+8:at+12],
				binary.BigEndian.Uint32(out[at+8:at+12])+uint32(base))
		}
	}

	path := filepath.Join(dir, "Go.ttc")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		test.Fatal(err)
	}
	return path
}

// A face inside a collection survives the whole way to the page.
//
// The engine chooses it, the printout records which one by index,
// and the renderer opens that same one out of the file. Nothing else
// in the suite carries a face index other than zero, and a collection
// whose faces are silently interchanged is a report set in the wrong
// weight with no diagnostic anywhere.
func TestCollectionFaceReachesThePage(test *testing.T) {
	dir := test.TempDir()
	collectionFile(test, dir)
	const source = `report name="Collection" {
  records { member "n" type="int" }
  font "bold" file="Go.ttc" size=12 bold=#true
  layout pagesize="A6" leftmargin=20 rightmargin=20 topmargin=20 bottommargin=20 {
    style font="bold" color="black"
    detail height=16 {
      field expr="n" format="row %d" left=0 width=120 height=14
    }
  }
}`
	tpl, err := ParseTemplate(filepath.Join(dir, "collection.kdl"), source)
	if err != nil {
		test.Fatalf("loading the template:\n%v", err)
	}
	doc, err := tpl.Build(rowsOf(1, 2), StrictFonts(), WithBuildTime(fixedTime))
	if err != nil {
		test.Fatalf("building:\n%v", err)
	}
	if err := doc.Validate(); err != nil {
		test.Fatal(err)
	}
	if len(doc.Header.Warnings) != 0 {
		test.Errorf("the collection holds the declared face,"+
			" so there is nothing to warn about: %v", doc.Header.Warnings)
	}
	if got := doc.Header.Fonts[0].ResolvedIndex; got != 1 {
		test.Fatalf("resolvedIndex = %d, want the bold face at 1", got)
	}

	pages, raw := renderScan(test, doc)
	checkRendered(test, doc, pages)
	file, err := pdfscan.Read(raw)
	if err != nil {
		test.Fatal(err)
	}
	fonts, err := file.Fonts(file.Pages()[0])
	if err != nil {
		test.Fatal(err)
	}
	if len(fonts) != 1 {
		test.Fatalf("fonts on the page = %d, want one", len(fonts))
	}
	for _, font := range fonts {
		if !strings.HasSuffix(font.BaseFont, "+Go-Bold") {
			test.Errorf("BaseFont = %q, want the collection's bold face", font.BaseFont)
		}
	}
}

package sr

import (
	"bytes"
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

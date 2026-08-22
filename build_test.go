package sr

import (
	"testing"

	"github.com/a1s/sr/printout"
)

// minimal is the smallest template the suite uses:
// one font, a detail band of one field, and nothing else.
const minimal = `report name="Minimal" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=10
  layout pagesize="A4" leftmargin=20 rightmargin=20 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    detail height=12 {
      field expr="n" format="row %d" left=0 width=200 height=12
    }
  }
}`

// TestMinimalPrintout checks a whole small printout against the spec, by hand.
//
// A4 is 595.276 by 841.89 points. With 20 pt margins the frame runs from x=20
// and from y=20 downward. Each detail band is 12 pt tall, so the rows sit
// at y=20, 32 and 44. Go Regular at 10 pt has a leading of 1.2 x 10 = 12 pt,
// and valign defaults to top, so each text mark's box is the field's box.
func TestMinimalPrintout(test *testing.T) {
	out := buildString(test, minimal, rowsOf(1, 2, 3))

	if len(out.Pages) != 1 {
		test.Fatalf("pages = %d, want 1", len(out.Pages))
	}
	if out.Header.Pages != 1 {
		test.Errorf("header pages = %d, want 1", out.Header.Pages)
	}
	if out.Header.Report == nil || out.Header.Report.Name != "Minimal" {
		test.Errorf("report metadata = %+v", out.Header.Report)
	}
	if out.Header.Built != "2026-08-04T09:12:44Z" {
		test.Errorf("built = %q", out.Header.Built)
	}
	if !out.Header.StrictFonts {
		test.Error("strictFonts must be recorded")
	}
	if page := out.Header.Page; page.Width != 595.276 || page.Height != 841.89 ||
		page.LeftMargin != 20 || page.RightMargin != 20 ||
		page.TopMargin != 20 || page.BottomMargin != 20 {
		test.Errorf("page geometry = %+v", page)
	}

	if len(out.Header.Fonts) != 1 {
		test.Fatalf("fonts = %d, want 1", len(out.Header.Fonts))
	}
	entry := out.Header.Fonts[0]
	if entry.Name != "body" || entry.Size != 10 ||
		entry.ResolvedBy != "explicit" || entry.ResolvedFace != "Go" {
		test.Errorf("font entry = %+v", entry)
	}
	if entry.Requested != "" {
		test.Errorf("a template that pinned its font by file records no typeface, got %q",
			entry.Requested)
	}

	marks := texts(out.Pages[0])
	if len(marks) != 3 {
		test.Fatalf("text marks = %d, want 3", len(marks))
	}
	for index, text := range marks {
		wantY := 20 + float64(index)*12
		if text.Box.Left != 20 || text.Box.Top != wantY ||
			text.Box.Width != 200 || text.Box.Height != 12 {
			test.Errorf("row %d box = %+v, want {20 %g 200 12}", index, text.Box, wantY)
		}
		if text.Leading != 12 {
			test.Errorf("row %d leading = %g, want 12", index, text.Leading)
		}
		if text.Font != "body" || text.Color != "#000000" || text.Align != "left" {
			test.Errorf("row %d = font %q colour %q align %q",
				index, text.Font, text.Color, text.Align)
		}
		want := []string{"row 1", "row 2", "row 3"}[index]
		if len(text.Lines) != 1 || text.Lines[0] != want {
			test.Errorf("row %d lines = %q, want %q", index, text.Lines, want)
		}
	}
}

// A band's declared height is a minimum, not a cap:
// content that needs more room gets it and the band grows.
func TestBandHeightIsAMinimum(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="string" }
  font "body" file="Go-Regular.ttf" size=10
  layout pagesize="A4" leftmargin=20 rightmargin=20 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    detail height=12 {
      field expr="n" stretch=#true left=0 width=60
    }
  }
}`
	out := buildString(test, src, []map[string]any{
		{"n": "one"},
		{"n": "a much longer piece of text that has to wrap"},
		{"n": "last"},
	})
	marks := texts(out.Pages[0])
	if len(marks) != 3 {
		test.Fatalf("text marks = %d", len(marks))
	}
	if marks[0].Box.Top != 20 {
		test.Errorf("first row at %g, want 20", marks[0].Box.Top)
	}
	if marks[1].Box.Top != 32 {
		test.Errorf("second row at %g, want 32", marks[1].Box.Top)
	}
	wrapped := len(marks[1].Lines)
	if wrapped < 2 {
		test.Fatalf("the middle row did not wrap: %q", marks[1].Lines)
	}
	// The band grew to the wrapped text, so the row after it
	// starts lower than the declared 12 would put it.
	want := 32 + float64(wrapped)*12
	if marks[2].Box.Top != want {
		test.Errorf("third row at %g, want %g — the band grew to %d lines",
			marks[2].Box.Top, want, wrapped)
	}
}

// Without stretch, text that does not fit is truncated at a line boundary
// rather than growing the band.
func TestNonStretchFieldTruncates(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="string" }
  font "body" file="Go-Regular.ttf" size=10
  layout pagesize="A4" leftmargin=20 rightmargin=20 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    detail height=12 {
      field expr="n" left=0 width=60 height=12
    }
  }
}`
	out := buildString(test, src, []map[string]any{
		{"n": "a much longer piece of text that has to wrap"},
	})
	marks := texts(out.Pages[0])
	if len(marks) != 1 {
		test.Fatalf("text marks = %d", len(marks))
	}
	if len(marks[0].Lines) != 1 {
		test.Errorf("lines = %q, want one: a 12 pt box holds one 12 pt line", marks[0].Lines)
	}
	if marks[0].Box.Height != 12 {
		test.Errorf("height = %g, want 12", marks[0].Box.Height)
	}
}

// A suppressed element contributes no marks and no height,
// so the printed rows stay contiguous.
func TestPrintWhenSuppression(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=10
  layout pagesize="A4" leftmargin=20 rightmargin=20 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    detail printwhen="n % 2" height=12 {
      field expr="n" format="row %d" left=0 width=200 height=12
    }
  }
}`
	out := buildString(test, src, rowsOf(1, 2, 3, 4, 5))
	if got, want := joined(lines(out.Pages[0])), "row 1,row 3,row 5"; got != want {
		test.Errorf("got %q, want %q", got, want)
	}
	for index, text := range texts(out.Pages[0]) {
		if want := 20 + float64(index)*12; text.Box.Top != want {
			test.Errorf("row %d at %g, want %g", index, text.Box.Top, want)
		}
	}
}

// ITEM_NUMBER describes the input and REPORT_COUNT the output,
// so they differ exactly where a printwhen suppresses a detail.
func TestItemNumberAgainstReportCount(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=10
  layout pagesize="A4" leftmargin=20 rightmargin=20 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    detail printwhen="n % 2" height=12 {
      field expr="(ITEM_NUMBER, REPORT_COUNT, DATA_COUNT)" format="%d/%d/%d" left=0 width=200 height=12
    }
  }
}`
	out := buildString(test, src, rowsOf(1, 2, 3, 4, 5))
	// REPORT_COUNT is read where the band is measured, which is before it is
	// committed, so it lags the row it appears in by one.
	if got, want := joined(lines(out.Pages[0])), "1/0/5,3/1/5,5/2/5"; got != want {
		test.Errorf("got %q, want %q", got, want)
	}
}

// Document order is paint order: a filled rectangle written before the fields
// comes out first, so the fields sit on it.
func TestPaintOrderIsDocumentOrder(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=10
  layout pagesize="A4" leftmargin=20 rightmargin=20 topmargin=20 bottommargin=20 {
    style font="body" color="black" bgcolor="#EEEEEE"
    detail height=12 {
      rectangle stroke=#false
      field expr="n" left=0 width=100 height=12
      line width=0 bottom=0 height=0
    }
  }
}`
	out := buildString(test, src, rowsOf(1))
	if got, want := joined(kinds(out.Pages[0])), "rectangle,text,line"; got != want {
		test.Errorf("marks = %q, want %q", got, want)
	}
}

// A rectangle's two halves switch off independently: stroke=#false suppresses
// the outline even though a colour is in scope from the layout style.
func TestRectangleStrokeAndFill(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=10
  layout pagesize="A4" leftmargin=20 rightmargin=20 topmargin=20 bottommargin=20 {
    style font="body" color="black" bgcolor="#F3EDE7"
    detail height=12 {
      rectangle stroke=#false
      rectangle opaque=#false left=0 width=10 height=10
    }
  }
}`
	out := buildString(test, src, rowsOf(1))
	fillOnly := out.Pages[0].Marks[0].(*printout.Rectangle)
	if fillOnly.Stroke != nil {
		test.Error("stroke=#false must leave the printout's stroke absent")
	}
	if fillOnly.Fill == nil || *fillOnly.Fill != "#F3EDE7" {
		test.Errorf("fill = %v, want #F3EDE7", fillOnly.Fill)
	}
	strokeOnly := out.Pages[0].Marks[1].(*printout.Rectangle)
	if strokeOnly.Fill != nil {
		test.Error("opaque=#false must leave the fill absent")
	}
	if strokeOnly.Stroke == nil || *strokeOnly.Stroke != "#000000" {
		test.Errorf("stroke = %v, want #000000", strokeOnly.Stroke)
	}
}

// Style resolution is first-match-wins in an outward walk,
// and unset properties fall through to the next match in that same walk.
func TestStyleFirstMatchAndFallThrough(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=10
  font "bold" file="Go-Bold.ttf" size=10
  layout pagesize="A4" leftmargin=20 rightmargin=20 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    detail height=12 {
      style color="#FF0000" when="n % 2"
      field expr="n" format="row %d" left=0 width=200 height=12 {
        style font="bold" when="n == 1"
      }
    }
  }
}`
	out := buildString(test, src, rowsOf(1, 2, 3))
	marks := texts(out.Pages[0])
	// Row 1: the element's own style supplies the font, the band's style the
	// colour, and neither supplies the other — so both fall through correctly.
	if marks[0].Font != "bold" || marks[0].Color != "#FF0000" {
		test.Errorf("row 1 = font %q colour %q, want bold and #FF0000",
			marks[0].Font, marks[0].Color)
	}
	// Row 2: the band's style does not match, so the layout's applies.
	if marks[1].Font != "body" || marks[1].Color != "#000000" {
		test.Errorf("row 2 = font %q colour %q, want body and black",
			marks[1].Font, marks[1].Color)
	}
	// Row 3: the band's style matches again; the font still comes from layout.
	if marks[2].Font != "body" || marks[2].Color != "#FF0000" {
		test.Errorf("row 3 = font %q colour %q", marks[2].Font, marks[2].Color)
	}
}

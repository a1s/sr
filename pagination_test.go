package sr

import (
	"fmt"
	"strings"
	"testing"

	"github.com/a1s/sr/printout"
)

// A page 20 pt tall inside its margins holds exactly two 10 pt rows,
// which makes page breaks countable by hand.
const paged = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=60 leftmargin=10 rightmargin=10 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    detail height=10 {
      field expr="n" format="row %d" left=0 width=100 height=10
    }
  }
}`

func TestPagination(test *testing.T) {
	out := buildString(test, paged, rowsOf(1, 2, 3, 4, 5))
	if len(out.Pages) != 3 {
		test.Fatalf("pages = %d, want 3 (two rows fit a 20 pt frame)", len(out.Pages))
	}
	want := [][]string{{"row 1", "row 2"}, {"row 3", "row 4"}, {"row 5"}}
	for index, page := range out.Pages {
		if page.Number != index+1 {
			test.Errorf("page %d numbered %d", index, page.Number)
		}
		if got := joined(lines(page)); got != joined(want[index]) {
			test.Errorf("page %d = %q, want %q", index+1, got, want[index])
		}
		for _, text := range texts(page) {
			if text.Box.Top < 20 || text.Box.Top+text.Box.Height > 40.001 {
				test.Errorf("page %d: a row at %g leaves the frame",
					index+1, text.Box.Top)
			}
		}
	}
}

// A page header and footer are reserved by measurement, not estimated,
// and a footer sits flush against the frame's reserved bottom band.
func TestHeaderAndFooterReservation(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=80 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    header height=10 { field text="head" left=0 width=100 height=10 }
    footer height=10 { field expr="PAGE_NUMBER" format="foot %d" left=0 width=100 height=10 }
    detail height=10 {
      field expr="n" format="row %d" left=0 width=100 height=10
    }
  }
}`
	// The frame runs from y=10 to y=70. The header takes 10 and
	// the footer reserves 10, leaving 40 for content: four rows a page.
	out := buildString(test, src, rowsOf(1, 2, 3, 4, 5))
	if len(out.Pages) != 2 {
		test.Fatalf("pages = %d, want 2", len(out.Pages))
	}
	first := texts(out.Pages[0])
	if first[0].Lines[0] != "head" || first[0].Box.Top != 10 {
		test.Errorf("the header opens the page: %+v", first[0].Box)
	}
	got := joined(lines(out.Pages[0]))
	want := "head,row 1,row 2,row 3,row 4,foot 1"
	if got != want {
		test.Errorf("page 1 = %q, want %q", got, want)
	}
	// A footer is built against the outgoing context, so it reports the page
	// it belongs to, and it is placed flush at the frame's reserved bottom.
	last := first[len(first)-1]
	if last.Lines[0] != "foot 1" {
		test.Errorf("page 1 footer = %q", last.Lines)
	}
	// The band is 10 pt tall and flush against the frame's reserved bottom
	// band, so it starts at 70 - 10; the text mark inside it is 9.6 pt, the
	// height of one line at this size.
	if last.Box.Top != 60 {
		test.Errorf("the footer text starts at %g, want 60", last.Box.Top)
	}
	if got, want := joined(lines(out.Pages[1])), "head,row 5,foot 2"; got != want {
		test.Errorf("page 2 = %q, want %q", got, want)
	}
}

// A band that does not fit ejects a column, not a page: in a two-column frame
// it moves to the next column and only starts a page when no column remains.
func TestColumnsFillBeforePages(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=60 leftmargin=10 rightmargin=10 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    columns count=2 gap=10 { }
    detail height=10 {
      field expr="n" format="row %d" left=0 right=0 height=10
    }
  }
}`
	out := buildString(test, src, rowsOf(1, 2, 3, 4, 5))
	if len(out.Pages) != 2 {
		test.Fatalf("pages = %d, want 2 — four rows fill a page's two columns", len(out.Pages))
	}
	if got, want := joined(lines(out.Pages[0])), "row 1,row 2,row 3,row 4"; got != want {
		test.Errorf("page 1 = %q, want %q", got, want)
	}
	// The frame is 180 wide; two columns with a 10 pt gap are 85 each, the
	// second starting at x = 10 + 85 + 10 = 105.
	marks := texts(out.Pages[0])
	for index, text := range marks {
		wantX, wantY := 10.0, 20.0+float64(index)*10
		if index >= 2 {
			wantX, wantY = 105, 20+float64(index-2)*10
		}
		if text.Box.Left != wantX || text.Box.Top != wantY {
			test.Errorf("row %d at (%g, %g), want (%g, %g)",
				index+1, text.Box.Left, text.Box.Top, wantX, wantY)
		}
		if text.Box.Width != 85 {
			test.Errorf("row %d width = %g, want 85", index+1, text.Box.Width)
		}
	}
}

// COLUMN_COUNT counts detail rows since the column began,
// and COLUMN_NUMBER the column itself.
func TestColumnCounters(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=60 leftmargin=10 rightmargin=10 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    columns count=2 gap=10 { }
    detail height=10 {
      field expr="(COLUMN_NUMBER, COLUMN_COUNT, PAGE_COUNT)" format="%d/%d/%d" left=0 right=0 height=10
    }
  }
}`
	out := buildString(test, src, rowsOf(1, 2, 3, 4, 5))
	if got, want := joined(lines(out.Pages[0])), "1/0/0,1/1/1,2/0/2,2/1/3"; got != want {
		test.Errorf("page 1 = %q, want %q", got, want)
	}
	if got, want := joined(lines(out.Pages[1])), "1/0/0"; got != want {
		test.Errorf("page 2 = %q, want %q", got, want)
	}
}

// An eject node forces a break; when is what selects the node
// and require then decides whether it ejects.
func TestEjectNodes(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=200 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    detail height=10 {
      eject type="page" when="n == 3"
      field expr="n" format="row %d" left=0 width=100 height=10
    }
  }
}`
	out := buildString(test, src, rowsOf(1, 2, 3, 4))
	if len(out.Pages) != 2 {
		test.Fatalf("pages = %d, want 2", len(out.Pages))
	}
	if got, want := joined(lines(out.Pages[0])), "row 1,row 2"; got != want {
		test.Errorf("page 1 = %q, want %q", got, want)
	}
	if got, want := joined(lines(out.Pages[1])), "row 3,row 4"; got != want {
		test.Errorf("page 2 = %q, want %q", got, want)
	}
}

// The first node whose when is true is selected and the search stops there,
// even when its require then declines to eject.
func TestEjectSelectionStopsAtTheFirstTrueWhen(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=200 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    detail height=10 {
      eject type="page" require=1
      eject type="page"
      field expr="n" format="row %d" left=0 width=100 height=10
    }
  }
}`
	// The first node is selected on every row; its require of 1 pt
	// is never unmet in a 180 pt frame, so nothing ejects and
	// the second node is never reached.
	out := buildString(test, src, rowsOf(1, 2, 3))
	if len(out.Pages) != 1 {
		test.Fatalf("pages = %d, want 1: the selected node declined, and the search stopped there",
			len(out.Pages))
	}
}

// A group breaks when its expr changes between adjacent records;
// its title and summary bracket the run, and its counters restart.
func TestGroupTransitions(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int"
            member "g" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    group "grp" expr="g" {
      title height=10 { field expr="g" format="open %s" left=0 width=200 height=10 }
      summary height=10 {
        field expr="(g, grp_COUNT)" format="close %s after %d" left=0 width=200 height=10
      }
      detail height=10 {
        field expr="(n, grp_COUNT)" format="row %d in %d" left=0 width=200 height=10
      }
    }
  }
}`
	rows := []map[string]any{
		{"n": 1, "g": "a"}, {"n": 2, "g": "a"}, {"n": 3, "g": "b"},
	}
	out := buildString(test, src, rows)
	got := joined(lines(out.Pages[0]))
	want := joined([]string{
		"open a", "row 1 in 0", "row 2 in 1",
		"close a after 2",
		"open b", "row 3 in 0",
		"close b after 1",
	})
	if got != want {
		test.Errorf("got %q\nwant %q", got, want)
	}
	if out.Header.GroupRuns["grp"] != 2 || out.Header.GroupKeys["grp"] != 2 {
		test.Errorf("groupRuns = %v, groupKeys = %v",
			out.Header.GroupRuns, out.Header.GroupKeys)
	}
}

// A repeating key is legal -- a report may group by a value such as weekday --
// and the header records the discrepancy so unsorted input is visible.
func TestRepeatingGroupKeyIsVisibleInTheHeader(test *testing.T) {
	const src = `report name="t" {
  records { member "g" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    group "grp" expr="g" {
      detail height=10 { field expr="g" left=0 width=100 height=10 }
    }
  }
}`
	out := buildString(test, src, []map[string]any{{"g": "a"}, {"g": "b"}, {"g": "a"}})
	if out.Header.GroupRuns["grp"] != 3 {
		test.Errorf("groupRuns = %d, want 3", out.Header.GroupRuns["grp"])
	}
	if out.Header.GroupKeys["grp"] != 2 {
		test.Errorf("groupKeys = %d, want 2", out.Header.GroupKeys["grp"])
	}
}

// A group summary reads that group's own total, because it is placed
// before the variables reset for the scope that just ended.
func TestGroupSummaryReadsItsOwnTotal(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int"
            member "g" type="string" }
  variable "total" expr="n" calc="sum" reset="group" resetgrp="grp"
  variable "grand" expr="n" calc="sum" reset="report"
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    summary height=10 { field expr="grand" format="grand %d" left=0 width=200 height=10 }
    group "grp" expr="g" {
      summary height=10 { field expr="(g, total)" format="%s = %d" left=0 width=200 height=10 }
      detail height=10 { field expr="n" format="%d" left=0 width=200 height=10 }
    }
  }
}`
	rows := []map[string]any{
		{"n": 1, "g": "a"}, {"n": 2, "g": "a"}, {"n": 4, "g": "b"},
	}
	out := buildString(test, src, rows)
	got := joined(lines(out.Pages[0]))
	want := joined([]string{"1", "2", "a = 3", "4", "b = 4", "grand 7"})
	if got != want {
		test.Errorf("got %q\nwant %q", got, want)
	}
}

// A band split at a line boundary carries its remaining lines
// to the next frame, and the head's last line is marked as continued.
func TestBandSplitting(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=80 leftmargin=10 rightmargin=10 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    detail split=#true {
      field expr="n" stretch=#true align="justified" left=0 width=60
    }
  }
}`
	// The frame is 40 pt tall, which holds four 9.6 pt lines. The text wraps to
	// more than that in a 60 pt box, so the band splits.
	long := strings.Repeat("alpha beta gamma delta ", 4)
	out := buildString(test, src, []map[string]any{{"n": strings.TrimSpace(long)}})
	if len(out.Pages) < 2 {
		test.Fatalf("pages = %d, want the band to split", len(out.Pages))
	}
	var all []string
	for index, page := range out.Pages {
		marks := texts(page)
		if len(marks) != 1 {
			test.Fatalf("page %d has %d text marks", index+1, len(marks))
		}
		all = append(all, marks[0].Lines...)
		if index < len(out.Pages)-1 && !marks[0].LastLineJustified {
			test.Errorf("page %d: a continued line must be marked so the renderer justifies it", index+1)
		}
		if index == len(out.Pages)-1 && marks[0].LastLineJustified {
			test.Error("the final fragment ends the paragraph and must not be marked continued")
		}
	}
	// Splitting preserves the whole text.
	got := strings.Join(strings.Fields(strings.Join(all, " ")), " ")
	want := strings.TrimSpace(long)
	if got != want {
		test.Errorf("splitting lost text:\n got %q\nwant %q", got, want)
	}
}

// A band split across a column continues in the column it lands in.
//
// The tail is carried over the eject rather than measured again --
// it is already wrapped, and re-evaluating it would ask its expressions
// a second question -- so the marks it carries were built against the column
// the band started in and have to be moved to the one it continues in.
func TestBandSplittingAcrossColumns(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=80 leftmargin=10 rightmargin=10 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    columns count=2 gap=10 { }
    detail split=#true {
      field expr="n" stretch=#true align="justified" left=0 width=60
    }
  }
}`
	// The frame is 40 pt tall and holds four 9.6 pt lines; the text wraps to
	// ten in a 60 pt box, so it fills both columns and runs onto a second page.
	long := strings.TrimSpace(strings.Repeat("alpha beta gamma delta ", 5))
	out := buildString(test, src, []map[string]any{{"n": long}})
	if len(out.Pages) != 2 {
		test.Fatalf("pages = %d, want 2", len(out.Pages))
	}
	// The frame is 280 wide; two columns with a 10 pt gap are 135 each,
	// the second starting at x = 10 + 135 + 10 = 155.
	var all []string
	for index, want := range []float64{10, 155, 10} {
		page := out.Pages[0]
		mark := index
		if index == 2 {
			page, mark = out.Pages[1], 0
		}
		marks := texts(page)
		if mark >= len(marks) {
			test.Fatalf("fragment %d is missing", index+1)
		}
		if got := marks[mark].Box.Left; got != want {
			test.Errorf("fragment %d at x = %g, want %g", index+1, got, want)
		}
		if got := marks[mark].Box.Top; got != 20 {
			test.Errorf("fragment %d at y = %g, want 20", index+1, got)
		}
		all = append(all, marks[mark].Lines...)
	}
	// Moving the fragments across changes nothing about what they say.
	if got := strings.Join(all, " "); got != long {
		test.Errorf("splitting lost text:\n got %q\nwant %q", got, long)
	}
}

// A cut with all the band's marks on one side of it is not a legal split point:
// splitting there would move whitespace and nothing else, so the band
// ejects whole instead.
func TestSplitMustDivideContent(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=70 leftmargin=10 rightmargin=10 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    detail height=20 split=#true {
      field expr="n" format="row %d" left=0 width=100 height=9.6
    }
  }
}`
	// The frame is 30 pt tall and each band is 20 pt with 9.6 pt of text in it.
	// Row 2 has 10 pt left, and every offset below its text divides nothing, so
	// it ejects whole rather than leaving a blank strip on the next page.
	out := buildString(test, src, rowsOf(1, 2))
	if len(out.Pages) != 2 {
		test.Fatalf("pages = %d, want 2", len(out.Pages))
	}
	if got, want := joined(lines(out.Pages[0])), "row 1"; got != want {
		test.Errorf("page 1 = %q, want %q", got, want)
	}
	if got, want := joined(lines(out.Pages[1])), "row 2"; got != want {
		test.Errorf("page 2 = %q, want %q", got, want)
	}
}

// orphans and widows are minimum line counts at a break.
func TestOrphansAndWidows(test *testing.T) {
	build := func(orphans, widows int) *Printout {
		src := fmt.Sprintf(`report name="t" {
  records { member "n" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=90 leftmargin=10 rightmargin=10 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    detail split=#true orphans=%d widows=%d {
      field expr="n" stretch=#true left=0 width=60
    }
  }
}`, orphans, widows)
		long := strings.TrimSpace(strings.Repeat("alpha beta gamma delta ", 3))
		return buildString(test, src, []map[string]any{{"n": long}})
	}

	loose := build(1, 1)
	if len(loose.Pages) < 2 {
		test.Fatalf("with orphans=1 widows=1 the band should split, got %d pages",
			len(loose.Pages))
	}
	head := texts(loose.Pages[0])[0]
	if len(head.Lines) < 1 {
		test.Fatal("no lines in the head")
	}

	// Raising orphans past what the first frame can hold makes every internal
	// cut illegal, so the band ejects whole.
	strict := build(9, 1)
	if count := len(texts(strict.Pages[0])); count != 0 {
		test.Errorf("page 1 has %d marks; with orphans=9 no cut is legal and the band moves whole", count)
	}
}

// A band taller than any frame is an error,
// and --allow-overflow downgrades it to a warning recorded in the printout.
func TestOversizedBand(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=60 leftmargin=10 rightmargin=10 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    detail height=100 {
      field expr="n" format="row %d" left=0 width=100 height=10
    }
  }
}`
	err := buildStringErr(test, src, rowsOf(1))
	if err == nil {
		test.Fatal("want an overflow error")
	}
	if !strings.Contains(err.Error(), "cannot be cut") {
		test.Errorf("diagnostic = %v", err)
	}

	tpl, perr := ParseTemplate("example/fonts/test.kdl", src)
	if perr != nil {
		test.Fatal(perr)
	}
	out, berr := tpl.Build(rowsOf(1), StrictFonts(), WithBuildTime(fixedTime), AllowOverflow())
	if berr != nil {
		test.Fatalf("with AllowOverflow the build must succeed: %v", berr)
	}
	if !out.HasWarning(printout.WarnOverflow) {
		test.Error("the printout must carry an overflow warning so the artifact is diagnosable")
	}
	// The invariant that marks stay inside the printable area is waived exactly
	// when that warning is present.
	if err := out.Validate(); err != nil {
		test.Errorf("%v", err)
	}
}

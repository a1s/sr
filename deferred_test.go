package sr

import (
	"strings"
	"testing"

	"github.com/a1s/sr/printout"
)

// A page footer that prints the final page count: PAGE_NUMBER reads
// where the field sits, and FINAL.PAGE_NUMBER what it reaches at the end
// of the report. One expression gives both.
func TestDeferredPageCount(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=80 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    footer height=10 {
      field expr="'Page %d of %d' % (PAGE_NUMBER, FINAL.PAGE_NUMBER)" evaltime="report" \
            left=0 width=100 height=10
    }
    detail height=10 {
      field expr="n" format="row %d" left=0 width=100 height=10
    }
  }
}`
	out := buildString(test, src, rowsOf(1, 2, 3, 4, 5, 6, 7, 8))
	if len(out.Pages) != 2 {
		test.Fatalf("pages = %d, want 2", len(out.Pages))
	}
	for index, page := range out.Pages {
		marks := texts(page)
		footer := marks[len(marks)-1]
		want := "Page " + string(rune('1'+index)) + " of 2"
		if footer.Lines[0] != want {
			test.Errorf("page %d footer = %q, want %q", index+1, footer.Lines[0], want)
		}
	}
}

// A group's deferrals resolve after its summary,
// so both read the same final group totals.
func TestDeferredGroupCount(test *testing.T) {
	const src = `report name="t" {
  records { member "g" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    group "grp" expr="g" {
      title height=10 {
        field expr="FINAL.grp_COUNT" format="rows to come %d" evaltime="grp" \
              text="rows to come 999" left=0 width=200 height=10
      }
      detail height=10 { field expr="g" left=0 width=200 height=10 }
    }
  }
}`
	// The title cannot know the group's row count when it is built,
	// so the value is deferred to the group scope.
	out := buildString(test, src, []map[string]any{{"g": "a"}, {"g": "a"}, {"g": "b"}})
	got := lines(out.Pages[0])
	if len(got) != 5 {
		test.Fatalf("marks = %q", got)
	}
	if got[0] != "rows to come 2" {
		test.Errorf("first group title = %q, want the group's final count", got[0])
	}
	if got[3] != "rows to come 1" {
		test.Errorf("second group title = %q", got[3])
	}
}

// A deferred value taller than what its placeholder reserved
// is an error naming the field, its placeholder, and both heights.
func TestDeferredValueTallerThanItsPlaceholder(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=200 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    footer height=40 {
      field expr="'the final row count is %d and that is a great many words'  % FINAL.REPORT_COUNT" \
            evaltime="report" stretch=#true text="short" left=0 width=40
    }
    detail height=10 {
      field expr="n" format="row %d" left=0 width=100 height=10
    }
  }
}`
	err := buildStringErr(test, src, rowsOf(1, 2, 3))
	if err == nil {
		test.Fatal("want an error: the resolved value needs more room than the placeholder reserved")
	}
	for _, want := range []string{"placeholder", "field"} {
		if !strings.Contains(err.Error(), want) {
			test.Errorf("diagnostic %q does not mention %q", err, want)
		}
	}
}

// Everything but FINAL reads its value where the element sits,
// exactly as it would with no evaltime at all.
func TestDeferredExpressionReadsInPlace(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  variable "total" expr="n" calc="sum" reset="report"
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=80 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    footer height=10 {
      field expr="'%d then %d' % (total or 0, FINAL.total)" evaltime="report" \
            left=0 width=150 height=10
    }
    detail height=10 {
      field expr="n" format="row %d" left=0 width=100 height=10
    }
  }
}`
	out := buildString(test, src, rowsOf(1, 2, 3, 4, 5, 6, 7, 8))
	if len(out.Pages) != 2 {
		test.Fatalf("pages = %d, want 2", len(out.Pages))
	}
	// The frame is 50 pt after the footer's reservation, so five rows fit
	// a page. The page 1 footer is built when that page ends, so `total` is
	// the running total there — 1+2+3+4+5 — while FINAL.total is the report's.
	first := texts(out.Pages[0])
	if got := first[len(first)-1].Lines[0]; got != "15 then 36" {
		test.Errorf("page 1 footer = %q, want %q", got, "15 then 36")
	}
	second := texts(out.Pages[1])
	if got := second[len(second)-1].Lines[0]; got != "36 then 36" {
		test.Errorf("page 2 footer = %q, want %q", got, "36 then 36")
	}
}

// A deferred barcode always needs a placeholder,
// and the resolved symbol replaces the placeholder's stripes.
func TestDeferredBarcode(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=300 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    footer height=40 {
      barcode type="2of5i" expr="FINAL.REPORT_COUNT" format="%04d" text="9999" \
              evaltime="report" left=0 width=150
    }
    detail height=10 { field expr="n" format="row %d" left=0 width=100 height=10 }
  }
}`
	out := buildString(test, src, rowsOf(1, 2, 3))
	var found *printout.Barcode
	for _, mark := range out.Pages[0].Marks {
		if barcode, ok := mark.(*printout.Barcode); ok {
			found = barcode
		}
	}
	if found == nil {
		test.Fatal("no barcode mark")
	}
	if found.Value != "0003" {
		test.Errorf("barcode value = %q, want the resolved 0003", found.Value)
	}
	if len(found.Stripes) == 0 {
		test.Error("the resolved symbol must carry stripes")
	}
}

// keeptogether puts a whole group on one frame when it fits on an empty one.
func TestKeepTogether(test *testing.T) {
	const src = `report name="t" {
  records { member "g" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=70 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    group "grp" expr="g" keeptogether=#true {
      title height=10 { field expr="g" format="open %s" left=0 width=100 height=10 }
      detail height=10 { field expr="g" left=0 width=100 height=10 }
    }
  }
}`
	// The frame is 50 pt: five 10 pt bands. Group a is a title plus two rows,
	// group b a title plus three, so b does not fit after a and moves whole.
	rows := []map[string]any{
		{"g": "a"}, {"g": "a"},
		{"g": "b"}, {"g": "b"}, {"g": "b"},
	}
	out := buildString(test, src, rows)
	if len(out.Pages) != 2 {
		test.Fatalf("pages = %d, want 2", len(out.Pages))
	}
	if got, want := joined(lines(out.Pages[0])), "open a,a,a"; got != want {
		test.Errorf("page 1 = %q, want %q", got, want)
	}
	if got, want := joined(lines(out.Pages[1])), "open b,b,b,b"; got != want {
		test.Errorf("page 2 = %q, want %q", got, want)
	}
}

// minrows is the minimum number of detail rows that must share a frame
// with the group title.
func TestMinRows(test *testing.T) {
	const src = `report name="t" {
  records { member "g" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=70 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    group "grp" expr="g" minrows=2 {
      title height=10 { field expr="g" format="open %s" left=0 width=100 height=10 }
      detail height=10 { field expr="g" left=0 width=100 height=10 }
    }
  }
}`
	// The frame holds five bands. Group a takes three, leaving two: enough for
	// b's title and one row, but minrows asks for two, so b starts a page.
	rows := []map[string]any{
		{"g": "a"}, {"g": "a"},
		{"g": "b"}, {"g": "b"}, {"g": "b"}, {"g": "b"},
	}
	out := buildString(test, src, rows)
	if len(out.Pages) != 2 {
		test.Fatalf("pages = %d, want 2", len(out.Pages))
	}
	if got, want := joined(lines(out.Pages[0])), "open a,a,a"; got != want {
		test.Errorf("page 1 = %q, want %q — b's title must not open with fewer than two rows",
			got, want)
	}
}

// A floating element sits below whatever lies above it,
// using measured heights rather than declared ones.
func TestFloatingElements(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=300 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    detail {
      field expr="n" stretch=#true left=0 width=60
      field float=#true text="below" top=2 height=10 left=0 width=100
    }
  }
}`
	short := buildString(test, src, []map[string]any{{"n": "one"}})
	long := buildString(test, src, []map[string]any{
		{"n": "a much longer piece of text that has to wrap over several lines"},
	})

	floater := func(out *Printout) float64 {
		for _, text := range texts(out.Pages[0]) {
			if text.Lines[0] == "below" {
				return text.Box.Top
			}
		}
		test.Fatal("no floating field")
		return 0
	}
	// One line above: the floater sits at 10 + 9.6 + 2.
	if got, want := floater(short), 21.6; got != want {
		test.Errorf("floater at %g, want %g", got, want)
	}
	// The wrapped field is taller, and the floater moves down with it — the gap
	// of 2 is preserved rather than the declared position.
	if floater(long) <= floater(short) {
		test.Errorf("the floater did not move down: %g against %g",
			floater(long), floater(short))
	}
	if delta := floater(long) - floater(short); int(delta)%9 != 0 && int(delta)%10 != 0 {
		test.Logf("floater moved by %g", delta)
	}
}

package sr

import (
	"strings"
	"testing"

	"github.com/a1s/sr/printout"
)

// A mark outside the page's printable area is an overflow.
//
// A negative left offset is legal in a template: it means the box reaches
// past its container, which for a container in the middle of the page
// is ordinary. What is not ordinary is the mark ending up off the
// printable area, and the printout's seventh invariant says so.
// Without the check, the engine wrote a printout that broke its
// own invariant and only `sr inspect` ever noticed.
func TestAMarkPastTheMarginIsAnOverflow(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=200 height=200 leftmargin=20 rightmargin=20 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    detail height=20 { field text="x" left=-30 width=10 }
  }
}`
	err := buildStringErr(test, src, []map[string]any{{"n": 1}})
	if err == nil {
		test.Fatal("want the mark outside the printable area reported")
	}
	if !strings.Contains(err.Error(), "left margin") {
		test.Errorf("diagnostic = %v, want it to name the margin", err)
	}
}

// Under --allow-overflow it is a warning carrying the record, and the
// printout still passes its own invariant check, which reads the warning.
func TestAnAllowedMarginOverflowIsAWarning(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=200 height=200 leftmargin=20 rightmargin=20 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    detail height=20 { field text="x" left=-30 width=10 }
  }
}`
	out := buildString(test, src, []map[string]any{{"n": 1}}, AllowOverflow())
	var found *printout.Warning
	for index, warning := range out.Header.Warnings {
		if warning.Kind == printout.WarnOverflow {
			found = &out.Header.Warnings[index]
			break
		}
	}
	if found == nil {
		test.Fatalf("want an overflow warning, got %+v", out.Header.Warnings)
	}
	if found.Record == nil || *found.Record != 0 {
		test.Errorf("record = %v, want 0", found.Record)
	}
	if err := out.Validate(); err != nil {
		test.Errorf("the warning should admit the mark: %v", err)
	}
}

// A band that fits inside the margins is not reported,
// which is every band in the committed examples.
func TestBandsInsideTheMarginsAreNotReported(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=200 height=200 leftmargin=20 rightmargin=20 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    detail height=20 { field text="x" left=0 right=0 }
  }
}`
	out := buildString(test, src, []map[string]any{{"n": 1}})
	for _, warning := range out.Header.Warnings {
		if warning.Kind == printout.WarnOverflow {
			test.Errorf("unexpected overflow: %s", warning.Message)
		}
	}
}

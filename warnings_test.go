package sr

import (
	"bytes"
	"strings"
	"testing"

	"github.com/a1s/sr/printout"
)

// An overflow on the first record carries record 0.
//
// The field was dropped on being zero, which made the first record's
// warning read as one no record produced. Record 0 is a record like
// any other, and a report whose very first row overflows is the
// ordinary way to meet this.
func TestOverflowOnTheFirstRecordCarriesItsRecord(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=200 height=60 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 {
    style font="body" color="black"
    detail height=200 { field text="x" left=0 right=0 }
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
	if found.Record == nil {
		test.Fatal("the warning carries no record; the first record is record 0")
	}
	if *found.Record != 0 {
		test.Errorf("record = %d, want 0", *found.Record)
	}
}

// A font warning names the font node it is about,
// as every other warning names the node that produced it.
func TestFontWarningNamesItsNode(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Bold.ttf" size=9 italic=#true
  layout width=200 height=200 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 {
    style font="body" color="black"
    detail height=20 { field text="x" left=0 right=0 }
  }
}`
	out := buildString(test, src, []map[string]any{{"n": 1}})
	for _, warning := range out.Header.Warnings {
		if warning.Kind != printout.WarnFont {
			continue
		}
		if warning.Node == "" {
			test.Errorf("font warning has no node: %q", warning.Message)
		}
		if !strings.Contains(warning.Node, "font") {
			test.Errorf("node = %q, want it to name the font node", warning.Node)
		}
		if warning.Record != nil {
			test.Errorf("record = %d; a font is resolved before any data is read",
				*warning.Record)
		}
		return
	}
	test.Fatalf("want a font warning, got %+v", out.Header.Warnings)
}

// Where no element prints, the font table is an empty array rather than
// null: the field's shape does not depend on whether anything used it.
func TestEmptyFontTableIsAnArray(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=200 height=200 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 {
    style font="body" color="black"
    detail height=20 printwhen="False" { field text="x" left=0 right=0 }
  }
}`
	out := buildString(test, src, []map[string]any{{"n": 1}})
	var buf bytes.Buffer
	if err := out.WriteNDJSON(&buf, "."); err != nil {
		test.Fatalf("writing: %v", err)
	}
	header, _, _ := strings.Cut(buf.String(), "\n")
	if strings.Contains(header, `"fonts":null`) {
		test.Errorf("fonts is null; want []: %s", header)
	}
	if !strings.Contains(header, `"fonts":[]`) {
		test.Errorf("want an empty fonts array: %s", header)
	}
}

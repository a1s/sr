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

// A warning from a band built before the first record carries no record.
//
// The title is laid out before the record loop opens, so there is
// no record to name and naming one would be a guess. ITEM_NUMBER is 0 there,
// which is what the engine reads the index from.
func TestATitleWarningCarriesNoRecord(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=200 height=200 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 {
    style font="body" color="black"
    title height=20 { field text="漢" left=0 right=0 }
    detail height=20 { field text="x" left=0 right=0 }
  }
}`
	out := buildString(test, src, []map[string]any{{"n": 1}})
	for _, warning := range out.Header.Warnings {
		if warning.Kind != printout.WarnGlyph {
			continue
		}
		if warning.Record != nil {
			test.Errorf("record = %d; the title is built before any record",
				*warning.Record)
		}
		return
	}
	test.Fatalf("want a glyph warning, got %+v", out.Header.Warnings)
}

// A glyph warning raised for a record carries that record,
// which is what tells it from one the title raised.
func TestADetailWarningCarriesItsRecord(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=200 height=200 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 {
    style font="body" color="black"
    detail height=20 { field expr="THIS.text" left=0 right=0 }
  }
}`
	out := buildString(test, src, []map[string]any{{"text": "x"}, {"text": "漢"}})
	for _, warning := range out.Header.Warnings {
		if warning.Kind != printout.WarnGlyph {
			continue
		}
		if warning.Record == nil {
			test.Fatal("the warning carries no record; a record produced it")
		}
		if *warning.Record != 1 {
			test.Errorf("record = %d, want 1", *warning.Record)
		}
		return
	}
	test.Fatalf("want a glyph warning, got %+v", out.Header.Warnings)
}

// Warnings stand in the order the build raised them, so a load diagnostic
// comes before anything a page raised however late it is gathered.
func TestLoadWarningsComeFirst(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=200 height=200 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 wobble=#true {
    style font="body" color="black"
    detail height=20 { field text="漢" left=0 right=0 }
  }
}`
	out := buildString(test, src, []map[string]any{{"n": 1}})
	var kinds []string
	for _, warning := range out.Header.Warnings {
		kinds = append(kinds, warning.Kind)
	}
	if len(kinds) < 2 {
		test.Fatalf("want an unknown and a glyph warning, got %q", kinds)
	}
	if kinds[0] != printout.WarnUnknown {
		test.Errorf("kinds = %q; the template is read before any page is made",
			kinds)
	}
}

// A warning about a property names it, as a diagnostic about one does.
func TestAPropertyWarningNamesTheProperty(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=200 height=200 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 wobble=#true {
    style font="body" color="black"
    detail height=20 { field text="x" left=0 right=0 }
  }
}`
	out := buildString(test, src, []map[string]any{{"n": 1}})
	for _, warning := range out.Header.Warnings {
		if warning.Kind != printout.WarnUnknown {
			continue
		}
		if warning.Prop != "wobble" {
			test.Errorf("prop = %q, want \"wobble\"", warning.Prop)
		}
		return
	}
	test.Fatalf("want an unknown warning, got %+v", out.Header.Warnings)
}

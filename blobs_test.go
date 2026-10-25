package sr

import "testing"

// Only entries something refers to are written.
//
// A header or a footer is built once to reserve its space, before the bands
// that fill the page, and that build publishes the blob of any image in it.
// Where the band then prints nothing, the blob is behind no mark, and
// an entry for it is bytes the document carries and nothing reads.
func TestAnUndrawnImageLeavesNoBlob(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=200 height=200 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 {
    style font="body" color="black"
    footer height=20 {
      image file="../sakila/swatch.png" width=8 height=8 printwhen="False"
    }
    detail height=20 { field text="x" left=0 right=0 }
  }
}`
	out := buildStringAt(test, "example/fonts/t.kdl", src,
		[]map[string]any{{"n": 1}})
	if len(out.Header.Data) != 0 {
		test.Errorf("data = %v, want no entries: nothing refers to one",
			out.Header.Data)
	}
}

// A `data` node nothing references does not appear either.
// Which names are spoken for is a property of the template,
// and which entries are written is a property of what
// the document turned out to draw.
func TestAnUnreferencedDataNodeLeavesNoBlob(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  data "unused" encoding="base64" { content "AAAA" }
  layout width=200 height=200 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 {
    style font="body" color="black"
    detail height=20 { field text="x" left=0 right=0 }
  }
}`
	out := buildStringAt(test, "example/fonts/t.kdl", src,
		[]map[string]any{{"n": 1}})
	if _, ok := out.Header.Data["unused"]; ok {
		test.Errorf("data = %v, want no entry for an unreferenced node",
			out.Header.Data)
	}
}

// An image that is drawn keeps its entry,
// which is the case the rule existssw to leave alone.
func TestADrawnImageKeepsItsBlob(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=200 height=200 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 {
    style font="body" color="black"
    detail height=20 {
      image file="../sakila/swatch.png" width=8 height=8
    }
  }
}`
	out := buildStringAt(test, "example/fonts/t.kdl", src,
		[]map[string]any{{"n": 1}})
	if len(out.Header.Data) != 1 {
		test.Errorf("data = %v, want the drawn image's entry", out.Header.Data)
	}
	if err := out.Validate(); err != nil {
		test.Errorf("the printout breaks its own invariants: %v", err)
	}
}

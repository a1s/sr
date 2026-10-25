package sr

import (
	"testing"

	"github.com/a1s/sr/printout"
)

// imageMark is the first image mark of a printout.
func imageMark(test *testing.T, doc *Printout) *printout.Image {
	test.Helper()
	for _, page := range doc.Pages {
		for _, mark := range page.Marks {
			if image, ok := mark.(*printout.Image); ok {
				return image
			}
		}
	}
	test.Fatal("no image mark in the printout")
	return nil
}

// A clipped image keeps the pixels its alignment leaves inside the box.
//
// doc/layout.md#an-image-in-its-box: the bitmap is aligned and then clipped,
// so an image wider than its box and aligned right keeps its right side.
// Keeping the top-left corner whatever the alignment says makes halign
// and valign decide nothing for the one scale that consults them most.
func TestCutCropFollowsTheAlignment(test *testing.T) {
	// swatch.png is 16 by 16 pixels, drawn at one point per pixel;
	// a box of 4 by 4 keeps a sixteenth of it, and which sixteenth
	// is what the alignment decides.
	template := func(halign, valign string) string {
		return `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=200 height=200 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 {
    style font="body" color="black"
    detail height=40 {
      image file="../sakila/swatch.png" scale="cut" embed=#false ` +
			`width=4 height=4 halign="` + halign + `" valign="` + valign + `"
    }
  }
}`
	}
	cases := []struct {
		halign, valign string
		wantX, wantY   float64
	}{
		{"left", "top", 0, 0},
		{"right", "bottom", 12, 12},
		{"center", "center", 6, 6},
	}
	for _, item := range cases {
		test.Run(item.halign+"-"+item.valign, func(test *testing.T) {
			out := buildStringAt(test, "example/fonts/t.kdl",
				template(item.halign, item.valign), []map[string]any{{"n": 1}})
			mark := imageMark(test, out)
			if mark.Crop == nil {
				test.Fatal("a clipped image carries a crop")
			}
			if mark.Crop.Left != item.wantX || mark.Crop.Top != item.wantY {
				test.Errorf("crop starts at %v,%v, want %v,%v",
					mark.Crop.Left, mark.Crop.Top, item.wantX, item.wantY)
			}
			if mark.Crop.Width != 4 || mark.Crop.Height != 4 {
				test.Errorf("crop is %vx%v, want 4x4",
					mark.Crop.Width, mark.Crop.Height)
			}
		})
	}
}

// A grow image shorter than its box is placed by valign, as it is by halign.
//
// grow expands the box only where the image exceeds it. Along an axis
// where the image is smaller the box is unchanged, and there the alignment
// decides where the image sits, vertically as well as horizontally.
func TestGrowImageHonoursValign(test *testing.T) {
	template := func(valign string) string {
		return `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=200 height=200 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 {
    style font="body" color="black"
    detail height=40 {
      image file="../sakila/swatch.png" scale="grow" embed=#false ` +
			`top=0 height=40 width=40 valign="` + valign + `"
    }
  }
}`
	}
	tops := map[string]float64{}
	for _, valign := range []string{"top", "center", "bottom"} {
		out := buildStringAt(test, "example/fonts/t.kdl",
			template(valign), []map[string]any{{"n": 1}})
		tops[valign] = imageMark(test, out).Box.Top
	}
	if tops["top"] == tops["bottom"] {
		test.Errorf("valign decides nothing: top and bottom both at %v",
			tops["top"])
	}
	if !(tops["top"] < tops["center"] && tops["center"] < tops["bottom"]) {
		test.Errorf("tops are not in order: %v", tops)
	}
}

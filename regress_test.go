package sr

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"strings"
	"testing"

	"github.com/a1s/sr/printout"
	"github.com/fxamacker/cbor/v2"
)

// embed=#false writes a path into the printout in place of the bytes,
// and only `file` supplies one. With `data` or a `content` child the mark
// used to carry neither, and the printout failed its own invariant 11.
func TestEmbedFalseNeedsAFile(test *testing.T) {
	const src = `report name="t" {
  member_placeholder
  font "body" file="Go-Regular.ttf" size=8
  data "logo" encoding="base64" { content "R0lGODlhAQABAIAAAP///wAAACH5BAEAAAAALAAAAAABAAEAAAICRAEAOw==" }
  layout width=200 height=80 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body"
    detail height=20 { image data="logo" embed=#false left=0 width=20 height=20 }
  }
}`
	_, err := ParseTemplate("example/fonts/test.kdl",
		strings.Replace(src, "  member_placeholder\n", "", 1))
	if err == nil {
		test.Fatal("want a load error for embed=#false without file=")
	}
	if !strings.Contains(err.Error(), "needs file=") {
		test.Errorf("diagnostic = %v", err)
	}
}

// gifData encodes a 1x1 GIF of one colour. Two of them differ in content and
// match in length exactly, which is the collision the blob key has to survive.
func gifData(test *testing.T, red, green, blue uint8) string {
	test.Helper()
	img := image.NewPaletted(image.Rect(0, 0, 1, 1),
		color.Palette{color.RGBA{red, green, blue, 0xFF}})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		test.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// An inline image is keyed by its content.
//
// Keying it by its byte length made two different images of equal size share one blob,
// and the second rendered as the first.
func TestInlineImagesOfEqualLengthStaySeparate(test *testing.T) {
	one, two := gifData(test, 0xFF, 0x00, 0x00), gifData(test, 0x00, 0x00, 0xFF)
	if len(one) != len(two) {
		test.Fatalf("the fixtures must be the same length to test the collision: %d vs %d",
			len(one), len(two))
	}
	if one == two {
		test.Fatal("the fixtures must differ")
	}
	src := `report name="t" {
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=80 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body"
    detail height=30 {
      image left=0 width=10 height=10 { content "` + one + `" }
      image left=20 width=10 height=10 { content "` + two + `" }
    }
  }
}`
	out := buildString(test, src, rowsOf(1))
	if got := len(out.Header.Data); got != 2 {
		test.Fatalf("data entries = %d, want one per distinct image", got)
	}
	var named []string
	for _, mark := range out.Pages[0].Marks {
		if img, ok := mark.(*printout.Image); ok {
			named = append(named, img.Data)
		}
	}
	if len(named) != 2 || named[0] == named[1] {
		test.Fatalf("the two images name %q; they must not share a blob", named)
	}
	first := out.Header.Data[named[0]].Content
	second := out.Header.Data[named[1]].Content
	if first == second {
		test.Error("the two blobs carry the same bytes")
	}
}

// maxheight clamps a declared height, which until now only maxwidth did: the
// clamp reached Extent.Resolve for bottom-anchored elements and nothing else.
func TestMaxHeightClampsADeclaredHeight(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=120 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body"
    detail {
      rectangle left=0 width=40 height=50 maxheight=20
    }
  }
}`
	out := buildString(test, src, rowsOf(1))
	for _, mark := range out.Pages[0].Marks {
		if rect, ok := mark.(*printout.Rectangle); ok {
			if rect.Box.Height != 20 {
				test.Errorf("rectangle height = %v, want the 20 pt clamp",
					rect.Box.Height)
			}
			return
		}
	}
	test.Fatal("no rectangle mark")
}

// The clamp reaches the content too: a field's lines are trimmed to the
// clamped box rather than overflowing it, which invariant 8 would reject.
func TestMaxHeightTrimsAField(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=200 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body"
    detail {
      field expr="n" left=0 width=50 height=100 maxheight=20
    }
  }
}`
	long := strings.Repeat("alpha beta gamma ", 6)
	out := buildString(test, src, []map[string]any{{"n": long}})
	marks := texts(out.Pages[0])
	if len(marks) != 1 {
		test.Fatalf("text marks = %d", len(marks))
	}
	// 20 pt of box at 9.6 pt leading holds two lines.
	if got := len(marks[0].Lines); got != 2 {
		test.Errorf("lines = %d, want 2 to fit the 20 pt clamp: %q", got, marks[0].Lines)
	}
}

// A stretch field measures wrapped text, which needs a face.
//
// With no font in scope the measure pass dereferenced a nil one and panicked,
// before emit could give the diagnostic it already had.
func TestStretchFieldWithoutAFontIsAnError(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=80 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    detail { field expr="n" stretch=#true left=0 width=60 }
  }
}`
	err := buildStringErr(test, src, []map[string]any{{"n": "text"}})
	if err == nil {
		test.Fatal("want an error, not a panic")
	}
	if !strings.Contains(err.Error(), "no font is in scope") {
		test.Errorf("error = %v", err)
	}
}

// ReadCBOR checks the format version, as ReadNDJSON already did.
//
// Without it a printout from a future format decoded as far as its bytes
// happened to line up, and reported whatever that produced.
//
// The header is built here rather than by writing a real printout,
// because serializable stamps the current version on the way out
// and there is no way to write a wrong one.
func TestCBORRejectsAnUnknownVersion(test *testing.T) {
	future := map[string]any{"kind": "header", "sr": printout.Version + 99}
	dir := test.TempDir()

	raw, err := cbor.Marshal(future)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := printout.ReadCBOR(bytes.NewReader(raw), dir); err == nil {
		test.Error("want a version error from the CBOR reader")
	} else if !strings.Contains(err.Error(), "format version") {
		test.Errorf("CBOR error = %v", err)
	}

	// The NDJSON reader has always done this; the two must agree.
	line, err := json.Marshal(future)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := printout.ReadNDJSON(bytes.NewReader(line), dir); err == nil {
		test.Error("want a version error from the NDJSON reader")
	} else if !strings.Contains(err.Error(), "format version") {
		test.Errorf("NDJSON error = %v", err)
	}
}

// A deferred field carried into a split band's tail still receives its value.
//
// splitAt clones every draft that moves below the cut, so the tail commits
// the clone and the original is dropped. A deferral holds the mark it patches,
// so one left pointing at the original wrote the resolved value into a mark
// the printout no longer carried, and the placeholder stayed on the page.
func TestDeferredFieldSurvivesASplit(test *testing.T) {
	const src = `report name="t" {
  records { member "n" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=80 leftmargin=10 rightmargin=10 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    detail split=#true {
      field expr="n" stretch=#true left=0 width=60 top=0
      field expr="'of %d' % FINAL.PAGE_NUMBER" evaltime="report" \
            left=80 width=60 top=60 height=10
    }
  }
}`
	long := strings.Repeat("alpha beta gamma delta ", 4)
	out := buildString(test, src, []map[string]any{{"n": strings.TrimSpace(long)}})
	if len(out.Pages) < 2 {
		test.Fatalf("pages = %d, want the band to split", len(out.Pages))
	}
	want := "of " + string(rune('0'+len(out.Pages)))

	var found bool
	for index, page := range out.Pages {
		for _, mark := range texts(page) {
			line := strings.Join(mark.Lines, "")
			if line == "" {
				test.Errorf("page %d carries an unresolved placeholder", index+1)
				continue
			}
			if line == want {
				found = true
			}
		}
	}
	if !found {
		test.Errorf("no page carries the resolved %q", want)
	}
}

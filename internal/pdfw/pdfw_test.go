package pdfw

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
	"time"
)

// A length reaches the file with at most three decimals,
// no exponent, and no signed zero.
func TestNum(test *testing.T) {
	for _, row := range []struct {
		value float64
		want  string
	}{
		{0, "0"},
		{-0, "0"},
		{72, "72"},
		{595.276, "595.276"},
		{0.0004, "0"},
		{-0.0004, "0"},
		{1.00049, "1"},
		{1.0005, "1.001"},
		{-42.5, "-42.5"},
		{0.000001234, "0"},
		{1e21, "1000000000000000000000"},
	} {
		if got := Num(row.value); got != row.want {
			test.Errorf("Num(%v) = %q, want %q", row.value, got, row.want)
		}
	}
}

// A string a reader shows to a person goes out as a literal when it is
// printable ASCII, and as UTF-16 with a byte order mark otherwise.
func TestTextString(test *testing.T) {
	for _, row := range []struct {
		text string
		want string
	}{
		{"Report", "(Report)"},
		{"a (nested) case", `(a \(nested\) case)`},
		{`back\slash`, `(back\\slash)`},
		// A control character is not printable ASCII, so the string goes
		// out as UTF-16 rather than with an escape.
		{"line\nbreak", "<FEFF006C0069006E0065000A0062007200650061006B>"},
		{"Ветвь", "<FEFF0412043504420432044C>"},
		{"emoji \U0001F600", "<FEFF0065006D006F006A00690020D83DDE00>"},
	} {
		if got := TextString(row.text); got != row.want {
			test.Errorf("TextString(%q) = %s, want %s", row.text, got, row.want)
		}
	}
}

// A date goes out in PDF's own spelling, in UTC.
func TestDate(test *testing.T) {
	when := time.Date(2026, 8, 4, 12, 12, 44, 0, time.FixedZone("plus3", 3*3600))
	if got, want := Date(when), "(D:20260804091244+00'00')"; got != want {
		test.Errorf("Date = %s, want %s", got, want)
	}
}

// The writer records each object's offset, so the cross reference table
// leads to the objects whatever order they were written in.
func TestObjectOffsets(test *testing.T) {
	doc := New()
	first := doc.Alloc()
	second := doc.Alloc()
	// Written out of order, which is what a page naming its content
	// stream before the stream exists amounts to.
	doc.Object(second, "<</Kind /second>>")
	doc.Object(first, "<</Kind /first /Next "+Ref(second)+">>")
	raw := doc.Finish(first, 0)

	text := string(raw)
	if !strings.Contains(text, "xref\n0 3\n") {
		test.Errorf("the table does not cover three slots:\n%s", text)
	}
	// The table opens with the free object,
	// so object n is the entry after that one.
	head := "xref\n0 3\n"
	entries := strings.Split(text[strings.Index(text, head)+len(head):], "\n")
	for num, want := range map[int]string{
		1: "1 0 obj\n<</Kind /first",
		2: "2 0 obj\n<</Kind /second>>",
	} {
		var offset int
		if _, err := fmt.Sscanf(entries[num], "%d", &offset); err != nil {
			test.Fatalf("entry %d reads %q", num, entries[num])
		}
		if !strings.HasPrefix(text[offset:], want) {
			test.Errorf("the table sends object %d to %q, want %q",
				num, text[offset:min(offset+len(want), len(text))], want)
		}
	}
}

// A stream is compressed when the document asks for it,
// and its stated length is the length of what was written.
func TestStreamCompression(test *testing.T) {
	body := bytes.Repeat([]byte("content stream text "), 50)

	doc := New()
	doc.Compress = false
	num := doc.Alloc()
	doc.Stream(num, "", body)
	plain := doc.Finish(num, 0)
	if !bytes.Contains(plain, body) {
		test.Error("an uncompressed stream is not in the file as it stands")
	}
	if bytes.Contains(plain, []byte("/Filter")) {
		test.Error("an uncompressed stream names a filter")
	}

	doc = New()
	num = doc.Alloc()
	doc.Stream(num, "", body)
	packed := doc.Finish(num, 0)
	if !bytes.Contains(packed, []byte("/Filter /FlateDecode")) {
		test.Error("a compressed stream does not name its filter")
	}
	if len(packed) >= len(plain) {
		test.Errorf("compression made the file grow: %d against %d", len(packed), len(plain))
	}
}

// A rounded rectangle is a path of lines and curves, and a radius wider
// than the box is clamped rather than turning the path inside out.
func TestRoundedRect(test *testing.T) {
	con := NewContent(100)
	con.RoundedRect(10, 20, 40, 30, 5)
	drawn := con.Bytes()
	if got := bytes.Count(drawn, []byte(" c\n")); got != 4 {
		test.Errorf("curves = %d, want one per corner", got)
	}

	con = NewContent(100)
	con.RoundedRect(10, 20, 40, 30, 500)
	// Clamped to half the shorter side, so the corners still meet.
	if got := bytes.Count(con.Bytes(), []byte(" c\n")); got != 4 {
		test.Errorf("curves = %d after clamping", got)
	}

	con = NewContent(100)
	con.RoundedRect(10, 20, 40, 30, 0)
	if !bytes.Contains(con.Bytes(), []byte("re\n")) {
		test.Error("a radius of zero did not draw a plain rectangle")
	}
}

// The Y axis is flipped once per coordinate, so a box given
// by its top left corner comes out as PDF's bottom left one.
func TestCoordinateFlip(test *testing.T) {
	con := NewContent(200)
	con.Rect(10, 30, 40, 20)
	if got, want := string(con.Bytes()), "10 150 40 20 re\n"; got != want {
		test.Errorf("Rect = %q, want %q", got, want)
	}

	con = NewContent(200)
	con.Matrix(40, 20, 10, 30)
	if got, want := string(con.Bytes()), "40 0 0 20 10 150 cm\n"; got != want {
		test.Errorf("Matrix = %q, want %q", got, want)
	}
}

// A PNG becomes RGB samples, a grey image stays grey,
// and a baseline JPEG keeps its own compression rather than
// being decoded and re-encoded twentyfold larger.
func TestDecodePicture(test *testing.T) {
	colored := image.NewRGBA(image.Rect(0, 0, 4, 3))
	colored.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var asPNG bytes.Buffer
	if err := png.Encode(&asPNG, colored); err != nil {
		test.Fatal(err)
	}
	pic, err := DecodePicture(asPNG.Bytes())
	if err != nil {
		test.Fatal(err)
	}
	if pic.ColorSpace != "DeviceRGB" || pic.Width != 4 || pic.Height != 3 {
		test.Errorf("PNG read as %s %dx%d", pic.ColorSpace, pic.Width, pic.Height)
	}
	if len(pic.Data) != 4*3*3 {
		test.Errorf("samples = %d, want three per pixel", len(pic.Data))
	}
	if pic.Filter != "" {
		test.Errorf("a decoded image claims filter %q", pic.Filter)
	}

	grey := image.NewGray(image.Rect(0, 0, 4, 3))
	asPNG.Reset()
	if err := png.Encode(&asPNG, grey); err != nil {
		test.Fatal(err)
	}
	pic, err = DecodePicture(asPNG.Bytes())
	if err != nil {
		test.Fatal(err)
	}
	if pic.ColorSpace != "DeviceGray" || len(pic.Data) != 4*3 {
		test.Errorf("a grey PNG read as %s with %d samples", pic.ColorSpace, len(pic.Data))
	}

	var asJPEG bytes.Buffer
	if err := jpeg.Encode(&asJPEG, colored, nil); err != nil {
		test.Fatal(err)
	}
	pic, err = DecodePicture(asJPEG.Bytes())
	if err != nil {
		test.Fatal(err)
	}
	if pic.Filter != "/DCTDecode" {
		test.Errorf("a baseline JPEG was re-encoded: filter %q", pic.Filter)
	}
	if !bytes.Equal(pic.Data, asJPEG.Bytes()) {
		test.Error("a passed-through JPEG is not the bytes it came in as")
	}

	var asGIF bytes.Buffer
	if err := gif.Encode(&asGIF, colored, nil); err != nil {
		test.Fatal(err)
	}
	if _, err := DecodePicture(asGIF.Bytes()); err != nil {
		test.Errorf("a GIF did not decode: %v", err)
	}

	if _, err := DecodePicture([]byte("not an image")); err == nil {
		test.Error("a file that is not an image decoded")
	}
}

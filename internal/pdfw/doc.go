// Package pdfw writes PDF files: indirect objects, streams,
// the cross reference table, and the content stream operators
// a printout's marks need.
//
// It is a writer and nothing else. It knows no marks, no layout and
// no fonts beyond embedding the bytes it is handed, so the mapping
// from a printout to a page is one level up, in package pdf.
package pdfw

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// Version is the PDF version the writer claims.
//
// 1.7 covers everything here; nothing emitted needs a later one.
const Version = "1.7"

// Doc collects indirect objects and writes the file around them.
//
// Object numbers are handed out by Alloc and written in any order,
// so a page can name its content stream before the stream exists.
// Output is a pure function of what was written: no timestamps of its own,
// no map iteration, so the same document twice is the same bytes twice.
type Doc struct {
	// Compress applies FlateDecode to content and font streams.
	// Off makes the output readable, which is what a failing test wants.
	Compress bool

	buf     bytes.Buffer
	offsets map[int]int
	next    int
}

// New starts a document.
func New() *Doc {
	doc := &Doc{Compress: true, offsets: map[int]int{}, next: 1}
	doc.buf.WriteString("%PDF-" + Version + "\n")
	// A comment of high bytes marks the file as binary for tools that
	// would otherwise treat it as text and translate line endings.
	doc.buf.Write([]byte{'%', 0xE2, 0xE3, 0xCF, 0xD3, '\n'})
	return doc
}

// Alloc reserves an object number.
func (doc *Doc) Alloc() int {
	num := doc.next
	doc.next++
	return num
}

// Object writes a reserved object number's body.
func (doc *Doc) Object(num int, body string) {
	doc.offsets[num] = doc.buf.Len()
	fmt.Fprintf(&doc.buf, "%d 0 obj\n", num)
	doc.buf.WriteString(body)
	doc.buf.WriteString("\nendobj\n")
}

// Stream writes a stream object.
//
// extra holds the dictionary entries other than Length and Filter,
// without the enclosing angle brackets.
func (doc *Doc) Stream(num int, extra string, data []byte) {
	filter := ""
	if doc.Compress {
		var packed bytes.Buffer
		writer := zlib.NewWriter(&packed)
		if _, err := writer.Write(data); err == nil {
			if err := writer.Close(); err == nil {
				data = packed.Bytes()
				filter = "/Filter /FlateDecode"
			}
		}
	}
	doc.offsets[num] = doc.buf.Len()
	fmt.Fprintf(&doc.buf, "%d 0 obj\n<<", num)
	if extra != "" {
		doc.buf.WriteString(extra)
		doc.buf.WriteString(" ")
	}
	if filter != "" {
		doc.buf.WriteString(filter)
		doc.buf.WriteString(" ")
	}
	fmt.Fprintf(&doc.buf, "/Length %d>>\nstream\n", len(data))
	doc.buf.Write(data)
	doc.buf.WriteString("\nendstream\nendobj\n")
}

// RawStream writes a stream whose data is already filtered,
// so the caller supplies the Filter entry itself.
// A JPEG passes through this way, keeping its own compression.
func (doc *Doc) RawStream(num int, extra string, data []byte) {
	doc.offsets[num] = doc.buf.Len()
	fmt.Fprintf(&doc.buf, "%d 0 obj\n<<", num)
	if extra != "" {
		doc.buf.WriteString(extra)
		doc.buf.WriteString(" ")
	}
	fmt.Fprintf(&doc.buf, "/Length %d>>\nstream\n", len(data))
	doc.buf.Write(data)
	doc.buf.WriteString("\nendstream\nendobj\n")
}

// Finish appends the cross reference table and the trailer,
// and returns the whole file.
func (doc *Doc) Finish(root, info int) []byte {
	count := doc.next
	start := doc.buf.Len()
	fmt.Fprintf(&doc.buf, "xref\n0 %d\n", count)
	doc.buf.WriteString("0000000000 65535 f \n")
	for num := 1; num < count; num++ {
		fmt.Fprintf(&doc.buf, "%010d 00000 n \n", doc.offsets[num])
	}
	fmt.Fprintf(&doc.buf, "trailer\n<</Size %d /Root %d 0 R", count, root)
	if info > 0 {
		fmt.Fprintf(&doc.buf, " /Info %d 0 R", info)
	}
	fmt.Fprintf(&doc.buf, ">>\nstartxref\n%d\n%%%%EOF\n", start)
	return doc.buf.Bytes()
}

// Ref formats an indirect reference.
func Ref(num int) string { return strconv.Itoa(num) + " 0 R" }

// Num formats a length for a PDF file: at most three decimals, which is
// the printout's own precision, with no trailing zeros and no exponent.
func Num(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "0"
	}
	rounded := math.Round(value*1000) / 1000
	if rounded == 0 {
		// Keep a signed zero from reaching the file as "-0".
		return "0"
	}
	return strconv.FormatFloat(rounded, 'f', -1, 64)
}

// TextString formats a string for a field a reader shows to a person --
// a title, an outline entry, a link caption.
//
// Printable ASCII goes out as a literal string; anything else
// as UTF-16BE with a byte order mark, which is the only encoding
// PDF defines for text outside PDFDocEncoding.
func TextString(text string) string {
	if isPrintableASCII(text) {
		return "(" + escapeLiteral(text) + ")"
	}
	var out strings.Builder
	out.WriteString("<FEFF")
	for _, unit := range utf16.Encode([]rune(text)) {
		fmt.Fprintf(&out, "%04X", unit)
	}
	out.WriteString(">")
	return out.String()
}

// ASCIIString formats a string that is not shown to a person
// and carries no encoding mark: a URI, a destination name.
func ASCIIString(text string) string { return "(" + escapeLiteral(text) + ")" }

// Date formats a timestamp as PDF spells one.
func Date(when time.Time) string {
	utc := when.UTC()
	return fmt.Sprintf("(D:%s+00'00')", utc.Format("20060102150405"))
}

func isPrintableASCII(text string) bool {
	for _, char := range text {
		if char < 0x20 || char > 0x7E {
			return false
		}
	}
	return true
}

func escapeLiteral(text string) string {
	var out strings.Builder
	for index := 0; index < len(text); index++ {
		switch char := text[index]; char {
		case '(', ')', '\\':
			out.WriteByte('\\')
			out.WriteByte(char)
		case '\n':
			out.WriteString("\\n")
		case '\r':
			out.WriteString("\\r")
		case '\t':
			out.WriteString("\\t")
		default:
			out.WriteByte(char)
		}
	}
	return out.String()
}

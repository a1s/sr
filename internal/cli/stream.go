package cli

import (
	"fmt"
	"io"
)

// stream is an output stream that remembers its first write error.
//
// Every command writes many small lines, and checking each write would bury
// the code that decides what to print. Keeping the first error instead means
// a broken pipe -- what "sr inspect | head" does -- is reported once, at the
// end, rather than nine times or not at all.
type stream struct {
	to  io.Writer
	err error
}

// newStream wraps a writer.
func newStream(to io.Writer) *stream {
	return &stream{to: to}
}

// printf writes a formatted string.
func (str *stream) printf(format string, args ...any) {
	if str.err != nil {
		return
	}
	_, str.err = fmt.Fprintf(str.to, format, args...)
}

// line writes a formatted string and a newline.
func (str *stream) line(format string, args ...any) {
	str.printf(format+"\n", args...)
}

// write copies raw bytes, for a document rather than a message.
func (str *stream) write(raw []byte) {
	if str.err != nil {
		return
	}
	_, str.err = str.to.Write(raw)
}

// Package pdfscan reads a PDF back: its objects, its pages,
// and the text, paths and images its content streams draw.
//
// It exists so that a rendered document can be checked against
// the printout it came from -- glyph positions, line widths, link
// rectangles, outline structure -- which no printout-level test can see.
// It is a verification tool, deliberately independent of the writer:
// it parses the cross reference table rather than trusting object order,
// and it recovers text through each font's own ToUnicode map rather than
// through anything the writer remembers.
//
// It reads the subset of PDF this project writes. It is not a general
// reader: no encryption, no object streams, no filters but FlateDecode.
package pdfscan

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Object is any PDF value: nil, bool, float64, String, Name,
// Ref, Array, Dict, or *Stream.
type Object any

// Name is a PDF name, without its leading slash.
type Name string

// String is a PDF string, after escape processing.
type String []byte

// Ref is an indirect reference.
type Ref struct{ Num int }

// Array is a PDF array.
type Array []Object

// Dict is a PDF dictionary.
type Dict map[Name]Object

// Stream is a dictionary with data attached.
type Stream struct {
	Dict Dict
	Raw  []byte
}

// File is a parsed PDF.
type File struct {
	raw     []byte
	offsets map[int]int
	Trailer Dict
}

// Read parses a file's cross reference table and trailer.
func Read(raw []byte) (*File, error) {
	file := &File{raw: raw, offsets: map[int]int{}}
	start, err := file.startXref()
	if err != nil {
		return nil, err
	}
	if err := file.readXref(start); err != nil {
		return nil, err
	}
	if file.Trailer == nil {
		return nil, fmt.Errorf("the file has no trailer")
	}
	return file, nil
}

func (file *File) startXref() (int, error) {
	tail := file.raw
	if len(tail) > 2048 {
		tail = tail[len(tail)-2048:]
	}
	at := bytes.LastIndex(tail, []byte("startxref"))
	if at < 0 {
		return 0, fmt.Errorf("no startxref")
	}
	lex := newLexer(tail[at+len("startxref"):])
	value, err := lex.object()
	if err != nil {
		return 0, err
	}
	number, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("startxref names %v", value)
	}
	return int(number), nil
}

// readXref reads a classic cross reference table and its trailer.
func (file *File) readXref(at int) error {
	if at < 0 || at >= len(file.raw) {
		return fmt.Errorf("startxref points outside the file")
	}
	lex := newLexer(file.raw[at:])
	if word := lex.keyword(); word != "xref" {
		return fmt.Errorf("the cross reference table begins %q", word)
	}
	for {
		lex.space()
		if lex.looking("trailer") {
			lex.advance(len("trailer"))
			trailer, err := lex.object()
			if err != nil {
				return err
			}
			dict, ok := trailer.(Dict)
			if !ok {
				return fmt.Errorf("the trailer is %T", trailer)
			}
			file.Trailer = dict
			return nil
		}
		first, err := lex.integer()
		if err != nil {
			return err
		}
		count, err := lex.integer()
		if err != nil {
			return err
		}
		for index := 0; index < count; index++ {
			offset, err := lex.integer()
			if err != nil {
				return err
			}
			if _, err := lex.integer(); err != nil {
				return err
			}
			lex.space()
			kind := lex.keyword()
			if kind == "n" {
				file.offsets[first+index] = offset
			}
		}
	}
}

// Resolve follows an indirect reference, as many times as it takes.
func (file *File) Resolve(object Object) Object {
	for depth := 0; depth < 32; depth++ {
		ref, ok := object.(Ref)
		if !ok {
			return object
		}
		value, err := file.object(ref.Num)
		if err != nil {
			return nil
		}
		object = value
	}
	return nil
}

// object parses the indirect object with a number.
func (file *File) object(num int) (Object, error) {
	at, ok := file.offsets[num]
	if !ok {
		return nil, fmt.Errorf("object %d is not in the cross reference table", num)
	}
	if at >= len(file.raw) {
		return nil, fmt.Errorf("object %d lies outside the file", num)
	}
	lex := newLexer(file.raw[at:])
	if got, err := lex.integer(); err != nil || got != num {
		return nil, fmt.Errorf("object %d is not at its recorded offset", num)
	}
	if _, err := lex.integer(); err != nil {
		return nil, err
	}
	if word := lex.keyword(); word != "obj" {
		return nil, fmt.Errorf("object %d begins %q", num, word)
	}
	value, err := lex.object()
	if err != nil {
		return nil, err
	}
	lex.space()
	if !lex.looking("stream") {
		return value, nil
	}
	dict, ok := value.(Dict)
	if !ok {
		return nil, fmt.Errorf("object %d attaches a stream to a %T", num, value)
	}
	lex.advance(len("stream"))
	// The keyword is followed by CRLF or LF, and nothing else.
	if lex.looking("\r") {
		lex.advance(1)
	}
	if lex.looking("\n") {
		lex.advance(1)
	}
	length, ok := file.Resolve(dict["Length"]).(float64)
	if !ok {
		return nil, fmt.Errorf("object %d has a stream of unstated length", num)
	}
	start := at + lex.at
	end := start + int(length)
	if end > len(file.raw) {
		return nil, fmt.Errorf("object %d has a stream running past the end of the file", num)
	}
	return &Stream{Dict: dict, Raw: file.raw[start:end]}, nil
}

// Get resolves a dictionary entry.
func (file *File) Get(dict Dict, key Name) Object {
	if dict == nil {
		return nil
	}
	return file.Resolve(dict[key])
}

// Number reads a dictionary entry as a number.
func (file *File) Number(dict Dict, key Name) (float64, bool) {
	value, ok := file.Get(dict, key).(float64)
	return value, ok
}

// Data decodes a stream's contents, undoing FlateDecode.
func (file *File) Data(stream *Stream) ([]byte, error) {
	filter := file.Get(stream.Dict, "Filter")
	switch typed := filter.(type) {
	case nil:
		return stream.Raw, nil
	case Name:
		return inflateIf(typed, stream.Raw)
	case Array:
		data := stream.Raw
		for _, entry := range typed {
			name, ok := file.Resolve(entry).(Name)
			if !ok {
				return nil, fmt.Errorf("a filter is %T", entry)
			}
			out, err := inflateIf(name, data)
			if err != nil {
				return nil, err
			}
			data = out
		}
		return data, nil
	}
	return nil, fmt.Errorf("a filter is %T", filter)
}

func inflateIf(filter Name, data []byte) ([]byte, error) {
	switch filter {
	case "FlateDecode":
		reader, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer reader.Close() // nolint:errcheck
		return io.ReadAll(reader)
	case "DCTDecode":
		// Image data this reader does not decode; the caller compares
		// the bytes, not the pixels.
		return data, nil
	}
	return nil, fmt.Errorf("unsupported filter %q", filter)
}

// Catalog is the document catalog.
func (file *File) Catalog() Dict {
	dict, _ := file.Get(file.Trailer, "Root").(Dict)
	return dict
}

// Info is the document information dictionary, absent as nil.
func (file *File) Info() Dict {
	dict, _ := file.Get(file.Trailer, "Info").(Dict)
	return dict
}

// Pages returns the page dictionaries in document order.
func (file *File) Pages() []Dict {
	tree, _ := file.Get(file.Catalog(), "Pages").(Dict)
	var out []Dict
	file.walkPages(tree, &out, 0)
	return out
}

func (file *File) walkPages(node Dict, out *[]Dict, depth int) {
	if node == nil || depth > 32 {
		return
	}
	kids, ok := file.Get(node, "Kids").(Array)
	if !ok {
		*out = append(*out, node)
		return
	}
	for _, kid := range kids {
		dict, _ := file.Resolve(kid).(Dict)
		file.walkPages(dict, out, depth+1)
	}
}

// PageContent returns a page's decoded content stream.
func (file *File) PageContent(page Dict) ([]byte, error) {
	switch typed := file.Get(page, "Contents").(type) {
	case *Stream:
		return file.Data(typed)
	case Array:
		var joined []byte
		for _, entry := range typed {
			stream, ok := file.Resolve(entry).(*Stream)
			if !ok {
				continue
			}
			data, err := file.Data(stream)
			if err != nil {
				return nil, err
			}
			joined = append(joined, data...)
			joined = append(joined, '\n')
		}
		return joined, nil
	}
	return nil, fmt.Errorf("the page has no content stream")
}

// Text renders an object the way a diagnostic wants to see it.
func Text(object Object) string {
	switch typed := object.(type) {
	case nil:
		return "null"
	case Name:
		return "/" + string(typed)
	case String:
		return string(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case Ref:
		return fmt.Sprintf("%d 0 R", typed.Num)
	case Array:
		parts := make([]string, len(typed))
		for index, entry := range typed {
			parts[index] = Text(entry)
		}
		return "[" + strings.Join(parts, " ") + "]"
	}
	return fmt.Sprintf("%v", object)
}

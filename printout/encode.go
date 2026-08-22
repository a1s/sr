package printout

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/fxamacker/cbor/v2"
)

// WriteNDJSON writes the printout as one JSON object per line:
// the header, then one line per page.
//
// dir is the directory the printout is being written to, which is what
// template-named paths are made relative to. An empty dir stands for
// the process's working directory, which is what a pipe or an in-memory
// hand-off gets.
func (doc *Printout) WriteNDJSON(writer io.Writer, dir string) error {
	objects, err := doc.serializable(dir)
	if err != nil {
		return err
	}
	out := bufio.NewWriter(writer)
	enc := json.NewEncoder(out)
	for _, obj := range objects {
		if err := enc.Encode(obj); err != nil {
			return err
		}
	}
	return out.Flush()
}

// WriteCBOR writes the same objects as a CBOR sequence, in the same order.
func (doc *Printout) WriteCBOR(writer io.Writer, dir string) error {
	objects, err := doc.serializable(dir)
	if err != nil {
		return err
	}
	out := bufio.NewWriter(writer)
	enc := cbor.NewEncoder(out)
	for _, obj := range objects {
		if err := enc.Encode(obj); err != nil {
			return err
		}
	}
	return out.Flush()
}

// WriteFile writes the printout, choosing the encoding from the extension:
// .srp.cbor is CBOR, anything else NDJSON.
func (doc *Printout) WriteFile(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if strings.HasSuffix(strings.ToLower(path), ".cbor") {
		err = doc.WriteCBOR(file, dir)
	} else {
		err = doc.WriteNDJSON(file, dir)
	}
	ec := file.Close()
	if err == nil {
		return ec
	} else {
		return err
	}
}

// serializable produces the header and page objects with paths rewritten.
//
// Rewriting happens here rather than at build time because that is
// when the printout's location is known: one printout serialized to two
// directories carries two different values, and both are right.
func (doc *Printout) serializable(dir string) ([]any, error) {
	base := dir
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		base = cwd
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return nil, err
	}

	header := doc.Header
	header.SR = Version
	header.Kind = "header"
	header.Pages = len(doc.Pages)
	header.Fonts = append([]FontEntry(nil), doc.Header.Fonts...)
	for index := range header.Fonts {
		if header.Fonts[index].absFile != "" {
			header.Fonts[index].ResolvedFile = relativeTo(
				abs, header.Fonts[index].absFile)
		}
	}
	if header.Data == nil {
		header.Data = map[string]*Blob{}
	}

	objects := []any{header}
	for _, page := range doc.Pages {
		copied := *page
		copied.Kind = "page"
		copied.Marks = rewriteMarkPaths(page.Marks, abs)
		objects = append(objects, &copied)
	}
	return objects, nil
}

func rewriteMarkPaths(marks []Mark, base string) []Mark {
	out := make([]Mark, len(marks))
	for index, mark := range marks {
		switch typed := mark.(type) {
		case *Image:
			if typed.absFile != "" {
				copied := *typed
				copied.File = relativeTo(base, typed.absFile)
				out[index] = &copied
				continue
			}
		case *Xref:
			copied := *typed
			copied.Marks = rewriteMarkPaths(typed.Marks, base)
			out[index] = &copied
			continue
		}
		out[index] = mark
	}
	return out
}

// relativeTo expresses target relative to base, with forward slashes,
// so a printout written on Windows renders on Linux.
//
// A path with no relative route to the printout at all -- one on
// a different Windows drive -- is written absolute.
func relativeTo(base, target string) string {
	abs, err := filepath.Abs(target)
	if err != nil {
		return filepath.ToSlash(target)
	}
	rel, err := filepath.Rel(base, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// ReadNDJSON reads a printout back.
//
// A relative path in the document resolves against dir, the directory
// the printout was read from; an absolute one is used as it stands.
// So a renderer needs no base directory and the header carries none.
func ReadNDJSON(reader io.Reader, dir string) (*Printout, error) {
	dec := json.NewDecoder(reader)
	var out Printout
	first := true
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		var probe struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, err
		}
		switch probe.Kind {
		case "header":
			if !first {
				return nil, fmt.Errorf("the header must be the first line")
			}
			if err := json.Unmarshal(raw, &out.Header); err != nil {
				return nil, err
			}
			if out.Header.SR != Version {
				return nil, fmt.Errorf(
					"printout format version %d; this reader understands %d",
					out.Header.SR, Version)
			}
		case "page":
			page, err := decodePage(raw, dir)
			if err != nil {
				return nil, err
			}
			out.Pages = append(out.Pages, page)
		default:
			return nil, fmt.Errorf("unknown line kind %q", probe.Kind)
		}
		first = false
	}
	for index := range out.Header.Fonts {
		entry := &out.Header.Fonts[index]
		if entry.ResolvedFile != "" && !filepath.IsAbs(entry.ResolvedFile) {
			entry.absFile = filepath.Join(dir, filepath.FromSlash(entry.ResolvedFile))
		}
	}
	return &out, nil
}

// ReadFile reads a printout, choosing the encoding from the extension.
func ReadFile(path string) (*Printout, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close() // nolint:errcheck
	if strings.HasSuffix(strings.ToLower(path), ".cbor") {
		return ReadCBOR(file, filepath.Dir(path))
	}
	return ReadNDJSON(file, filepath.Dir(path))
}

// ReadCBOR reads a printout from a CBOR sequence.
func ReadCBOR(reader io.Reader, dir string) (*Printout, error) {
	dec := cbor.NewDecoder(reader)
	var out Printout
	first := true
	for {
		var raw cbor.RawMessage
		if err := dec.Decode(&raw); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		var probe struct {
			Kind string `cbor:"kind"`
		}
		if err := cbor.Unmarshal(raw, &probe); err != nil {
			return nil, err
		}
		// The two encodings carry the same data model,
		// so route through JSON once the object kind is known
		// rather than duplicating the mark decoding.
		asJSON, err := cborToJSON(raw)
		if err != nil {
			return nil, err
		}
		switch probe.Kind {
		case "header":
			if !first {
				return nil, fmt.Errorf("the header must come first")
			}
			if err := json.Unmarshal(asJSON, &out.Header); err != nil {
				return nil, err
			}
			if out.Header.SR != Version {
				return nil, fmt.Errorf(
					"printout format version %d; this reader understands %d",
					out.Header.SR, Version)
			}
		case "page":
			page, err := decodePage(asJSON, dir)
			if err != nil {
				return nil, err
			}
			out.Pages = append(out.Pages, page)
		default:
			return nil, fmt.Errorf("unknown object kind %q", probe.Kind)
		}
		first = false
	}
	return &out, nil
}

// cborDecMode decodes CBOR maps with string keys, so that the decoded value
// can be handed straight to the JSON path the two encodings share.
var cborDecMode = func() cbor.DecMode {
	mode, err := cbor.DecOptions{
		DefaultMapType: reflect.TypeOf(map[string]any(nil)),
	}.DecMode()
	if err != nil {
		panic(err)
	}
	return mode
}()

func cborToJSON(raw cbor.RawMessage) ([]byte, error) {
	var value any
	if err := cborDecMode.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func decodePage(raw []byte, dir string) (*Page, error) {
	var shape struct {
		Kind   string            `json:"kind"`
		Number int               `json:"number"`
		Width  float64           `json:"width"`
		Height float64           `json:"height"`
		Marks  []json.RawMessage `json:"marks"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		return nil, err
	}
	page := &Page{Kind: "page", Number: shape.Number, Width: shape.Width, Height: shape.Height}
	marks, err := decodeMarks(shape.Marks, dir)
	if err != nil {
		return nil, err
	}
	page.Marks = marks
	return page, nil
}

func decodeMarks(raws []json.RawMessage, dir string) ([]Mark, error) {
	out := make([]Mark, 0, len(raws))
	for _, raw := range raws {
		var probe struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, err
		}
		switch probe.Kind {
		case "text":
			var text Text
			if err := json.Unmarshal(raw, &text); err != nil {
				return nil, err
			}
			out = append(out, &text)
		case "line":
			var line Line
			if err := json.Unmarshal(raw, &line); err != nil {
				return nil, err
			}
			out = append(out, &line)
		case "rectangle":
			var rect Rectangle
			if err := json.Unmarshal(raw, &rect); err != nil {
				return nil, err
			}
			out = append(out, &rect)
		case "image":
			var image Image
			if err := json.Unmarshal(raw, &image); err != nil {
				return nil, err
			}
			if image.File != "" && !filepath.IsAbs(image.File) {
				image.absFile = filepath.Join(dir, filepath.FromSlash(image.File))
			}
			out = append(out, &image)
		case "barcode":
			var barcode Barcode
			if err := json.Unmarshal(raw, &barcode); err != nil {
				return nil, err
			}
			out = append(out, &barcode)
		case "outline":
			var outline Outline
			if err := json.Unmarshal(raw, &outline); err != nil {
				return nil, err
			}
			out = append(out, &outline)
		case "xref":
			var shape struct {
				Xref
				Marks []json.RawMessage `json:"marks"`
			}
			if err := json.Unmarshal(raw, &shape); err != nil {
				return nil, err
			}
			nested, err := decodeMarks(shape.Marks, dir)
			if err != nil {
				return nil, err
			}
			xref := shape.Xref
			xref.Marks = nested
			out = append(out, &xref)
		default:
			return nil, fmt.Errorf("unknown mark kind %q", probe.Kind)
		}
	}
	return out, nil
}

// ResolvedPath returns a font entry's file as an absolute path,
// resolved against wherever the printout was read from.
func (entry FontEntry) ResolvedPath() string {
	if entry.absFile != "" {
		return entry.absFile
	}
	return entry.ResolvedFile
}

// ResolvedPath returns an image mark's file as an absolute path.
func (image *Image) ResolvedPath() string {
	if image.absFile != "" {
		return image.absFile
	}
	return image.File
}

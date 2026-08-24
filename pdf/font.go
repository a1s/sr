package pdf

import (
	"encoding/base64"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/a1s/sr/internal/fontres"
	"github.com/a1s/sr/internal/pdfw"
	"github.com/a1s/sr/internal/sfnt"
	"github.com/a1s/sr/printout"
)

// renderFont is one of the printout's fonts.
//
// The same face the engine measured with, the font program to embed,
// and the characters this document asks of it.
type renderFont struct {
	entry   printout.FontEntry
	face    *fontres.Face
	program *sfnt.Font
	// resource is the name a content stream selects the font by.
	resource string
	object   int

	used map[rune]bool
	// code maps a character to the two-byte code a content stream
	// shows for it. One code per character rather than per glyph:
	// two characters can share a glyph, and one code for both would
	// make the text come back out of the file as the wrong one of them.
	code map[rune]uint16
	// order is the characters in code order, which is code 1 upward.
	order []rune
}

// loadFonts opens every font the header names.
//
// Both the metrics and the font program come from the one file the
// printout resolved, read through the same code the engine measures
// with. Nothing here consults the resolution chain: resolution already
// happened, and its answer is in the document.
func (ren *renderer) loadFonts() error {
	for index, entry := range ren.src.Header.Fonts {
		raw, err := ren.fontBytes(entry)
		if err != nil {
			return fmt.Errorf("font %q: %w", entry.Name, err)
		}
		face, err := fontres.Load(raw, entry.ResolvedIndex, entry.Size)
		if err != nil {
			return fmt.Errorf("font %q: %s: %w", entry.Name, ren.fontSource(entry), err)
		}
		program, err := sfnt.Parse(raw, entry.ResolvedIndex)
		if err != nil {
			return fmt.Errorf("font %q: %s: %w", entry.Name, ren.fontSource(entry), err)
		}
		if !program.TrueType {
			return fmt.Errorf(
				"font %q: %s: the face has PostScript (CFF) outlines,"+
					" and this renderer embeds TrueType outlines",
				entry.Name, ren.fontSource(entry))
		}
		ren.fonts[entry.Name] = &renderFont{
			entry:    entry,
			face:     face,
			program:  program,
			resource: fmt.Sprintf("F%d", index+1),
			used:     map[rune]bool{},
			code:     map[rune]uint16{},
		}
		ren.fontNames = append(ren.fontNames, entry.Name)
	}
	return nil
}

// fontSource names where a font came from, for a diagnostic.
func (ren *renderer) fontSource(entry printout.FontEntry) string {
	if entry.ResolvedData != "" {
		return "data blob " + entry.ResolvedData
	}
	return entry.ResolvedPath()
}

// fontBytes reads a font entry's file, or its blob out of the header.
func (ren *renderer) fontBytes(entry printout.FontEntry) ([]byte, error) {
	if entry.ResolvedData != "" {
		blob, ok := ren.src.Header.Data[entry.ResolvedData]
		if !ok || blob == nil {
			return nil, fmt.Errorf("the header has no data entry %q", entry.ResolvedData)
		}
		return blobBytes(blob)
	}
	path := entry.ResolvedPath()
	if path == "" {
		return nil, fmt.Errorf("the entry names neither a file nor a data blob")
	}
	return os.ReadFile(path)
}

// blobBytes decodes a header data entry.
func blobBytes(blob *printout.Blob) ([]byte, error) {
	switch blob.Encoding {
	case "":
		return []byte(blob.Content), nil
	case "base64":
		clean := strings.Map(func(char rune) rune {
			if char == '\n' || char == '\r' || char == '\t' || char == ' ' {
				return -1
			}
			return char
		}, blob.Content)
		return base64.StdEncoding.DecodeString(clean)
	}
	return nil, fmt.Errorf("unknown blob encoding %q", blob.Encoding)
}

// note records that a character is used, so the subset carries it.
func (font *renderFont) note(text string) {
	for _, char := range text {
		font.used[char] = true
	}
}

// codeLimit is the number of codes a two-byte encoding holds, one of
// which is spent on the empty glyph.
const codeLimit = 0xFFFF

// assign gives every used character a code, in code point order
// so that the same document produces the same subset on every run.
func (font *renderFont) assign() error {
	font.order = make([]rune, 0, len(font.used))
	for char := range font.used {
		font.order = append(font.order, char)
	}
	sort.Slice(font.order, func(one, two int) bool {
		return font.order[one] < font.order[two]
	})
	if len(font.order) >= codeLimit {
		// Two characters sharing a code would draw one of them wrongly
		// and extract as the other, with nothing to show it happened.
		return fmt.Errorf("%d distinct characters, and a font carries at most %d",
			len(font.order), codeLimit-1)
	}
	for index, char := range font.order {
		font.code[char] = uint16(index + 1)
	}
	return nil
}

// embedded describes the subset to write.
//
// Code 0 is the empty box a font draws for a character it does not have,
// which is what the specification asks a missing glyph to look like. A
// character the face lacks gets a code of its own that draws that same
// glyph, so the text still reads back out of the file as what the
// printout said.
func (font *renderFont) embedded() *pdfw.Embedded {
	glyphs := make([]uint16, 0, len(font.order)+1)
	advances := make([]float64, 0, len(font.order)+1)
	runes := make(map[uint16][]rune, len(font.order))

	glyphs = append(glyphs, 0)
	advances = append(advances, font.face.GlyphAdvance(0))
	for index, char := range font.order {
		gid, _ := font.face.Glyph(char)
		glyphs = append(glyphs, gid)
		advances = append(advances, font.face.GlyphAdvance(gid))
		runes[uint16(index+1)] = []rune{char}
	}
	return &pdfw.Embedded{
		Program:  font.program,
		Glyphs:   glyphs,
		Advances: advances,
		Runes:    runes,
		Italic:   font.entry.Italic,
	}
}

// glyphs encodes a string as the codes a content stream shows.
func (font *renderFont) glyphs(text string) []uint16 {
	out := make([]uint16, 0, len(text))
	for _, char := range text {
		out = append(out, font.code[char])
	}
	return out
}

func (ren *renderer) writeFonts() error {
	for _, name := range ren.fontNames {
		font := ren.fonts[name]
		if err := font.assign(); err != nil {
			return fmt.Errorf("font %q: %w", name, err)
		}
		object, err := ren.out.WriteFont(font.embedded())
		if err != nil {
			return fmt.Errorf("font %q: %w", name, err)
		}
		font.object = object
	}
	return nil
}

package pdfscan

import (
	"fmt"
	"strings"
	"unicode/utf16"
)

// Font is a font as the file describes it: the widths a reader
// advances the pen by, and the map back from codes to characters.
type Font struct {
	// Resource is the name a content stream selects it by.
	Resource string
	// BaseFont is the /BaseFont name, subset tag included.
	BaseFont string
	// Composite reports Identity-H encoding, whose codes are two bytes.
	Composite bool
	// Widths are in thousandths of an em, by character code.
	Widths map[uint16]float64
	// Unicode maps a code to the characters it stands for.
	Unicode map[uint16]string
	// Descriptor is the font descriptor dictionary.
	Descriptor Dict
	// FontFile is the embedded font program.
	FontFile []byte
}

// font loads a page resource's font, once per name.
func (file *File) font(resources Dict, name string, cache map[string]*Font) (*Font, error) {
	if font, ok := cache[name]; ok {
		return font, nil
	}
	table, _ := file.Get(resources, "Font").(Dict)
	dict, _ := file.Get(table, Name(name)).(Dict)
	if dict == nil {
		return nil, fmt.Errorf("the page resources name no font %q", name)
	}
	font, err := file.readFont(dict)
	if err != nil {
		return nil, fmt.Errorf("font %q: %w", name, err)
	}
	font.Resource = name
	cache[name] = font
	return font, nil
}

// Fonts reads every font a page's resources offer, which is what a test
// comparing the file's widths against the original tables needs.
func (file *File) Fonts(page Dict) (map[string]*Font, error) {
	resources, _ := file.Get(page, "Resources").(Dict)
	table, _ := file.Get(resources, "Font").(Dict)
	out := map[string]*Font{}
	for name, entry := range table {
		dict, ok := file.Resolve(entry).(Dict)
		if !ok {
			continue
		}
		font, err := file.readFont(dict)
		if err != nil {
			return nil, fmt.Errorf("font %q: %w", name, err)
		}
		font.Resource = string(name)
		out[string(name)] = font
	}
	return out, nil
}

func (file *File) readFont(dict Dict) (*Font, error) {
	font := &Font{
		Widths:  map[uint16]float64{},
		Unicode: map[uint16]string{},
	}
	if base, ok := file.Get(dict, "BaseFont").(Name); ok {
		font.BaseFont = string(base)
	}
	if encoding, ok := file.Get(dict, "Encoding").(Name); ok {
		font.Composite = encoding == "Identity-H"
	}

	descendants, _ := file.Get(dict, "DescendantFonts").(Array)
	if len(descendants) > 0 {
		cid, _ := file.Resolve(descendants[0]).(Dict)
		if cid == nil {
			return nil, fmt.Errorf("the descendant font is missing")
		}
		font.readCIDWidths(file, cid)
		font.Descriptor, _ = file.Get(cid, "FontDescriptor").(Dict)
		if stream, ok := file.Get(font.Descriptor, "FontFile2").(*Stream); ok {
			data, err := file.Data(stream)
			if err != nil {
				return nil, err
			}
			font.FontFile = data
		}
	}

	if stream, ok := file.Get(dict, "ToUnicode").(*Stream); ok {
		data, err := file.Data(stream)
		if err != nil {
			return nil, err
		}
		if err := font.readToUnicode(data); err != nil {
			return nil, err
		}
	}
	return font, nil
}

// readCIDWidths reads the W array, whose entries are either a first code
// and a list of widths, or a range of codes and one width for all.
func (font *Font) readCIDWidths(file *File, cid Dict) {
	array, _ := file.Get(cid, "W").(Array)
	for index := 0; index < len(array); {
		first, ok := file.Resolve(array[index]).(float64)
		if !ok {
			index++
			continue
		}
		if index+1 >= len(array) {
			break
		}
		switch next := file.Resolve(array[index+1]).(type) {
		case Array:
			for offset, entry := range next {
				width, _ := file.Resolve(entry).(float64)
				font.Widths[uint16(int(first)+offset)] = width
			}
			index += 2
		case float64:
			if index+2 >= len(array) {
				return
			}
			width, _ := file.Resolve(array[index+2]).(float64)
			for code := int(first); code <= int(next); code++ {
				font.Widths[uint16(code)] = width
			}
			index += 3
		default:
			index++
		}
	}
}

// readToUnicode reads the bfchar and bfrange blocks of a CMap.
func (font *Font) readToUnicode(data []byte) error {
	lex := newLexer(data)
	var stack []Object
	for {
		lex.space()
		if lex.at >= len(lex.raw) {
			return nil
		}
		char := lex.raw[lex.at]
		if char == '/' || char == '(' || char == '<' || char == '[' ||
			char == '+' || char == '-' || char == '.' || (char >= '0' && char <= '9') {
			value, err := lex.object()
			if err != nil {
				return err
			}
			stack = append(stack, value)
			continue
		}
		switch word := lex.keyword(); word {
		case "":
			lex.at++
		case "beginbfchar":
			if err := font.readBfChars(lex); err != nil {
				return err
			}
			stack = stack[:0]
		case "beginbfrange":
			if err := font.readBfRanges(lex); err != nil {
				return err
			}
			stack = stack[:0]
		default:
			stack = stack[:0]
		}
	}
}

func (font *Font) readBfChars(lex *lexer) error {
	for {
		lex.space()
		if lex.looking("endbfchar") {
			lex.advance(len("endbfchar"))
			return nil
		}
		if lex.at >= len(lex.raw) {
			return fmt.Errorf("a bfchar block is not closed")
		}
		code, err := lex.object()
		if err != nil {
			return err
		}
		value, err := lex.object()
		if err != nil {
			return err
		}
		from, ok := code.(String)
		to, ok2 := value.(String)
		if !ok || !ok2 {
			continue
		}
		font.Unicode[codeOf(from)] = utf16BE(to)
	}
}

func (font *Font) readBfRanges(lex *lexer) error {
	for {
		lex.space()
		if lex.looking("endbfrange") {
			lex.advance(len("endbfrange"))
			return nil
		}
		if lex.at >= len(lex.raw) {
			return fmt.Errorf("a bfrange block is not closed")
		}
		low, err := lex.object()
		if err != nil {
			return err
		}
		high, err := lex.object()
		if err != nil {
			return err
		}
		value, err := lex.object()
		if err != nil {
			return err
		}
		first, ok := low.(String)
		last, ok2 := high.(String)
		if !ok || !ok2 {
			continue
		}
		switch typed := value.(type) {
		case String:
			base := []rune(utf16BE(typed))
			low, high := codeOf(first), codeOf(last)
			// Counted in a wider type: a range ending at FFFF
			// is an ordinary thing to write, and a uint16 counter
			// never reaches a value past it.
			for code := int(low); code <= int(high); code++ {
				shifted := append([]rune(nil), base...)
				if len(shifted) > 0 {
					shifted[len(shifted)-1] += rune(code - int(low))
				}
				font.Unicode[uint16(code)] = string(shifted)
			}
		case Array:
			for offset, entry := range typed {
				text, ok := entry.(String)
				if !ok {
					continue
				}
				font.Unicode[codeOf(first)+uint16(offset)] = utf16BE(text)
			}
		}
	}
}

func codeOf(raw String) uint16 {
	value := uint16(0)
	for _, char := range raw {
		value = value<<8 | uint16(char)
	}
	return value
}

func utf16BE(raw String) string {
	units := make([]uint16, 0, len(raw)/2)
	for index := 0; index+1 < len(raw); index += 2 {
		units = append(units, uint16(raw[index])<<8|uint16(raw[index+1]))
	}
	return string(utf16.Decode(units))
}

// decode turns a shown string into text, the advance the file says it
// has in thousandths of an em, and the number of character codes it
// held -- which is what character spacing counts, and is not the
// number of characters the codes stand for.
func (font *Font) decode(raw String) (string, float64, int) {
	var text strings.Builder
	width := 0.0
	codes := 0
	step := 1
	if font.Composite {
		step = 2
	}
	for index := 0; index+step <= len(raw); index += step {
		code := codeOf(raw[index : index+step])
		text.WriteString(font.Unicode[code])
		width += font.Widths[code]
		codes++
	}
	return text.String(), width, codes
}

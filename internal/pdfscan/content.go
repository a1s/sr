package pdfscan

import (
	"fmt"
	"strings"
	"unicode/utf16"
)

// Run is one shown string, with where the reader's pen was
// when it was shown and how far the string advances it.
//
// Left and Baseline are in the printout's coordinates -- origin
// top left, Y down -- so they compare directly against a text mark.
type Run struct {
	Font     string
	Size     float64
	Left     float64
	Baseline float64
	Text     string
	// Width is the advance the file itself says the string has, summed
	// from the font dictionary's W array. It is what a reader would move
	// the pen by, which is not necessarily what the engine measured.
	Width float64
}

// Rect is a rectangle the content stream painted.
type Rect struct {
	Left, Top, Width, Height float64
	// Painted is "fill", "stroke", "fillstroke", or "clip".
	Painted   string
	Fill      [3]float64
	Stroke    [3]float64
	LineWidth float64
	Dash      []float64
}

// Segment is a stroked straight line.
type Segment struct {
	FromLeft, FromTop float64
	ToLeft, ToTop     float64
	Stroke            [3]float64
	LineWidth         float64
	Dash              []float64
}

// Draw is an XObject placement, with the box the matrix maps it onto.
type Draw struct {
	Name                     string
	Left, Top, Width, Height float64
}

// Annot is a link annotation.
type Annot struct {
	Subtype                  string
	Left, Top, Width, Height float64
	URI                      string
	// DestPage is the index of the page a destination points at,
	// -1 for a link that has none.
	DestPage          int
	DestLeft, DestTop float64
	Contents          string
}

// Page is one page as read back.
type Page struct {
	Number        int
	Width, Height float64
	Runs          []Run
	Rects         []Rect
	Segments      []Segment
	Draws         []Draw
	Annots        []Annot
}

// Text joins a page's runs, which is the whole
// of what a reader would select on it.
func (page *Page) Text() string {
	parts := make([]string, len(page.Runs))
	for index, run := range page.Runs {
		parts[index] = run.Text
	}
	return strings.Join(parts, "")
}

// matrix is a PDF transformation matrix: a b c d e f.
type matrix [6]float64

var identity = matrix{1, 0, 0, 1, 0, 0}

// multiply returns one matrix concatenated with another, one applied first.
func (one matrix) multiply(two matrix) matrix {
	return matrix{
		one[0]*two[0] + one[1]*two[2],
		one[0]*two[1] + one[1]*two[3],
		one[2]*two[0] + one[3]*two[2],
		one[2]*two[1] + one[3]*two[3],
		one[4]*two[0] + one[5]*two[2] + two[4],
		one[4]*two[1] + one[5]*two[3] + two[5],
	}
}

func (one matrix) apply(left, top float64) (float64, float64) {
	return one[0]*left + one[2]*top + one[4], one[1]*left + one[3]*top + one[5]
}

// state is the part of the graphics state this reader tracks.
type state struct {
	ctm       matrix
	fill      [3]float64
	stroke    [3]float64
	lineWidth float64
	dash      []float64
}

// ReadPages replays every page's content stream.
func (file *File) ReadPages() ([]*Page, error) {
	dicts := file.Pages()
	fonts := map[string]*Font{}
	out := make([]*Page, 0, len(dicts))
	for at, dict := range dicts {
		page, err := file.readPage(at, dict, dicts, fonts)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", at+1, err)
		}
		out = append(out, page)
	}
	return out, nil
}

func (file *File) readPage(
	at int, dict Dict, all []Dict, fonts map[string]*Font) (*Page, error) {
	page := &Page{Number: at + 1}
	if box, ok := file.Get(dict, "MediaBox").(Array); ok && len(box) == 4 {
		width, _ := file.Resolve(box[2]).(float64)
		height, _ := file.Resolve(box[3]).(float64)
		page.Width, page.Height = width, height
	}
	content, err := file.PageContent(dict)
	if err != nil {
		return nil, err
	}
	resources, _ := file.Get(dict, "Resources").(Dict)
	if err := file.replay(page, content, resources, fonts); err != nil {
		return nil, err
	}
	page.Annots = file.annotations(dict, all)
	return page, nil
}

// replay walks a content stream, keeping just enough graphics state
// to say where each mark landed.
func (file *File) replay(
	page *Page, content []byte, resources Dict, fonts map[string]*Font) error {
	lex := newLexer(content)
	var stack []Object
	var saved []state
	cur := state{ctm: identity}

	var path []matrix // the current path's points, as translations
	var rects []Rect
	pending := ""

	var textMatrix, lineMatrix matrix
	var font *Font
	fontName := ""
	var size, leading, charSpace float64
	horizontal := 1.0

	number := func(back int) float64 {
		if len(stack) < back+1 {
			return 0
		}
		value, _ := stack[len(stack)-1-back].(float64)
		return value
	}
	flip := func(top float64) float64 { return page.Height - top }

	show := func(text String) {
		if font == nil {
			return
		}
		decoded, width, codes := font.decode(text)
		originLeft, originTop := textMatrix.apply(0, 0)
		originLeft, originTop = cur.ctm.apply(originLeft, originTop)
		// Word spacing is deliberately not applied: it acts on the
		// single-byte code 32, which an Identity-H composite font
		// has none of, so a file that relied on it would be wrong.
		advance := (width/1000*size + charSpace*float64(codes)) * horizontal
		page.Runs = append(page.Runs, Run{
			Font: fontName, Size: size,
			Left: originLeft, Baseline: flip(originTop),
			Text: decoded, Width: width / 1000 * size,
		})
		textMatrix = matrix{1, 0, 0, 1, advance, 0}.multiply(textMatrix)
	}

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
		operator := lex.keyword()
		if operator == "" {
			lex.at++
			continue
		}
		switch operator {
		case "q":
			saved = append(saved, cur)
		case "Q":
			if len(saved) > 0 {
				cur = saved[len(saved)-1]
				saved = saved[:len(saved)-1]
			}
		case "cm":
			given := matrix{number(5), number(4), number(3), number(2), number(1), number(0)}
			cur.ctm = given.multiply(cur.ctm)
		case "w":
			cur.lineWidth = number(0)
		case "d":
			cur.dash = nil
			if len(stack) >= 2 {
				if array, ok := stack[len(stack)-2].(Array); ok {
					for _, entry := range array {
						value, _ := entry.(float64)
						cur.dash = append(cur.dash, value)
					}
				}
			}
		case "rg":
			cur.fill = [3]float64{number(2), number(1), number(0)}
		case "RG":
			cur.stroke = [3]float64{number(2), number(1), number(0)}
		case "g":
			grey := number(0)
			cur.fill = [3]float64{grey, grey, grey}
		case "G":
			grey := number(0)
			cur.stroke = [3]float64{grey, grey, grey}
		case "re":
			left, bottom := number(3), number(2)
			width, height := number(1), number(0)
			oneLeft, oneBottom := cur.ctm.apply(left, bottom)
			twoLeft, twoTop := cur.ctm.apply(left+width, bottom+height)
			rects = append(rects, Rect{
				Left: min(oneLeft, twoLeft), Top: flip(max(oneBottom, twoTop)),
				Width: abs(twoLeft - oneLeft), Height: abs(twoTop - oneBottom),
				Fill: cur.fill, Stroke: cur.stroke,
				LineWidth: cur.lineWidth, Dash: cur.dash,
			})
			path = append(path, matrix{1, 0, 0, 1, oneLeft, oneBottom})
		case "m", "l":
			x, y := cur.ctm.apply(number(1), number(0))
			path = append(path, matrix{1, 0, 0, 1, x, y})
		case "c":
			x, y := cur.ctm.apply(number(1), number(0))
			path = append(path, matrix{1, 0, 0, 1, x, y})
		case "h":
		case "S", "s", "f", "F", "f*", "B", "B*", "b", "b*", "n":
			painted := map[string]string{
				"S": "stroke", "s": "stroke",
				"f": "fill", "F": "fill", "f*": "fill",
				"B": "fillstroke", "B*": "fillstroke",
				"b": "fillstroke", "b*": "fillstroke",
				"n": "",
			}[operator]
			if pending == "clip" {
				painted = "clip"
			}
			if painted != "" {
				for _, rect := range rects {
					rect.Painted = painted
					page.Rects = append(page.Rects, rect)
				}
				if len(rects) == 0 && len(path) >= 2 && painted == "stroke" {
					page.Segments = append(page.Segments, Segment{
						FromLeft: path[0][4], FromTop: flip(path[0][5]),
						ToLeft: path[len(path)-1][4], ToTop: flip(path[len(path)-1][5]),
						Stroke: cur.stroke, LineWidth: cur.lineWidth, Dash: cur.dash,
					})
				}
			}
			rects = nil
			path = nil
			pending = ""
		case "W", "W*":
			pending = "clip"
		case "BT":
			textMatrix, lineMatrix = identity, identity
		case "ET":
		case "Tf":
			size = number(0)
			if len(stack) >= 2 {
				if name, ok := stack[len(stack)-2].(Name); ok {
					fontName = string(name)
					loaded, err := file.font(resources, fontName, fonts)
					if err != nil {
						return err
					}
					font = loaded
				}
			}
		case "Td":
			lineMatrix = matrix{1, 0, 0, 1, number(1), number(0)}.multiply(lineMatrix)
			textMatrix = lineMatrix
		case "TD":
			leading = -number(0)
			lineMatrix = matrix{1, 0, 0, 1, number(1), number(0)}.multiply(lineMatrix)
			textMatrix = lineMatrix
		case "Tm":
			lineMatrix = matrix{number(5), number(4), number(3), number(2), number(1), number(0)}
			textMatrix = lineMatrix
		case "T*":
			lineMatrix = matrix{1, 0, 0, 1, 0, -leading}.multiply(lineMatrix)
			textMatrix = lineMatrix
		case "TL":
			leading = number(0)
		case "Tc":
			charSpace = number(0)
		case "Tw":
			// Word spacing acts on the single-byte code 32, which an
			// Identity-H font has none of, so it is read and discarded.
		case "Tz":
			horizontal = number(0) / 100
		case "Tj":
			if len(stack) >= 1 {
				if text, ok := stack[len(stack)-1].(String); ok {
					show(text)
				}
			}
		case "TJ":
			if len(stack) >= 1 {
				if array, ok := stack[len(stack)-1].(Array); ok {
					for _, entry := range array {
						switch typed := entry.(type) {
						case String:
							show(typed)
						case float64:
							textMatrix = matrix{
								1, 0, 0, 1, -typed / 1000 * size * horizontal, 0,
							}.multiply(textMatrix)
						}
					}
				}
			}
		case "Do":
			if len(stack) >= 1 {
				if name, ok := stack[len(stack)-1].(Name); ok {
					left, top := cur.ctm.apply(0, 1)
					page.Draws = append(page.Draws, Draw{
						Name: string(name),
						Left: left, Top: flip(top),
						Width: cur.ctm[0], Height: cur.ctm[3],
					})
				}
			}
		}
		stack = stack[:0]
	}
}

// annotations reads a page's link annotations, resolving
// a destination's page reference to an index in the document.
func (file *File) annotations(page Dict, all []Dict) []Annot {
	array, ok := file.Get(page, "Annots").(Array)
	if !ok {
		return nil
	}
	height := 0.0
	if box, ok := file.Get(page, "MediaBox").(Array); ok && len(box) == 4 {
		height, _ = file.Resolve(box[3]).(float64)
	}

	var out []Annot
	for _, entry := range array {
		dict, ok := file.Resolve(entry).(Dict)
		if !ok {
			continue
		}
		annot := Annot{DestPage: -1}
		if name, ok := file.Get(dict, "Subtype").(Name); ok {
			annot.Subtype = string(name)
		}
		if rect, ok := file.Get(dict, "Rect").(Array); ok && len(rect) == 4 {
			left, _ := file.Resolve(rect[0]).(float64)
			bottom, _ := file.Resolve(rect[1]).(float64)
			right, _ := file.Resolve(rect[2]).(float64)
			top, _ := file.Resolve(rect[3]).(float64)
			annot.Left, annot.Width = left, right-left
			annot.Top, annot.Height = height-top, top-bottom
		}
		if contents, ok := file.Get(dict, "Contents").(String); ok {
			annot.Contents = decodeTextString(contents)
		}
		if action, ok := file.Get(dict, "A").(Dict); ok {
			if uri, ok := file.Get(action, "URI").(String); ok {
				annot.URI = string(uri)
			}
		}
		if dest, ok := file.Get(dict, "Dest").(Array); ok {
			annot.DestPage, annot.DestLeft, annot.DestTop = file.destination(dest, all)
		}
		out = append(out, annot)
	}
	return out
}

// destination reads an explicit destination array.
func (file *File) destination(dest Array, all []Dict) (int, float64, float64) {
	if len(dest) == 0 {
		return -1, 0, 0
	}
	target, _ := dest[0].(Ref)
	index := -1
	height := 0.0
	for at, page := range all {
		if file.pageIs(page, target) {
			index = at
			if box, ok := file.Get(page, "MediaBox").(Array); ok && len(box) == 4 {
				height, _ = file.Resolve(box[3]).(float64)
			}
			break
		}
	}
	left, top := 0.0, 0.0
	if len(dest) >= 4 {
		left, _ = file.Resolve(dest[2]).(float64)
		if value, ok := file.Resolve(dest[3]).(float64); ok {
			top = height - value
		}
	}
	return index, left, top
}

// pageIs reports whether a reference names a page dictionary.
//
// Two page dictionaries are compared by their Contents entries, since
// each page here has a content stream of its own and the caller does not
// hold the pages' own object numbers.
func (file *File) pageIs(page Dict, ref Ref) bool {
	resolved, ok := file.Resolve(ref).(Dict)
	if !ok {
		return false
	}
	one, okOne := page["Contents"].(Ref)
	two, okTwo := resolved["Contents"].(Ref)
	return okOne && okTwo && one == two
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

// decodeTextString reads a PDF text string, which is UTF-16BE when it
// opens with a byte order mark and PDFDocEncoding otherwise.
func decodeTextString(raw String) string {
	if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF {
		units := make([]uint16, 0, (len(raw)-2)/2)
		for index := 2; index+1 < len(raw); index += 2 {
			units = append(units, uint16(raw[index])<<8|uint16(raw[index+1]))
		}
		return string(utf16.Decode(units))
	}
	return string(raw)
}

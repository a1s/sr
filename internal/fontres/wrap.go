package fontres

import (
	"os"
	"strings"

	"github.com/a1s/sr/internal/geom"
)

// Wrap breaks text into lines that fit a box of the given width.
//
// Breaking is greedy and by word: the longest run of words that fits
// goes on the line. A single word wider than the box is broken by character,
// since the alternative is a line that overflows silently.
// Explicit newlines in the text always break.
//
// Trailing spaces at a break are dropped, so a line's measured width
// is the width of what is drawn.
func Wrap(face *Face, text string, width float64) []string {
	var out []string
	for _, paragraph := range strings.Split(text, "\n") {
		out = append(out, wrapParagraph(face, paragraph, width)...)
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

func wrapParagraph(face *Face, text string, width float64) []string {
	if text == "" {
		return []string{""}
	}
	if width <= 0 {
		// With no room to measure against, the text is one line;
		// whether it fits is judged on the box, not here.
		return []string{text}
	}

	words := splitKeepingSpaces(text)
	var lines []string
	var line strings.Builder
	lineWidth := 0.0

	flush := func() {
		lines = append(lines, strings.TrimRight(line.String(), " \t"))
		line.Reset()
		lineWidth = 0
	}

	for _, word := range words {
		wWidth := face.Width(word)
		if line.Len() > 0 && !geom.Fits(geom.Round(lineWidth+wWidth), width) {
			flush()
			if strings.TrimSpace(word) == "" {
				// Whitespace that fell at a break is dropped
				// rather than opening the next line.
				continue
			}
		}
		if line.Len() == 0 && !geom.Fits(wWidth, width) {
			// One word wider than the box: break it by character.
			parts := breakWord(face, word, width)
			for index, part := range parts {
				if index == len(parts)-1 {
					line.WriteString(part)
					lineWidth = face.Width(part)
					break
				}
				lines = append(lines, part)
			}
			continue
		}
		line.WriteString(word)
		lineWidth = geom.Round(lineWidth + wWidth)
	}
	if line.Len() > 0 || len(lines) == 0 {
		flush()
	}
	return lines
}

// splitKeepingSpaces breaks text into words with their trailing whitespace,
// so that a break point falls between words and the space that separated
// them is dropped rather than measured.
func splitKeepingSpaces(text string) []string {
	var out []string
	var cur strings.Builder
	inSpace := false
	for _, char := range text {
		isSpace := char == ' ' || char == '\t'
		if isSpace {
			cur.WriteRune(char)
			inSpace = true
			continue
		}
		if inSpace {
			out = append(out, cur.String())
			cur.Reset()
			inSpace = false
		}
		cur.WriteRune(char)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// breakWord splits one over-long word at character boundaries.
func breakWord(face *Face, word string, width float64) []string {
	var out []string
	var cur strings.Builder
	curWidth := 0.0
	for _, char := range word {
		advance := face.Advance(char)
		if cur.Len() > 0 && !geom.Fits(geom.Round(curWidth+advance), width) {
			out = append(out, cur.String())
			cur.Reset()
			curWidth = 0
		}
		cur.WriteRune(char)
		curWidth = geom.Round(curWidth + advance)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// LineWidth measures a wrapped line, which is what alignment positions.
func LineWidth(face *Face, line string) float64 { return face.Width(line) }

// TextHeight is the height a run of lines occupies.
func TextHeight(face *Face, lines int) float64 {
	if lines <= 0 {
		return 0
	}
	return geom.Round(float64(lines) * face.Leading())
}

// readFile is os.ReadFile, wrapped so tests can reach it without importing os.
func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

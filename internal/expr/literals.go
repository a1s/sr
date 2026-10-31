package expr

import (
	"strings"
	"unicode"
)

// What an expression is told where it reads as Python would join
// two literals or format one. Starlark's parser sees an identifier,
// or one literal, followed by another literal, and says only that
// it did not want a literal there. These say what the template meant,
// and what to write instead. See doc/expressions.md#literals.
const (
	noFStrings       = "there are no f-strings; use the format() builtin"
	adjacentLiterals = "two literals in a row are not joined; write + between them"
)

// literalPrefixes are the prefixes Python reads a string or bytes literal
// with, in lower case. Starlark has only r, b and rb, and the rest are
// here so that the literal they open is skipped rather than read as code.
var literalPrefixes = map[string]bool{
	"r": true, "u": true, "b": true, "br": true, "rb": true,
	"f": true, "fr": true, "rf": true,
}

// misreadLiteral finds the first f-string in an expression,
// or the first literal that directly follows another.
//
// It is a scan rather than a parse: it skips what is inside literals
// and comments and reads nothing else, so it is only asked about an
// expression the parser has already refused. A literal it cannot find
// the end of stops it. The offset counts characters, as a diagnostic does.
func misreadLiteral(source string) (message string, offset int, found bool) {
	text := []rune(source)
	// afterLiteral is whether the last token was a literal, with nothing
	// between it and here but white space, comments, and line joins.
	afterLiteral := false
	for index := 0; index < len(text); {
		char := text[index]
		switch {
		case char == '#':
			for index < len(text) && text[index] != '\n' && text[index] != '\r' {
				index++
			}
		case unicode.IsSpace(char):
			index++
		case char == '\\' && index+1 < len(text) &&
			(text[index+1] == '\n' || text[index+1] == '\r'):
			index++
		case char == '\'' || char == '"':
			if afterLiteral {
				return adjacentLiterals, index, true
			}
			end, closed := skipLiteral(text, index)
			if !closed {
				return "", 0, false
			}
			index, afterLiteral = end, true
		case char == '_' || unicode.IsLetter(char):
			start := index
			for index < len(text) && isWordPart(text[index]) {
				index++
			}
			word := strings.ToLower(string(text[start:index]))
			opensLiteral := index < len(text) &&
				(text[index] == '\'' || text[index] == '"') && literalPrefixes[word]
			if !opensLiteral {
				afterLiteral = false
				continue
			}
			if strings.Contains(word, "f") {
				return noFStrings, start, true
			}
			if afterLiteral {
				return adjacentLiterals, start, true
			}
			end, closed := skipLiteral(text, index)
			if !closed {
				return "", 0, false
			}
			index, afterLiteral = end, true
		case unicode.IsDigit(char):
			// A number runs on through its letters and points,
			// as in 0x1F and 1.5e3, so none of them starts a word.
			for index < len(text) && (isWordPart(text[index]) || text[index] == '.') {
				index++
			}
			afterLiteral = false
		default:
			index++
			afterLiteral = false
		}
	}
	return "", 0, false
}

// skipLiteral returns where the literal whose opening quote is at start
// ends, and whether it ends at all.
//
// A backslash takes the character after it with it, in a raw literal
// as in any other, which is how both languages find a literal's end.
// That character may be the "\r\n" that ends a line, which is one.
// Any other line break ends a literal that is not triple-quoted
// unfinished.
func skipLiteral(text []rune, start int) (int, bool) {
	quote := text[start]
	width := 1
	if start+2 < len(text) && text[start+1] == quote && text[start+2] == quote {
		width = 3
	}
	for index := start + width; index < len(text); {
		switch char := text[index]; {
		case char == '\\' && index+2 < len(text) &&
			text[index+1] == '\r' && text[index+2] == '\n':
			index += 3
		case char == '\\':
			index += 2
		case char == quote && closes(text, index, width):
			return index + width, true
		case width == 1 && (char == '\n' || char == '\r'):
			return 0, false
		default:
			index++
		}
	}
	return 0, false
}

// closes reports whether width quotes in a row start at index.
func closes(text []rune, index, width int) bool {
	if index+width > len(text) {
		return false
	}
	for _, char := range text[index : index+width] {
		if char != text[index] {
			return false
		}
	}
	return true
}

func isWordPart(char rune) bool {
	return char == '_' || unicode.IsLetter(char) || unicode.IsDigit(char)
}

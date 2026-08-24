package pdfscan

import (
	"fmt"
	"strconv"
	"strings"
)

// lexer reads PDF syntax out of a byte slice.
type lexer struct {
	raw []byte
	at  int
}

func newLexer(raw []byte) *lexer { return &lexer{raw: raw} }

func isWhite(char byte) bool {
	switch char {
	case 0, '\t', '\n', '\f', '\r', ' ':
		return true
	}
	return false
}

func isDelimiter(char byte) bool {
	switch char {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func (lex *lexer) advance(count int) { lex.at += count }

// space skips whitespace and comments.
func (lex *lexer) space() {
	for lex.at < len(lex.raw) {
		char := lex.raw[lex.at]
		if isWhite(char) {
			lex.at++
			continue
		}
		if char == '%' {
			for lex.at < len(lex.raw) && lex.raw[lex.at] != '\n' && lex.raw[lex.at] != '\r' {
				lex.at++
			}
			continue
		}
		return
	}
}

func (lex *lexer) looking(text string) bool {
	return strings.HasPrefix(string(lex.raw[min(lex.at, len(lex.raw)):]), text)
}

// keyword reads a run of regular characters.
func (lex *lexer) keyword() string {
	lex.space()
	start := lex.at
	for lex.at < len(lex.raw) &&
		!isWhite(lex.raw[lex.at]) && !isDelimiter(lex.raw[lex.at]) {
		lex.at++
	}
	return string(lex.raw[start:lex.at])
}

func (lex *lexer) integer() (int, error) {
	value, err := lex.object()
	if err != nil {
		return 0, err
	}
	number, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("wanted a number, read %v", value)
	}
	return int(number), nil
}

// object reads one PDF object.
//
// An integer followed by another integer and an R is an indirect
// reference, which is the one place PDF syntax needs lookahead.
func (lex *lexer) object() (Object, error) {
	lex.space()
	if lex.at >= len(lex.raw) {
		return nil, fmt.Errorf("the input ends where an object was expected")
	}
	switch char := lex.raw[lex.at]; {
	case char == '/':
		lex.at++
		return Name(lex.name()), nil
	case char == '(':
		return lex.literalString()
	case char == '<' && lex.looking("<<"):
		return lex.dictionary()
	case char == '<':
		return lex.hexString()
	case char == '[':
		lex.at++
		var out Array
		for {
			lex.space()
			if lex.at >= len(lex.raw) {
				return nil, fmt.Errorf("an array is not closed")
			}
			if lex.raw[lex.at] == ']' {
				lex.at++
				return out, nil
			}
			entry, err := lex.object()
			if err != nil {
				return nil, err
			}
			out = append(out, entry)
		}
	case char == '+' || char == '-' || char == '.' || (char >= '0' && char <= '9'):
		return lex.number()
	}
	switch word := lex.keyword(); word {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	case "":
		return nil, fmt.Errorf("a delimiter %q where an object was expected",
			string(lex.raw[lex.at]))
	default:
		return Name(word), nil
	}
}

// name reads a name's characters, undoing #xx escapes.
func (lex *lexer) name() string {
	var out strings.Builder
	for lex.at < len(lex.raw) {
		char := lex.raw[lex.at]
		if isWhite(char) || isDelimiter(char) {
			break
		}
		if char == '#' && lex.at+2 < len(lex.raw) {
			value, err := strconv.ParseUint(string(lex.raw[lex.at+1:lex.at+3]), 16, 8)
			if err == nil {
				out.WriteByte(byte(value))
				lex.at += 3
				continue
			}
		}
		out.WriteByte(char)
		lex.at++
	}
	return out.String()
}

func (lex *lexer) number() (Object, error) {
	start := lex.at
	for lex.at < len(lex.raw) {
		char := lex.raw[lex.at]
		if (char >= '0' && char <= '9') || char == '+' || char == '-' || char == '.' {
			lex.at++
			continue
		}
		break
	}
	text := string(lex.raw[start:lex.at])
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		// A number like "--3" or "4." appears in the wild; take what
		// parses rather than failing the whole file.
		value = 0
	}
	// Look ahead for the "<gen> R" that makes this a reference.
	if value == float64(int(value)) && value >= 0 {
		save := lex.at
		probe := &lexer{raw: lex.raw, at: lex.at}
		probe.space()
		digits := probe.at
		for probe.at < len(probe.raw) && probe.raw[probe.at] >= '0' && probe.raw[probe.at] <= '9' {
			probe.at++
		}
		if probe.at > digits {
			probe.space()
			if probe.looking("R") &&
				(probe.at+1 >= len(probe.raw) ||
					isWhite(probe.raw[probe.at+1]) || isDelimiter(probe.raw[probe.at+1])) {
				probe.at++
				lex.at = probe.at
				return Ref{Num: int(value)}, nil
			}
		}
		lex.at = save
	}
	return value, nil
}

func (lex *lexer) dictionary() (Object, error) {
	lex.at += 2
	out := Dict{}
	for {
		lex.space()
		if lex.looking(">>") {
			lex.at += 2
			return out, nil
		}
		if lex.at >= len(lex.raw) {
			return nil, fmt.Errorf("a dictionary is not closed")
		}
		if lex.raw[lex.at] != '/' {
			return nil, fmt.Errorf("a dictionary key begins %q", string(lex.raw[lex.at]))
		}
		lex.at++
		key := Name(lex.name())
		value, err := lex.object()
		if err != nil {
			return nil, err
		}
		out[key] = value
	}
}

func (lex *lexer) literalString() (Object, error) {
	lex.at++
	var out []byte
	depth := 1
	for lex.at < len(lex.raw) {
		char := lex.raw[lex.at]
		lex.at++
		switch char {
		case '\\':
			if lex.at >= len(lex.raw) {
				break
			}
			escape := lex.raw[lex.at]
			lex.at++
			switch escape {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '\n':
			case '\r':
				if lex.at < len(lex.raw) && lex.raw[lex.at] == '\n' {
					lex.at++
				}
			default:
				if escape >= '0' && escape <= '7' {
					value := int(escape - '0')
					for count := 0; count < 2 && lex.at < len(lex.raw); count++ {
						digit := lex.raw[lex.at]
						if digit < '0' || digit > '7' {
							break
						}
						value = value*8 + int(digit-'0')
						lex.at++
					}
					out = append(out, byte(value))
					continue
				}
				out = append(out, escape)
			}
		case '(':
			depth++
			out = append(out, char)
		case ')':
			depth--
			if depth == 0 {
				return String(out), nil
			}
			out = append(out, char)
		default:
			out = append(out, char)
		}
	}
	return nil, fmt.Errorf("a string is not closed")
}

func (lex *lexer) hexString() (Object, error) {
	lex.at++
	var digits []byte
	for lex.at < len(lex.raw) {
		char := lex.raw[lex.at]
		lex.at++
		if char == '>' {
			if len(digits)%2 == 1 {
				digits = append(digits, '0')
			}
			out := make([]byte, len(digits)/2)
			for index := 0; index < len(digits); index += 2 {
				value, err := strconv.ParseUint(string(digits[index:index+2]), 16, 8)
				if err != nil {
					return nil, err
				}
				out[index/2] = byte(value)
			}
			return String(out), nil
		}
		if isWhite(char) {
			continue
		}
		digits = append(digits, char)
	}
	return nil, fmt.Errorf("a hex string is not closed")
}

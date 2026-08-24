package sfnt

import (
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

// decodeName turns a name record's bytes into a string.
func decodeName(platform, encoding int, raw []byte) string {
	if platform == 1 {
		// Macintosh; the Roman encoding agrees with ASCII
		// over the range a font name uses.
		var out strings.Builder
		for _, char := range raw {
			out.WriteRune(rune(char))
		}
		return strings.TrimSpace(out.String())
	}
	// Platforms 0 and 3 are UTF-16BE.
	units := make([]uint16, 0, len(raw)/2)
	for index := 0; index+1 < len(raw); index += 2 {
		units = append(units, binary.BigEndian.Uint16(raw[index:index+2]))
	}
	_ = encoding
	return strings.TrimSpace(string(utf16.Decode(units)))
}

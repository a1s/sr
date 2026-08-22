package build

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

// decodeBlob turns a data node's content into bytes: first
// decode base64 when the node declares it, then decompression.
func decodeBlob(content, encoding, compress string) ([]byte, error) {
	raw := []byte(content)
	switch encoding {
	case "":
	case "base64":
		clean := strings.Map(func(char rune) rune {
			if char == '\n' || char == '\r' || char == '\t' || char == ' ' {
				return -1
			}
			return char
		}, content)
		decoded, err := base64.StdEncoding.DecodeString(clean)
		if err != nil {
			return nil, fmt.Errorf("base64: %w", err)
		}
		raw = decoded
	default:
		return nil, fmt.Errorf("unknown encoding %q", encoding)
	}

	switch compress {
	case "":
		return raw, nil
	case "zlib":
		reader, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("zlib: %w", err)
		}
		defer reader.Close() // nolint:errcheck
		return io.ReadAll(reader)
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		defer reader.Close() // nolint:errcheck
		return io.ReadAll(reader)
	}
	return nil, fmt.Errorf("unknown compression %q", compress)
}

package tmpl

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	gotime "time"

	"github.com/a1s/sr/internal/expr"
	"github.com/shopspring/decimal"
	startime "go.starlark.net/lib/time"
	"go.starlark.net/starlark"
)

// DateLayout is the accepted text form for a date parameter or member
// when the node gives no format.
const DateLayout = "2006-01-02"

// ParseScalar reads text as a value of this type.
//
// It reads every type but object and list, which are JSON and which
// the data package reads. A `default` is held to it when the template
// loads, and a parameter or a record member given as text when the report
// is built, so the two cannot disagree about what a type accepts.
func (kind ValueType) ParseScalar(text, format string) (starlark.Value, error) {
	switch kind {
	case TypeString:
		return starlark.String(text), nil

	case TypeInt:
		text = strings.TrimSpace(text)
		num, ok := new(big.Int).SetString(text, 10)
		if !ok {
			return nil, fmt.Errorf("%q is not an integer", text)
		}
		return starlark.MakeBigInt(num), nil

	case TypeDecimal:
		text = strings.TrimSpace(text)
		dec, err := decimal.NewFromString(text)
		if err != nil {
			return nil, fmt.Errorf("%q is not a decimal", text)
		}
		return expr.NewDecimal(dec), nil

	case TypeFloat:
		text = strings.TrimSpace(text)
		num, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", text)
		}
		return starlark.Float(num), nil

	case TypeBool:
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "true", "1":
			return starlark.True, nil
		case "false", "0":
			return starlark.False, nil
		}
		return nil, fmt.Errorf("%q is not a boolean", text)

	case TypeDate, TypeDatetime:
		when, err := kind.parseTime(text, format)
		if err != nil {
			return nil, err
		}
		return startime.Time(when), nil
	}
	return nil, fmt.Errorf("a %s is JSON, not a scalar", kind)
}

// parseTime reads a timestamp.
//
// A date yields a time value with zero time of day.
func (kind ValueType) parseTime(text, format string) (gotime.Time, error) {
	layouts := []string{gotime.RFC3339}
	if kind == TypeDate {
		layouts = []string{DateLayout, gotime.RFC3339}
	}
	if format != "" {
		layouts = []string{format}
	}
	var lastErr error
	for _, layout := range layouts {
		when, err := gotime.Parse(layout, text)
		if err == nil {
			if kind == TypeDate {
				when = gotime.Date(when.Year(), when.Month(),
					when.Day(), 0, 0, 0, 0, when.Location())
			}
			return when, nil
		}
		lastErr = err
	}
	return gotime.Time{}, fmt.Errorf("%q is not a %s: %v", text, kind, lastErr)
}

// checkDefault reports a `default` that does not read as its declared type.
//
// An object or a list is JSON, which the data package turns into values
// when the report is built. Here it is enough that the text decodes
// as one, read as the data package reads it.
func checkDefault(text string, kind ValueType, format string) error {
	switch kind {
	case TypeObject:
		var obj map[string]any
		if json.NewDecoder(strings.NewReader(text)).Decode(&obj) != nil {
			return fmt.Errorf("%q is not a JSON object", text)
		}
		return nil
	case TypeList:
		var list []any
		if json.NewDecoder(strings.NewReader(text)).Decode(&list) != nil {
			return fmt.Errorf("%q is not a JSON array", text)
		}
		return nil
	}
	_, err := kind.ParseScalar(text, format)
	return err
}

// DecodeBase64 reads the bytes a `content` child spells in base64.
//
// A long blob is a multi-line string, so it arrives with the line breaks
// and the indentation the template laid it out with. Those are not part
// of it and are dropped; anything else that is not base64 is an error.
func DecodeBase64(content string) ([]byte, error) {
	clean := strings.Map(func(char rune) rune {
		if char == '\n' || char == '\r' || char == '\t' || char == ' ' {
			return -1
		}
		return char
	}, content)
	return base64.StdEncoding.DecodeString(clean)
}

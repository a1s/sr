package expr

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	startime "go.starlark.net/lib/time"
	"go.starlark.net/starlark"
)

// Format applies a %-style specification to the arguments.
//
// This is the engine's formatter, reached from a field's or barcode's
// `format=` property and from the `format` builtin. Unlike Starlark's own `%`
// operator it supports the full set of flags, width, and precision, which is
// why numeric presentation belongs here rather than inside an expression.
func Format(spec string, args []starlark.Value) (string, error) {
	var out strings.Builder
	next := 0
	take := func() (starlark.Value, error) {
		if next >= len(args) {
			return nil, fmt.Errorf("not enough arguments for format %q: got %d", spec, len(args))
		}
		value := args[next]
		next++
		return value, nil
	}

	runes := []rune(spec)
	for index := 0; index < len(runes); index++ {
		if runes[index] != '%' {
			out.WriteRune(runes[index])
			continue
		}
		start := index
		index++
		if index < len(runes) && runes[index] == '%' {
			out.WriteByte('%')
			continue
		}
		var flags string
		for index < len(runes) && strings.ContainsRune("-+# 0", runes[index]) {
			flags += string(runes[index])
			index++
		}
		width := ""
		for index < len(runes) && runes[index] >= '0' && runes[index] <= '9' {
			width += string(runes[index])
			index++
		}
		prec := ""
		hasPrec := false
		if index < len(runes) && runes[index] == '.' {
			hasPrec = true
			index++
			for index < len(runes) && runes[index] >= '0' && runes[index] <= '9' {
				prec += string(runes[index])
				index++
			}
			if prec == "" {
				prec = "0"
			}
		}
		if index >= len(runes) {
			return "", fmt.Errorf("truncated conversion at %q", string(runes[start:]))
		}
		verb := runes[index]
		arg, err := take()
		if err != nil {
			return "", err
		}
		text, err := convert(verb, flags, width, prec, hasPrec, arg)
		if err != nil {
			return "", fmt.Errorf("format %q: %w", spec, err)
		}
		out.WriteString(text)
	}
	if next != len(args) {
		return "", fmt.Errorf("too many arguments for format %q: want %d, got %d", spec, next, len(args))
	}
	return out.String(), nil
}

func convert(verb rune, flags, width, prec string, hasPrec bool, arg starlark.Value) (string, error) {
	goSpec := func(char rune) string {
		spec := "%" + flags + width
		if hasPrec {
			spec += "." + prec
		}
		return spec + string(char)
	}

	switch verb {
	case 's':
		return fmt.Sprintf(goSpec('s'), Str(arg)), nil
	case 'q':
		return fmt.Sprintf(goSpec('q'), Str(arg)), nil
	case 'c':
		num, err := asBigInt(arg)
		if err != nil {
			return "", err
		}
		if !num.IsInt64() || num.Int64() < 0 || num.Int64() > 0x10FFFF {
			return "", fmt.Errorf("%%c: %s is not a code point", num)
		}
		return fmt.Sprintf("%"+flags+width+"c", rune(num.Int64())), nil
	case 'd', 'i', 'o', 'x', 'X', 'b':
		num, err := asBigInt(arg)
		if err != nil {
			return "", err
		}
		char := verb
		if char == 'i' {
			char = 'd'
		}
		return fmt.Sprintf(goSpec(char), num), nil
	case 'f', 'F', 'e', 'E', 'g', 'G':
		if dec, ok := arg.(Decimal); ok && (verb == 'f' || verb == 'F') {
			return formatExactDecimal(dec, flags, width, prec, hasPrec), nil
		}
		num, err := asFloat(arg)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(goSpec(verb), num), nil
	}
	return "", fmt.Errorf("unknown conversion %%%c", verb)
}

// formatExactDecimal renders a decimal under %f without going through a float,
// rounding half away from zero at the requested precision.
func formatExactDecimal(dec Decimal, flags, width, prec string, hasPrec bool) string {
	places := 6
	if hasPrec {
		places, _ = strconv.Atoi(prec)
	}
	rounded := dec.Val.Round(int32(places))
	body := rounded.StringFixed(int32(places))
	negative := strings.HasPrefix(body, "-")
	if negative {
		body = body[1:]
	}
	sign := ""
	switch {
	case negative:
		sign = "-"
	case strings.Contains(flags, "+"):
		sign = "+"
	case strings.Contains(flags, " "):
		sign = " "
	}
	total := 0
	if width != "" {
		total, _ = strconv.Atoi(width)
	}
	pad := total - len(sign) - len(body)
	if pad <= 0 {
		return sign + body
	}
	switch {
	case strings.Contains(flags, "-"):
		return sign + body + strings.Repeat(" ", pad)
	case strings.Contains(flags, "0"):
		return sign + strings.Repeat("0", pad) + body
	default:
		return strings.Repeat(" ", pad) + sign + body
	}
}

func asBigInt(value starlark.Value) (*big.Int, error) {
	switch typed := value.(type) {
	case starlark.Int:
		return typed.BigInt(), nil
	case Decimal:
		return typed.Val.Round(0).BigInt(), nil
	case starlark.Float:
		num := float64(typed)
		if math.IsInf(num, 0) || math.IsNaN(num) {
			return nil, fmt.Errorf("cannot convert %v to an integer", num)
		}
		bf := new(big.Float).SetFloat64(math.Trunc(num))
		whole, _ := bf.Int(nil)
		return whole, nil
	case starlark.Bool:
		if typed {
			return big.NewInt(1), nil
		}
		return big.NewInt(0), nil
	}
	return nil, fmt.Errorf("cannot format %s as an integer", value.Type())
}

func asFloat(value starlark.Value) (float64, error) {
	switch typed := value.(type) {
	case starlark.Float:
		return float64(typed), nil
	case starlark.Int:
		num, _ := starlark.AsFloat(typed)
		return num, nil
	case Decimal:
		return typed.Float(), nil
	}
	return 0, fmt.Errorf("cannot format %s as a number", value.Type())
}

// Str renders a value the way %s does: a string is itself, everything else is
// its Starlark string form.
func Str(value starlark.Value) string {
	switch typed := value.(type) {
	case starlark.String:
		return string(typed)
	case starlark.NoneType:
		return "None"
	case startime.Time:
		return typed.String()
	}
	return value.String()
}

// FormatArgs turns an expression result into the argument list a format
// specification consumes. A tuple spreads positionally; anything else is a
// single argument.
func FormatArgs(value starlark.Value) []starlark.Value {
	if tuple, ok := value.(starlark.Tuple); ok {
		return []starlark.Value(tuple)
	}
	return []starlark.Value{value}
}

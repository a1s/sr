package expr

import (
	"fmt"
	"strconv"
	"strings"
	gotime "time"

	starmath "go.starlark.net/lib/math"
	startime "go.starlark.net/lib/time"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// PredefinedNames are the names the engine supplies to every expression,
// per doc/expressions.md#predefined-variables. Per-group names --
// <group>_COUNT and <group>_PAGE_NUMBER -- are added by the template.
var PredefinedNames = []string{
	"THIS",
	"ITEM_NUMBER",
	"DATA_COUNT",
	"REPORT_COUNT",
	"PAGE_COUNT",
	"COLUMN_COUNT",
	"PAGE_NUMBER",
	"COLUMN_NUMBER",
	"VERTICAL_POSITION",
	"VERTICAL_SPACE",
	"BUILD_TIME",
	"FINAL",
}

// FinalName is the namespace through which end-of-scope values are read.
const FinalName = "FINAL"

// PositionNames are the two predefined names whose value depends on where in
// the frame a band lands. A band whose expressions read either is not cached.
var PositionNames = []string{"VERTICAL_POSITION", "VERTICAL_SPACE"}

// timeModule is Starlark's time module with `now` removed:
// expressions are hermetic, and BUILD_TIME is the only clock.
var timeModule = func() *starlarkstruct.Module {
	members := make(starlark.StringDict, len(startime.Module.Members))
	for name, value := range startime.Module.Members {
		if name == "now" {
			continue
		}
		members[name] = value
	}
	return &starlarkstruct.Module{Name: "time", Members: members}
}()

// Predeclared is the fixed part of every expression's environment: the modules
// and the engine's own builtins. Names here resolve ahead of parameters,
// variables, and record fields, which is why a template may not reuse one.
var Predeclared = starlark.StringDict{
	"math":     starmath.Module,
	"time":     timeModule,
	"strftime": starlark.NewBuiltin("strftime", builtinStrftime),
	"format":   starlark.NewBuiltin("format", builtinFormat),
	"decimal":  starlark.NewBuiltin("decimal", builtinDecimal),
	"quantize": starlark.NewBuiltin("quantize", builtinQuantize),
	// float and int shadow the universals so they also accept a decimal.
	"float": starlark.NewBuiltin("float", builtinFloat),
	"int":   starlark.NewBuiltin("int", builtinInt),
}

// blockedUniversals are Starlark universals the dialect does not offer.
// A template has nowhere to print to, so naming `print` is a compile error
// rather than a call that goes nowhere.
var blockedUniversals = map[string]string{
	"print": "print is not available: a template has nowhere to print to",
}

// IsPredeclared reports whether a name is supplied by the fixed environment.
func IsPredeclared(name string) bool {
	_, ok := Predeclared[name]
	return ok
}

// IsUniversal reports whether a name is a Starlark builtin the dialect keeps.
func IsUniversal(name string) bool {
	if _, blocked := blockedUniversals[name]; blocked {
		return false
	}
	_, ok := starlark.Universe[name]
	return ok
}

// IsReserved reports whether a name is unavailable for a parameter, variable,
// or group because resolution puts something else first.
func IsReserved(name string) bool {
	if IsPredeclared(name) || IsUniversal(name) {
		return true
	}
	for _, predefined := range PredefinedNames {
		if predefined == name {
			return true
		}
	}
	_, blocked := blockedUniversals[name]
	return blocked
}

func builtinFormat(
	thread *starlark.Thread,
	fn *starlark.Builtin,
	args starlark.Tuple,
	kwargs []starlark.Tuple,
) (starlark.Value, error) {
	if len(kwargs) > 0 {
		return nil, fmt.Errorf("format: unexpected keyword argument")
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("format: missing format specification")
	}
	spec, ok := starlark.AsString(args[0])
	if !ok {
		return nil, fmt.Errorf("format: specification must be a string, got %s", args[0].Type())
	}
	text, err := Format(spec, args[1:])
	if err != nil {
		return nil, err
	}
	return starlark.String(text), nil
}

func builtinDecimal(
	thread *starlark.Thread,
	fn *starlark.Builtin,
	args starlark.Tuple,
	kwargs []starlark.Tuple,
) (starlark.Value, error) {
	var value starlark.Value
	if err := starlark.UnpackPositionalArgs("decimal", args, kwargs, 1, &value); err != nil {
		return nil, err
	}
	switch typed := value.(type) {
	case Decimal:
		return typed, nil
	case starlark.String:
		return ParseDecimal(string(typed))
	case starlark.Int:
		return ParseDecimal(typed.String())
	case starlark.Float:
		return nil, fmt.Errorf("decimal(): a float is not exact; write decimal(str(f)) to accept the conversion")
	}
	return nil, fmt.Errorf("decimal(): cannot convert %s", value.Type())
}

func builtinQuantize(
	thread *starlark.Thread,
	fn *starlark.Builtin,
	args starlark.Tuple,
	kwargs []starlark.Tuple,
) (starlark.Value, error) {
	var value starlark.Value
	var places int
	err := starlark.UnpackPositionalArgs("quantize", args, kwargs, 2, &value, &places)
	if err != nil {
		return nil, err
	}
	dec, ok := value.(Decimal)
	if !ok {
		return nil, fmt.Errorf("quantize(): want a decimal, got %s", value.Type())
	}
	return dec.Quantize(int32(places)), nil
}

func builtinFloat(
	thread *starlark.Thread,
	fn *starlark.Builtin,
	args starlark.Tuple,
	kwargs []starlark.Tuple,
) (starlark.Value, error) {
	if len(args) == 1 {
		if dec, ok := args[0].(Decimal); ok {
			return starlark.Float(dec.Float()), nil
		}
	}
	return starlark.Call(thread, starlark.Universe["float"], args, kwargs)
}

func builtinInt(
	thread *starlark.Thread,
	fn *starlark.Builtin,
	args starlark.Tuple,
	kwargs []starlark.Tuple,
) (starlark.Value, error) {
	if len(args) == 1 {
		if dec, ok := args[0].(Decimal); ok {
			return starlark.MakeBigInt(dec.BigInt()), nil
		}
	}
	return starlark.Call(thread, starlark.Universe["int"], args, kwargs)
}

func builtinStrftime(
	thread *starlark.Thread,
	fn *starlark.Builtin,
	args starlark.Tuple,
	kwargs []starlark.Tuple,
) (starlark.Value, error) {
	var value starlark.Value
	var spec string
	err := starlark.UnpackPositionalArgs("strftime", args, kwargs, 2, &value, &spec)
	if err != nil {
		return nil, err
	}
	when, ok := value.(startime.Time)
	if !ok {
		return nil, fmt.Errorf("strftime(): want a time, got %s", value.Type())
	}
	return starlark.String(Strftime(gotime.Time(when), spec)), nil
}

// Strftime formats a time with the familiar % directives.
// It is locale-independent: month and day names are English.
func Strftime(when gotime.Time, spec string) string {
	var out strings.Builder
	runes := []rune(spec)
	for index := 0; index < len(runes); index++ {
		if runes[index] != '%' || index+1 >= len(runes) {
			out.WriteRune(runes[index])
			continue
		}
		index++
		// nolint:staticcheck // False positive: QF1012 (out is not an io.Writer)
		switch runes[index] {
		case 'Y':
			out.WriteString(strconv.Itoa(when.Year()))
		case 'y':
			out.WriteString(fmt.Sprintf("%02d", when.Year()%100))
		case 'm':
			out.WriteString(fmt.Sprintf("%02d", int(when.Month())))
		case 'd':
			out.WriteString(fmt.Sprintf("%02d", when.Day()))
		case 'H':
			out.WriteString(fmt.Sprintf("%02d", when.Hour()))
		case 'M':
			out.WriteString(fmt.Sprintf("%02d", when.Minute()))
		case 'S':
			out.WriteString(fmt.Sprintf("%02d", when.Second()))
		case 'j':
			out.WriteString(fmt.Sprintf("%03d", when.YearDay()))
		case 'B':
			out.WriteString(when.Month().String())
		case 'b':
			out.WriteString(when.Format("Jan"))
		case 'A':
			out.WriteString(when.Weekday().String())
		case 'a':
			out.WriteString(when.Format("Mon"))
		case 'p':
			out.WriteString(when.Format("PM"))
		case 'I':
			out.WriteString(when.Format("03"))
		case 'Z':
			out.WriteString(when.Format("MST"))
		case 'z':
			out.WriteString(when.Format("-0700"))
		case '%':
			out.WriteByte('%')
		default:
			out.WriteByte('%')
			out.WriteRune(runes[index])
		}
	}
	return out.String()
}

// Truth applies the truth rules of doc/expressions.md#truth-values.
//
// The one rule that is not Starlark's own is for a record, which is
// true whenever it is not None. Everything else -- including a time value,
// which is false only when it is the zero time -- follows the host dialect.
func Truth(value starlark.Value) bool {
	if value == nil {
		return false
	}
	return bool(value.Truth())
}

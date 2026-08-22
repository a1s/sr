// Package data loads input records and coerces them to their declared types.
//
// Coercion happens once, at load, which is what turns "19.99" into an
// exact decimal and "2005-05-24T22:53:30Z" into a time value, rather than
// making every expression do it.
package data

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
	gotime "time"

	"github.com/a1s/sr/internal/expr"
	"github.com/a1s/sr/internal/tmpl"
	"github.com/shopspring/decimal"
	startime "go.starlark.net/lib/time"
	"go.starlark.net/starlark"
)

// DateLayout is the accepted text form for a date parameter or member
// when the node gives no format.
const DateLayout = "2006-01-02"

// ReadJSON reads records from a JSON array document or from NDJSON,
// one record per line.
//
// The whole dataset is buffered: DATA_COUNT, report-scoped aggregates,
// and keep-together lookahead all need the full sequence.
// NDJSON exists so that a large dataset is not one giant JSON value,
// not to enable streaming.
func ReadJSON(reader io.Reader) ([]map[string]any, error) {
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, nil
	}
	if text[0] == '[' {
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		var rows []map[string]any
		if err := dec.Decode(&rows); err != nil {
			return nil, fmt.Errorf("reading the JSON array: %w", err)
		}
		return rows, nil
	}
	var rows []map[string]any
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	for line := 1; ; line++ {
		var row map[string]any
		if err := dec.Decode(&row); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("record %d: %w", line, err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// Records converts loaded rows into the frozen records expressions see.
func Records(rows []map[string]any, decl *tmpl.Records) ([]*expr.Record, error) {
	out := make([]*expr.Record, 0, len(rows))
	for index, row := range rows {
		rec, err := Record(row, decl, index)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// Record converts one row.
//
// A member present in the data but not declared is not an error — data often
// carries members a report does not use. It is not reachable as a bare name,
// but it stays reachable as THIS["name"].
func Record(row map[string]any, decl *tmpl.Records, index int) (*expr.Record, error) {
	values := make(map[string]starlark.Value, len(row))
	var keys []string

	declared := map[string]bool{}
	if decl != nil {
		for _, member := range decl.Members {
			declared[member.Name] = true
			raw, present := row[member.Name]
			value, err := coerce(raw, present, member)
			if err != nil {
				return nil, fmt.Errorf("record %d: member %q: %w",
					index, member.Name, err)
			}
			keys = append(keys, member.Name)
			values[member.Name] = value
		}
	}
	for name, raw := range row {
		if declared[name] {
			continue
		}
		keys = append(keys, name)
		values[name] = generic(raw)
	}
	rec := expr.NewRecord(keys, values)
	rec.Freeze()
	return rec, nil
}

func coerce(raw any, present bool, member *tmpl.RecordMember) (starlark.Value, error) {
	if !present || raw == nil {
		if member.Nullable {
			return starlark.None, nil
		}
		if !present {
			return nil, fmt.Errorf(
				"the record has no such field, and the member is not nullable")
		}
		return nil, fmt.Errorf("null in a member that is not nullable")
	}
	return Convert(raw, member.Type, member.Format)
}

// Convert coerces one value to a declared type.
//
// The value may have come from JSON or straight from Go, since the library API
// takes records as maps or structs: a decimal.Decimal and a time.Time arrive
// as themselves rather than as text.
func Convert(raw any, kind tmpl.ValueType, format string) (starlark.Value, error) {
	switch typed := raw.(type) {
	case decimal.Decimal:
		if kind == tmpl.TypeDecimal {
			return expr.NewDecimal(typed), nil
		}
		raw = typed.String()
	case expr.Decimal:
		if kind == tmpl.TypeDecimal {
			return typed, nil
		}
		raw = typed.String()
	case gotime.Time:
		if kind == tmpl.TypeDate {
			return startime.Time(gotime.Date(typed.Year(),
				typed.Month(), typed.Day(), 0, 0, 0, 0, typed.Location())), nil
		}
		if kind == tmpl.TypeDatetime {
			return startime.Time(typed), nil
		}
		raw = typed.Format(gotime.RFC3339)
	case startime.Time:
		return Convert(gotime.Time(typed), kind, format)
	}

	switch kind {
	case tmpl.TypeString:
		switch typed := raw.(type) {
		case string:
			return starlark.String(typed), nil
		case json.Number:
			return starlark.String(typed.String()), nil
		case bool:
			return starlark.String(strconv.FormatBool(typed)), nil
		}
		return nil, fmt.Errorf("want text, got %T", raw)

	case tmpl.TypeInt:
		text, err := numericText(raw)
		if err != nil {
			return nil, err
		}
		num, ok := new(big.Int).SetString(text, 10)
		if !ok {
			return nil, fmt.Errorf("%q is not a whole number", text)
		}
		return starlark.MakeBigInt(num), nil

	case tmpl.TypeDecimal:
		text, err := numericText(raw)
		if err != nil {
			return nil, err
		}
		dec, err := decimal.NewFromString(text)
		if err != nil {
			return nil, fmt.Errorf("%q is not a decimal", text)
		}
		return expr.NewDecimal(dec), nil

	case tmpl.TypeFloat:
		text, err := numericText(raw)
		if err != nil {
			return nil, err
		}
		num, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", text)
		}
		return starlark.Float(num), nil

	case tmpl.TypeBool:
		switch typed := raw.(type) {
		case bool:
			return starlark.Bool(typed), nil
		case string:
			return parseBoolText(typed)
		case json.Number:
			return starlark.Bool(typed.String() != "0"), nil
		}
		return nil, fmt.Errorf("want a boolean, got %T", raw)

	case tmpl.TypeDate, tmpl.TypeDatetime:
		text, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("want a timestamp as text, got %T", raw)
		}
		when, err := parseTime(text, kind, format)
		if err != nil {
			return nil, err
		}
		return startime.Time(when), nil

	case tmpl.TypeObject:
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("want an object, got %T", raw)
		}
		return objectValue(obj), nil

	case tmpl.TypeList:
		list, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("want an array, got %T", raw)
		}
		items := make([]starlark.Value, len(list))
		for index, item := range list {
			items[index] = generic(item)
		}
		values := starlark.NewList(items)
		values.Freeze()
		return values, nil
	}
	return nil, fmt.Errorf("unknown type")
}

func numericText(raw any) (string, error) {
	switch typed := raw.(type) {
	case json.Number:
		return typed.String(), nil
	case string:
		return strings.TrimSpace(typed), nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case bool:
		if typed {
			return "1", nil
		}
		return "0", nil
	}
	return "", fmt.Errorf("want a number, got %T", raw)
}

func parseBoolText(text string) (starlark.Value, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "true", "1":
		return starlark.True, nil
	case "false", "0":
		return starlark.False, nil
	}
	return nil, fmt.Errorf("%q is not a boolean", text)
}

// parseTime reads a timestamp.
//
// A date yields a time value with zero time of day.
func parseTime(text string, kind tmpl.ValueType, format string) (gotime.Time, error) {
	layouts := []string{gotime.RFC3339}
	if kind == tmpl.TypeDate {
		layouts = []string{DateLayout, gotime.RFC3339}
	}
	if format != "" {
		layouts = []string{format}
	}
	var lastErr error
	for _, layout := range layouts {
		when, err := gotime.Parse(layout, text)
		if err == nil {
			if kind == tmpl.TypeDate {
				when = gotime.Date(when.Year(), when.Month(),
					when.Day(), 0, 0, 0, 0, when.Location())
			}
			return when, nil
		}
		lastErr = err
	}
	return gotime.Time{}, fmt.Errorf("%q is not a %s: %v", text, kind, lastErr)
}

func objectValue(obj map[string]any) *expr.Record {
	keys := make([]string, 0, len(obj))
	values := make(map[string]starlark.Value, len(obj))
	for key, value := range obj {
		keys = append(keys, key)
		values[key] = generic(value)
	}
	// Sorted, because a Go map has no order
	// and the printout must not depend on one.
	sort.Strings(keys)
	rec := expr.NewRecord(keys, values)
	rec.Freeze()
	return rec
}

// Generic converts a Go or JSON value with no declared type.
func Generic(raw any) starlark.Value { return generic(raw) }

// generic converts a JSON value with no declared type, which is what
// an undeclared field and the contents of an object or a list get.
func generic(raw any) starlark.Value {
	switch typed := raw.(type) {
	case nil:
		return starlark.None
	case bool:
		return starlark.Bool(typed)
	case string:
		return starlark.String(typed)
	case json.Number:
		if whole, ok := new(big.Int).SetString(typed.String(), 10); ok {
			return starlark.MakeBigInt(whole)
		}
		num, err := typed.Float64()
		if err != nil {
			return starlark.String(typed.String())
		}
		return starlark.Float(num)
	case float64:
		return starlark.Float(typed)
	case int:
		return starlark.MakeInt(typed)
	case int64:
		return starlark.MakeInt64(typed)
	case decimal.Decimal:
		return expr.NewDecimal(typed)
	case expr.Decimal:
		return typed
	case gotime.Time:
		return startime.Time(typed)
	case map[string]any:
		return objectValue(typed)
	case []any:
		items := make([]starlark.Value, len(typed))
		for index, item := range typed {
			items[index] = generic(item)
		}
		list := starlark.NewList(items)
		list.Freeze()
		return list
	case starlark.Value:
		return typed
	}
	return goReflect(raw)
}

// goReflect converts a Go value the library API supplied:
// a struct, a slice, or a map whose element type is not `any`.
func goReflect(raw any) starlark.Value {
	rv := reflect.ValueOf(raw)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return starlark.None
		}
		return generic(rv.Elem().Interface())
	case reflect.Struct:
		return objectValue(StructFields(raw))
	case reflect.Slice, reflect.Array:
		items := make([]starlark.Value, rv.Len())
		for index := range items {
			items[index] = generic(rv.Index(index).Interface())
		}
		list := starlark.NewList(items)
		list.Freeze()
		return list
	case reflect.Map:
		obj := map[string]any{}
		for _, key := range rv.MapKeys() {
			obj[fmt.Sprint(key.Interface())] = rv.MapIndex(key).Interface()
		}
		return objectValue(obj)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return starlark.MakeInt64(rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return starlark.MakeUint64(rv.Uint())
	case reflect.Float32, reflect.Float64:
		return starlark.Float(rv.Float())
	case reflect.Bool:
		return starlark.Bool(rv.Bool())
	case reflect.String:
		return starlark.String(rv.String())
	}
	return starlark.None
}

// StructFields maps a struct's exported fields to member names,
// by an `sr` tag where one is given and by field name otherwise.
func StructFields(value any) map[string]any {
	out := map[string]any{}
	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return out
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return out
	}
	rt := rv.Type()
	for index := 0; index < rt.NumField(); index++ {
		field := rt.Field(index)
		if !field.IsExported() {
			continue
		}
		name := field.Name
		if tag, ok := field.Tag.Lookup("sr"); ok {
			tag = strings.Split(tag, ",")[0]
			if tag == "-" {
				continue
			}
			if tag != "" {
				name = tag
			}
		}
		out[name] = rv.Field(index).Interface()
	}
	return out
}

// Rows normalises the library API's input — a slice of maps,
// or of structs — into the map form the coercion path takes.
func Rows(records any) ([]map[string]any, error) {
	switch typed := records.(type) {
	case nil:
		return nil, nil
	case []map[string]any:
		return typed, nil
	}
	rv := reflect.ValueOf(records)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, fmt.Errorf("records must be a slice, got %T", records)
	}
	out := make([]map[string]any, rv.Len())
	for index := range out {
		item := rv.Index(index).Interface()
		switch typed := item.(type) {
		case map[string]any:
			out[index] = typed
		default:
			fields := StructFields(typed)
			if len(fields) == 0 {
				return nil, fmt.Errorf(
					"record %d: want a map or a struct, got %T", index, item)
			}
			out[index] = fields
		}
	}
	return out, nil
}

// ParamText parses a parameter value supplied as text — from the command line,
// or from a `default` — according to its declared type.
func ParamText(text string, kind tmpl.ValueType, format string) (starlark.Value, error) {
	switch kind {
	case tmpl.TypeString:
		return starlark.String(text), nil
	case tmpl.TypeObject:
		var obj map[string]any
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		if err := dec.Decode(&obj); err != nil {
			return nil, fmt.Errorf("%q is not a JSON object", text)
		}
		return objectValue(obj), nil
	case tmpl.TypeList:
		var list []any
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		if err := dec.Decode(&list); err != nil {
			return nil, fmt.Errorf("%q is not a JSON array", text)
		}
		return Convert(list, tmpl.TypeList, "")
	case tmpl.TypeFloat:
		trimmed := strings.ToLower(strings.TrimSpace(text))
		switch trimmed {
		case "inf", "+inf":
			return starlark.Float(inf(1)), nil
		case "-inf":
			return starlark.Float(inf(-1)), nil
		case "nan":
			return starlark.Float(nan()), nil
		}
	}
	return Convert(text, kind, format)
}

// MatchesType reports whether a value already has the declared type,
// which is what a defaultexpr result and a subreport arg must satisfy.
func MatchesType(value starlark.Value, kind tmpl.ValueType) bool {
	switch kind {
	case tmpl.TypeString:
		_, ok := value.(starlark.String)
		return ok
	case tmpl.TypeInt:
		_, ok := value.(starlark.Int)
		return ok
	case tmpl.TypeDecimal:
		_, ok := value.(expr.Decimal)
		return ok
	case tmpl.TypeFloat:
		_, ok := value.(starlark.Float)
		return ok
	case tmpl.TypeBool:
		_, ok := value.(starlark.Bool)
		return ok
	case tmpl.TypeDate, tmpl.TypeDatetime:
		_, ok := value.(startime.Time)
		return ok
	case tmpl.TypeObject:
		_, ok := value.(*expr.Record)
		return ok
	case tmpl.TypeList:
		_, ok := value.(*starlark.List)
		return ok
	}
	return false
}

func inf(sign int) float64 { return math.Inf(sign) }
func nan() float64         { return math.NaN() }

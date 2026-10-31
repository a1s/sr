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

	case tmpl.TypeInt, tmpl.TypeDecimal, tmpl.TypeFloat:
		text, err := numericText(raw)
		if err != nil {
			return nil, err
		}
		return kind.ParseScalar(text, format)

	case tmpl.TypeBool:
		switch typed := raw.(type) {
		case bool:
			return starlark.Bool(typed), nil
		case string:
			return kind.ParseScalar(typed, format)
		case json.Number:
			return starlark.Bool(typed.String() != "0"), nil
		}
		return nil, fmt.Errorf("want a boolean, got %T", raw)

	case tmpl.TypeDate, tmpl.TypeDatetime:
		text, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("want a timestamp as text, got %T", raw)
		}
		return kind.ParseScalar(text, format)

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

// ParamText parses a parameter value supplied as text, from the command
// line or from a `default`, according to its declared type.
//
// The text is read by the functions the template package holds
// a `default` to when the template loads, so a default that loads
// is one the build accepts.
func ParamText(text string, kind tmpl.ValueType, format string) (starlark.Value, error) {
	switch kind {
	case tmpl.TypeObject:
		obj, err := tmpl.DecodeObject(text)
		if err != nil {
			return nil, err
		}
		return objectValue(obj), nil
	case tmpl.TypeList:
		list, err := tmpl.DecodeList(text)
		if err != nil {
			return nil, err
		}
		return Convert(list, tmpl.TypeList, "")
	}
	// strconv.ParseFloat takes inf and nan in any case, with a sign,
	// so a float needs no reading of its own here.
	return kind.ParseScalar(text, format)
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

// Sequence converts an expression's result into rows.
//
// This is how a subreport reads the nested sequence its `data` names.
//
// The elements arrive as Starlark values, already generic: a member declared
// type="list" holds whatever JSON put there, untyped. Turning them back into
// the plain Go shape a record is read from means the subreport's `records`
// coerces them exactly as a top-level `records` coerces the input file,
// rather than through a second path that could disagree with it.
func Sequence(value starlark.Value) ([]map[string]any, error) {
	if value == nil || value == starlark.None {
		return nil, nil
	}
	seq, ok := value.(starlark.Iterable)
	if !ok {
		return nil, fmt.Errorf("want a sequence of records, got %s", value.Type())
	}
	iter := seq.Iterate()
	defer iter.Done()
	var rows []map[string]any
	var item starlark.Value
	for index := 0; iter.Next(&item); index++ {
		row, ok := FromStarlark(item).(map[string]any)
		if !ok {
			return nil, fmt.Errorf("element %d is %s, and a record is an object",
				index, item.Type())
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// FromStarlark converts a value an expression produced
// back to the plain Go shape Record reads.
//
// Anything without a plain equivalent -- a decimal, a time -- is
// passed through as itself, because Convert already accepts those.
func FromStarlark(value starlark.Value) any {
	switch typed := value.(type) {
	case nil, starlark.NoneType:
		return nil
	case starlark.String:
		return string(typed)
	case starlark.Bool:
		return bool(typed)
	case starlark.Int:
		return json.Number(typed.String())
	case starlark.Float:
		return float64(typed)
	case *expr.Record:
		out := make(map[string]any, len(typed.Keys()))
		for _, key := range typed.Keys() {
			member, err := typed.Attr(key)
			if err != nil || member == nil {
				continue
			}
			out[key] = FromStarlark(member)
		}
		return out
	case *starlark.Dict:
		out := make(map[string]any, typed.Len())
		for _, item := range typed.Items() {
			key, ok := starlark.AsString(item[0])
			if !ok {
				key = item[0].String()
			}
			out[key] = FromStarlark(item[1])
		}
		return out
	case *starlark.List:
		out := make([]any, typed.Len())
		for index := range out {
			out[index] = FromStarlark(typed.Index(index))
		}
		return out
	case starlark.Tuple:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = FromStarlark(item)
		}
		return out
	}
	return value
}

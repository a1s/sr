package expr

import (
	"fmt"
	"sort"
	"strings"

	"go.starlark.net/starlark"
)

// Record is a data record, or a nested JSON object declared type="object".
//
// It supports both attribute access and subscripting, so a field is reachable
// as THIS.amount and as THIS["odd name"]. It is immutable, and true whenever it
// exists — an empty record is still true, which is why record truth is defined
// as "not None".
type Record struct {
	keys   []string
	values map[string]starlark.Value
}

// NewRecord builds a record from ordered keys and their values.
func NewRecord(keys []string, values map[string]starlark.Value) *Record {
	return &Record{keys: keys, values: values}
}

// Keys returns the field names in declaration order.
func (record *Record) Keys() []string { return record.keys }

// String renders the record for repr and str.
func (record *Record) String() string {
	var buf strings.Builder
	buf.WriteString("record(")
	for index, key := range record.keys {
		if index > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(key)
		buf.WriteString("=")
		buf.WriteString(record.values[key].String())
	}
	buf.WriteString(")")
	return buf.String()
}

// Type names the Starlark type.
func (record *Record) Type() string { return "record" }

// Freeze is a no-op: a record is immutable.
func (record *Record) Freeze() {}

// Truth reports true. A record that is absent is None, not an empty record.
func (record *Record) Truth() starlark.Bool { return starlark.True }

// Hash refuses: a record is not usable as a dict key.
func (record *Record) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable type: record")
}

// Attr implements attribute access.
func (record *Record) Attr(name string) (starlark.Value, error) {
	if value, ok := record.values[name]; ok {
		return value, nil
	}
	return nil, nil // no such field; Starlark reports it
}

// AttrNames lists the field names, sorted, as Starlark requires.
func (record *Record) AttrNames() []string {
	names := append([]string(nil), record.keys...)
	sort.Strings(names)
	return names
}

// Get implements subscripting, which is how a field whose name is not a valid
// identifier, or one that `records` does not declare, is reached.
func (record *Record) Get(keyValue starlark.Value) (starlark.Value, bool, error) {
	key, ok := starlark.AsString(keyValue)
	if !ok {
		return nil, false, fmt.Errorf("record key must be a string, got %s", keyValue.Type())
	}
	value, found := record.values[key]
	return value, found, nil
}

var (
	_ starlark.Value    = (*Record)(nil)
	_ starlark.HasAttrs = (*Record)(nil)
	_ starlark.Mapping  = (*Record)(nil)
)

// Namespace is a fixed set of named values reachable by attribute, used for
// FINAL.
type Namespace struct {
	name   string
	values map[string]starlark.Value
}

// NewNamespace builds a namespace.
func NewNamespace(name string, values map[string]starlark.Value) *Namespace {
	return &Namespace{name: name, values: values}
}

// String renders the namespace.
func (ns *Namespace) String() string { return ns.name }

// Type names the Starlark type.
func (ns *Namespace) Type() string { return ns.name }

// Freeze is a no-op.
func (ns *Namespace) Freeze() {}

// Truth reports true.
func (ns *Namespace) Truth() starlark.Bool { return starlark.True }

// Hash refuses.
func (ns *Namespace) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable type: %s", ns.name)
}

// Attr looks a name up in the namespace.
func (ns *Namespace) Attr(name string) (starlark.Value, error) {
	if value, ok := ns.values[name]; ok {
		return value, nil
	}
	return nil, fmt.Errorf("%s has no field %s", ns.name, name)
}

// AttrNames lists the namespace's names, sorted.
func (ns *Namespace) AttrNames() []string {
	names := make([]string, 0, len(ns.values))
	for key := range ns.values {
		names = append(names, key)
	}
	sort.Strings(names)
	return names
}

var (
	_ starlark.Value    = (*Namespace)(nil)
	_ starlark.HasAttrs = (*Namespace)(nil)
)

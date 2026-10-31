package printout

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/fxamacker/cbor/v2"
)

// The CBOR spelling of a printout, per doc/printout.md#cbor.
//
// What a CBOR encoder would choose for itself is fixed here, for the reason
// the JSON spelling is: two engines that each write valid CBOR still write
// two files unless every choice is made the same way.
//
//   - Maps and arrays have a definite length, and no item carries a tag.
//   - A map writes its keys in the order the record's NDJSON does, which for
//     a struct is field order and for the tables below is sorted by name.
//   - A count is an integer in the shortest form; every other number is
//     a length, and a length is the eight-byte float after the byte 0xfb,
//     whole or not.
var cborMode = func() cbor.EncMode {
	mode, err := cbor.EncOptions{
		Sort:          cbor.SortNone,
		ShortestFloat: cbor.ShortestFloatNone,
		IndefLength:   cbor.IndefLengthForbidden,
		NaNConvert:    cbor.NaNConvertNone,
		InfConvert:    cbor.InfConvertNone,
	}.EncMode()
	if err != nil {
		panic(fmt.Sprintf("printout: CBOR encoder options: %v", err))
	}
	return mode
}()

// BlobTable is the header's `data`: shared blobs, keyed by name.
//
// It is a type of its own so that CBOR can write its keys in the order
// NDJSON does. Go's JSON encoder sorts a map's keys; Go's maps have no
// order at all, so a CBOR encoder left to itself writes them differently
// on every run, and a CBOR printout would not be reproducible.
type BlobTable map[string]*Blob

// CountTable is the header's `groupRuns` and `groupKeys`, keyed by group name.
type CountTable map[string]int

// MarshalCBOR writes the table as a map whose keys are in name order.
func (table BlobTable) MarshalCBOR() ([]byte, error) {
	return marshalOrdered(len(table), sortedNames(table), func(name string) any {
		return table[name]
	})
}

// MarshalCBOR writes the table as a map whose keys are in name order.
func (table CountTable) MarshalCBOR() ([]byte, error) {
	return marshalOrdered(len(table), sortedNames(table), func(name string) any {
		return table[name]
	})
}

// sortedNames is the keys of a table, in the order the JSON encoder
// would write them, which is a plain comparison of the names.
func sortedNames[Value any](table map[string]Value) []string {
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// marshalOrdered writes a definite-length CBOR map, key by key.
func marshalOrdered(size int, names []string, value func(string) any) ([]byte, error) {
	var out bytes.Buffer
	out.Write(cborMapHead(size))
	for _, name := range names {
		key, err := cborMode.Marshal(name)
		if err != nil {
			return nil, err
		}
		out.Write(key)
		item, err := cborMode.Marshal(value(name))
		if err != nil {
			return nil, err
		}
		out.Write(item)
	}
	return out.Bytes(), nil
}

// cborMapHead is the initial bytes of a definite-length map of size entries:
// major type 5, with the size in the shortest form that holds it.
func cborMapHead(size int) []byte {
	const major = 0xa0
	switch {
	case size < 24:
		return []byte{byte(major | size)}
	case size < 1<<8:
		return []byte{major | 24, byte(size)}
	case size < 1<<16:
		return []byte{major | 25, byte(size >> 8), byte(size)}
	default:
		return []byte{major | 26,
			byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}
	}
}

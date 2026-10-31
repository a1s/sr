package printout

import (
	"bytes"
	"testing"
)

// The header's three name-keyed tables are Go maps, which have no order,
// so an encoder left to itself writes them differently on every run.
// doc/printout.md#cbor says a map writes its keys in the order the
// record's NDJSON does, and NDJSON sorts them, so these must sort too.
func TestCBORTablesAreInNameOrder(test *testing.T) {
	doc := &Printout{Header: Header{
		Report:    &ReportMeta{Name: "ordered"},
		GroupRuns: CountTable{"zebra": 1, "alpha": 2, "middle": 3},
		GroupKeys: CountTable{"zebra": 1, "alpha": 2, "middle": 3},
		Data: BlobTable{
			"zebra.png":  {Content: "AA=="},
			"alpha.png":  {Content: "AQ=="},
			"middle.png": {Content: "Ag=="},
		},
	}}
	var first bytes.Buffer
	if err := doc.WriteCBOR(&first, "."); err != nil {
		test.Fatalf("writing CBOR: %v", err)
	}
	// Each name appears once, and the order of the three is the sorted one.
	for _, table := range [][]string{
		{"alpha", "middle", "zebra"},
		{"alpha.png", "middle.png", "zebra.png"},
	} {
		at := -1
		for _, name := range table {
			next := bytes.Index(first.Bytes(), []byte(name))
			if next < 0 {
				test.Fatalf("%q is not in the output", name)
			}
			if next < at {
				test.Errorf("%q comes before the name that should precede it", name)
			}
			at = next
		}
	}
}

// Two runs of one printout produce one file. A Go map's iteration order
// changes between runs, so this is the test that would have caught the
// tables being written in it.
func TestCBORIsReproducible(test *testing.T) {
	build := func() []byte {
		doc := &Printout{Header: Header{
			Report:    &ReportMeta{Name: "same"},
			GroupRuns: CountTable{},
			Data:      BlobTable{},
		}}
		for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
			doc.Header.GroupRuns[name] = len(name)
			doc.Header.Data[name] = &Blob{Content: "AA=="}
		}
		var out bytes.Buffer
		if err := doc.WriteCBOR(&out, "."); err != nil {
			test.Fatalf("writing CBOR: %v", err)
		}
		return out.Bytes()
	}
	want := build()
	for round := 0; round < 8; round++ {
		if got := build(); !bytes.Equal(got, want) {
			test.Fatalf("run %d differs from the first", round+1)
		}
	}
}

// A length is the eight-byte float after 0xfb, whole or not, so that
// one number has one spelling: 200 is fb 40 69 00 00 00 00 00 00.
func TestCBORWritesLengthsAsDoubles(test *testing.T) {
	doc := &Printout{
		Header: Header{Report: &ReportMeta{Name: "lengths"}, Data: BlobTable{}},
		Pages: []*Page{{Number: 1, Marks: []Mark{&Line{
			Box:   Box{Left: 200, Top: 0, Width: 0, Height: 0},
			Width: 0.5,
			Dash:  "solid",
			Color: "#000000",
		}}}},
	}
	var out bytes.Buffer
	if err := doc.WriteCBOR(&out, "."); err != nil {
		test.Fatalf("writing CBOR: %v", err)
	}
	want := []byte{0xfb, 0x40, 0x69, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if !bytes.Contains(out.Bytes(), want) {
		test.Errorf("200 is not written as an eight-byte double")
	}
}

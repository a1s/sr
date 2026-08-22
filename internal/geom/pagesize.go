package geom

import "fmt"

// PageSize is a page's portrait width and height in points.
type PageSize struct {
	Width, Height float64
}

func mustDim(text string) float64 {
	value, err := ParseDim(text)
	if err != nil {
		panic(err)
	}
	return value
}

// pageSizes lists every name accepted by layout pagesize=, portrait
// (envelope sizes are turned vertically too), as width by height.
var pageSizes = map[string][2]string{
	// ISO 216 paper sizes.
	"A1": {"594mm", "841mm"},
	"A2": {"420mm", "594mm"},
	"A3": {"297mm", "420mm"},
	"A4": {"210mm", "297mm"},
	"A5": {"148mm", "210mm"},
	"A6": {"105mm", "148mm"},
	"B3": {"353mm", "500mm"},
	"B4": {"250mm", "353mm"},
	"B5": {"176mm", "250mm"},
	"B6": {"125mm", "176mm"},
	// North American paper sizes.
	"BusinessCard": {"2.125in", "3.37in"},
	"Executive":    {"7.25in", "10.5in"},
	"Ledger":       {"11in", "17in"},
	"Legal":        {"8.5in", "14in"},
	"Letter":       {"8.5in", "11in"},
	"Quatro":       {"8in", "10in"},
	"Royal":        {"20in", "25in"},
	"Statement":    {"5.5in", "8.5in"},
	// ISO 269 envelope sizes.
	"EnvelopeB4": {"250mm", "353mm"},
	"EnvelopeB5": {"176mm", "250mm"},
	"EnvelopeC3": {"324mm", "458mm"},
	"EnvelopeC4": {"229mm", "324mm"},
	"EnvelopeC5": {"162mm", "229mm"},
	"EnvelopeC6": {"114mm", "162mm"},
	"EnvelopeDL": {"110mm", "220mm"},
	// North American envelope sizes.
	"Envelope#10": {"4.125in", "9.5in"},
	"EnvelopeA2":  {"4.375in", "5.75in"},
	"EnvelopeA6":  {"4.75in", "6.5in"},
	"EnvelopeA7":  {"5.25in", "7.25in"},
}

// LookupPageSize returns the named page size in portrait orientation.
func LookupPageSize(name string) (PageSize, error) {
	dims, ok := pageSizes[name]
	if !ok {
		return PageSize{}, fmt.Errorf("unknown page size %q", name)
	}
	return PageSize{Width: mustDim(dims[0]), Height: mustDim(dims[1])}, nil
}

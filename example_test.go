package sr

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/a1s/sr/meta"
	"github.com/a1s/sr/printout"
)

// buildSakila is the reference example end to end:
// the template, its dataset, strict fonts, and a fixed build time.
func buildSakila(test *testing.T, options ...Option) *Printout {
	test.Helper()
	tpl, err := LoadTemplate("example/sakila/sakila.kdl")
	if err != nil {
		test.Fatalf("%v", err)
	}
	if warnings := tpl.Warnings(); len(warnings) > 0 {
		test.Logf("load warnings: %v", warnings)
	}
	file, err := os.Open("example/sakila/payments.jsonl")
	if err != nil {
		test.Fatal(err)
	}
	defer file.Close() // nolint:errcheck
	options = append([]Option{StrictFonts(), WithBuildTime(fixedTime)}, options...)
	out, err := tpl.BuildJSON(file, options...)
	if err != nil {
		test.Fatalf("%v", err)
	}
	if err := out.Validate(); err != nil {
		test.Fatalf("%v", err)
	}
	return out
}

// The reference template exercises every band type, columns, groups,
// six barcode types, both image paths, both kinds of xref, outlines,
// a deferred page count and a deferred group count.
func TestSakilaEndToEnd(test *testing.T) {
	out := buildSakila(test)

	if out.Header.Report == nil || out.Header.Report.Name != "DVD rental payments" {
		test.Errorf("report metadata = %+v", out.Header.Report)
	}
	if out.Header.Engine != "sr "+meta.Version {
		test.Errorf("engine = %q", out.Header.Engine)
	}
	// Four fonts, all pinned by file, so every path in this printout is
	// relative and the artifact carries its fonts with it.
	if len(out.Header.Fonts) != 4 {
		test.Fatalf("fonts = %d, want 4", len(out.Header.Fonts))
	}
	for _, entry := range out.Header.Fonts {
		if entry.ResolvedBy != "explicit" {
			test.Errorf(
				"font %q resolved by %q; under strict fonts only explicit can occur",
				entry.Name, entry.ResolvedBy)
		}
		if entry.Requested != "" {
			test.Errorf(
				"font %q records a typeface %q it never asked for",
				entry.Name, entry.Requested)
		}
	}

	counts := map[string]int{}
	types := map[string]bool{}
	var outlines []*printout.Outline
	var walk func([]printout.Mark)
	walk = func(marks []printout.Mark) {
		for _, mark := range marks {
			counts[mark.MarkKind()]++
			switch typed := mark.(type) {
			case *printout.Barcode:
				types[typed.Type] = true
			case *printout.Outline:
				outlines = append(outlines, typed)
			case *printout.Xref:
				walk(typed.Marks)
			}
		}
	}
	for _, page := range out.Pages {
		walk(page.Marks)
	}

	// Six barcode types, as the template's own comment promises.
	want := []string{"Code128", "Code39", "2of5i", "Aztec", "QR-L", "QR-Q"}
	for _, kind := range want {
		if !types[kind] {
			test.Errorf("no %s barcode in the printout", kind)
		}
	}
	if len(types) != len(want) {
		test.Errorf("barcode types = %v, want exactly %v", types, want)
	}
	for _, kind := range []string{"text", "line", "rectangle", "image", "barcode", "xref", "outline"} {
		if counts[kind] == 0 {
			test.Errorf("no %s marks", kind)
		}
	}

	// The top-level outline is named, and the group titles nest one level
	// below it.
	if outlines[0].Name != "top" || outlines[0].Level != 1 || !outlines[0].Closed {
		test.Errorf("first outline = %+v", outlines[0])
	}
	for _, outline := range outlines[1:] {
		if outline.Level != 2 {
			test.Errorf("outline %q at level %d, want 2", outline.Title, outline.Level)
		}
	}
	// The alternating prefix exercises first-match `when` on outlines.
	if !strings.HasPrefix(outlines[1].Title, `\ `) || !strings.HasPrefix(outlines[2].Title, "/ ") {
		test.Errorf("outline titles = %q, %q", outlines[1].Title, outlines[2].Title)
	}

	// The deferred page count resolved.
	for _, page := range out.Pages {
		found := false
		for _, text := range texts(page) {
			if strings.HasPrefix(strings.Join(text.Lines, ""), "Page ") {
				found = true
				if strings.Contains(text.Lines[0], "999") {
					test.Errorf(
						"the placeholder survived into the output: %q",
						text.Lines[0])
				}
			}
		}
		if !found {
			test.Errorf("page %d has no page-count footer", page.Number)
		}
	}

	// One image is embedded and one referenced, so the printout carries both forms.
	var embedded, referenced int
	for _, page := range out.Pages {
		var count func([]printout.Mark)
		count = func(marks []printout.Mark) {
			for _, mark := range marks {
				switch typed := mark.(type) {
				case *printout.Image:
					if typed.Data != "" {
						embedded++
					} else {
						referenced++
					}
				case *printout.Xref:
					count(typed.Marks)
				}
			}
		}
		count(page.Marks)
	}
	if embedded == 0 {
		test.Error("no embedded image")
	}
	if referenced != 0 {
		test.Errorf("sakila embeds both of its images, got %d referenced", referenced)
	}
}

// Parameters narrow the report, and a date parameter supplied as text
// is parsed per its declared type.
func TestSakilaParameters(test *testing.T) {
	full := buildSakila(test)
	narrowed := buildSakila(test,
		WithTextParam("period_start", "2005-06-01"),
		WithTextParam("period_end", "2005-07-01"))

	rows := func(out *Printout) int {
		count := 0
		for _, page := range out.Pages {
			for _, text := range texts(page) {
				// Detail rows are the only marks carrying a date in this shape.
				if len(text.Lines) == 1 && strings.Count(text.Lines[0], ".") == 2 && len(text.Lines[0]) == 10 {
					count++
				}
			}
		}
		return count
	}
	if rows(narrowed) >= rows(full) {
		test.Errorf("narrowing the period did not suppress rows: %d against %d",
			rows(narrowed), rows(full))
	}
}

// A required parameter with no default and no value supplied is an error naming it.
func TestRequiredParameter(test *testing.T) {
	const src = `report name="t" {
  parameter "needed" type="int"
  records { member "n" type="int" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=200 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    detail height=10 { field expr="needed" left=0 width=100 height=10 }
  }
}`
	err := buildStringErr(test, src, rowsOf(1))
	if err == nil || !strings.Contains(err.Error(), "needed") {
		test.Errorf("want an error naming the parameter, got %v", err)
	}

	tpl, perr := ParseTemplate("example/fonts/test.kdl", src)
	if perr != nil {
		test.Fatal(perr)
	}
	_, err = tpl.Build(rowsOf(1), StrictFonts(), WithBuildTime(fixedTime), WithParam("needed", 7))
	if err != nil {
		test.Errorf("supplying the parameter must build: %v", err)
	}
}

// Records may be structs as well as maps; struct fields map to declared members
// by name, or by an sr tag.
func TestStructRecords(test *testing.T) {
	type row struct {
		Number int `sr:"n"`
		Unused string
	}
	tpl, err := ParseTemplate("example/fonts/test.kdl", minimal)
	if err != nil {
		test.Fatal(err)
	}
	out, err := tpl.Build([]row{{Number: 1}, {Number: 2}},
		StrictFonts(), WithBuildTime(fixedTime))
	if err != nil {
		test.Fatal(err)
	}
	if got, want := joined(lines(out.Pages[0])), "row 1,row 2"; got != want {
		test.Errorf("got %q, want %q", got, want)
	}
}

// A null in a member that is not nullable is an error naming the member
// and the record; in a nullable one it becomes None, which is false.
func TestNullHandling(test *testing.T) {
	const src = `report name="t" {
  records {
    member "n" type="int"
    member "opt" type="string" nullable=#true
  }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=200 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    detail height=10 {
      field expr="opt" printwhen="opt" left=0 width=100 height=10
    }
  }
}`
	out := buildString(test, src, []map[string]any{
		{"n": 1, "opt": "here"},
		{"n": 2, "opt": nil},
	})
	if got, want := joined(lines(out.Pages[0])), "here"; got != want {
		test.Errorf("got %q, want %q — a null suppresses the field", got, want)
	}

	err := buildStringErr(test, src, []map[string]any{{"n": nil, "opt": "x"}})
	if err == nil || !strings.Contains(err.Error(), `"n"`) {
		test.Errorf("want an error naming the member, got %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "record 0") {
		test.Errorf("the diagnostic must name the record index: %v", err)
	}
}

// Times and decimals arrive as their declared types,
// so a date formats and a money value stays exact.
func TestDeclaredTypesReachExpressions(test *testing.T) {
	const src = `report name="t" {
  records {
    member "amount" type="decimal"
    member "when" type="datetime"
    member "day" type="date"
  }
  variable "total" expr="amount" calc="sum" reset="report"
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=300 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    summary height=10 { field expr="total" format="total %.2f" left=0 width=200 height=10 }
    detail height=10 {
      field expr="(strftime(when, '%d.%m.%Y %H:%M'), strftime(day, '%Y'), amount)" \
            format="%s / %s / %.2f" left=0 width=250 height=10
    }
  }
}`
	out := buildString(test, src, []map[string]any{
		{"amount": "2.99", "when": "2005-05-24T22:53:30Z", "day": "2005-05-24"},
		{"amount": "0.02", "when": "2005-05-25T01:00:00Z", "day": "2006-01-01"},
	})
	got := lines(out.Pages[0])
	want := []string{
		"24.05.2005 22:53 / 2005 / 2.99",
		"25.05.2005 01:00 / 2006 / 0.02",
		"total 3.01",
	}
	if joined(got) != joined(want) {
		test.Errorf("got %q\nwant %q", got, want)
	}
}

func BenchmarkSakila(bench *testing.B) {
	tpl, err := LoadTemplate("example/sakila/sakila.kdl")
	if err != nil {
		bench.Fatal(err)
	}
	raw, err := os.ReadFile("example/sakila/payments.jsonl")
	if err != nil {
		bench.Fatal(err)
	}
	bench.ReportAllocs()
	for bench.Loop() {
		if _, err := tpl.BuildJSON(strings.NewReader(string(raw)),
			StrictFonts(), WithBuildTime(time.Unix(0, 0))); err != nil {
			bench.Fatal(err)
		}
	}
}

// BenchmarkSakilaScaled measures throughput on a dataset large enough that the
// fixed cost of loading the template and parsing the fonts does not dominate.
func BenchmarkSakilaScaled(bench *testing.B) {
	tpl, err := LoadTemplate("example/sakila/sakila.kdl")
	if err != nil {
		bench.Fatal(err)
	}
	raw, err := os.ReadFile("example/sakila/payments.jsonl")
	if err != nil {
		bench.Fatal(err)
	}
	var repeated strings.Builder
	const copies = 250 // 16 records apiece
	for index := 0; index < copies; index++ {
		repeated.Write(raw)
	}
	source := repeated.String()
	bench.ReportAllocs()
	for bench.Loop() {
		out, err := tpl.BuildJSON(strings.NewReader(source),
			StrictFonts(), WithBuildTime(time.Unix(0, 0)))
		if err != nil {
			bench.Fatal(err)
		}
		bench.ReportMetric(float64(len(out.Pages)), "pages")
	}
}

// TestReadmeExample is the library snippet the README shows.
//
// Exercise the documented entry point rather than only describe it.
func TestReadmeExample(test *testing.T) {
	tpl, err := LoadTemplate("example/sakila/sakila.kdl")
	if err != nil {
		test.Fatal(err)
	}
	rows := []map[string]any{
		{
			"customer_id": 1,
			"amount":      Decimal("2.99"),
			"rental_date": time.Date(2005, 5, 24, 22, 53, 30, 0, time.UTC),
			"return_date": nil,
			"customer":    map[string]any{"first_name": "MARY", "last_name": "SMITH"},
			"film":        map[string]any{"title": "ACADEMY DINOSAUR"},
		},
	}
	out, err := tpl.Build(rows,
		WithParam("period_start", time.Date(2005, 1, 1, 0, 0, 0, 0, time.UTC)),
		WithBuildTime(fixedTime),
		StrictFonts(),
	)
	if err != nil {
		test.Fatal(err)
	}
	if err := out.Validate(); err != nil {
		test.Fatalf("%v", err)
	}
	found := false
	for _, text := range texts(out.Pages[0]) {
		if strings.Contains(strings.Join(text.Lines, " "), "ACADEMY DINOSAUR") {
			found = true
		}
	}
	if !found {
		test.Error("the record did not reach the page")
	}
	// A nullable member given nil suppresses its field rather than failing.
	for _, text := range texts(out.Pages[0]) {
		if strings.Join(text.Lines, "") == "None" {
			test.Error("a null must suppress the field, not print None")
		}
	}
}

// buildInvoices builds the second reference template, which is the acceptance
// example for subreports: an embedded layout invoked once per invoice.
func buildInvoices(test *testing.T) *Printout {
	test.Helper()
	tpl, err := LoadTemplate("example/invoices/invoices.kdl")
	if err != nil {
		test.Fatalf("the second reference template must load and validate:\n%v", err)
	}
	if warnings := tpl.Warnings(); len(warnings) > 0 {
		test.Errorf("unexpected load warnings: %v", warnings)
	}
	file, err := os.Open("example/invoices/invoices.jsonl")
	if err != nil {
		test.Fatal(err)
	}
	defer file.Close() // nolint:errcheck
	out, err := tpl.BuildJSON(file, StrictFonts(), WithBuildTime(fixedTime))
	if err != nil {
		test.Fatalf("building the invoice register:\n%v", err)
	}
	if err := out.Validate(); err != nil {
		test.Fatalf("%v", err)
	}
	return out
}

// invoices.kdl builds, subreport and all.
func TestInvoicesBuilds(test *testing.T) {
	out := buildInvoices(test)

	// Every line item in invoices.jsonl reaches a page, under the heading
	// its arg supplied, with the subreport's own summary after it.
	var all []string
	for _, page := range out.Pages {
		all = append(all, lines(page)...)
	}
	joinedText := strings.Join(all, "\n")
	for _, want := range []string{
		"Torque wrench, 40-200 Nm", // a line item, from the nested sequence
		"Mooring rope, 12mm, per metre",
		"Invoice #01041 — lines total 410.50", // the arg, and the child's own sum
	} {
		if !strings.Contains(joinedText, want) {
			test.Errorf("the printout does not carry %q", want)
		}
	}

	// The line items of a void invoice are not printed: the host band
	// is suppressed by printwhen, and the subreport is on that band.
	if strings.Contains(joinedText, "#01044") {
		test.Error("a suppressed invoice must not reach the page")
	}

	// The external, self-paginating subreport put its pages into the document
	// where it occurs, with its own header, its own title and summary,
	// and the argument the host passed it.
	for _, want := range []string{
		"Region sheet: Baltic",
		"3 invoices, 1650.70 in total",
		"sheet total 1650.70",
	} {
		if !strings.Contains(joinedText, want) {
			test.Errorf("the printout does not carry %q", want)
		}
	}

	// The host and its subreports share one document: one page sequence,
	// whose numbers run straight through, because neither subreport asked
	// for ownpageno.
	for index, page := range out.Pages {
		if page.Number != index+1 {
			test.Errorf("page %d is numbered %d; without ownpageno the numbering runs on",
				index+1, page.Number)
		}
	}

	// And one font table: both templates call a font "body" and name
	// the same file, so it is measured, published and embedded once.
	names := map[string]bool{}
	for _, entry := range out.Header.Fonts {
		if names[entry.Name] {
			test.Errorf("two font entries answer to %q", entry.Name)
		}
		names[entry.Name] = true
	}
	if len(out.Header.Fonts) != 5 {
		test.Errorf("fonts = %d, want the five distinct faces the two templates name",
			len(out.Header.Fonts))
	}
}

// The region sheet is a report in its own right, so it validates
// on its own even though it is only ever built as a subreport.
func TestRegionSheetValidatesAlone(test *testing.T) {
	tpl, err := LoadTemplate("example/invoices/region_sheet.kdl")
	if err != nil {
		test.Fatalf("%v", err)
	}
	if warnings := tpl.Warnings(); len(warnings) > 0 {
		test.Errorf("unexpected load warnings: %v", warnings)
	}
	params := tpl.Info().Parameters
	if len(params) != 2 || !params[0].Required || !params[1].Required {
		test.Errorf("parameters = %+v, want two the host has to supply", params)
	}
}

// The line items follow their invoice row, because the subreport's seq is 1.
func TestInvoiceLinesFollowTheirRow(test *testing.T) {
	out := buildInvoices(test)
	var order []string
	for _, page := range out.Pages {
		for _, text := range texts(page) {
			joined := strings.Join(text.Lines, " ")
			switch {
			case strings.HasPrefix(joined, "#01041"):
				order = append(order, "row")
			case strings.Contains(joined, "Anchor bolt, galvanised"):
				order = append(order, "line")
			}
		}
	}
	if len(order) < 2 || order[0] != "row" {
		test.Errorf("seq=1 puts the line items after the invoice row; got %v", order)
	}
}

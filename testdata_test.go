package sr

import (
	"strings"
	"testing"
	"time"

	"github.com/a1s/sr/printout"
)

// fixedTime keeps BUILD_TIME out of the way of every comparison.
var fixedTime = time.Date(2026, 8, 4, 9, 12, 44, 0, time.UTC)

// buildString compiles a template written inline and applies it to rows.
//
// The template's base directory is example/fonts, so `font file="Go-Regular.ttf"`
// resolves under strict fonts, which is how the whole suite runs:
// no outcome depends on what is installed on the machine.
func buildString(test *testing.T, source string, rows []map[string]any, options ...Option) *Printout {
	test.Helper()
	tpl, err := ParseTemplate("example/fonts/test.kdl", source)
	if err != nil {
		test.Fatalf("loading the template:\n%v", err)
	}
	options = append([]Option{StrictFonts(), WithBuildTime(fixedTime)}, options...)
	out, err := tpl.Build(rows, options...)
	if err != nil {
		test.Fatalf("building:\n%v", err)
	}
	// Every printout the suite produces is checked against the format's own
	// invariants.
	if err := out.Validate(); err != nil {
		test.Fatalf("%v", err)
	}
	return out
}

// buildStringErr builds and returns the error, for the cases that must fail.
func buildStringErr(test *testing.T, source string, rows []map[string]any, options ...Option) error {
	test.Helper()
	tpl, err := ParseTemplate("example/fonts/test.kdl", source)
	if err != nil {
		test.Fatalf("loading the template:\n%v", err)
	}
	options = append([]Option{StrictFonts(), WithBuildTime(fixedTime)}, options...)
	_, err = tpl.Build(rows, options...)
	return err
}

// texts collects the text marks of a page in paint order, flattening xrefs.
func texts(page *printout.Page) []*printout.Text {
	var out []*printout.Text
	var walk func([]printout.Mark)
	walk = func(marks []printout.Mark) {
		for _, mark := range marks {
			switch typed := mark.(type) {
			case *printout.Text:
				out = append(out, typed)
			case *printout.Xref:
				walk(typed.Marks)
			}
		}
	}
	walk(page.Marks)
	return out
}

// lines renders a page's text marks as their content, joined per mark.
func lines(page *printout.Page) []string {
	var out []string
	for _, text := range texts(page) {
		out = append(out, strings.Join(text.Lines, "|"))
	}
	return out
}

// kinds names a page's marks in paint order.
func kinds(page *printout.Page) []string {
	out := make([]string, 0, len(page.Marks))
	for _, mark := range page.Marks {
		out = append(out, mark.MarkKind())
	}
	return out
}

// rowsOf builds records with a single member named n.
func rowsOf(values ...any) []map[string]any {
	out := make([]map[string]any, len(values))
	for index, value := range values {
		out[index] = map[string]any{"n": value}
	}
	return out
}

func joined(values []string) string { return strings.Join(values, ",") }

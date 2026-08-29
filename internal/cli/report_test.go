package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lineOf returns the first line of text whose start matches prefix, or "".
func lineOf(text, prefix string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// The template check reports what the document says and what
// the machine resolved, and ends in a verdict a script can read.
func TestValidateReport(test *testing.T) {
	_, template, _ := fixture(test)
	got := run(test, "", "validate", template)
	if got.code != ExitOK {
		test.Fatalf("exit = %d, stderr %q", got.code, got.err)
	}
	if got.err != "" {
		test.Errorf("a check that passes says nothing on standard error, got %q", got.err)
	}
	lines := strings.Split(strings.TrimRight(got.out, "\n"), "\n")
	if last := lines[len(lines)-1]; last != "ok" {
		test.Errorf("the last line is %q, want \"ok\"", last)
	}
	for _, want := range []string{
		`report "Tiny" version 1 by als`,
		"page 419.528 x 595.276 pt, margins left 20 right 20 top 20 bottom 20",
		"1 column, 1 member, 1 font",
		"parameters",
		`note    string  default "hello"`,
		"wanted  int     required",
		"fonts",
	} {
		if !strings.Contains(got.out, want) {
			test.Errorf("the report does not contain %q; it is:\n%s", want, got.out)
		}
	}
	// The font line names the file it resolved and the face inside it.
	if line := lineOf(got.out, "body"); !strings.Contains(line, "explicit") ||
		!strings.Contains(line, "Go-Regular.ttf") || !strings.HasSuffix(line, `"Go"`) {
		test.Errorf("font line = %q", line)
	}
}

// A required parameter is a fact about the template, not a failure of it.
func TestValidateNeedsNoParameters(test *testing.T) {
	_, template, _ := fixture(test)
	got := run(test, "", "validate", "-t", template, "--strict-fonts")
	if got.code != ExitOK {
		test.Fatalf("exit = %d, stderr %q", got.code, got.err)
	}
	if !strings.Contains(got.out, "required") {
		test.Error("the report does not say which parameter is required")
	}
}

func TestValidateQuiet(test *testing.T) {
	_, template, _ := fixture(test)
	got := run(test, "", "validate", "-q", template)
	if got.code != ExitOK {
		test.Fatalf("exit = %d, stderr %q", got.code, got.err)
	}
	if got.out != "" || got.err != "" {
		test.Errorf("a quiet check that passes prints nothing, got %q and %q",
			got.out, got.err)
	}
}

// A font that cannot be resolved is the one thing a check can fail on.
func TestValidateUnresolvedFont(test *testing.T) {
	dir := test.TempDir()
	template := filepath.Join(dir, "unresolved.kdl")
	source := `report name="Unresolved" {
  font "body" typeface="No Such Typeface At All" size=10
  layout pagesize="A5" {
    style font="body"
    detail height=12 { field text="x" }
  }
}`
	if err := os.WriteFile(template, []byte(source), 0o644); err != nil {
		test.Fatal(err)
	}
	got := run(test, "", "validate", template, "--strict-fonts")
	if got.code != ExitFail {
		test.Errorf("exit = %d, want %d", got.code, ExitFail)
	}
	if strings.Contains(got.out, "ok") {
		test.Error("a check that failed must not say ok")
	}
	if !strings.Contains(got.out, "No Such Typeface At All") {
		test.Errorf("the report does not name the typeface; it is:\n%s", got.out)
	}
	// Under failures, not warnings: this is why the check failed.
	if !strings.Contains(got.out, "failures\n  font \"body\"") {
		test.Errorf("the failure is not filed as one; the report is:\n%s", got.out)
	}
	if strings.Contains(got.out, "warnings") {
		test.Errorf("nothing here is a warning; the report is:\n%s", got.out)
	}
	// The resolver's message already names the font, so nothing may name it
	// again: no font resolved here, so this line is the only place it occurs.
	if seen := strings.Count(got.out, `font "body"`); seen != 1 {
		test.Errorf("the font is named %d times; the report is:\n%s", seen, got.out)
	}
	if !strings.Contains(got.err, "1 font did not resolve") {
		test.Errorf("stderr = %q", got.err)
	}
}

func TestValidateUsage(test *testing.T) {
	_, template, _ := fixture(test)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "no template", args: []string{"validate"}, want: "--template is required"},
		{name: "two templates", args: []string{"validate", template, template},
			want: "one template at a time"},
		{name: "both spellings at once",
			args: []string{"validate", template, "-t", template}, want: "given twice"},
		{name: "a parameter the template does not declare",
			args: []string{"validate", template, "--param", "not=1"},
			want: "--param not names no parameter"},
	}
	for _, item := range cases {
		test.Run(item.name, func(test *testing.T) {
			got := run(test, "", item.args...)
			if got.code != ExitUsage {
				test.Errorf("exit = %d, want %d; stderr %q",
					got.code, ExitUsage, got.err)
			}
			if !strings.Contains(got.err, item.want) {
				test.Errorf("stderr = %q, want it to contain %q",
					got.err, item.want)
			}
		})
	}
}

// A template naming a subreport validates, and the check names the node.
func TestValidateNamesSubreports(test *testing.T) {
	got := run(test, "", "validate",
		filepath.Join("..", "..", "example", "invoices", "invoices.kdl"))
	if got.code != ExitOK {
		test.Fatalf("exit = %d, stderr %q", got.code, got.err)
	}
	for _, want := range []string{"2 subreports", "subreports\n  report >"} {
		if !strings.Contains(got.out, want) {
			test.Errorf("the report does not carry %q; it is:\n%s", want, got.out)
		}
	}
}

// dumpFixture builds the tiny report to a printout and returns its path.
func dumpFixture(test *testing.T) string {
	test.Helper()
	dir, template, data := fixture(test)
	path := filepath.Join(dir, "doc.srp.jsonl")
	got := run(test, "", "build", "-t", template, "-d", data, "-o", path,
		"--strict-fonts", "--param", "wanted=1",
		"--build-time", "2026-08-04T09:12:44Z")
	if got.code != ExitOK {
		test.Fatalf("building the fixture: exit %d, stderr %q", got.code, got.err)
	}
	return path
}

// The dump carries the header, the pages, and one line per mark.
func TestInspectDump(test *testing.T) {
	path := dumpFixture(test)
	got := run(test, "", "inspect", path)
	if got.code != ExitOK {
		test.Fatalf("exit = %d, stderr %q", got.code, got.err)
	}
	for _, want := range []string{
		"printout " + path,
		"built 2026-08-04T09:12:44Z, strict fonts",
		`report "Tiny" version 1 by als`,
		"1 page, 1 font",
		"fonts",
		"page 1  419.528 x 595.276  3 marks",
		"text  box 20,20 200x12  font body  color #000000  align left  leading 12",
		`"hello"`,
		`"row 1"`,
		`"row 2"`,
	} {
		if !strings.Contains(got.out, want) {
			test.Errorf("the dump does not contain %q; it is:\n%s", want, got.out)
		}
	}
	// A text mark's lines are quoted underneath it, indented one step further.
	if !strings.Contains(got.out, "leading 12\n    \"hello\"\n") {
		test.Errorf("text lines are not nested under their mark:\n%s", got.out)
	}
}

func TestInspectSummary(test *testing.T) {
	path := dumpFixture(test)
	got := run(test, "", "inspect", path, "--summary")
	if got.code != ExitOK {
		test.Fatalf("exit = %d, stderr %q", got.code, got.err)
	}
	if strings.Contains(got.out, "page 1  ") {
		test.Errorf("--summary printed a page:\n%s", got.out)
	}
	if !strings.Contains(got.out, "1 page, 1 font") {
		test.Errorf("--summary dropped the header:\n%s", got.out)
	}
}

// Page selection, including the ranges with an open end.
func TestInspectPages(test *testing.T) {
	// Twenty rows of 12 pt in a 595 pt page with 20 pt margins: several pages.
	dir, template, _ := fixture(test)
	var rows strings.Builder
	for number := 1; number <= 200; number++ {
		fmt.Fprintf(&rows, "{\"n\": %d}\n", number)
	}
	data := filepath.Join(dir, "many.jsonl")
	if err := os.WriteFile(data, []byte(rows.String()), 0o644); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(dir, "many.srp.jsonl")
	if got := run(test, "", "build", "-t", template, "-d", data, "-o", path,
		"--strict-fonts", "--param", "wanted=1"); got.code != ExitOK {
		test.Fatalf("exit = %d, stderr %q", got.code, got.err)
	}
	all := run(test, "", "inspect", path)
	if all.code != ExitOK {
		test.Fatalf("exit = %d, stderr %q", all.code, all.err)
	}
	pages := strings.Count(all.out, "\npage ")
	if pages < 5 {
		test.Fatalf("the fixture has %d pages, and this test needs at least 5", pages)
	}

	cases := []struct {
		spec string
		want []int
	}{
		{spec: "1", want: []int{1}},
		{spec: "2-3", want: []int{2, 3}},
		{spec: "1,3", want: []int{1, 3}},
		{spec: "-2", want: []int{1, 2}},
		{spec: fmt.Sprintf("%d-", pages-1), want: []int{pages - 1, pages}},
	}
	for _, item := range cases {
		test.Run(item.spec, func(test *testing.T) {
			got := run(test, "", "inspect", path, "--pages", item.spec)
			if got.code != ExitOK {
				test.Fatalf("exit = %d, stderr %q", got.code, got.err)
			}
			for _, number := range item.want {
				if !strings.Contains(got.out, fmt.Sprintf("\npage %d  ", number)) {
					test.Errorf("--pages %s left out page %d", item.spec, number)
				}
			}
			if seen := strings.Count(got.out, "\npage "); seen != len(item.want) {
				test.Errorf("--pages %s dumped %d pages, want %d",
					item.spec, seen, len(item.want))
			}
		})
	}
}

func TestInspectUsage(test *testing.T) {
	path := dumpFixture(test)
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{name: "no printout", args: []string{"inspect"},
			code: ExitUsage, want: "a printout file is required"},
		{name: "standard input", args: []string{"inspect", "-"},
			code: ExitUsage, want: "not from standard input"},
		{name: "a range that is not a number",
			args: []string{"inspect", path, "--pages", "one"},
			code: ExitUsage, want: `"one" is not a number`},
		{name: "a range counting backwards",
			args: []string{"inspect", path, "--pages", "5-2"},
			code: ExitUsage, want: "counts backwards"},
		{name: "page zero", args: []string{"inspect", path, "--pages", "0"},
			code: ExitUsage, want: "pages count from 1"},
		{name: "an empty range item", args: []string{"inspect", path, "--pages", "1,,3"},
			code: ExitUsage, want: "not a page number or a range"},
		{name: "a printout that is not there",
			args: []string{"inspect", filepath.Join(test.TempDir(), "nope.srp.jsonl")},
			code: ExitFail, want: "nope.srp.jsonl"},
	}
	for _, item := range cases {
		test.Run(item.name, func(test *testing.T) {
			got := run(test, "", item.args...)
			if got.code != item.code {
				test.Errorf("exit = %d, want %d; stderr %q",
					got.code, item.code, got.err)
			}
			if !strings.Contains(got.err, item.want) {
				test.Errorf("stderr = %q, want it to contain %q",
					got.err, item.want)
			}
		})
	}
}

// A printout that breaks its own invariants is dumped and then reported,
// because an inspector that read one silently would be the wrong tool.
func TestInspectChecksInvariants(test *testing.T) {
	dir := test.TempDir()
	path := filepath.Join(dir, "broken.srp.jsonl")
	body := strings.Join([]string{
		`{"sr":1,"kind":"header","built":"2026-08-04T09:12:44Z","engine":"sr test",` +
			`"strictFonts":true,"pages":1,` +
			`"page":{"width":200,"height":200,"leftMargin":20,"rightMargin":20,` +
			`"topMargin":20,"bottomMargin":20},"fonts":[],"data":{}}`,
		`{"kind":"page","number":1,"marks":[` +
			`{"kind":"rectangle","box":{"x":150,"y":20,"width":100,"height":10},` +
			`"width":1,"dash":"solid","fill":"#000000"}]}`,
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		test.Fatal(err)
	}
	got := run(test, "", "inspect", path)
	if got.code != ExitFail {
		test.Errorf("exit = %d, want %d", got.code, ExitFail)
	}
	if !strings.Contains(got.out, "rectangle  box 150,20 100x10") {
		test.Errorf("the dump is missing the offending mark:\n%s", got.out)
	}
	if !strings.Contains(got.err, "past the printable width") {
		test.Errorf("stderr = %q, want it to name the violated invariant", got.err)
	}
}

// parsePages is small enough to check directly, over the cases
// the command-line tests cannot reach through a fixture.
func TestParsePages(test *testing.T) {
	cases := []struct {
		spec    string
		matches []int
		misses  []int
	}{
		{spec: "", matches: []int{1, 7, 900}},
		{spec: "4", matches: []int{4}, misses: []int{3, 5}},
		{spec: "2-4", matches: []int{2, 3, 4}, misses: []int{1, 5}},
		{spec: "-3", matches: []int{1, 3}, misses: []int{4}},
		{spec: "8-", matches: []int{8, 800}, misses: []int{7}},
		{spec: "1,5-6", matches: []int{1, 5, 6}, misses: []int{2, 4, 7}},
		{spec: " 2 , 4 ", matches: []int{2, 4}, misses: []int{3}},
		{spec: "-", matches: []int{1, 99}},
	}
	for _, item := range cases {
		test.Run(item.spec, func(test *testing.T) {
			set, err := parsePages(item.spec)
			if err != nil {
				test.Fatalf("%v", err)
			}
			for _, number := range item.matches {
				if !set.matches(number) {
					test.Errorf("%q should match page %d", item.spec, number)
				}
			}
			for _, number := range item.misses {
				if set.matches(number) {
					test.Errorf("%q should not match page %d", item.spec, number)
				}
			}
		})
	}
	for _, spec := range []string{"x", "0", "5-2", "1,,2", "-0", "1-x"} {
		if _, err := parsePages(spec); err == nil {
			test.Errorf("--pages %q was accepted", spec)
		}
	}
}

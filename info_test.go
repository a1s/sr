package sr

import (
	"strings"
	"testing"
)

// A template describes itself without being built, which is from what
// works anything that has to show a template before it has data --
// such as a front end.
func TestInfo(test *testing.T) {
	tpl, err := LoadTemplate("example/sakila/sakila.kdl")
	if err != nil {
		test.Fatalf("%v", err)
	}
	info := tpl.Info()
	if info.Name != "DVD rental payments" || info.Version != "2" || info.Author != "als" {
		test.Errorf("identification = %+v", info)
	}
	if info.Page.Width != 595.276 || info.Page.Height != 841.89 {
		test.Errorf("page = %+v", info.Page)
	}
	if info.Columns != 2 {
		test.Errorf("columns = %d, want 2", info.Columns)
	}
	if joined(info.Groups) != "customer" {
		test.Errorf("groups = %v", info.Groups)
	}
	if joined(info.Fonts) != "title,pagetitle,body,bold" {
		test.Errorf("fonts = %v, want them in declaration order", info.Fonts)
	}
	if joined(info.Members) !=
		"customer_id,amount,rental_date,return_date,customer,film" {
		test.Errorf("members = %v", info.Members)
	}
	if len(info.Variables) != 5 || len(info.Data) != 1 {
		test.Errorf("variables = %v, data = %v", info.Variables, info.Data)
	}
	if len(info.Subreports) != 0 {
		test.Errorf("subreports = %v, and sakila has none", info.Subreports)
	}

	if len(info.Parameters) != 3 {
		test.Fatalf("parameters = %+v, want 3", info.Parameters)
	}
	first := info.Parameters[0]
	if first.Name != "period_start" || first.Type != "date" ||
		first.Default != "2005-01-01" || !first.HasDefault || first.Required ||
		!first.Prompt {
		test.Errorf("first parameter = %+v", first)
	}
	if last := info.Parameters[2]; last.Name != "as_of" ||
		!last.HasDefaultExpr || last.Required {
		test.Errorf("a computed default = %+v", last)
	}
}

// A template with no value for a required parameter is still describable,
// and the check does not need one.
func TestInfoRequiredParameter(test *testing.T) {
	source := `report name="Needs one" {
  parameter "wanted" type="int"
  font "body" file="Go-Regular.ttf" size=10
  layout pagesize="A5" {
    style font="body"
    detail height=12 { field expr="wanted" }
  }
}`
	tpl, err := ParseTemplate("example/fonts/test.kdl", source)
	if err != nil {
		test.Fatalf("%v", err)
	}
	param := tpl.Info().Parameters[0]
	if !param.Required || param.HasDefault || param.HasDefaultExpr {
		test.Errorf("parameter = %+v", param)
	}
	check, err := tpl.CheckFonts(StrictFonts())
	if err != nil {
		test.Fatalf("a check must not need the parameter: %v", err)
	}
	if len(check.Failures) != 0 || len(check.Fonts) != 1 {
		test.Errorf("check = %+v", check)
	}
}

// Subreports are named by a check, since a build refuses them.
func TestInfoNamesSubreports(test *testing.T) {
	tpl, err := LoadTemplate("example/invoices/invoices.kdl")
	if err != nil {
		test.Fatalf("%v", err)
	}
	subreports := tpl.Info().Subreports
	if len(subreports) != 1 || !strings.Contains(subreports[0], "subreport") {
		test.Errorf("subreports = %v", subreports)
	}
}

// Checking fonts resolves every one the template declares, and reports
// what each resolved to -- the same entries a printout header would carry.
func TestCheckFonts(test *testing.T) {
	tpl, err := LoadTemplate("example/sakila/sakila.kdl")
	if err != nil {
		test.Fatalf("%v", err)
	}
	check, err := tpl.CheckFonts(StrictFonts())
	if err != nil {
		test.Fatalf("%v", err)
	}
	if len(check.Failures) != 0 {
		test.Fatalf("failures = %v", check.Failures)
	}
	if len(check.Fonts) != 4 {
		test.Fatalf("fonts = %d, want 4", len(check.Fonts))
	}
	for _, entry := range check.Fonts {
		if entry.ResolvedBy != "explicit" {
			test.Errorf("font %q resolved by %q, and strict mode admits only explicit",
				entry.Name, entry.ResolvedBy)
		}
		if entry.ResolvedFile == "" || entry.ResolvedFace == "" {
			test.Errorf("font %q = %+v", entry.Name, entry)
		}
	}
	// Sorted by name, as a printout header holds them.
	if check.Fonts[0].Name != "body" || check.Fonts[3].Name != "title" {
		test.Errorf("fonts are not sorted by name: %+v", check.Fonts)
	}
}

// One typeface that cannot be resolved does not hide the next:
// a check collects the failures instead of returning the first.
func TestCheckFontsCollectsFailures(test *testing.T) {
	source := `report name="Unresolved" {
  font "one" typeface="No Such Typeface At All" size=10
  font "two" typeface="Nor This One Either" size=10
  font "body" file="Go-Regular.ttf" size=10
  layout pagesize="A5" {
    style font="body"
    detail height=12 { field text="x" }
  }
}`
	tpl, err := ParseTemplate("example/fonts/test.kdl", source)
	if err != nil {
		test.Fatalf("%v", err)
	}
	check, err := tpl.CheckFonts(StrictFonts())
	if err != nil {
		test.Fatalf("%v", err)
	}
	if len(check.Failures) != 2 {
		test.Fatalf("failures = %v, want both typefaces", check.Failures)
	}
	for index, want := range []string{"No Such Typeface At All", "Nor This One Either"} {
		if !strings.Contains(check.Failures[index], want) {
			test.Errorf("failure %d = %q, want it to name %q", index, check.Failures[index], want)
		}
	}
	// Each failure names its font once: the resolver's messages carry the
	// name already, so a collector that adds its own says it twice.
	for _, failure := range check.Failures {
		if seen := strings.Count(failure, `font "`); seen != 1 {
			test.Errorf("failure %q names the font %d times, want 1", failure, seen)
		}
	}
	// The one font that did resolve is still reported.
	if len(check.Fonts) != 1 || check.Fonts[0].Name != "body" {
		test.Errorf("fonts = %+v", check.Fonts)
	}
}

// A value supplied for a name the template does not declare is refused.
//
// It is the mistake with the worst failure mode: the report builds with
// the default in place of what the caller meant, and nothing mentions it.
// The CLI catches it first, as a usage error; this is the library saying so.
func TestUndeclaredParameter(test *testing.T) {
	source := `report name="One parameter" {
  parameter "period_start" type="date" default="2005-01-01"
  font "body" file="Go-Regular.ttf" size=10
  layout pagesize="A5" {
    style font="body"
    detail height=12 { field expr="period_start" }
  }
}`
	tpl, err := ParseTemplate("example/fonts/test.kdl", source)
	if err != nil {
		test.Fatalf("%v", err)
	}
	for _, option := range []Option{
		WithTextParam("perod_start", "2005-06-01"),
		WithParam("perod_start", "2005-06-01"),
	} {
		_, err := tpl.Build(nil, StrictFonts(), WithBuildTime(fixedTime), option)
		if err == nil {
			test.Fatal("a misspelled parameter name built without complaint")
		}
		for _, want := range []string{"perod_start", "period_start"} {
			if !strings.Contains(err.Error(), want) {
				test.Errorf("error = %q, want it to name %q", err, want)
			}
		}
	}
	// The declared spelling still works, which is the point of the check
	// being about names rather than about values.
	if _, err := tpl.Build(nil, StrictFonts(), WithBuildTime(fixedTime),
		WithTextParam("period_start", "2005-06-01")); err != nil {
		test.Errorf("%v", err)
	}
}

// A check does not refuse it: there the caller may be checking a template
// rather than building it. The command line reports it instead.
func TestCheckIgnoresUndeclaredParameter(test *testing.T) {
	source := `report name="None" {
  font "body" file="Go-Regular.ttf" size=10
  layout pagesize="A5" {
    style font="body"
    detail height=12 { field text="x" }
  }
}`
	tpl, err := ParseTemplate("example/fonts/test.kdl", source)
	if err != nil {
		test.Fatalf("%v", err)
	}
	if _, err := tpl.CheckFonts(StrictFonts(), WithTextParam("nonesuch", "1")); err != nil {
		test.Errorf("%v", err)
	}
	if _, err := tpl.Build(nil, StrictFonts(), WithTextParam("nonesuch", "1")); err == nil {
		test.Error("a build must refuse it")
	} else if !strings.Contains(err.Error(), "declares none") {
		test.Errorf("error = %q, want it to say the template declares none", err)
	}
}

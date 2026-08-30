package tmpl

import (
	"errors"
	"strings"
	"testing"
)

func TestLoadReferenceTemplates(test *testing.T) {
	for _, path := range []string{
		"../../example/sakila/sakila.kdl",
		"../../example/invoices/invoices.kdl",
	} {
		report, err := Load(path)
		if err != nil {
			test.Fatalf("%s:\n%v", path, err)
		}
		if report.Name == "" {
			test.Errorf("%s: report has no name", path)
		}
		if report.Layout == nil {
			test.Fatalf("%s: no layout", path)
		}
		for _, diag := range report.Warnings {
			test.Logf("%s: warning: %v", path, diag)
		}
	}
}

func TestSakilaShape(test *testing.T) {
	report, err := Load("../../example/sakila/sakila.kdl")
	if err != nil {
		test.Fatal(err)
	}
	if got := len(report.Params); got != 3 {
		test.Errorf("parameters = %d, want 3", got)
	}
	if got := len(report.Variables); got != 5 {
		test.Errorf("variables = %d, want 5", got)
	}
	if got := len(report.Fonts); got != 4 {
		test.Errorf("fonts = %d, want 4", got)
	}
	if got := len(report.Records.Members); got != 6 {
		test.Errorf("members = %d, want 6", got)
	}
	if got := strings.Join(report.GroupNames, ","); got != "customer" {
		test.Errorf("groups = %q, want customer", got)
	}
	if report.Layout.Body.Columns == nil || report.Layout.Body.Columns.Count != 2 {
		test.Error("want a two-column layout")
	}
	// Document order is paint order: the detail band's children come back in
	// the order they were written, rectangle first so the fields sit on it.
	detail := report.Layout.Body.Group.Detail
	var kinds []string
	for _, el := range detail.Elements {
		kinds = append(kinds, el.Kind())
	}
	want := "rectangle,field,field,field,field,line"
	if got := strings.Join(kinds, ","); got != want {
		test.Errorf("detail elements = %q, want %q", got, want)
	}
}

func TestInvoicesShape(test *testing.T) {
	report, err := Load("../../example/invoices/invoices.kdl")
	if err != nil {
		test.Fatal(err)
	}
	if got := len(report.Layout.Embedded); got != 1 || report.Layout.Embedded[0].Name != "lines" {
		test.Fatalf("want one embedded layout named lines, got %d", len(report.Layout.Embedded))
	}
	lines := report.Layout.Embedded[0]
	if lines.Records == nil || len(lines.Records.Members) != 4 {
		test.Error("the embedded layout declares its own records")
	}
	detail := report.Layout.Body.Group.Detail
	if len(detail.Subreports) != 1 {
		test.Fatalf("want one subreport, got %d", len(detail.Subreports))
	}
	if !detail.Subreports[0].Inline || detail.Subreports[0].Seq != 1 {
		test.Error("the subreport is inline with seq=1")
	}
	if !report.Layout.Body.Summary.SwapFooter {
		test.Error("the summary sets swapfooter")
	}
	if !detail.Split || detail.Orphans != 2 || detail.Widows != 2 {
		test.Error("the detail band splits with orphans=2 widows=2")
	}
	// All twelve calc modes appear across the two templates; invoices carries
	// the ones sakila leaves out.
	seen := map[CalcMode]bool{}
	for _, variable := range report.Variables {
		seen[variable.Calc] = true
	}
	for _, calc := range []CalcMode{CalcSet, CalcChain, CalcStd, CalcVar, CalcCount,
		CalcMin, CalcMax, CalcAvg, CalcFirst, CalcLast, CalcList, CalcSum} {
		if !seen[calc] {
			test.Errorf("calc %q is missing from invoices.kdl", calc)
		}
	}
}

// mustFail loads a template that is expected to be rejected and returns the
// diagnostics as one string.
func mustFail(test *testing.T, src string) string {
	test.Helper()
	_, err := LoadString(src, "test.kdl")
	if err == nil {
		test.Fatalf("want a validation error for:\n%s", src)
	}
	return err.Error()
}

// wrap puts a body inside a minimal report that is otherwise valid.
func wrap(body string) string {
	return `report name="t" {
  font "body" file="f.ttf" size=9
  layout pagesize="A4" {
    style font="body"
` + body + `
  }
}`
}

func TestValidationRejects(test *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"all three on one axis",
			wrap(`detail { field left=1 right=2 width=3 text="x" }`),
			"at most two of left, right and width"},
		{"all three vertically",
			wrap(`detail { field top=1 bottom=2 height=3 text="x" }`),
			"at most two of top, bottom and height"},
		{"two content sources",
			wrap(`detail { field text="x" expr="1" }`),
			"exactly one of expr, text and data"},
		{"no content source",
			wrap(`detail { field left=0 }`),
			"exactly one of expr, text and data"},
		{"evaltime without expr",
			wrap(`detail { field text="x" evaltime="report" }`),
			"takes its content from expr"},
		{"deferred stretch field with no placeholder",
			wrap(`detail { field expr="FINAL.PAGE_NUMBER" evaltime="report" stretch=#true }`),
			"needs a placeholder"},
		{"deferred barcode with no placeholder",
			wrap(`detail { barcode type="Code128" expr="FINAL.PAGE_NUMBER" evaltime="report" }`),
			"needs a placeholder"},
		{"FINAL without evaltime",
			wrap(`detail { field expr="FINAL.PAGE_NUMBER" }`),
			"FINAL lives in the expr of a field or barcode with an evaltime"},
		{"FINAL in printwhen",
			wrap(`detail { field text="x" printwhen="FINAL.PAGE_NUMBER > 1" }`),
			"FINAL lives in the expr"},
		{"evaltime without FINAL",
			wrap(`detail { field expr="PAGE_NUMBER" evaltime="report" }`),
			"never names FINAL"},
		{"FINAL of an unknown name",
			wrap(`detail { field expr="FINAL.nonesuch" evaltime="report" }`),
			"neither a predefined variable nor a declared variable"},
		{"unknown evaltime scope",
			wrap(`detail { field expr="FINAL.PAGE_NUMBER" evaltime="nonesuch" }`),
			"names neither report, page, column, nor a group"},
		{"unknown style font",
			wrap(`detail { field text="x" { style font="nonesuch" } }`),
			`no font named "nonesuch"`},
		{"unknown outline target",
			wrap(`detail { xref type="outline" target="'nowhere'" { field text="x" } }`),
			`no outline is named "nowhere"`},
		{"unknown property",
			wrap(`detail { field text="x" nonesuch=1 }`),
			"unknown property"},
		{"barcode ink that reflects red light",
			wrap(`detail { barcode type="Code128" text="x" ink="yellow" }`),
			"will not scan"},
		{"barcode paper that absorbs red light",
			wrap(`detail { barcode type="Code128" text="x" paper="navy" }`),
			"will not scan"},
		{"inverted barcode",
			wrap(`detail { barcode type="QR-L" text="x" ink="white" paper="black" }`),
			"will not scan"},
		{"barcode ink too close to its paper",
			wrap(`detail { barcode type="Code128" text="x" ink="navy" paper="#654321" }`),
			"will not scan"},
		{"unknown node",
			wrap(`detail { nonesuch }`),
			"unexpected node here"},
		{"both group and detail",
			wrap(`detail { field text="x" }
    group "g" expr="1" { detail { field text="y" } }`),
			"exactly one of group and detail"},
		{"neither group nor detail",
			wrap(`title { field text="x" }`),
			"exactly one of group and detail is required"},
		{"format on a non-temporal member",
			`report name="t" {
  records { member "a" type="int" format="2006" }
  font "body" file="f.ttf" size=9
  layout pagesize="A4" { detail { field text="x" } }
}`,
			"applies only to a date or datetime member"},
		{"a name that collides with a predefined one",
			`report name="t" {
  variable "PAGE_NUMBER" expr="1"
  font "body" file="f.ttf" size=9
  layout pagesize="A4" { detail { field text="x" } }
}`,
			"collides with a predefined name"},
		{"a group whose derived name collides",
			`report name="t" {
  font "body" file="f.ttf" size=9
  layout pagesize="A4" { group "PAGE" expr="1" { detail { field text="x" } } }
}`,
			"would derive PAGE_COUNT"},
		{"duplicate variable",
			`report name="t" {
  variable "v" expr="1"
  variable "v" expr="2"
  font "body" file="f.ttf" size=9
  layout pagesize="A4" { detail { field text="x" } }
}`,
			`duplicate variable "v"`},
		{"itergrp naming no group",
			`report name="t" {
  variable "v" expr="1" iter="group" itergrp="nonesuch"
  font "body" file="f.ttf" size=9
  layout pagesize="A4" { detail { field text="x" } }
}`,
			`no group named "nonesuch"`},
		{"font with neither typeface nor file",
			`report name="t" {
  font "body" size=9
  layout pagesize="A4" { detail { field text="x" } }
}`,
			"exactly one of typeface, file or data"},
		{"layout with no page size",
			`report name="t" {
  font "body" file="f.ttf" size=9
  layout { detail { field text="x" } }
}`,
			"either pagesize, or both width and height"},
		{"a column count that leaves no width",
			`report name="t" {
  font "body" file="f.ttf" size=9
  layout pagesize="A4" {
    columns count=500 gap="5mm" { }
    detail { field text="x" }
  }
}`,
			"leaves no width"},
		{"both default and defaultexpr",
			`report name="t" {
  parameter "p" default="1" defaultexpr="2"
  font "body" file="f.ttf" size=9
  layout pagesize="A4" { detail { field text="x" } }
}`,
			"at most one of default and defaultexpr"},
		{"an inline subreport whose layout has a header",
			`report name="t" {
  font "body" file="f.ttf" size=9
  layout pagesize="A4" {
    embedded "e" {
      header height=5 { field text="h" }
      detail { field text="d" }
    }
    detail {
      field text="x"
      subreport embedded="e" seq=1 data="[]" inline=#true
    }
  }
}`,
			"must not define them"},
		{"a subreport combining inline with ownpageno",
			`report name="t" {
  font "body" file="f.ttf" size=9
  layout pagesize="A4" {
    embedded "e" { detail { field text="d" } }
    detail {
      field text="x"
      subreport embedded="e" seq=1 data="[]" inline=#true ownpageno=#true
    }
  }
}`,
			"cannot restart numbering"},
		{"a subreport on a columns header",
			`report name="t" {
  font "body" file="f.ttf" size=9
  layout pagesize="A4" {
    embedded "e" { detail { field text="d" } }
    columns count=2 {
      header height=5 {
        field text="h"
        subreport embedded="e" seq=1 data="[]"
      }
    }
    detail { field text="x" }
  }
}`,
			"put it on a title, summary or detail band"},
		{"an arg naming no parameter",
			`report name="t" {
  font "body" file="f.ttf" size=9
  layout pagesize="A4" {
    embedded "e" { detail { field text="d" } }
    detail {
      field text="x"
      subreport embedded="e" seq=1 data="[]" { arg "nonesuch" value="1" }
    }
  }
}`,
			"has no parameter named"},
		{"a floating element with no height of its own",
			wrap(`detail height=20 { field float=#true text="x" left=0 right=0 }`),
			"a floating element's height must come from the element"},
		{"an expression that does not parse",
			wrap(`detail { field expr="1 +" }`),
			"got end of file"},
		{"KDL's #false in an expression property",
			wrap(`detail { field text="x" printwhen="#false" }`),
			"got end of file"},
	}
	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			got := mustFail(test, testCase.src)
			if !strings.Contains(got, testCase.want) {
				test.Errorf("diagnostics:\n%s\nwant a mention of %q", got, testCase.want)
			}
		})
	}
}

// A font named by file selects its face outright, so bold and italic decide
// nothing there. They are still accepted, and kept: they describe the face the
// template named, and the printout's font entry carries them.
func TestFontFileKeepsStyleFlags(test *testing.T) {
	report, err := LoadString(`report name="t" {
  font "body" file="Go-Bold.ttf" size=9 bold=#true italic=#true
  layout pagesize="A4" { detail { field text="x" } }
}`, "test.kdl")
	if err != nil {
		test.Fatal(err)
	}
	font := report.Fonts[0]
	if !font.Bold || !font.Italic {
		test.Errorf("bold=%v italic=%v, want both kept", font.Bold, font.Italic)
	}
}

func TestGeometryDefaultsAndAliases(test *testing.T) {
	report, err := LoadString(wrap(`detail height=20 {
    field text="a" x=5 y=6 width=10 height=8
    line width=2 left=0 right=0 height=0
    rectangle width=1 radius=2
  }`), "test.kdl")
	if err != nil {
		test.Fatal(err)
	}
	els := report.Layout.Body.Detail.Elements
	field := els[0].(*Field)
	if field.Horiz.Near.Value != 5 || field.Vert.Near.Value != 6 {
		test.Errorf("x/y aliases did not land on left/top: %+v %+v", field.Horiz, field.Vert)
	}
	// On a line and a rectangle, width is the stroke width, not an extent.
	line := els[1].(*Line)
	if line.PenWidth != 2 || line.Horiz.Size.Set {
		test.Errorf("line width must be the stroke width, got pen=%v extent set=%v",
			line.PenWidth, line.Horiz.Size.Set)
	}
	rect := els[2].(*Rectangle)
	if rect.PenWidth != 1 || rect.Radius != 2 || rect.Horiz.Size.Set {
		test.Errorf("rectangle: pen=%v radius=%v extent set=%v",
			rect.PenWidth, rect.Radius, rect.Horiz.Size.Set)
	}
	if !rect.Opaque || !rect.Stroke {
		test.Error("a rectangle is opaque and stroked by default")
	}
}

func TestSectionHeightAuto(test *testing.T) {
	report, err := LoadString(wrap(`detail height="auto" { field text="x" height=10 }`), "test.kdl")
	if err != nil {
		test.Fatal(err)
	}
	if report.Layout.Body.Detail.Height.Set {
		test.Error(`height="auto" means no declared minimum`)
	}
}

func TestCollapsingBandWarning(test *testing.T) {
	report, err := LoadString(wrap(`detail { field text="x" left=0 right=0 }`), "test.kdl")
	if err != nil {
		test.Fatal(err)
	}
	if len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0].Message, "collapses") {
		test.Errorf("want a collapse warning, got %v", report.Warnings)
	}
	// A band with a declared height, or with an element that brings its own,
	// does not warn.
	report, err = LoadString(wrap(`detail { field text="x" stretch=#true }`), "test.kdl")
	if err != nil {
		test.Fatal(err)
	}
	if len(report.Warnings) != 0 {
		test.Errorf("unexpected warnings: %v", report.Warnings)
	}
}

func TestColors(test *testing.T) {
	cases := []struct{ in, want string }{
		{"#F3EDE7", "#F3EDE7"},
		{"black", "#000000"},
		{"BLACK", "#000000"},
		{"orange", "#FFA500"},
		{"0,89,0", "#005900"},
		{"0,0.5,1", "#0080FF"},
		{"22016", "#005600"},
	}
	for _, testCase := range cases {
		got, err := ParseColor(testCase.in)
		if err != nil {
			test.Errorf("ParseColor(%q): %v", testCase.in, err)
			continue
		}
		if got.Hex() != testCase.want {
			test.Errorf("ParseColor(%q) = %s, want %s",
				testCase.in, got.Hex(), testCase.want)
		}
	}
	for _, bad := range []string{"", "#FFF", "nosuchcolour", "1,2", "300,0,0"} {
		if _, err := ParseColor(bad); err == nil {
			test.Errorf("ParseColor(%q): want an error", bad)
		}
	}
}

// A barcode's colours are its own, and a combination a red-light
// scanner cannot read is refused rather than quietly printed.
func TestBarcodeInkAndPaper(test *testing.T) {
	report, err := LoadString(wrap(
		`detail { barcode type="Code128" text="x" ink="navy" paper="yellow" }`), "test.kdl")
	if err != nil {
		test.Fatalf("loading: %v", err)
	}
	barcode := report.Layout.Body.Detail.Elements[0].(*Barcode)
	if barcode.Ink == nil || barcode.Ink.Hex() != "#000080" {
		test.Errorf("ink = %v, want navy", barcode.Ink)
	}
	if barcode.Paper == nil || barcode.Paper.Hex() != "#FFFF00" {
		test.Errorf("paper = %v, want yellow", barcode.Paper)
	}
}

// Saying nothing about colour is always legal: the barcode is black,
// and the background is left to whatever the band put there.
func TestBarcodeWithoutColoursIsUncoloured(test *testing.T) {
	report, err := LoadString(wrap(
		`detail { barcode type="Code128" text="x" }`), "test.kdl")
	if err != nil {
		test.Fatalf("loading: %v", err)
	}
	barcode := report.Layout.Body.Detail.Elements[0].(*Barcode)
	if barcode.Ink != nil || barcode.Paper != nil {
		test.Errorf("ink = %v, paper = %v, want both unset", barcode.Ink, barcode.Paper)
	}
}

// Naming only the ink measures it against white,
// because that is what an unprinted background is.
func TestBarcodeInkAloneIsJudgedAgainstWhite(test *testing.T) {
	if _, err := LoadString(wrap(
		`detail { barcode type="Code128" text="x" ink="#654321" }`), "test.kdl"); err != nil {
		test.Errorf("brown ink on white should load: %v", err)
	}
	msg := mustFail(test, wrap(`detail { barcode type="Code128" text="x" ink="orange" }`))
	if !strings.Contains(msg, "will not scan") {
		test.Errorf("diagnostic = %q", msg)
	}
}

// The diagnostic points at the property that has to change:
// a background even black bars could not be read on is the paper's fault,
// and anything else the ink's.
func TestBarcodeContrastBlamesTheRightProperty(test *testing.T) {
	cases := []struct {
		name, declaration, want string
	}{
		{"the ink reflects red", `ink="yellow" paper="white"`, "ink"},
		{"the paper absorbs red", `ink="black" paper="navy"`, "paper"},
		{"only the paper is named", `paper="navy"`, "paper"},
		{"only the ink is named", `ink="yellow"`, "ink"},
	}
	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			_, err := LoadString(wrap(
				`detail { barcode type="Code128" text="x" `+testCase.declaration+` }`),
				"test.kdl")
			if err == nil {
				test.Fatal("want a validation error")
			}
			var diags DiagnosticList
			if !errors.As(err, &diags) {
				test.Fatalf("error is %T, want a diagnostic list", err)
			}
			if got := diags[0].Prop; got != testCase.want {
				test.Errorf("blamed %q, want %q", got, testCase.want)
			}
		})
	}
}

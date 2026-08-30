package sr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fontFile is the test font, named absolutely so that a template written to
// a temporary directory resolves it under strict fonts like every other test.
func fontFile(test *testing.T, name string) string {
	test.Helper()
	abs, err := filepath.Abs(filepath.Join("example", "fonts", name))
	if err != nil {
		test.Fatal(err)
	}
	return filepath.ToSlash(abs)
}

// writeTemplate puts a template in dir and returns its path.
func writeTemplate(test *testing.T, dir, name, source string) string {
	test.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		test.Fatal(err)
	}
	return path
}

// buildFile loads a template from disk and builds it.
func buildFile(test *testing.T, path string, rows []map[string]any) *Printout {
	test.Helper()
	tpl, err := LoadTemplate(path)
	if err != nil {
		test.Fatalf("loading %s:\n%v", filepath.Base(path), err)
	}
	out, err := tpl.Build(rows, StrictFonts(), WithBuildTime(fixedTime))
	if err != nil {
		test.Fatalf("building %s:\n%v", filepath.Base(path), err)
	}
	if err := out.Validate(); err != nil {
		test.Fatalf("%v", err)
	}
	return out
}

// allLines is every text mark in the document, page by page.
func allLines(doc *Printout) []string {
	var out []string
	for _, page := range doc.Pages {
		out = append(out, lines(page)...)
	}
	return out
}

// inlineHost is the smallest report with an inline subreport: one detail row
// per record, and the record's nested list run through an embedded layout.
//
// The seq and the extras are substituted, so one source covers the ordering
// rules and the per-invocation bands.
const inlineHost = `report name="host" {
  records {
    member "name"  type="string"
    member "items" type="list"
  }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"

    embedded "lines" {
      parameter "label" type="string"
      records {
        member "sku"   type="string"
        member "price" type="decimal"
      }
      variable "total" expr="price" calc="sum" reset="report"
      title height=10 { field expr="label" left=0 right=0 }
      detail height=10 { field expr="sku" left=10 right=0 }
      summary height=10 { field expr="total" format="sum %.2f" left=10 right=0 }
    }

    detail height=10 {
      field expr="name" left=0 right=0
      subreport embedded="lines" seq=SEQ data="items" inline=#true WHEN {
        arg "label" value="'items of ' + name"
      }
    }
  }
}`

func inlineTemplate(seq, when string) string {
	src := strings.Replace(inlineHost, "seq=SEQ", "seq="+seq, 1)
	return strings.Replace(src, " WHEN", when, 1)
}

func itemRows() []map[string]any {
	return []map[string]any{{
		"name": "alpha",
		"items": []any{
			map[string]any{"sku": "x", "price": "1.50"},
			map[string]any{"sku": "y", "price": "2.25"},
		},
	}}
}

// A non-negative seq puts the subreport's bands after the whole host band,
// and its title and summary print once per invocation.
func TestInlineSubreportFollowsItsBand(test *testing.T) {
	doc := buildString(test, inlineTemplate("1", ""), itemRows())
	got := joined(allLines(doc))
	want := "alpha,items of alpha,x,y,sum 3.75"
	if got != want {
		test.Errorf("marks = %q, want %q", got, want)
	}
}

// A negative seq puts them before it.
func TestInlineSubreportPrecedesItsBand(test *testing.T) {
	doc := buildString(test, inlineTemplate("-1", ""), itemRows())
	got := joined(allLines(doc))
	want := "items of alpha,x,y,sum 3.75,alpha"
	if got != want {
		test.Errorf("marks = %q, want %q", got, want)
	}
}

// A false `when` skips the invocation and leaves the host band alone.
func TestSubreportWhenSkipsTheInvocation(test *testing.T) {
	doc := buildString(test, inlineTemplate("1", ` when="1 == 2"`), itemRows())
	if got := joined(allLines(doc)); got != "alpha" {
		test.Errorf("marks = %q, want just the host band", got)
	}
}

// A host band suppressed by printwhen runs none of its subreports:
// an invoice that does not print has no line items to print either.
func TestSuppressedBandSuppressesItsSubreport(test *testing.T) {
	src := strings.Replace(inlineTemplate("1", ""),
		`detail height=10 {
      field expr="name"`,
		`detail height=10 printwhen="name != 'alpha'" {
      field expr="name"`, 1)
	doc := buildString(test, src, itemRows())
	if got := allLines(doc); len(got) != 0 {
		test.Errorf("marks = %v, want none", got)
	}
}

// The child's records are coerced by the child's own declaration: the prices
// arrive from JSON as text and sum exactly, which only a decimal does.
func TestSubreportCoercesItsOwnRecords(test *testing.T) {
	doc := buildString(test, inlineTemplate("1", ""), itemRows())
	if got := joined(allLines(doc)); !strings.HasSuffix(got, "sum 3.75") {
		test.Errorf("marks = %q, want an exact decimal total", got)
	}
}

// A report-scoped variable in the child resets for every invocation:
// the child's report scope is one invocation.
func TestSubreportVariablesResetPerInvocation(test *testing.T) {
	rows := []map[string]any{
		{"name": "one", "items": []any{map[string]any{"sku": "a", "price": "1.00"}}},
		{"name": "two", "items": []any{map[string]any{"sku": "b", "price": "2.00"}}},
	}
	doc := buildString(test, inlineTemplate("1", ""), rows)
	got := joined(allLines(doc))
	want := "one,items of one,a,sum 1.00,two,items of two,b,sum 2.00"
	if got != want {
		test.Errorf("marks = %q, want %q", got, want)
	}
}

// An arg whose value does not match the parameter's declared type is refused,
// with the node, the value's type, and the declaration named.
func TestSubreportArgTypeIsChecked(test *testing.T) {
	src := strings.Replace(inlineTemplate("1", ""),
		`value="'items of ' + name"`, `value="42"`, 1)
	err := buildStringErr(test, src, itemRows())
	if err == nil {
		test.Fatal("want the mistyped arg reported")
	}
	for _, want := range []string{"arg", "int", "string"} {
		if !strings.Contains(err.Error(), want) {
			test.Errorf("diagnostic = %v, want it to mention %q", err, want)
		}
	}
}

// A parameter the embedded layout requires and no arg supplies is
// a load error: a subreport has no command line to fall back on.
func TestSubreportRequiredParameterIsALoadError(test *testing.T) {
	src := strings.Replace(inlineTemplate("1", ""),
		`{
        arg "label" value="'items of ' + name"
      }`, `{ }`, 1)
	_, err := ParseTemplate("example/fonts/test.kdl", src)
	if err == nil {
		test.Fatal("want the unsupplied parameter reported")
	}
	if !strings.Contains(err.Error(), `"label"`) {
		test.Errorf("diagnostic = %v", err)
	}
}

// data= must name a sequence, and the diagnostic names the node.
func TestSubreportDataMustBeASequence(test *testing.T) {
	src := strings.Replace(inlineTemplate("1", ""), `data="items"`, `data="name"`, 1)
	err := buildStringErr(test, src, itemRows())
	if err == nil {
		test.Fatal("want the non-sequence reported")
	}
	if !strings.Contains(err.Error(), "subreport") {
		test.Errorf("diagnostic = %v, want the node named", err)
	}
}

// An inline subreport shares the host's pagination: when its bands fill the
// frame the host's page ejects, and the host's header prints on the next one.
func TestInlineSubreportEjectsTheHostPage(test *testing.T) {
	const src = `report name="host" {
  records { member "items" type="list" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=120 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    embedded "lines" {
      records { member "sku" type="string" }
      detail height=10 { field expr="sku" left=0 right=0 }
    }
    header height=12 { field expr="'p%d' % PAGE_NUMBER" left=0 right=0 }
    detail height=10 {
      field text="row" left=0 right=0
      subreport embedded="lines" seq=1 data="items" inline=#true
    }
  }
}`
	items := make([]any, 20)
	for index := range items {
		items[index] = map[string]any{"sku": "s"}
	}
	doc := buildString(test, src, []map[string]any{{"items": items}})
	if len(doc.Pages) < 3 {
		test.Fatalf("pages = %d, want the subreport to fill several", len(doc.Pages))
	}
	for index, page := range doc.Pages {
		if page.Number != index+1 {
			test.Errorf("page %d numbered %d", index+1, page.Number)
		}
		if got := lines(page); len(got) == 0 || !strings.HasPrefix(got[0], "p") {
			test.Errorf("page %d has no host header: %v", page.Number, got)
		}
	}
}

// A subreport that paginates itself closes the host's page, builds its own,
// and the host resumes on a fresh one. Numbering runs straight through.
func TestOwnPagesSubreportSplicesItsPages(test *testing.T) {
	const src = `report name="host" {
  records { member "name" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=120 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    embedded "lines" {
      records { member "sku" type="string" }
      header height=12 { field expr="'sub p%d' % PAGE_NUMBER" left=0 right=0 }
      detail height=10 { field expr="sku" left=0 right=0 }
    }
    header height=12 { field expr="'host p%d' % PAGE_NUMBER" left=0 right=0 }
    detail height=10 {
      field expr="name" left=0 right=0
      subreport embedded="lines" seq=1 data="[{'sku': 'a'}, {'sku': 'b'}]"
    }
  }
}`
	doc := buildString(test, src, []map[string]any{{"name": "row"}})
	if len(doc.Pages) != 3 {
		test.Fatalf("pages = %d, want the host's, the child's, and the host's again",
			len(doc.Pages))
	}
	want := []string{"host p1|row", "sub p2|a|b", "host p3"}
	for index, page := range doc.Pages {
		if got := strings.Join(lines(page), "|"); got != want[index] {
			test.Errorf("page %d = %q, want %q", index+1, got, want[index])
		}
	}
}

// ownpageno restarts numbering inside the subreport and leaves
// the host's alone, so the host resumes from where it was.
func TestOwnPageNoRestartsNumbering(test *testing.T) {
	const src = `report name="host" {
  records { member "name" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=120 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    embedded "lines" {
      records { member "sku" type="string" }
      header height=12 { field expr="'sub p%d' % PAGE_NUMBER" left=0 right=0 }
      detail height=10 { field expr="sku" left=0 right=0 }
    }
    header height=12 { field expr="'host p%d' % PAGE_NUMBER" left=0 right=0 }
    detail height=10 {
      field expr="name" left=0 right=0
      subreport embedded="lines" seq=1 ownpageno=#true data="[{'sku': 'a'}]"
    }
  }
}`
	doc := buildString(test, src, []map[string]any{{"name": "row"}})
	if len(doc.Pages) != 3 {
		test.Fatalf("pages = %d", len(doc.Pages))
	}
	numbers := []int{doc.Pages[0].Number, doc.Pages[1].Number, doc.Pages[2].Number}
	if numbers[0] != 1 || numbers[1] != 1 || numbers[2] != 2 {
		test.Errorf("page numbers = %v, want 1, 1 (restarted), 2", numbers)
	}
	if got := strings.Join(lines(doc.Pages[1]), "|"); got != "sub p1|a" {
		test.Errorf("the subreport's page = %q", got)
	}
}

// A subreport naming another template file loads it with the parent,
// resolves it against basedir, and runs it as a nested report.
func TestExternalTemplateSubreport(test *testing.T) {
	dir := test.TempDir()
	writeTemplate(test, dir, "lines.kdl", `report name="lines" {
  parameter "label" type="string"
  records { member "sku" type="string" }
  font "body" file="`+fontFile(test, "Go-Regular.ttf")+`" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    title height=10 { field expr="label" left=0 right=0 }
    detail height=10 { field expr="sku" left=10 right=0 }
  }
}`)
	host := writeTemplate(test, dir, "host.kdl", `report name="host" {
  records {
    member "name"  type="string"
    member "items" type="list"
  }
  font "body" file="`+fontFile(test, "Go-Bold.ttf")+`" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    detail height=10 {
      field expr="name" left=0 right=0
      subreport template="lines.kdl" seq=1 data="items" inline=#true {
        arg "label" value="'items of ' + name"
      }
    }
  }
}`)
	doc := buildFile(test, host, itemRows())
	got := joined(allLines(doc))
	if want := "alpha,items of alpha,x,y"; got != want {
		test.Errorf("marks = %q, want %q", got, want)
	}

	// Both templates call their font "body" and they are different faces,
	// so the printout has two entries and the second took a distinct name.
	if len(doc.Header.Fonts) != 2 {
		test.Fatalf("fonts = %+v, want one per template", doc.Header.Fonts)
	}
	seen := map[string]bool{}
	for _, entry := range doc.Header.Fonts {
		if seen[entry.Name] {
			test.Errorf("two font entries answer to %q", entry.Name)
		}
		seen[entry.Name] = true
	}
	// Every text mark names an entry that exists.
	for _, page := range doc.Pages {
		for _, text := range texts(page) {
			if !seen[text.Font] {
				test.Errorf("a mark names the font %q, which the header has not", text.Font)
			}
		}
	}
}

// A template that reaches itself through a subreport
// is reported at load rather than followed.
func TestSubreportCycleIsRefused(test *testing.T) {
	dir := test.TempDir()
	writeTemplate(test, dir, "b.kdl", `report name="b" {
  records { member "sku" type="string" }
  font "body" file="`+fontFile(test, "Go-Regular.ttf")+`" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    detail height=10 {
      field expr="sku" left=0 right=0
      subreport template="a.kdl" seq=1 data="[]" inline=#true
    }
  }
}`)
	host := writeTemplate(test, dir, "a.kdl", `report name="a" {
  records { member "sku" type="string" }
  font "body" file="`+fontFile(test, "Go-Regular.ttf")+`" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    detail height=10 {
      field expr="sku" left=0 right=0
      subreport template="b.kdl" seq=1 data="[]" inline=#true
    }
  }
}`)
	_, err := LoadTemplate(host)
	if err == nil {
		test.Fatal("want the cycle reported")
	}
	if !strings.Contains(err.Error(), "includes itself") {
		test.Errorf("diagnostic = %v", err)
	}
}

// What an inline subreport cannot have, each reported at load with the layout
// named. All of them are the same rule: it does not own the pages it prints on.
// A columns block is not among them -- it opens a frame inside the host's,
// which is the one thing an inline subreport does own.
func TestInlineSubreportRestrictions(test *testing.T) {
	cases := []struct {
		name  string
		bands string
		want  string
	}{
		{"header", `header height=10 { field text="h" left=0 right=0 }`, "header and footer"},
		{"footer", `footer height=10 { field text="f" left=0 right=0 }`, "header and footer"},
		{"swapheader", `title swapheader=#true height=10 { field text="t" left=0 right=0 }`, "swapheader"},
		{"swapfooter", `summary swapfooter=#true height=10 { field text="s" left=0 right=0 }`, "swapfooter"},
	}
	for _, item := range cases {
		test.Run(item.name, func(test *testing.T) {
			src := `report name="host" {
  records { member "items" type="list" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    embedded "lines" {
      records { member "sku" type="string" }
      ` + item.bands + `
      detail height=10 { field expr="sku" left=0 right=0 }
    }
    detail height=10 {
      subreport embedded="lines" seq=1 data="items" inline=#true
    }
  }
}`
			_, err := ParseTemplate("example/fonts/test.kdl", src)
			if err == nil {
				test.Fatalf("want %s refused under inline", item.name)
			}
			if !strings.Contains(err.Error(), item.want) {
				test.Errorf("diagnostic = %v, want it to mention %q", err, item.want)
			}
		})
	}
}

// An inline subreport must run at the host's page size,
// which for a template named by file is a thing the load can check.
func TestInlineSubreportPageSizeMustMatch(test *testing.T) {
	dir := test.TempDir()
	writeTemplate(test, dir, "lines.kdl", `report name="lines" {
  records { member "sku" type="string" }
  font "body" file="`+fontFile(test, "Go-Regular.ttf")+`" size=8
  layout width=400 height=500 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    detail height=10 { field expr="sku" left=0 right=0 }
  }
}`)
	host := writeTemplate(test, dir, "host.kdl", `report name="host" {
  records { member "items" type="list" }
  font "body" file="`+fontFile(test, "Go-Regular.ttf")+`" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    detail height=10 {
      subreport template="lines.kdl" seq=1 data="items" inline=#true
    }
  }
}`)
	_, err := LoadTemplate(host)
	if err == nil {
		test.Fatal("want the page size mismatch reported")
	}
	if !strings.Contains(err.Error(), "400 by 500") {
		test.Errorf("diagnostic = %v, want both sizes named", err)
	}
}

// A subreport that paginates itself may run at a page size of its own,
// and the pages it produces carry the difference.
func TestOwnPagesSubreportRecordsItsGeometry(test *testing.T) {
	dir := test.TempDir()
	writeTemplate(test, dir, "lines.kdl", `report name="lines" {
  records { member "sku" type="string" }
  font "body" file="`+fontFile(test, "Go-Regular.ttf")+`" size=8
  layout width=400 height=500 leftmargin=20 rightmargin=20 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    detail height=10 { field expr="sku" left=0 right=0 }
  }
}`)
	host := writeTemplate(test, dir, "host.kdl", `report name="host" {
  records { member "items" type="list" }
  font "body" file="`+fontFile(test, "Go-Regular.ttf")+`" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    detail height=10 {
      field text="row" left=0 right=0
      subreport template="lines.kdl" seq=1 data="items"
    }
  }
}`)
	doc := buildFile(test, host, []map[string]any{
		{"items": []any{map[string]any{"sku": "a"}}},
	})
	if len(doc.Pages) != 3 {
		test.Fatalf("pages = %d", len(doc.Pages))
	}
	child := doc.Pages[1]
	if child.Width != 400 || child.Height != 500 {
		test.Errorf("the child's page is %g x %g, want 400 x 500", child.Width, child.Height)
	}
	if child.LeftMargin == nil || *child.LeftMargin != 20 {
		test.Errorf("the child's page records no left margin override: %+v", child.LeftMargin)
	}
	// The host's pages carry no override at all.
	if doc.Pages[0].Width != 0 || doc.Pages[0].LeftMargin != nil {
		test.Error("a page at the document's own size must record nothing")
	}
}

// A subreport on a page header or footer is refused: those bands are
// measured and reserved before the page they belong to is filled.
func TestSubreportOnAHeaderIsRefused(test *testing.T) {
	const src = `report name="host" {
  records { member "items" type="list" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    embedded "lines" {
      records { member "sku" type="string" }
      detail height=10 { field expr="sku" left=0 right=0 }
    }
    header height=10 {
      field text="h" left=0 right=0
      subreport embedded="lines" seq=1 data="items" inline=#true
    }
    detail height=10 { field text="d" left=0 right=0 }
  }
}`
	_, err := ParseTemplate("example/fonts/test.kdl", src)
	if err == nil {
		test.Fatal("want a subreport on a header refused")
	}
	if !strings.Contains(err.Error(), "header band") {
		test.Errorf("diagnostic = %v", err)
	}
}

// A layout that names itself recurses on the data, and stops at a fixed depth
// rather than at the stack.
func TestSubreportNestingIsBounded(test *testing.T) {
	const src = `report name="host" {
  records { member "sku" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=4000 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    embedded "node" {
      records { member "sku" type="string" }
      detail height=8 {
        field expr="sku" left=0 right=0
        subreport embedded="node" seq=1 data="[{'sku': sku}]" inline=#true
      }
    }
    detail height=8 {
      subreport embedded="node" seq=1 data="[{'sku': 'deep'}]" inline=#true
    }
  }
}`
	err := buildStringErr(test, src, []map[string]any{{"sku": "root"}})
	if err == nil {
		test.Fatal("want the runaway nesting stopped")
	}
	if !strings.Contains(err.Error(), "nested") {
		test.Errorf("diagnostic = %v", err)
	}
}

// An embedded layout's styles fall through to the enclosing layout's,
// because the style search walks outward through the document
// and an embedded layout is written inside one.
func TestEmbeddedLayoutInheritsTheEnclosingStyles(test *testing.T) {
	const src = `report name="host" {
  records { member "items" type="list" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="#123456"
    embedded "lines" {
      records { member "sku" type="string" }
      detail height=10 { field expr="sku" left=0 right=0 }
    }
    detail height=10 {
      subreport embedded="lines" seq=1 data="items" inline=#true
    }
  }
}`
	doc := buildString(test, src, []map[string]any{
		{"items": []any{map[string]any{"sku": "x"}}},
	})
	marks := texts(doc.Pages[0])
	if len(marks) != 1 {
		test.Fatalf("marks = %d", len(marks))
	}
	if marks[0].Color != "#123456" {
		test.Errorf("colour = %q, want the enclosing layout's", marks[0].Color)
	}
	if marks[0].Font != "body" {
		test.Errorf("font = %q, want the enclosing layout's", marks[0].Font)
	}
}

// A subreport on a swapheader title or a swapfooter summary is refused:
// those bands are placed outside the frame's own fill, and a subreport
// takes frame space of its own.
func TestSubreportOnASwappedBandIsRefused(test *testing.T) {
	for _, band := range []string{
		`title swapheader=#true height=10 { SUB }`,
		`summary swapfooter=#true height=10 { SUB }`,
	} {
		src := `report name="host" {
  records { member "items" type="list" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    embedded "lines" {
      records { member "sku" type="string" }
      detail height=10 { field expr="sku" left=0 right=0 }
    }
    ` + strings.Replace(band, "SUB",
			`field text="x" left=0 right=0
      subreport embedded="lines" seq=1 data="items" inline=#true`, 1) + `
    detail height=10 { field text="d" left=0 right=0 }
  }
}`
		_, err := ParseTemplate("example/fonts/test.kdl", src)
		if err == nil {
			test.Fatalf("want a subreport on %s refused", band[:7])
		}
		if !strings.Contains(err.Error(), "outside the frame") {
			test.Errorf("diagnostic = %v", err)
		}
	}
}

// An embedded layout may be named from a subreport nested inside another
// embedded layout, which is what makes a two-level nesting expressible.
func TestSubreportNestsTwoDeep(test *testing.T) {
	const src = `report name="host" {
  records { member "groups" type="list" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=800 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    embedded "outer" {
      records {
        member "title" type="string"
        member "items" type="list"
      }
      detail height=10 {
        field expr="title" left=0 right=0
        subreport embedded="inner" seq=1 data="items" inline=#true
      }
    }
    embedded "inner" {
      records { member "sku" type="string" }
      detail height=10 { field expr="sku" left=10 right=0 }
    }
    detail height=10 {
      field text="root" left=0 right=0
      subreport embedded="outer" seq=1 data="groups" inline=#true
    }
  }
}`
	rows := []map[string]any{{"groups": []any{
		map[string]any{"title": "g1", "items": []any{
			map[string]any{"sku": "a"}, map[string]any{"sku": "b"},
		}},
		map[string]any{"title": "g2", "items": []any{map[string]any{"sku": "c"}}},
	}}}
	doc := buildString(test, src, rows)
	got := joined(allLines(doc))
	if want := "root,g1,a,b,g2,c"; got != want {
		test.Errorf("marks = %q, want %q", got, want)
	}
}

// A check resolves the fonts of the templates a subreport names, not only
// the host's: a template that half resolves is not a template that resolves.
func TestCheckFontsReachesSubreportTemplates(test *testing.T) {
	dir := test.TempDir()
	writeTemplate(test, dir, "lines.kdl", `report name="lines" {
  records { member "sku" type="string" }
  font "body"  file="`+fontFile(test, "Go-Regular.ttf")+`" size=8
  font "heavy" file="`+fontFile(test, "Go-Bold.ttf")+`" size=14
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    detail height=10 { field expr="sku" left=0 right=0 { style font="heavy" } }
  }
}`)
	host := writeTemplate(test, dir, "host.kdl", `report name="host" {
  records { member "items" type="list" }
  font "body" file="`+fontFile(test, "Go-Regular.ttf")+`" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    detail height=10 {
      subreport template="lines.kdl" seq=1 data="items" inline=#true
    }
  }
}`)
	tpl, err := LoadTemplate(host)
	if err != nil {
		test.Fatal(err)
	}
	check, err := tpl.CheckFonts(StrictFonts())
	if err != nil {
		test.Fatal(err)
	}
	if len(check.Failures) != 0 {
		test.Fatalf("failures = %v", check.Failures)
	}
	// "body" is one face named by both templates, so it is reported once;
	// "heavy" belongs to the subreport alone and is reported too.
	names := make([]string, 0, len(check.Fonts))
	for _, entry := range check.Fonts {
		names = append(names, entry.Name)
	}
	if joined(names) != "body,heavy" {
		test.Errorf("fonts = %v, want the host's and the subreport's, deduplicated", names)
	}
}

// And a font the subreport names that does not resolve is a failure
// of the check, with the font named.
func TestCheckFontsReportsASubreportFailure(test *testing.T) {
	dir := test.TempDir()
	writeTemplate(test, dir, "lines.kdl", `report name="lines" {
  records { member "sku" type="string" }
  font "missing" typeface="Nothing Named This" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="missing" color="black"
    detail height=10 { field expr="sku" left=0 right=0 }
  }
}`)
	host := writeTemplate(test, dir, "host.kdl", `report name="host" {
  records { member "items" type="list" }
  font "body" file="`+fontFile(test, "Go-Regular.ttf")+`" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    detail height=10 {
      subreport template="lines.kdl" seq=1 data="items" inline=#true
    }
  }
}`)
	tpl, err := LoadTemplate(host)
	if err != nil {
		test.Fatal(err)
	}
	check, err := tpl.CheckFonts(StrictFonts())
	if err != nil {
		test.Fatal(err)
	}
	if len(check.Failures) != 1 {
		test.Fatalf("failures = %v, want the subreport's unresolved font", check.Failures)
	}
	if !strings.Contains(check.Failures[0], "missing") {
		test.Errorf("failure = %q", check.Failures[0])
	}
}

// A band's printwhen is answered once, where the band is,
// and the answer decides the band and its subreports together.
//
// A negative-seq subreport runs between the question and the band's own
// measurement, and it moves the fill position, so a condition reading
// VERTICAL_SPACE used to answer differently at each: the second row's
// line items printed with no row above them.
func TestPrintwhenIsAnsweredOnceAroundASubreport(test *testing.T) {
	const src = `report name="host" {
  records {
    member "name"  type="string"
    member "items" type="list"
  }
  font "body" file="Go-Regular.ttf" size=8
  layout width=200 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    embedded "lines" {
      records { member "sku" type="string" }
      detail height=20 { field expr="sku" left=10 right=0 }
    }
    header height=12 { field text="HEADER" left=0 right=0 }
    detail height=12 printwhen="VERTICAL_SPACE > 320" {
      field expr="name" left=0 right=0
      subreport embedded="lines" seq=-1 data="items" inline=#true
    }
  }
}`
	rows := []map[string]any{
		{"name": "alpha", "items": []any{map[string]any{"sku": "x"}}},
		{"name": "beta", "items": []any{map[string]any{"sku": "y"}}},
	}
	doc := buildString(test, src, rows)

	// Whatever the condition decides, a row and its line items agree.
	// The seq is negative, so each line item is immediately followed by
	// its own row; what must never happen is a line item with no row after it.
	got := allLines(doc)
	rowOf := map[string]string{"x": "alpha", "y": "beta"}
	items := 0
	for index, line := range got {
		want, ok := rowOf[line]
		if !ok {
			continue
		}
		items++
		if index+1 >= len(got) || got[index+1] != want {
			test.Errorf("marks = %v: %q printed without %q after it", got, line, want)
		}
	}
	if items == 0 {
		test.Errorf("marks = %v: the fixture must place at least one row", got)
	}
}

// scopedHost has an embedded layout nested inside another, and the
// OUTER marker replaced by whatever the top-level detail band invokes.
const scopedHost = `report name="host" {
  records { member "items" type="list" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    embedded "outer" {
      records { member "sku" type="string" }
      embedded "private" {
        records { member "sku" type="string" }
        detail height=10 { field expr="'private ' + sku" left=20 right=0 }
      }
      detail height=10 {
        field expr="'outer ' + sku" left=10 right=0
        subreport embedded="private" seq=1 data="[{'sku': sku}]" inline=#true
      }
    }
    detail height=10 {
      field text="root" left=0 right=0
      subreport embedded="TARGET" seq=1 data="items" inline=#true
    }
  }
}`

func scopedTemplate(target string) string {
	return strings.Replace(scopedHost, `embedded="TARGET"`, `embedded="`+target+`"`, 1)
}

// A layout nested inside another is private to it: a subreport outside
// cannot name it, and the diagnostic says so rather than silently running it.
func TestNestedEmbeddedLayoutIsPrivate(test *testing.T) {
	_, err := ParseTemplate("example/fonts/test.kdl", scopedTemplate("private"))
	if err == nil {
		test.Fatal("want the out-of-scope reference refused")
	}
	if !strings.Contains(err.Error(), "in scope") {
		test.Errorf("diagnostic = %v", err)
	}
}

// From inside its parent it resolves, and so does the parent itself.
func TestNestedEmbeddedLayoutIsInScopeInsideItsParent(test *testing.T) {
	doc := buildString(test, scopedTemplate("outer"), []map[string]any{
		{"items": []any{map[string]any{"sku": "a"}}},
	})
	got := joined(allLines(doc))
	if want := "root,outer a,private a"; got != want {
		test.Errorf("marks = %q, want %q", got, want)
	}
}

// Validation and the engine resolve a name the same way, because the name
// is resolved once, at load, and the node keeps what it resolved to.
//
// A nested layout that shadows an outer one of the same name is refused,
// so the case where the two could disagree cannot be written.
func TestEmbeddedLayoutNamesDoNotShadow(test *testing.T) {
	src := strings.Replace(scopedTemplate("lines"),
		`embedded "private" {`, `embedded "lines" {`, 1)
	src = strings.Replace(src, `embedded="private"`, `embedded="lines"`, 1)
	src = strings.Replace(src, `embedded "outer" {`,
		`embedded "lines" {
      records { member "sku" type="string" }
      detail height=10 { field text="top level" left=0 right=0 }
    }
    embedded "outer" {`, 1)
	_, err := ParseTemplate("example/fonts/test.kdl", src)
	if err == nil {
		test.Fatal("want the shadowing name refused")
	}
	if !strings.Contains(err.Error(), "shadow") {
		test.Errorf("diagnostic = %v", err)
	}
}

// Two embedded layouts side by side may not share a name, at any depth.
func TestDuplicateEmbeddedSiblingsAreRefused(test *testing.T) {
	src := strings.Replace(scopedTemplate("outer"),
		`embedded "private" {
        records { member "sku" type="string" }
        detail height=10 { field expr="'private ' + sku" left=20 right=0 }
      }`,
		`embedded "private" {
        records { member "sku" type="string" }
        detail height=10 { field expr="'private ' + sku" left=20 right=0 }
      }
      embedded "private" {
        records { member "sku" type="string" }
        detail height=10 { field text="second" left=20 right=0 }
      }`, 1)
	_, err := ParseTemplate("example/fonts/test.kdl", src)
	if err == nil {
		test.Fatal("want the duplicate nested name refused")
	}
	if !strings.Contains(err.Error(), "duplicate embedded layout") {
		test.Errorf("diagnostic = %v", err)
	}
}

// Two unrelated layouts may each keep a private one of the same name:
// neither is in the other's scope, so there is nothing to confuse.
func TestPrivateEmbeddedNamesMayRepeatAcrossScopes(test *testing.T) {
	const src = `report name="host" {
  records { member "items" type="list" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    embedded "first" {
      records { member "sku" type="string" }
      embedded "rows" {
        records { member "sku" type="string" }
        detail height=10 { field expr="'one ' + sku" left=10 right=0 }
      }
      detail height=10 {
        subreport embedded="rows" seq=1 data="[{'sku': sku}]" inline=#true
      }
    }
    embedded "second" {
      records { member "sku" type="string" }
      embedded "rows" {
        records { member "sku" type="string" }
        detail height=10 { field expr="'two ' + sku" left=10 right=0 }
      }
      detail height=10 {
        subreport embedded="rows" seq=1 data="[{'sku': sku}]" inline=#true
      }
    }
    detail height=10 {
      subreport embedded="first"  seq=1 data="items" inline=#true
      subreport embedded="second" seq=2 data="items" inline=#true
    }
  }
}`
	doc := buildString(test, src, []map[string]any{
		{"items": []any{map[string]any{"sku": "a"}}},
	})
	if got := joined(allLines(doc)); got != "one a,two a" {
		test.Errorf("marks = %q, want each private layout to run its own", got)
	}
}

// A template two subreports both name is read once, so its warnings
// arrive once and a diamond of includes costs one read per file
// rather than one per path through the graph.
func TestSharedSubreportTemplateIsReadOnce(test *testing.T) {
	dir := test.TempDir()
	// A layout that warns: a band with no height whose only element
	// takes its height from the band collapses to nothing.
	writeTemplate(test, dir, "lines.kdl", `report name="lines" {
  records { member "sku" type="string" }
  font "body" file="`+fontFile(test, "Go-Regular.ttf")+`" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    detail { field expr="sku" left=0 right=0 }
  }
}`)
	host := writeTemplate(test, dir, "host.kdl", `report name="host" {
  records { member "items" type="list" }
  font "body" file="`+fontFile(test, "Go-Regular.ttf")+`" size=8
  layout width=300 height=400 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    title height=10 {
      field text="t" left=0 right=0
      subreport template="lines.kdl" seq=1 data="[]" inline=#true
    }
    detail height=10 {
      subreport template="lines.kdl" seq=1 data="items" inline=#true
    }
  }
}`)
	tpl, err := LoadTemplate(host)
	if err != nil {
		test.Fatal(err)
	}
	warnings := tpl.Warnings()
	if len(warnings) != 1 {
		test.Errorf("warnings = %v, want the referenced template's one, once", warnings)
	}
}

// An inline subreport may open a columns block. Its frames hang off the host's
// for the length of the invocation, beginning where the host is filled rather
// than where its frame begins, and its column header is measured in the child's
// own context -- here reading a parameter the host passed and the column it is
// opening.
func TestInlineSubreportColumns(test *testing.T) {
	const src = `report name="host" {
  records { member "n" type="string"; member "items" type="list" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=120 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    embedded "lines" {
      records { member "sku" type="string" }
      parameter "who" type="string"
      columns count=2 gap=10 {
        header height=10 { field expr="who + '/' + str(COLUMN_NUMBER)" left=0 right=0 }
      }
      detail height=10 { field expr="sku" left=0 right=0 }
    }
    detail height=10 {
      field expr="n" left=0 right=0
      subreport embedded="lines" seq=1 data="items" inline=#true { arg "who" value="n" }
    }
  }
}`
	var rows []map[string]any
	for _, name := range []string{"A", "B"} {
		var items []any
		for index := 1; index <= 5; index++ {
			items = append(items, map[string]any{"sku": name + string(rune('0'+index))})
		}
		rows = append(rows, map[string]any{"n": name, "items": items})
	}
	doc := buildString(test, src, rows)
	if len(doc.Pages) != 2 {
		test.Fatalf("pages = %d, want 2", len(doc.Pages))
	}
	// A's five rows follow the host's band at 20 under a header of their own.
	// B starts at 80, so its columns start there too and the second one opens
	// at 90 rather than at the top of the host's frame, which is the host's.
	if got, want := joined(placements(&Printout{Pages: doc.Pages[:1]})),
		"A@10,10,A/1@10,20,A1@10,30,A2@10,40,A3@10,50,A4@10,60,A5@10,70,"+
			"B@10,80,B/1@10,90,B1@10,100,B/2@155,90,B2@155,100"; got != want {
		test.Errorf("page 1 = %q", got)
	}
	// The page break puts the child's frames back at the top of the host's.
	if got, want := joined(placements(&Printout{Pages: doc.Pages[1:]})),
		"B/1@10,10,B3@10,20,B4@10,30,B5@10,40"; got != want {
		test.Errorf("page 2 = %q", got)
	}
}

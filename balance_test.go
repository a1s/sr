package sr

import (
	"fmt"
	"strings"
	"testing"
)

// placements is every text mark as content@x,y, page by page,
// which is what a balanced layout has to be compared by.
func placements(doc *Printout) []string {
	var out []string
	for _, page := range doc.Pages {
		for _, text := range texts(page) {
			out = append(out, fmt.Sprintf("%s@%g,%g",
				strings.Join(text.Lines, "|"), text.Box.Left, text.Box.Top))
		}
	}
	return out
}

// balanceSource is a two-column layout of 10 pt rows on a page whose frame
// is 100 pt tall, so a column holds ten of them and the second column starts
// at x = 10 + 135 + 10 = 155. The bands and the balance flag are substituted.
func balanceSource(balance bool, bands string) string {
	return fmt.Sprintf(`report name="t" {
  records { member "n" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=120 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    columns count=2 gap=10 balance=#%t { %s }
    detail height=10 { field expr="n" left=0 right=0 }
  }
}`, balance, bands)
}

// letters is n single-letter records, a, b, c and so on.
func letters(count int) []map[string]any {
	values := make([]any, count)
	for index := range values {
		values[index] = string(rune('a' + index))
	}
	return rowsOf(values...)
}

// The last run of bands is spread over the columns it filled,
// instead of leaving the last one short.
func TestBalancedColumnsSpreadTheLastFragment(test *testing.T) {
	rows := letters(12)
	ragged := buildString(test, balanceSource(false, ""), rows)
	if got, want := joined(placements(ragged)),
		"a@10,10,b@10,20,c@10,30,d@10,40,e@10,50,f@10,60,"+
			"g@10,70,h@10,80,i@10,90,j@10,100,k@155,10,l@155,20"; got != want {
		test.Fatalf("unbalanced = %q, want %q", got, want)
	}
	balanced := buildString(test, balanceSource(true, ""), rows)
	if got, want := joined(placements(balanced)),
		"a@10,10,b@10,20,c@10,30,d@10,40,e@10,50,f@10,60,"+
			"g@155,10,h@155,20,i@155,30,j@155,40,k@155,50,l@155,60"; got != want {
		test.Errorf("balanced = %q, want six rows in each column", got)
	}
}

// Only the last page balances: a page the content filled is already even.
func TestBalanceLeavesFilledPagesAlone(test *testing.T) {
	doc := buildString(test, balanceSource(true, ""), letters(26))
	if len(doc.Pages) != 2 {
		test.Fatalf("pages = %d, want 2", len(doc.Pages))
	}
	if got, want := joined(lines(doc.Pages[0])),
		"a,b,c,d,e,f,g,h,i,j,k,l,m,n,o,p,q,r,s,t"; got != want {
		test.Errorf("page 1 = %q, want twenty rows filling both columns", got)
	}
	if got, want := joined(placements(&Printout{Pages: doc.Pages[1:]})),
		"u@10,10,v@10,20,w@10,30,x@155,10,y@155,20,z@155,30"; got != want {
		test.Errorf("page 2 = %q, want three rows in each column", got)
	}
}

// A column the fill never reached is still open to the balance,
// which is the case the fill leaves emptiest.
func TestBalanceOpensAColumnTheFillNeverReached(test *testing.T) {
	rows := letters(6)
	if got, want := joined(allLines(buildString(test, balanceSource(false, ""), rows))),
		"a,b,c,d,e,f"; got != want {
		test.Fatalf("unbalanced = %q", got)
	}
	doc := buildString(test, balanceSource(true, ""), rows)
	if got, want := joined(placements(doc)),
		"a@10,10,b@10,20,c@10,30,d@155,10,e@155,20,f@155,30"; got != want {
		test.Errorf("balanced = %q, want the second column opened", got)
	}
}

// A column header is placed as its column opens, against the context
// at moment, and balancing cannot place one afterwards. So a frame
// that has one balances between the columns it opened and no further.
func TestBalanceWillNotOpenAColumnUnderAHeader(test *testing.T) {
	const header = `header height=10 { field text="CH" left=0 right=0 }`

	// Six rows never leave the first column, and stay there.
	if got, want := joined(placements(
		buildString(test, balanceSource(true, header), letters(6)))),
		"CH@10,10,a@10,20,b@10,30,c@10,40,d@10,50,e@10,60,f@10,70"; got != want {
		test.Errorf("one column = %q, want the fill left alone", got)
	}

	// Eleven reach the second, and balance across the two.
	if got, want := joined(placements(
		buildString(test, balanceSource(true, header), letters(11)))),
		"CH@10,10,a@10,20,b@10,30,c@10,40,d@10,50,e@10,60,f@10,70,"+
			"g@155,20,h@155,30,i@155,40,CH@155,10,j@155,50,k@155,60"; got != want {
		test.Errorf("two columns = %q, want six and five under their headers", got)
	}
}

// What comes after balanced columns starts immediately below the deepest
// of them, not at the bottom the ragged fill would have reached.
func TestWhatFollowsBalancedColumnsStartsBelowThem(test *testing.T) {
	const summary = `summary height=10 { field text="SUM" left=0 right=0 }`
	source := func(balance bool) string {
		return strings.Replace(balanceSource(balance, ""),
			`detail height=10 { field expr="n" left=0 right=0 }`,
			`detail height=10 { field expr="n" left=0 right=0 }`+"\n    "+summary, 1)
	}
	rows := letters(12)

	// The ragged fill reaches the bottom of the first column, so the frame
	// containing it is full and the summary is pushed onto a second page.
	ragged := buildString(test, source(false), rows)
	if got, want := placementOf(placements(ragged), "SUM"), "SUM@10,10"; got != want {
		test.Fatalf("unbalanced summary = %q, want %q", got, want)
	}
	if len(ragged.Pages) != 2 {
		test.Fatalf("unbalanced pages = %d, want the summary on a page of its own",
			len(ragged.Pages))
	}

	balanced := buildString(test, source(true), rows)
	if got, want := placementOf(placements(balanced), "SUM"), "SUM@10,70"; got != want {
		test.Errorf("balanced summary = %q, want it under the balanced columns", got)
	}
	if len(balanced.Pages) != 1 {
		test.Errorf("balanced pages = %d, want 1", len(balanced.Pages))
	}
}

// placementOf is the one placement whose content is name.
func placementOf(all []string, name string) string {
	for _, item := range all {
		if strings.HasPrefix(item, name+"@") {
			return item
		}
	}
	return ""
}

// What balancing refuses to move. In each case the page is laid out
// exactly as it would be with balance off: moving a band would answer
// something that was already answered, or leave something behind.
func TestBalanceIsRefused(test *testing.T) {
	cases := []struct {
		name    string
		members string
		detail  string
		extra   string
		rows    []map[string]any
	}{
		{
			// The eject node put the band where it is for a reason of its own,
			// and packing by height would undo it.
			name:    "an eject node",
			members: `member "n" type="string"`,
			detail: `detail height=10 {
      eject type="column" when="ITEM_NUMBER == 3"
      field expr="n" left=0 right=0 }`,
			rows: letters(6),
		},
		{
			// Resolved when the column ends, against the column it ended in.
			name:    "a column deferral",
			members: `member "n" type="string"`,
			detail: `detail height=10 {
      field expr="'%s/%d' % (n, FINAL.COLUMN_COUNT)" evaltime="column" left=0 right=0 }`,
			rows: letters(6),
		},
		{
			// The inner columns fill side by side, so the bands do not
			// reach the outer frame in the order the page reads them.
			name:    "a columns block inside it",
			members: `member "n" type="string"`,
			detail: `group "g" expr="'one'" {
      columns count=2 gap=5 { }
      detail height=10 { field expr="n" left=0 right=0 } }`,
			rows: letters(6),
		},
		{
			// The child's bands are its own, and the host cannot carry them.
			name:    "an inline subreport",
			members: `member "n" type="string"; member "items" type="list"`,
			extra: `embedded "lines" { records { member "sku" type="string" }
      detail height=10 { field expr="sku" left=0 right=0 } }`,
			detail: `detail height=10 {
      field expr="n" left=0 right=0
      subreport embedded="lines" seq=1 data="items" inline=#true }`,
			rows: []map[string]any{
				{"n": "a", "items": []any{map[string]any{"sku": "a1"}}},
				{"n": "b", "items": []any{map[string]any{"sku": "b1"}}},
				{"n": "c", "items": []any{map[string]any{"sku": "c1"}}},
			},
		},
	}
	for _, item := range cases {
		test.Run(item.name, func(test *testing.T) {
			source := func(balance bool) string {
				return fmt.Sprintf(`report name="t" {
  records { %s }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=120 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    %s
    columns count=2 gap=10 balance=#%t { }
    %s
  }
}`, item.members, item.extra, balance, item.detail)
			}
			plain := joined(placements(buildString(test, source(false), item.rows)))
			asked := joined(placements(buildString(test, source(true), item.rows)))
			if plain != asked {
				test.Errorf("balanced = %q, want it left as %q", asked, plain)
			}
		})
	}
}

// A split band is cut at a column edge, so the two halves cannot be moved
// apart from it. The page is laid out as it would be with balance off.
func TestBalanceIsRefusedByASplitBand(test *testing.T) {
	source := func(balance bool) string {
		return fmt.Sprintf(`report name="t" {
  records { member "n" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=80 leftmargin=10 rightmargin=10 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    columns count=2 gap=10 balance=#%t { }
    detail split=#true {
      field expr="n" stretch=#true align="justified" left=0 width=60
    }
  }
}`, balance)
	}
	rows := []map[string]any{
		{"n": strings.TrimSpace(strings.Repeat("alpha beta gamma delta ", 3))},
	}
	plain := joined(placements(buildString(test, source(false), rows)))
	asked := joined(placements(buildString(test, source(true), rows)))
	if plain != asked {
		test.Errorf("balanced = %q, want it left as %q", asked, plain)
	}
}

// Balancing spreads content between columns, so it needs more than one.
func TestBalanceNeedsMoreThanOneColumn(test *testing.T) {
	src := `report name="t" {
  records { member "n" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=120 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    columns count=1 balance=#true { }
    detail height=10 { field expr="n" left=0 right=0 }
  }
}`
	_, err := ParseTemplate("example/fonts/test.kdl", src)
	if err == nil {
		test.Fatal("want balance refused on a single column")
	}
	if !strings.Contains(err.Error(), "only one") {
		test.Errorf("diagnostic = %v", err)
	}
}

// The columns are evened by height rather than by the number of bands.
func TestBalanceEvensHeightsRatherThanCounts(test *testing.T) {
	// The rectangle makes a band 40 pt tall instead of 10, for two records
	// out of five: 110 pt of bands for a 100 pt frame.
	src := `report name="t" {
  records { member "n" type="string" }
  font "body" file="Go-Regular.ttf" size=8
  layout width=300 height=120 leftmargin=10 rightmargin=10 topmargin=10 bottommargin=10 {
    style font="body" color="black"
    columns count=2 gap=10 balance=#true { }
    detail height=10 {
      field expr="n" left=0 top=0 width=20 height=10
      rectangle printwhen="n in ('a', 'b')" left=0 top=0 width=10 height=40
    }
  }
}`
	rows := letters(5)

	// The fill puts a, b, c and d on the left, 110 pt deep, and e on the right.
	// Evened, the left keeps a alone: the shallowest the two 40 pt bands can
	// reach is one in each column, which puts four bands on the right against
	// one on the left and takes the deepest column from 110 pt to 80.
	if got, want := joined(placements(buildString(test, src, rows))),
		"a@10,10,b@155,10,c@155,50,d@155,60,e@155,70"; got != want {
		test.Errorf("balanced = %q, want the heights evened", got)
	}
}

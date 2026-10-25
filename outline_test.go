package sr

import (
	"strings"
	"testing"

	"github.com/a1s/sr/printout"
)

// outlineEntries lists a printout's outline marks in document order.
func outlineEntries(doc *Printout) []*printout.Outline {
	var out []*printout.Outline
	for _, page := range doc.Pages {
		for _, mark := range page.Marks {
			if entry, ok := mark.(*printout.Outline); ok {
				out = append(out, entry)
			}
		}
	}
	return out
}

// An entry's level is written as its depth in the tree,
// which is not always the level its template declared.
//
// doc/printout.md#outline: an entry's parent is the nearest entry
// before it whose declared level is lower. Declared 1, 4, 3, the
// second and the third are both the first one's children, and are
// written 1, 2, 2. The tenth invariant then holds for a reader,
// who meets no gap.
func TestOutlineLevelsAreWrittenAsDepths(test *testing.T) {
	const src = `report name="t" {
  records { member "level" type="int" }
  font "body" file="Go-Regular.ttf" size=9
  layout width=300 height=400 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 {
    style font="body" color="black"
    detail height=12 {
      outline title="'entry'" level=1 when="level == 1"
      outline title="'entry'" level=4 when="level == 4"
      outline title="'entry'" level=3 when="level == 3"
      field text="x" left=0 right=0
    }
  }
}`
	out := buildString(test, src, []map[string]any{
		{"level": 1}, {"level": 4}, {"level": 3},
	})
	entries := outlineEntries(out)
	if len(entries) != 3 {
		test.Fatalf("got %d entries, want 3", len(entries))
	}
	want := []int{1, 2, 2}
	for index, entry := range entries {
		if entry.Level != want[index] {
			test.Errorf("entry %d level = %d, want %d",
				index, entry.Level, want[index])
		}
	}
	if err := out.Validate(); err != nil {
		test.Errorf("the printout breaks its own invariants: %v", err)
	}
}

// A name is written only where an xref targets it.
// The field exists so that a link can find its entry, and
// an entry nothing links to has nothing to be found by.
func TestOutlineNamesAreWrittenOnlyWhereTargeted(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=300 height=400 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 {
    style font="body" color="black"
    title height=12 {
      outline title="'top'" level=1 name="'wanted'"
      field text="t" left=0 right=0
    }
    detail height=12 {
      outline title="'row'" level=2 name="'ignored'"
      xref type="outline" target="'wanted'" height=10 {
        field text="x" left=0 right=0
      }
    }
  }
}`
	out := buildString(test, src, []map[string]any{{"n": 1}})
	entries := outlineEntries(out)
	if len(entries) != 2 {
		test.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Name != "wanted" {
		test.Errorf("the targeted entry's name = %q, want %q",
			entries[0].Name, "wanted")
	}
	if entries[1].Name != "" {
		test.Errorf("an untargeted name is written: %q", entries[1].Name)
	}
}

// An xref to an entry no entry carries is a build error. The printout
// would break its fifth invariant, and a renderer refuses such an xref:
// a region that looks clickable and is not is worse than a failed build.
func TestXrefToAMissingOutlineEntryIsAnError(test *testing.T) {
	const src = `report name="t" {
  records { member "want" type="string" }
  font "body" file="Go-Regular.ttf" size=9
  layout width=300 height=400 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 {
    style font="body" color="black"
    detail height=12 {
      outline title="'row'" level=1 name="'here'"
      xref type="outline" target="want" height=10 {
        field text="x" left=0 right=0
      }
    }
  }
}`
	err := buildStringErr(test, src, []map[string]any{{"want": "nowhere"}})
	if err == nil {
		test.Fatal("want the unreachable target reported")
	}
	if !strings.Contains(err.Error(), "nowhere") {
		test.Errorf("diagnostic = %v, want it to name the target", err)
	}
	if !strings.Contains(err.Error(), "record 0") {
		test.Errorf("diagnostic = %v, want it to name the record", err)
	}
}

// A target the data supplies is held to the document rather than
// to the template, so an entry built from the same data satisfies it.
func TestXrefToAComputedOutlineNameResolves(test *testing.T) {
	const src = `report name="t" {
  records { member "key" type="string" }
  font "body" file="Go-Regular.ttf" size=9
  layout width=300 height=400 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 {
    style font="body" color="black"
    detail height=12 {
      outline title="'row'" level=1 name="key"
      xref type="outline" target="key" height=10 {
        field text="x" left=0 right=0
      }
    }
  }
}`
	out := buildString(test, src, []map[string]any{{"key": "one"}})
	if err := out.Validate(); err != nil {
		test.Errorf("the printout breaks its own invariants: %v", err)
	}
	entries := outlineEntries(out)
	if len(entries) != 1 || entries[0].Name != "one" {
		test.Errorf("entries = %+v, want one named \"one\"", entries)
	}
}

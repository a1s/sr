package sr

import (
	"strings"
	"testing"

	"github.com/a1s/sr/printout"
)

const unknownNamesTemplate = `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=200 height=200 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 {
    style font="body" color="black"
    detail height=20 wobble=#true {
      field text="x" left=0 right=0
      sprocket teeth=12 { cog }
    }
  }
}`

// A name the format does not define reaches the printout header
// as a warning of kind "unknown".
//
// The template still builds: the name carries no meaning, so an engine
// meeting a template written for a newer or a different implementation
// reads the parts it knows and reports the rest. The report is in the
// artifact, so a build that was not watched can still be asked about it.
func TestUnknownNamesWarnAndBuild(test *testing.T) {
	out := buildString(test, unknownNamesTemplate, []map[string]any{{"n": 1}})
	var kinds []string
	for _, warning := range out.Header.Warnings {
		if warning.Kind != printout.WarnUnknown {
			continue
		}
		kinds = append(kinds, warning.Message)
		if warning.Node == "" {
			test.Errorf("an unknown warning names no node: %q", warning.Message)
		}
		if warning.Record != nil {
			test.Errorf("record = %d; a name is found before any data is read",
				*warning.Record)
		}
	}
	if len(kinds) != 2 {
		test.Fatalf("want a warning for the property and the node, got %q", kinds)
	}
	// An unknown node is reported once and then not read,
	// so nothing inside it is reported: one node, one diagnostic,
	// however much it contains.
	joined := strings.Join(kinds, "\n")
	for _, want := range []string{"`wobble`", "`sprocket`"} {
		if !strings.Contains(joined, want) {
			test.Errorf("want a warning naming %s, got %q", want, joined)
		}
	}
	for _, unwanted := range []string{"`teeth`", "`cog`"} {
		if strings.Contains(joined, unwanted) {
			test.Errorf("an unknown node's contents were read: %q", joined)
		}
	}
}

// StrictNames makes every unknown name an error.
func TestStrictNamesRefusesThem(test *testing.T) {
	_, err := ParseTemplate("example/fonts/test.kdl", unknownNamesTemplate,
		StrictNames())
	if err == nil {
		test.Fatal("want the unknown names refused")
	}
	if !strings.Contains(err.Error(), "wobble") {
		test.Errorf("diagnostic = %v", err)
	}
}

// A registered name is accepted in silence, under strictness too:
// it is already a statement that the name is deliberate,
// which is the only question strictness asks.
func TestAcceptedNamesAreSilent(test *testing.T) {
	tpl, err := ParseTemplate("example/fonts/test.kdl", unknownNamesTemplate,
		StrictNames(), AcceptName("wobble", "sprocket"))
	if err != nil {
		test.Fatalf("want the registered names accepted: %v", err)
	}
	if warnings := tpl.Warnings(); len(warnings) != 0 {
		test.Errorf("warnings = %q, want none", warnings)
	}
}

// An `x-` name is reserved for another implementation
// and says so for itself, so it needs no registration.
func TestReservedNamesAreSilent(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=200 height=200 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 x-vendor-mode="duplex" {
    style font="body" color="black"
    detail height=20 { field text="x" left=0 right=0 }
  }
}`
	tpl, err := ParseTemplate("example/fonts/test.kdl", src, StrictNames())
	if err != nil {
		test.Fatalf("want the reserved name accepted: %v", err)
	}
	if warnings := tpl.Warnings(); len(warnings) != 0 {
		test.Errorf("warnings = %q, want none", warnings)
	}
}

// A near miss is an error, because a misspelling is far more often what
// it is than an extension, and the cost of guessing wrong is silent:
// the element paints on every record instead of the ones printwhen
// would have kept, and the page looks entirely plausible.
func TestANearMissIsAnError(test *testing.T) {
	const src = `report name="t" {
  font "body" file="Go-Regular.ttf" size=9
  layout width=200 height=200 leftmargin=5 rightmargin=5 topmargin=5 bottommargin=5 {
    style font="body" color="black"
    detail height=20 { field text="x" left=0 right=0 printwhn="True" }
  }
}`
	_, err := ParseTemplate("example/fonts/test.kdl", src)
	if err == nil {
		test.Fatal("want the near miss refused")
	}
	if !strings.Contains(err.Error(), "did you mean `printwhen`?") {
		test.Errorf("diagnostic = %v, want it to suggest the name", err)
	}
}

package build

import (
	"fmt"

	"github.com/a1s/sr/internal/tmpl"
	"github.com/a1s/sr/printout"
)

// FontCheck is what resolving a template's fonts found out.
type FontCheck struct {
	// Fonts are the fonts that resolved, sorted by name
	// as a printout header holds them.
	Fonts []printout.FontEntry
	// Failures name the fonts that did not resolve, one message each.
	Failures []string
	// Warnings are diagnostics about the document: a typeface that reached
	// the substitute, a face whose style bits contradict the declaration.
	Warnings []string
	// Diagnostics describe the machine rather than the document:
	// a font file that could not be read, two faces claiming one identity.
	Diagnostics []string
}

// Fonts resolves every font a template declares, without any data.
//
// It is what a template check works from. Everything else in a template
// is settled at load, from the document alone; font resolution is the one part
// that depends on the machine, so it is the one part a check has to run.
//
// A font that does not resolve is collected rather than returned,
// so that one missing typeface does not hide the next.
func Fonts(report *tmpl.Report, opts Options) (*FontCheck, error) {
	eng := newEngine(report, opts)
	if err := eng.bindParams(false); err != nil {
		return nil, err
	}
	if err := eng.bindVariables(); err != nil {
		return nil, err
	}
	check := &FontCheck{}
	for _, def := range report.Fonts {
		if _, err := eng.face(def.Name); err != nil {
			check.Failures = append(check.Failures,
				fmt.Sprintf("font %q: %v", def.Name, err))
		}
	}
	check.Fonts = eng.out.Header.Fonts
	check.Warnings = eng.resolver.Warnings
	check.Diagnostics = eng.resolver.Diagnostics
	return check, nil
}

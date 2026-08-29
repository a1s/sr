package build

import (
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
	check := &FontCheck{}
	if err := eng.checkFonts(check); err != nil {
		return nil, err
	}

	// A subreport in a file of its own declares its own fonts,
	// against its own base directory, and a build will need them.
	// Checking only the host would say a template resolves
	// when half of what it prints does not.
	for _, child := range externalTemplates(report) {
		sub := eng.nested(newUnit(child, opts, eng.doc))
		if err := sub.checkFonts(check); err != nil {
			return nil, err
		}
	}

	check.Fonts = eng.out.Header.Fonts
	for _, resolver := range eng.doc.resolvers {
		check.Warnings = append(check.Warnings, resolver.Warnings...)
		check.Diagnostics = append(check.Diagnostics, resolver.Diagnostics...)
	}
	return check, nil
}

// checkFonts resolves one template's fonts into the shared table.
func (eng *engine) checkFonts(check *FontCheck) error {
	if err := eng.bindParams(false); err != nil {
		return err
	}
	if err := eng.bindVariables(); err != nil {
		return err
	}
	for _, def := range eng.report.Fonts {
		// Every error out of face already names the font, so this adds nothing.
		if _, err := eng.face(def.Name); err != nil {
			check.Failures = append(check.Failures, err.Error())
		}
	}
	return nil
}

// externalTemplates is every template a subreport names by file,
// reachable from this one, each once and outermost first.
func externalTemplates(report *tmpl.Report) []*tmpl.Report {
	var out []*tmpl.Report
	seen := map[*tmpl.Report]bool{report: true}
	var walk func(*tmpl.Report)
	walk = func(host *tmpl.Report) {
		tmpl.ForEachSection(host, func(section *tmpl.Section) {
			for _, sub := range section.Subreports {
				if sub.Report == nil || seen[sub.Report] {
					continue
				}
				seen[sub.Report] = true
				out = append(out, sub.Report)
				walk(sub.Report)
			}
		})
	}
	walk(report)
	return out
}

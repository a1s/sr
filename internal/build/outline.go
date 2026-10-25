package build

import (
	"fmt"

	"github.com/a1s/sr/printout"
)

// Outline entries and the xrefs that point at them are settled once the
// document is complete, because neither question can be answered before.
//
// An entry's depth depends on the entries before it, which a band
// placed on page 1 does not know. Whether a name is pointed at depends
// on an xref that may be on any page, before the entry or after it.
// So this runs after the last page, over the pages in order, which is
// the order the outline tree is built in: see doc/printout.md#outline.

// xrefSite is an outline xref as it was built, for a diagnostic to name.
type xrefSite struct {
	target string
	// record is the record the band was built for, or -1 where no record
	// produced the band.
	record int
}

// settleOutline rewrites outline levels as depths, drops the names
// nothing points at, and refuses an xref whose target no entry carries.
//
// A printout that broke any of these would break its own invariants, which
// is a fault in the engine rather than something for a reader to discover.
func (eng *engine) settleOutline() error {
	entries := collectOutlines(eng.out)
	targeted := map[string]bool{}
	for _, site := range eng.doc.outlineXrefs {
		targeted[site.target] = true
	}

	// A name no xref points at is not written: the field exists so that
	// an xref can find its entry, and an entry nothing points at
	// has nothing to be found by.
	for _, entry := range entries {
		if entry.Name != "" && !targeted[entry.Name] {
			entry.Name = ""
		}
	}

	// The level a template declared is what the tree is built from,
	// and the depth is what is written. A template may skip a level,
	// and a band that would have been an entry's parent may not have printed,
	// so the two are not the same number.
	//
	// declared and depth are parallel stacks of the entries still open.
	var declared, depth []int
	for _, entry := range entries {
		for len(declared) > 0 && declared[len(declared)-1] >= entry.Level {
			declared = declared[:len(declared)-1]
			depth = depth[:len(depth)-1]
		}
		level := 1
		if len(depth) > 0 {
			level = depth[len(depth)-1] + 1
		}
		declared = append(declared, entry.Level)
		depth = append(depth, level)
		entry.Level = level
	}

	carried := map[string]bool{}
	for _, entry := range entries {
		if entry.Name != "" {
			carried[entry.Name] = true
		}
	}
	for _, site := range eng.doc.outlineXrefs {
		if carried[site.target] {
			continue
		}
		where := ""
		if site.record >= 0 {
			where = fmt.Sprintf(", built for record %d", site.record)
		}
		return fmt.Errorf(
			"an xref targets the outline entry '%s'%s, and no entry in the document carries that name",
			site.target, where)
	}
	return nil
}

// collectOutlines lists the document's outline entries in document order,
// which is page order and, within a page, paint order.
//
// An entry inside an xref is not a thing the format has:
// an xref holds drawn elements, and an outline entry is a band's.
func collectOutlines(doc *printout.Printout) []*printout.Outline {
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

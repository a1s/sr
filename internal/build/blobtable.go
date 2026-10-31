package build

import "github.com/a1s/sr/printout"

// Only entries something refers to are written.
//
// A `data` node nothing references does not appear, and neither does one a
// `field` consumed: a field's text reaches the printout in its mark's lines,
// so the blob behind it is not needed. An image with embed=#false contributes
// no entry either. Nor does an image whose band was built and never reached
// the page, though it took its name: a header or a footer is built once to
// reserve its space, before the bands that fill the page, and that build
// publishes a blob the page may never show.
//
// Names are settled while the document is built, because which name a
// blob holds depends on document order. Which entries are kept can only
// be settled at the end, when every mark that could refer to one exists.
// See doc/printout.md#data.
func (eng *engine) pruneBlobs() {
	table := eng.out.Header.Data
	if len(table) == 0 {
		return
	}
	wanted := map[string]bool{}
	for _, entry := range eng.out.Header.Fonts {
		if entry.ResolvedData != "" {
			wanted[entry.ResolvedData] = true
		}
	}
	for _, page := range eng.out.Pages {
		noteBlobRefs(page.Marks, wanted)
	}
	for name := range table {
		if !wanted[name] {
			delete(table, name)
		}
	}
}

// noteBlobRefs collects the blob names a run of marks refers to,
// descending into an xref, whose nested marks are marks like any other.
func noteBlobRefs(marks []printout.Mark, wanted map[string]bool) {
	for _, mark := range marks {
		switch typed := mark.(type) {
		case *printout.Image:
			if typed.Data != "" {
				wanted[typed.Data] = true
			}
		case *printout.Xref:
			noteBlobRefs(typed.Marks, wanted)
		}
	}
}

package build

import (
	"fmt"

	"github.com/a1s/sr/internal/expr"
	"github.com/a1s/sr/internal/geom"
	"github.com/a1s/sr/internal/tmpl"
	"github.com/a1s/sr/printout"
	"go.starlark.net/starlark"
)

// run is the whole build: the title, the record loop, the summary,
// and the closing footers.
func (eng *engine) run() error {
	layout := eng.report.Layout
	if eng.frames == nil {
		eng.frames = buildFrames(eng, layout)
	}

	for group := layout.Body.Group; group != nil; group = group.Group {
		eng.groups = append(eng.groups, group)
		eng.groupKey = append(eng.groupKey, nil)
		eng.groupOpen = append(eng.groupOpen, false)
	}

	// iter="report" fires once, before the title band is built,
	// so a report-scoped value seeded by init is available to it.
	if err := eng.ctx.reset(tmpl.ScopeReport, ""); err != nil {
		return err
	}
	if err := eng.ctx.iterate(tmpl.ScopeReport, ""); err != nil {
		return err
	}

	// A swapheader title is placed above the page header,
	// so the page has to know about it before its frames are opened.
	// An inline subreport prints on a page that is already open,
	// with a header that is already reserved, so it has neither to do.
	if !eng.inline {
		if title := layout.Body.Title; title != nil && title.SwapHeader {
			eng.swapTitle = title
		}
		if err := eng.startPage(); err != nil {
			return err
		}
	}
	if title := layout.Body.Title; title != nil {
		if err := eng.placeReportTitle(title); err != nil {
			return err
		}
	}

	for index := range eng.records {
		if err := eng.consume(index); err != nil {
			return err
		}
	}

	// The last record's detail is committed, then every remaining
	// group summary innermost-first, then the report's summary.
	if err := eng.closeGroups(len(eng.groups) - 1); err != nil {
		return err
	}

	// The frames have everything they are going to get, so a balanced one
	// spreads its last fragment now -- before the summary, which is placed
	// below it and has to start at the balanced bottom.
	eng.balanceOwn()

	if summary := layout.Body.Summary; summary != nil {
		if err := eng.placeSummary(summary); err != nil {
			return err
		}
	}

	// The closing footers are placed before anything is resolved,
	// because a footer registers deferrals of its own --
	// the page count in a page footer is one.
	if !eng.inline {
		if err := eng.closePage(); err != nil {
			return err
		}
	}

	// The last page, column and group end without an eject,
	// so their deferrals resolve here. For an inline subreport that is every
	// deferral it still holds: its report scope ends with the invocation, and
	// its page and column scopes belong to a host it cannot see the end of --
	// once the invocation is over it has no band left to contribute, so
	// nothing it registered is still outstanding. A deferred value that
	// has to read the host's final page state belongs on a host band.
	if err := eng.resolveScope("column"); err != nil {
		return err
	}
	if err := eng.resolveScope("page"); err != nil {
		return err
	}
	for _, group := range eng.groups {
		if err := eng.resolveScope(group.Name); err != nil {
			return err
		}
	}
	if err := eng.resolveScope("report"); err != nil {
		return err
	}

	// The report-scoped reset is nominal: nothing is built after it.
	return eng.ctx.reset(tmpl.ScopeReport, "")
}

// startPage opens a page.
//
// Reserve the header and footer of every frame,
// and place the headers outermost first.
func (eng *engine) startPage() error {
	sheet := &printout.Page{Kind: "page", Number: eng.ctx.pages.number}
	sheet.SetGeometry(eng.pageGeometry(), eng.out.Header.Page)
	eng.doc.page = sheet
	eng.out.Pages = append(eng.out.Pages, sheet)

	page := eng.frames.page
	page.outerTop = eng.report.Layout.TopMargin
	eng.frames.walk(func(fr *frame) {
		fr.column = 0
		// A balanced fragment is what one page holds, so it starts here,
		// and a grafted frame is back at the top of the host's.
		fr.fragment = nil
		fr.graftTop = 0
		if fr.parent != nil {
			fr.left = fr.parent.left
			if fr.columnCount > 1 {
				fr.width = columnWidth(fr.parent.width, fr.columnCount, fr.columnGap)
			} else {
				fr.width = fr.parent.width
			}
		}
	})

	// swapheader puts the title at the top of the page frame, and that page's
	// header is reserved below it. Content on page 1 starts that much lower.
	if eng.swapTitle != nil {
		page.top, page.bottom = page.outerTop, page.outerBottom
		page.fillY = page.top
		measured, err := eng.measureSection(eng.swapTitle,
			eng.frames.scopesOf[eng.swapTitle], page, page.outerBottom-page.outerTop)
		if err != nil {
			return err
		}
		if measured.printed {
			eng.commit(measured, page, page.outerTop)
			page.outerTop = geom.Round(page.outerTop + measured.height)
		}
		eng.swapTitle = nil
	}
	return openFrames(page)
}

// openFrames reserves header and footer space for a frame and its descendants,
// and places the headers.
//
// Both bands are measured against the context as it stands when the frame
// begins, and a header opens the frame it belongs to. The engine that measures
// them is the frame's own: a frame an inline subreport grafted on is opened by
// the host's page machinery and measured in the child's context, so the bands
// and their styles are read off the frame rather than out of one engine's tree.
func openFrames(fr *frame) error {
	if fr.parent != nil {
		fr.outerTop = fr.parent.top
		if fr.graftTop > fr.outerTop {
			// The invocation started part way down the host's frame,
			// and every column of this one starts there for this page.
			fr.outerTop = fr.graftTop
		}
		fr.outerBottom = fr.parent.bottom
	}
	return fr.open()
}

// open reserves and places a frame's own furniture, and then its children's.
//
// The outer bounds are already set, which is what lets an inline subreport's
// frames be opened where the host is filled rather than where it begins.
func (fr *frame) open() error {
	eng := fr.eng
	fr.top, fr.bottom = fr.outerTop, fr.outerBottom
	fr.fillY = fr.top

	if fr.footer != nil {
		measured, err := eng.measureSection(fr.footer,
			fr.footerScopes, fr, fr.outerBottom-fr.outerTop)
		if err != nil {
			return err
		}
		fr.footerHeight = measured.height
		fr.bottom = geom.Round(fr.outerBottom - measured.height)
	} else {
		fr.footerHeight = 0
	}
	if fr.top > fr.bottom {
		return fmt.Errorf("the header and footer reservations together exceed the frame")
	}

	if fr.header != nil {
		measured, err := eng.measureSection(fr.header,
			fr.headerScopes, fr, fr.bottom-fr.top)
		if err != nil {
			return err
		}
		if measured.printed {
			if geom.Round(fr.top+measured.height) > fr.bottom {
				return fmt.Errorf(
					"the header and footer reservations together exceed the frame")
			}
			eng.commit(measured, fr, fr.top)
			fr.top = geom.Round(fr.top + measured.height)
		}
	}
	fr.fillY = fr.top

	for _, child := range fr.children {
		if err := openFrames(child); err != nil {
			return err
		}
	}
	return nil
}

// openGrafted opens the frames an inline subreport just added to a host frame.
//
// They begin where the host is filled rather than where it begins, and stay
// there for the rest of the page: the space above belongs to the host.
func openGrafted(host *frame, from int) error {
	for _, child := range host.children[from:] {
		child.graftTop = host.fillY
		child.outerTop = host.fillY
		child.outerBottom = host.bottom
		if err := child.open(); err != nil {
			return err
		}
	}
	return nil
}

// closePage places every frame's footer, innermost first.
func (eng *engine) closePage() error {
	frames := []*frame{}
	var collect func(*frame)
	collect = func(fr *frame) {
		for _, child := range fr.children {
			collect(child)
		}
		frames = append(frames, fr)
	}
	collect(eng.frames.page)
	for _, fr := range frames {
		if fr.footer == nil {
			continue
		}
		measured, err := fr.eng.measureSection(fr.footer,
			fr.footerScopes, fr, fr.footerHeight)
		if err != nil {
			return err
		}
		if !measured.printed {
			continue
		}
		// A footer is placed flush against the frame's reserved bottom band.
		fr.eng.commit(measured, fr, geom.Round(fr.outerBottom-measured.height))
	}
	return nil
}

// consume runs the record loop for one record.
func (eng *engine) consume(index int) error {
	// 1. THIS and ITEM_NUMBER advance.
	eng.index = index
	eng.ctx.record = eng.records[index]
	eng.ctx.itemNumber = index + 1

	// 2. Every group's expr, outermost first. The outermost that changed
	// determines the break level, and every group nested inside it breaks too.
	breakLevel := -1
	keys := make([]starlark.Value, len(eng.groups))
	for level, group := range eng.groups {
		value, err := eng.ctx.eval(group.Expr)
		if err != nil {
			return fmt.Errorf("group %q: %w", group.Name, err)
		}
		keys[level] = value
		if breakLevel >= 0 {
			continue
		}
		if !eng.groupOpen[level] {
			breakLevel = level
			continue
		}
		same, err := starlark.Equal(eng.groupKey[level], value)
		if err != nil || !same {
			breakLevel = level
		}
	}

	if breakLevel >= 0 {
		// 3. For each breaking group, innermost first:
		// its summary, built against the previous record's context,
		// so it can print its own total.
		if index > 0 {
			eng.ctx.record, eng.ctx.itemNumber = eng.records[index-1], index
		}
		err := eng.closeGroups(breakLevel)
		eng.ctx.record, eng.ctx.itemNumber = eng.records[index], index+1
		if err != nil {
			return err
		}
		// 4. Variables reset for the scopes that just ended.
		for level := len(eng.groups) - 1; level >= breakLevel; level-- {
			if err := eng.ctx.reset(tmpl.ScopeGroup, eng.groups[level].Name); err != nil {
				return err
			}
		}
		// 5. For each breaking group, outermost first:
		// variables iterate for that scope, then its title.
		for level := breakLevel; level < len(eng.groups); level++ {
			group := eng.groups[level]
			eng.groupKey[level] = keys[level]
			eng.groupOpen[level] = true
			eng.ctx.groupCount[group.Name] = 0
			eng.ctx.groupPageNumber[group.Name] = 1
			eng.groupRuns[group.Name]++
			eng.groupKeys[group.Name][keys[level].String()] = true

			if err := eng.ctx.iterate(tmpl.ScopeGroup, group.Name); err != nil {
				return err
			}
			if group.Title != nil {
				if err := eng.placeGroupTitle(group, level, index); err != nil {
					return err
				}
			}
		}
	}

	// iter="item" folds once per record, whether or not its detail prints,
	// which is what distinguishes it from iter="detail".
	if err := eng.ctx.iterate(tmpl.ScopeItem, ""); err != nil {
		return err
	}

	// 6. The detail band's variables iterate, then the detail prints.
	detail := eng.detailSection()
	if detail == nil {
		return nil
	}
	return eng.placeDetail(detail, index)
}

// detailSection is the innermost detail band.
func (eng *engine) detailSection() *tmpl.Section {
	if len(eng.groups) == 0 {
		return eng.report.Layout.Body.Detail
	}
	return eng.groups[len(eng.groups)-1].Detail
}

// closeGroups places the summary of every group from the innermost
// down to level, built against the outgoing record.
func (eng *engine) closeGroups(level int) error {
	if level < 0 {
		return nil
	}
	for depth := len(eng.groups) - 1; depth >= level; depth-- {
		group := eng.groups[depth]
		if !eng.groupOpen[depth] {
			continue
		}
		if group.Summary != nil {
			err := eng.place(group.Summary,
				eng.frames.frameOf[group.Summary], eng.frames.scopesOf[group.Summary])
			if err != nil {
				return err
			}
		}
		// A group's deferrals resolve after its summary,
		// so both read the same final group totals.
		if err := eng.resolveScope(group.Name); err != nil {
			return err
		}
		eng.groupOpen[depth] = false
	}
	return nil
}

// placeReportTitle places the band directly under layout.
//
// A report's own title evaluates its ejects after it is placed,
// so an eject there gives the title a page of its own.
// A group's title is not the exception.
func (eng *engine) placeReportTitle(sec *tmpl.Section) error {
	fr := eng.frames.frameOf[sec]
	// A swapheader title was already placed when the page opened, and a
	// subreport on one is refused at load, so there is nothing to decide.
	if !sec.SwapHeader {
		prints, err := eng.bandPrints(sec, fr)
		if err != nil {
			return err
		}
		if err := eng.runSubreports(sec, true, prints); err != nil {
			return err
		}
		if err := eng.placeMeasured(sec, fr, eng.frames.scopesOf[sec], nil, &prints); err != nil {
			return err
		}
		if err := eng.runSubreports(sec, false, prints); err != nil {
			return err
		}
	}
	kind, want, err := eng.selectEject(sec, fr)
	if err != nil {
		return err
	}
	if want {
		return eng.forcedEject(fr, kind)
	}
	return nil
}

// placeSummary places the report summary.
//
// The summary may sit below the last page footer.
func (eng *engine) placeSummary(sec *tmpl.Section) error {
	fr := eng.frames.frameOf[sec]
	if sec.SwapFooter {
		// No subreport hooks here: a swapfooter band is placed outside the
		// frame's own fill, and validation refuses a subreport on one.
		// The summary goes into the page frame's reserved bottom band,
		// and that page's footer is placed immediately above it.
		// The last page's content space is that much shorter,
		// and if what remains is already filled a page eject happens first.
		prints, err := eng.bandPrints(sec, fr)
		if err != nil {
			return err
		}
		measured, err := eng.measureDecided(sec,
			eng.frames.scopesOf[sec], fr, fr.available(), &prints)
		if err != nil {
			return err
		}
		if !measured.printed {
			return nil
		}
		if !geom.Fits(measured.height, geom.Round(fr.outerBottom-fr.fillY-fr.footerHeight)) {
			if err := eng.forcedEject(fr, tmpl.EjectPage); err != nil {
				return err
			}
			// The same answer after the eject: one placement, one printwhen.
			measured, err = eng.measureDecided(sec,
				eng.frames.scopesOf[sec], fr, fr.available(), &prints)
			if err != nil {
				return err
			}
		}
		// Placing it lowers the frame's bottom, so the footer lands above it.
		fr.outerBottom = geom.Round(fr.outerBottom - measured.height)
		fr.bottom = geom.Round(fr.outerBottom - fr.footerHeight)
		eng.commit(measured, fr, geom.Round(fr.outerBottom))
		return nil
	}
	return eng.place(sec, fr, eng.frames.scopesOf[sec])
}

// placeGroupTitle applies the keep-together mechanisms, then places the title.
func (eng *engine) placeGroupTitle(group *tmpl.Group, level, record int) error {
	fr := eng.frames.frameOf[group.Title]
	scopes := eng.frames.scopesOf[group.Title]

	// Each mechanism that applies contributes the height it wants available;
	// the largest is compared against the space remaining,
	// and at most one eject results.
	want := 0.0
	kind := tmpl.EjectColumn

	prints, err := eng.bandPrints(group.Title, fr)
	if err != nil {
		return err
	}
	measured, err := eng.measureDecided(group.Title, scopes, fr, fr.available(), &prints)
	if err != nil {
		return err
	}
	if measured.printed {
		want = measured.height
	}

	if group.KeepTogether || group.MinRows > 0 {
		extent, err := eng.lookahead(group, level, record, fr, measured.height)
		if err != nil {
			return err
		}
		if extent > want {
			want = extent
		}
	}

	ejectKind, ejects, err := eng.selectEject(group.Title, fr)
	if err != nil {
		return err
	}
	if ejects {
		// A selected eject node's type decides the kind,
		// whichever contributor demanded the most space:
		// escalating costs nothing, and deciding the kind
		// from which contributor happened to be largest
		// would make the kind of break depend on the data.
		if ejectKind == tmpl.EjectPage {
			kind = tmpl.EjectPage
		}
		if err := eng.forcedEject(fr, kind); err != nil {
			return err
		}
	} else if want > 0 && !geom.Fits(want, fr.available()) && geom.Fits(want, fr.emptyHeight()) {
		// Keeping the group together decided this column, and it decided it
		// from what follows the title rather than from the title's own height.
		if err := eng.forcedEject(fr, kind); err != nil {
			return err
		}
	}
	// The eject decision has been made here, so the band goes straight
	// to placement rather than having its eject nodes tested a second time.
	if err := eng.runSubreports(group.Title, true, prints); err != nil {
		return err
	}
	if err := eng.placeMeasured(group.Title, fr, scopes, nil, &prints); err != nil {
		return err
	}
	return eng.runSubreports(group.Title, false, prints)
}

// lookahead measures a group's extent, or its title plus minrows detail rows.
//
// Accumulation stops at an empty frame's height,
// which bounds the cost regardless of group size.
func (eng *engine) lookahead(
	group *tmpl.Group,
	level, record int,
	fr *frame,
	titleHeight float64,
) (float64, error) {
	cap := fr.emptyHeight()
	detail := eng.detailSection()
	if detail == nil {
		return 0, nil
	}
	detailFrame := eng.frames.frameOf[detail]
	detailScopes := eng.frames.scopesOf[detail]

	// The lookahead runs the record loop's evaluation without emitting, so
	// every mutable piece of context is saved and put back.
	savedRecord, savedItem := eng.ctx.record, eng.ctx.itemNumber
	savedVars := eng.ctx.snapshotVars()
	savedCounts := map[string]int{}
	for name, count := range eng.ctx.groupCount {
		savedCounts[name] = count
	}
	savedReport, savedPage, savedColumn := eng.ctx.reportCount, eng.ctx.pageCount, eng.ctx.columnCount
	defer func() {
		eng.ctx.record, eng.ctx.itemNumber = savedRecord, savedItem
		eng.ctx.restoreVars(savedVars)
		for name, count := range savedCounts {
			eng.ctx.groupCount[name] = count
		}
		eng.ctx.reportCount, eng.ctx.pageCount, eng.ctx.columnCount =
			savedReport, savedPage, savedColumn
	}()

	total := titleHeight
	rows := 0
	for index := record; index < len(eng.records); index++ {
		eng.ctx.record = eng.records[index]
		eng.ctx.itemNumber = index + 1
		if index > record {
			key, err := eng.ctx.eval(group.Expr)
			if err != nil {
				return 0, err
			}
			same, err := starlark.Equal(eng.groupKey[level], key)
			if err != nil || !same {
				break
			}
		}
		if err := eng.ctx.iterate(tmpl.ScopeDetail, ""); err != nil {
			return 0, err
		}
		measured, err := eng.measureSection(detail, detailScopes, detailFrame, cap)
		if err != nil {
			return 0, err
		}
		if !measured.printed {
			// Rows means printed rows: a record whose detail is suppressed
			// occupies no space, so the lookahead skips it and counts on.
			continue
		}
		total = geom.Round(total + measured.height)
		rows++
		if total > cap {
			return cap, nil
		}
		if !group.KeepTogether && rows >= group.MinRows {
			return total, nil
		}
	}
	if group.KeepTogether && group.Summary != nil {
		measured, err := eng.measureSection(group.Summary,
			eng.frames.scopesOf[group.Summary], eng.frames.frameOf[group.Summary], cap)
		if err != nil {
			return 0, err
		}
		total = geom.Round(total + measured.height)
	}
	if total > cap {
		return cap, nil
	}
	return total, nil
}

// placeDetail iterates detail-scoped variables and places the band,
// rolling the fold back if the band turns out not to fit.
func (eng *engine) placeDetail(sec *tmpl.Section, record int) error {
	fr := eng.frames.frameOf[sec]
	scopes := eng.frames.scopesOf[sec]

	// Ejects are evaluated at the beginning of the section,
	// before the band's variables fold.
	kind, ejects, err := eng.selectEject(sec, fr)
	if err != nil {
		return err
	}
	if ejects {
		if err := eng.forcedEject(fr, kind); err != nil {
			return err
		}
	}

	// Step 6 iterates variables before placing because
	// the band's content usually depends on them.
	snapshot := eng.ctx.snapshotVars()
	if err := eng.ctx.iterate(tmpl.ScopeDetail, ""); err != nil {
		return err
	}

	// The band's printwhen, answered here and used for the band and for both
	// sides of it. A negative seq puts the subreport's bands in the frame ahead
	// of this one, which then follows them; it runs after the fold, so both
	// sides of the band read the same variables.
	prints, err := eng.bandPrints(sec, fr)
	if err != nil {
		return err
	}
	if err := eng.runSubreports(sec, true, prints); err != nil {
		return err
	}

	measured, merr := eng.measureDecided(sec, scopes, fr, fr.available(), &prints)
	if merr != nil {
		return merr
	}
	if !measured.printed {
		// A band suppressed by printwhen does not iterate
		// detail-scoped variables, but does advance ITEM_NUMBER.
		eng.ctx.restoreVars(snapshot)
		return nil
	}

	if geom.Fits(measured.height, fr.available()) {
		eng.commit(measured, fr, fr.fillY)
		eng.afterDetail()
		return eng.runSubreports(sec, false, prints)
	}

	// The band splits where it can, and the fold stands:
	// part of the row is committed here.
	if sec.Split {
		if _, ok := legalSplit(measured, sec, fr.available()); ok {
			if err := eng.placeMeasured(sec, fr, scopes, measured, &prints); err != nil {
				return err
			}
			return eng.runSubreports(sec, false, prints)
		}
	}

	// Otherwise the fold is rolled back before the eject and reapplied after,
	// so no value is counted twice.
	eng.ctx.restoreVars(snapshot)
	if _, err := eng.eject(fr, tmpl.EjectColumn); err != nil {
		return err
	}
	if err := eng.ctx.iterate(tmpl.ScopeDetail, ""); err != nil {
		return err
	}
	if err := eng.placeMeasured(sec, fr, scopes, nil, &prints); err != nil {
		return err
	}
	// After the whole band, split or not: a subreport goes outside the split,
	// not between its fragments.
	return eng.runSubreports(sec, false, prints)
}

func (eng *engine) afterDetail() {
	eng.ctx.reportCount++
	eng.ctx.pageCount++
	eng.ctx.columnCount++
	for _, group := range eng.groups {
		eng.ctx.groupCount[group.Name]++
	}
}

// place measures a band and puts it where it fits.
func (eng *engine) place(sec *tmpl.Section, fr *frame, scopes styleScopes) error {
	kind, ejects, err := eng.selectEject(sec, fr)
	if err != nil {
		return err
	}
	if ejects {
		if err := eng.forcedEject(fr, kind); err != nil {
			return err
		}
	}
	prints, err := eng.bandPrints(sec, fr)
	if err != nil {
		return err
	}
	if err := eng.runSubreports(sec, true, prints); err != nil {
		return err
	}
	if err := eng.placeMeasured(sec, fr, scopes, nil, &prints); err != nil {
		return err
	}
	return eng.runSubreports(sec, false, prints)
}

// placeMeasured runs the four branches of doc/layout.md#placing-a-band.
func (eng *engine) placeMeasured(
	sec *tmpl.Section,
	fr *frame,
	scopes styleScopes,
	carried *measurement,
	prints *bool,
) error {
	for attempt := 0; ; attempt++ {
		measured := carried
		if measured == nil {
			var err error
			// The same printwhen answer on every retry: an eject between them
			// moves the frame, and a band cannot appear or vanish part way
			// through being placed.
			measured, err = eng.measureDecided(sec, scopes, fr, fr.available(), prints)
			if err != nil {
				return err
			}
		}
		if !measured.printed {
			return nil
		}
		available := fr.available()

		if geom.Fits(measured.height, available) {
			eng.commit(measured, fr, fr.fillY)
			if sec.Kind == tmpl.BandDetail {
				eng.afterDetail()
			}
			return nil
		}

		if sec.Split {
			if cut, ok := legalSplit(measured, sec, available); ok {
				head, tail := splitAt(measured, cut)
				// The two halves of a split band belong at a column edge,
				// and moving either would put the cut somewhere else.
				fr.blockBalance()
				eng.commit(head, fr, fr.fillY)
				if _, err := eng.eject(fr, tmpl.EjectColumn); err != nil {
					return err
				}
				carried = tail
				continue
			}
		}

		if geom.Fits(measured.height, fr.emptyHeight()) {
			if _, err := eng.eject(fr, tmpl.EjectColumn); err != nil {
				return err
			}
			carried = nil
			continue
		}

		// Taller than an empty frame: every split preference is given up.
		if sec.Split {
			if cut, ok := lastCutPoint(measured, available); ok {
				head, tail := splitAt(measured, cut)
				fr.blockBalance()
				eng.commit(head, fr, fr.fillY)
				if _, err := eng.eject(fr, tmpl.EjectColumn); err != nil {
					return err
				}
				carried = tail
				continue
			}
		}

		node := sec.Node.Path()
		msg := fmt.Sprintf(
			"the band measures %g pt and the largest frame offers %g pt, and it cannot be cut",
			measured.height, fr.emptyHeight())
		if err := eng.overflow(node, msg); err != nil {
			return err
		}
		eng.commit(measured, fr, fr.fillY)
		if sec.Kind == tmpl.BandDetail {
			eng.afterDetail()
		}
		return nil
	}
}

// selectEject applies the eject nodes of a band.
//
// The first node whose when is true is selected and the search stops there,
// whether or not it ejects. The selected node ejects unconditionally
// without a require, and otherwise only when less than require remains.
func (eng *engine) selectEject(sec *tmpl.Section, fr *frame) (tmpl.EjectType, bool, error) {
	for _, ej := range sec.Ejects {
		ok, err := eng.ctx.truth(ej.When)
		if err != nil {
			return 0, false, err
		}
		if !ok {
			continue
		}
		if !ej.Require.Set {
			return ej.Type, true, nil
		}
		return ej.Type, fr.available() < ej.Require.Value, nil
	}
	return tmpl.EjectPage, false, nil
}

// eject ends the current page or column and starts the next,
// and reports which of the two it turned out to be.
func (eng *engine) eject(from *frame, kind tmpl.EjectType) (tmpl.EjectType, error) {
	owner := eng.owner()

	// Which frames participate: for a column eject, the frame and
	// its ancestors, stopping at the first that still has an unused column.
	// If none does, the walk reaches the page frame and it becomes a page eject.
	target := owner.frames.page
	if kind == tmpl.EjectColumn {
		found := false
		for fr := from; fr != nil; fr = fr.parent {
			if fr.hasUnusedColumn() {
				target, found = fr, true
				break
			}
		}
		if !found {
			kind = tmpl.EjectPage
		}
	}

	if err := eng.endPage(target, kind); err != nil {
		return kind, err
	}

	if kind == tmpl.EjectColumn {
		owner.ctx.pages.column++
		target.setColumn(target.column + 1)
		// Re-opening the frame re-places its own header in the new column
		// and re-reserves its footer.
		return kind, openFrames(target)
	}
	return kind, eng.beginPage()
}

// forcedEject ejects because the template asked for it rather than because
// a band ran out of room, and refuses to balance the fragment the band
// lands in when that is still this page: packing by height would put the band
// back in the column the eject moved it out of. A page eject starts a fragment
// that nothing has decided yet, so it leaves that one alone.
func (eng *engine) forcedEject(from *frame, kind tmpl.EjectType) error {
	done, err := eng.eject(from, kind)
	if err != nil {
		return err
	}
	if done == tmpl.EjectColumn {
		from.blockBalance()
	}
	return nil
}

// endPage is the closing half of a break: footers, then the deferrals
// and the column-scoped variables of every engine printing on the page.
//
// An inline subreport is one of those engines. The break is the host's --
// the pages are the host's -- but the child's page-scoped variables reset
// with it and its deferrals for the ending scope resolve with it,
// because the scope that ended is the one both are printing in.
func (eng *engine) endPage(target *frame, kind tmpl.EjectType) error {
	owner, chain := eng.owner(), eng.chain()

	// A page ends holding whatever its balanced frames were given, so
	// they spread it before the footers go against the frame bottoms.
	// A column eject is not the end of anything a frame balances --
	// the fragment is what the whole page holds.
	if kind == tmpl.EjectPage {
		owner.balancePage()
	}

	// 1. Footers, innermost first.
	if err := owner.closeFrameFooters(target, kind); err != nil {
		return err
	}

	// 2. Deferred values for the scopes that just ended.
	for _, part := range chain {
		if err := part.resolveScope("column"); err != nil {
			return err
		}
	}
	if kind == tmpl.EjectPage {
		for _, part := range chain {
			if err := part.resolveScope("page"); err != nil {
				return err
			}
		}
	}

	// 3. The column scope restarts whichever kind of break this is.
	for _, part := range chain {
		part.ctx.columnCount = 0
		if err := part.ctx.reset(tmpl.ScopeColumn, ""); err != nil {
			return err
		}
		if err := part.ctx.iterate(tmpl.ScopeColumn, ""); err != nil {
			return err
		}
	}
	return nil
}

// beginPage is the opening half: the page scope restarts for every engine
// printing on it, and then the headers are placed, outermost first.
func (eng *engine) beginPage() error {
	owner, chain := eng.owner(), eng.chain()
	owner.ctx.pages.number++
	owner.ctx.pages.column = 1
	for _, part := range chain {
		part.ctx.pageCount = 0
		if err := part.ctx.reset(tmpl.ScopePage, ""); err != nil {
			return err
		}
		if err := part.ctx.iterate(tmpl.ScopePage, ""); err != nil {
			return err
		}
		for _, group := range part.groups {
			part.ctx.groupPageNumber[group.Name]++
		}
	}
	return owner.startPage()
}

// closeFrameFooters places the footer of the ejecting frame and its ancestors,
// innermost first, against the outgoing context.
func (eng *engine) closeFrameFooters(target *frame, kind tmpl.EjectType) error {
	var frames []*frame
	if kind == tmpl.EjectPage {
		var collect func(*frame)
		collect = func(fr *frame) {
			for _, child := range fr.children {
				collect(child)
			}
			frames = append(frames, fr)
		}
		collect(eng.frames.page)
	} else {
		var collect func(*frame)
		collect = func(fr *frame) {
			for _, child := range fr.children {
				collect(child)
			}
			frames = append(frames, fr)
		}
		collect(target)
	}
	for _, fr := range frames {
		if fr.footer == nil {
			continue
		}
		measured, err := fr.eng.measureSection(fr.footer,
			fr.footerScopes, fr, fr.footerHeight)
		if err != nil {
			return err
		}
		if !measured.printed {
			continue
		}
		fr.eng.commit(measured, fr, geom.Round(fr.outerBottom-measured.height))
	}
	return nil
}

// commit emits a measured band's marks at a page position and advances the frame.
func (eng *engine) commit(measured *measurement, fr *frame, top float64) {
	if !measured.printed {
		return
	}
	// A measurement carried across an eject was laid out against the column
	// the band started in -- the tail of a split band is the only one -- so
	// it moves to the column it is committed in as it is placed.
	across := geom.Round(fr.left - measured.left)
	measured.left = fr.left

	// The marks are kept as they are appended, so that a balanced frame
	// can move the band afterwards without going looking for them.
	// Only a frame that has one above it pays for the list.
	var placed []printout.Mark
	if fr.recording() {
		placed = make([]printout.Mark, 0, len(measured.drafts)+1)
	}
	for _, dft := range measured.drafts {
		translate(dft.mark, across, top)
		eng.doc.page.Marks = append(eng.doc.page.Marks, dft.mark)
		if placed != nil {
			placed = append(placed, dft.mark)
		}
	}
	if measured.outline != nil {
		measured.outline.Top = geom.Round(measured.outline.Top + top)
		eng.doc.page.Marks = append(eng.doc.page.Marks, measured.outline)
		if placed != nil {
			placed = append(placed, measured.outline)
		}
	}
	fr.record(measured.section, placed, top, measured.height)
	for _, deferred := range measured.defers {
		eng.pending[deferred.scope] = append(eng.pending[deferred.scope], deferred)
		if deferred.scope == "column" {
			// Resolved when the column ends, against the column it ended in.
			// Moving the band would answer it from the wrong one.
			fr.blockBalance()
		}
	}
	if bottom := geom.Round(top + measured.height); bottom > fr.fillY {
		fr.advance(bottom)
	}
}

// translate moves a mark and everything nested in it across and down the page.
//
// Placing a band moves it down to where the frame is filled; balancing
// a column moves the band again, across to the column it was given.
// An outline is a position in the document rather than on the page,
// so it has nothing to move sideways.
func translate(mark printout.Mark, dx, dy float64) {
	switch typed := mark.(type) {
	case *printout.Text:
		typed.Box.Left = geom.Round(typed.Box.Left + dx)
		typed.Box.Top = geom.Round(typed.Box.Top + dy)
	case *printout.Line:
		typed.Box.Left = geom.Round(typed.Box.Left + dx)
		typed.Box.Top = geom.Round(typed.Box.Top + dy)
	case *printout.Rectangle:
		typed.Box.Left = geom.Round(typed.Box.Left + dx)
		typed.Box.Top = geom.Round(typed.Box.Top + dy)
	case *printout.Image:
		typed.Box.Left = geom.Round(typed.Box.Left + dx)
		typed.Box.Top = geom.Round(typed.Box.Top + dy)
	case *printout.Barcode:
		typed.Box.Left = geom.Round(typed.Box.Left + dx)
		typed.Box.Top = geom.Round(typed.Box.Top + dy)
	case *printout.Outline:
		typed.Top = geom.Round(typed.Top + dy)
	case *printout.Xref:
		typed.Box.Left = geom.Round(typed.Box.Left + dx)
		typed.Box.Top = geom.Round(typed.Box.Top + dy)
		for _, inner := range typed.Marks {
			translate(inner, dx, dy)
		}
	}
}

// resolveScope substitutes every deferred value registered against a scope.
func (eng *engine) resolveScope(scope string) error {
	pending := eng.pending[scope]
	if len(pending) == 0 {
		return nil
	}
	eng.pending[scope] = nil
	final := eng.ctx.finalNamespace()
	for _, deferred := range pending {
		if err := eng.resolveDeferral(deferred, final); err != nil {
			return err
		}
	}
	return nil
}

func (eng *engine) resolveDeferral(deferred *deferral, final *expr.Namespace) error {
	value, err := eng.ctx.callWithFinal(deferred.program, deferred.snapshot, final)
	if err != nil {
		return err
	}
	text, err := expr.Format(deferred.format, expr.FormatArgs(value))
	if err != nil {
		return fmt.Errorf("%s: %w", deferred.node, err)
	}

	if deferred.kind == "field" {
		lines := fontres_Wrap(deferred.face, text, deferred.boxWidth)
		if !deferred.stretch {
			lines = trimToBox(lines, deferred.text.Leading, deferred.reserved)
		}
		height := float64(len(lines)) * deferred.text.Leading
		if height > deferred.reserved+geom.Tolerance {
			return fmt.Errorf(
				"%s: the deferred value %q needs %g pt and its placeholder reserved %g pt; size the placeholder for the worst case",
				deferred.node, text, height, deferred.reserved)
		}
		deferred.text.Lines = lines
		return nil
	}

	sym, metrics, err := encodeDeferredBarcode(deferred, text)
	if err != nil {
		return fmt.Errorf("%s: %w", deferred.node, err)
	}
	extent := metrics.Length
	reserved := deferred.barcode.Box.Width
	if deferred.vertical {
		reserved = deferred.barcode.Box.Height
	}
	if extent > reserved+geom.Tolerance {
		return fmt.Errorf(
			"%s: the deferred barcode value %q needs %g pt and its placeholder reserved %g pt; size the placeholder for the worst case",
			deferred.node, text, extent, reserved)
	}
	deferred.barcode.Value = sym.Value
	deferred.barcode.Stripes = sym.Stripes
	deferred.barcode.Rows = sym.Rows
	deferred.barcode.Module = metrics.Module
	if deferred.vertical {
		deferred.barcode.Box.Height = extent
	} else {
		deferred.barcode.Box.Width = extent
	}
	return nil
}

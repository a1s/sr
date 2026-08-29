package build

import (
	"fmt"

	"github.com/a1s/sr/internal/data"
	"github.com/a1s/sr/internal/tmpl"
	"go.starlark.net/starlark"
)

// maxSubreportDepth bounds nesting.
//
// A subreport may name the layout it sits in, which is how a template walks
// a tree, and data normally ends that walk. Nothing in the engine guarantees
// the data is finite, so the recursion is bounded here rather than by the stack.
const maxSubreportDepth = 32

// subreportGate decides whether a band's subreports run at all.
//
// A subreport hangs off a band, so a band suppressed by printwhen runs none
// of them: an invoice that does not print has no line items to print either.
// The answer is taken once, where the band is, because the two sides of the
// band are asked at different points in the record loop and have to agree.
//
// A band with no subreports is not asked, so a printwhen with an error in it
// is still reported by the band itself rather than here.
func (eng *engine) subreportGate(sec *tmpl.Section) (bool, error) {
	if sec == nil || len(sec.Subreports) == 0 {
		return false, nil
	}
	return eng.ctx.truth(sec.PrintWhen)
}

// runSubreports runs the subreports a band carries, on one side of it.
//
// The list is already ordered by seq and then document position. Negative seq
// runs before the band, non-negative after it, and the band is placed whole
// in between: a subreport emits bands of its own rather than content inside
// the host band's box, so a host band that splits is not split around it.
func (eng *engine) runSubreports(sec *tmpl.Section, before, gate bool) error {
	if !gate || sec == nil || len(sec.Subreports) == 0 {
		return nil
	}
	fr := eng.frames.frameOf[sec]
	for _, sub := range sec.Subreports {
		if (sub.Seq < 0) != before {
			continue
		}
		if err := eng.runSubreport(sub, fr); err != nil {
			return err
		}
	}
	return nil
}

// runSubreport runs one subreport over the sequence its data names.
func (eng *engine) runSubreport(sub *tmpl.Subreport, fr *frame) error {
	node := sub.Node.Path()
	ok, err := eng.ctx.truth(sub.When)
	if err != nil {
		return fmt.Errorf("%s: %w", node, err)
	}
	if !ok {
		return nil
	}
	if eng.depth >= maxSubreportDepth {
		return fmt.Errorf(
			"%s: subreports are nested %d deep, which is as far as this engine goes",
			node, maxSubreportDepth)
	}

	item, err := eng.unitFor(sub)
	if err != nil {
		return fmt.Errorf("%s: %w", node, err)
	}
	value, err := eng.ctx.eval(sub.Data)
	if err != nil {
		return fmt.Errorf("%s: %w", node, err)
	}
	rows, err := data.Sequence(value)
	if err != nil {
		return fmt.Errorf("%s data=: %w", node, err)
	}
	records, err := data.Records(rows, item.report.Records)
	if err != nil {
		return fmt.Errorf("%s: %w", node, err)
	}

	child := eng.newChild(sub, item, fr)
	// Attaching the child's unit pointed the resolver's way back to the data
	// blobs at the child's context. This puts it back, however this returns.
	defer eng.attach(eng.unit)
	if child.frames != nil && child.frames.release != nil {
		defer child.frames.release()
	}

	child.args, err = eng.subreportArgs(sub, item.report)
	if err != nil {
		return err
	}
	if err := child.bindParams(true); err != nil {
		return fmt.Errorf("%s: %w", node, err)
	}
	if err := child.bindVariables(); err != nil {
		return fmt.Errorf("%s: %w", node, err)
	}
	child.records = records
	child.ctx.dataCount = len(records)

	if err := eng.runChild(child, sub); err != nil {
		return fmt.Errorf("%s: %w", node, err)
	}
	return nil
}

// runChild runs the nested build, on the host's pages or on its own.
func (eng *engine) runChild(child *engine, sub *tmpl.Subreport) error {
	if sub.Inline {
		return child.run()
	}

	// A subreport that paginates itself builds complete pages, and they
	// go into the printout where the subreport occurs: the host's current
	// page is closed, the child's pages follow it, and the host resumes
	// on a fresh one. Appending is the splice, because everything the host
	// has built so far is already before this point.
	owner := eng.owner()
	if err := eng.endPage(owner.frames.page, tmpl.EjectPage); err != nil {
		return err
	}
	if !sub.OwnPageNo {
		// Numbering continues into the child, which shares the counter,
		// and beginPage below resumes from wherever the child left it.
		child.ctx.pages.number++
	}
	if err := child.run(); err != nil {
		return err
	}
	return eng.beginPage()
}

// newChild builds the nested engine one invocation runs in.
//
// It shares the document -- the printout, the page, the blob and font tables --
// and the unit's caches, and has a context, a record loop, and a set of
// deferrals of its own.
func (eng *engine) newChild(sub *tmpl.Subreport, item *unit, fr *frame) *engine {
	child := &engine{
		opts:    eng.opts,
		out:     eng.out,
		ctx:     newScopeContext(eng.ctx.buildTime),
		host:    eng,
		inline:  sub.Inline,
		depth:   eng.depth + 1,
		pending: map[string][]*deferral{},
	}
	child.adopt(eng.doc)
	child.attach(item)
	switch {
	case sub.Inline:
		// The host's pages, so the host's pagination and the host's frame.
		child.ctx.pages = eng.ctx.pages
		child.frames = buildFramesIn(item.report.Layout, fr)
	case !sub.OwnPageNo:
		// Its own pages, numbered on from the host's.
		child.ctx.pages = eng.ctx.pages
	}
	return child
}

// subreportArgs evaluates the arg nodes in the host's context.
//
// An arg is an expression, not text, so its result is type-checked
// against the parameter rather than parsed for it.
func (eng *engine) subreportArgs(
	sub *tmpl.Subreport,
	child *tmpl.Report,
) (map[string]starlark.Value, error) {
	out := make(map[string]starlark.Value, len(sub.Args))
	for _, arg := range sub.Args {
		value, err := eng.ctx.eval(arg.Value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", arg.Node.Path(), err)
		}
		param, ok := child.ParamByName[arg.Name]
		if !ok {
			return nil, fmt.Errorf("%s: no parameter named %q", arg.Node.Path(), arg.Name)
		}
		if !data.MatchesType(value, param.Type) {
			return nil, fmt.Errorf(
				"%s: the value is %s, and %q is declared %s",
				arg.Node.Path(), value.Type(), param.Name, param.Type)
		}
		out[arg.Name] = value
	}
	return out, nil
}

// unitFor is the template a subreport runs, prepared the first time the node
// is reached and kept afterwards, so that a subreport invoked once per record
// resolves its fonts once for the document rather than once per record.
func (eng *engine) unitFor(sub *tmpl.Subreport) (*unit, error) {
	if item, ok := eng.doc.units[sub]; ok {
		return item, nil
	}
	var item *unit
	switch {
	case sub.Report != nil:
		// A separate document: its own fonts, its own data, its own
		// base directory, so its own caches and its own resolver.
		item = newUnit(sub.Report, eng.opts, eng.doc)
	default:
		found := findEmbedded(eng.report.Layout, sub.Embedded)
		if found == nil {
			return nil, fmt.Errorf("no embedded layout named %q", sub.Embedded)
		}
		// An embedded layout shares the enclosing report's fonts and data,
		// so it shares the caches those names are keyed in.
		item = &unit{
			report:    embeddedReport(eng.report, found),
			resolver:  eng.resolver,
			faces:     eng.faces,
			fontNames: eng.fontNames,
			blobs:     eng.blobs,
			published: eng.published,
			images:    eng.images,
		}
	}
	if err := checkInlineLayout(sub, item.report.Layout); err != nil {
		return nil, err
	}
	eng.doc.units[sub] = item
	return item, nil
}

// checkInlineLayout refuses what an inline subreport cannot have.
//
// The header and footer rule is checked at load, where both sides are known;
// this catches what a load cannot -- a layout reached through a chain of
// subreports whose own inline flag was decided elsewhere.
func checkInlineLayout(sub *tmpl.Subreport, layout *tmpl.Layout) error {
	if !sub.Inline {
		return nil
	}
	if layout.Body.Header != nil || layout.Body.Footer != nil {
		return fmt.Errorf(
			"an inline subreport prints on the host's pages, whose header and footer are already reserved, so its layout must define neither")
	}
	if layout.Body.Columns != nil {
		return fmt.Errorf(
			"an inline subreport prints in the host's frame, and a columns block reserves a frame across the pages it spans, so an inline layout cannot open one")
	}
	for group := layout.Body.Group; group != nil; group = group.Group {
		if group.Columns != nil {
			return fmt.Errorf(
				"an inline subreport prints in the host's frame, and a columns block reserves a frame across the pages it spans, so an inline layout cannot open one")
		}
	}
	return nil
}

// findEmbedded looks an embedded layout up by name, anywhere in the layout.
func findEmbedded(layout *tmpl.Layout, name string) *tmpl.Embedded {
	var walk func([]*tmpl.Embedded) *tmpl.Embedded
	walk = func(list []*tmpl.Embedded) *tmpl.Embedded {
		for _, item := range list {
			if item.Name == name {
				return item
			}
			if found := walk(item.Embedded); found != nil {
				return found
			}
		}
		return nil
	}
	return walk(layout.Embedded)
}

// embeddedReport presents an embedded layout as the report it behaves like.
//
// An embedded layout is its own namespace for parameters, records, variables
// and groups, and shares everything else with the report it is written in:
// its fonts, its data blobs, its base directory, and its page. Building that
// as a report means one engine runs both, rather than a second one that would
// have to keep in step.
//
// The styles are the embedded layout's and then the enclosing layout's,
// because the style search walks outward through the document and an embedded
// layout is written inside one. A layout named by file is a separate document
// and its search ends at its own layout, which is the same rule.
func embeddedReport(host *tmpl.Report, emb *tmpl.Embedded) *tmpl.Report {
	layout := *host.Layout
	layout.Styles = append(append([]*tmpl.Style{}, emb.Styles...), host.Layout.Styles...)
	// An embedded layout may name one defined beside it or one defined
	// further out, so both are in scope for a subreport nested in it.
	layout.Embedded = append(append([]*tmpl.Embedded{}, emb.Embedded...),
		host.Layout.Embedded...)
	layout.Body = emb.Body
	return &tmpl.Report{
		File:        host.File,
		BaseDir:     host.BaseDir,
		Params:      emb.Params,
		Records:     emb.Records,
		Variables:   emb.Variables,
		Fonts:       host.Fonts,
		Data:        host.Data,
		Layout:      &layout,
		ParamByName: emb.ParamByName,
		VarByName:   emb.VarByName,
		FontByName:  host.FontByName,
		DataByName:  host.DataByName,
		GroupNames:  emb.GroupNames,
		Node:        emb.Node,
	}
}

// owner is the engine whose pages these are: this one, or,
// for an inline subreport, the engine it prints alongside.
func (eng *engine) owner() *engine {
	for eng.inline && eng.host != nil {
		eng = eng.host
	}
	return eng
}

// chain is every engine printing on the current page, outermost first.
//
// A page break reaches all of them: each has variables that reset per page,
// deferrals waiting for the page to end, and group page numbers to advance.
func (eng *engine) chain() []*engine {
	out := []*engine{eng}
	for cur := eng; cur.inline && cur.host != nil; cur = cur.host {
		out = append(out, cur.host)
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}

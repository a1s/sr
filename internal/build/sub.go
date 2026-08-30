package build

import (
	"fmt"

	"github.com/a1s/sr/internal/data"
	"github.com/a1s/sr/internal/geom"
	"github.com/a1s/sr/internal/tmpl"
	"go.starlark.net/starlark"
)

// maxSubreportDepth bounds nesting.
//
// A subreport may name the layout it sits in, which is how a template walks
// a tree, and data normally ends that walk. Nothing in the engine guarantees
// the data is finite, so the recursion is bounded here rather than by the stack.
const maxSubreportDepth = 32

// bandPrints answers a band's printwhen, once, where the band is.
//
// The answer decides the band and its subreports together -- a subreport hangs
// off a band, and an invoice that does not print has no line items to print
// either -- and the three points that need it are reached at different moments:
// the negative-seq subreports before the band, the band's own measurement, and
// the non-negative subreports after it. Between the first and the second a
// subreport can eject, so a printwhen reading VERTICAL_SPACE, PAGE_NUMBER or
// PAGE_COUNT would answer differently at each. Asking here and passing the
// answer to measureDecided is what makes it one answer.
//
// The vertical position and space are set exactly as measureDecided sets them,
// because a printwhen may read them and they are what it would have read.
func (eng *engine) bandPrints(sec *tmpl.Section, fr *frame) (bool, error) {
	if sec == nil {
		return false, nil
	}
	eng.ctx.verticalPosition = geom.Round(fr.fillY - fr.top)
	eng.ctx.verticalSpace = geom.Round(fr.available())
	return eng.ctx.truth(sec.PrintWhen)
}

// runSubreports runs the subreports a band carries, on one side of it.
//
// The list is already ordered by seq and then document position. Negative seq
// runs before the band, non-negative after it, and the band is placed whole
// in between: a subreport emits bands of its own rather than content inside
// the host band's box, so a host band that splits is not split around it.
//
// prints is the band's own printwhen, from bandPrints.
// A band that does not print runs none of them.
func (eng *engine) runSubreports(sec *tmpl.Section, before, prints bool) error {
	if !prints || sec == nil || len(sec.Subreports) == 0 {
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

	// A subreport emits bands of its own into this frame, and they are the
	// child engine's rather than a band of the host's, so a balanced frame
	// has no way to carry them along when it moves one.
	fr.blockBalance()

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
	child := eng.nested(item)
	child.inline = sub.Inline
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

// nested builds an engine under this one, sharing the document
// and running one template's unit.
//
// It is short of everything a run needs -- no records, no frames, no page
// arrangement -- because what those should be is what the caller knows.
func (eng *engine) nested(item *unit) *engine {
	child := &engine{
		opts:    eng.opts,
		out:     eng.out,
		ctx:     newScopeContext(eng.ctx.buildTime),
		host:    eng,
		depth:   eng.depth + 1,
		pending: map[string][]*deferral{},
	}
	child.adopt(eng.doc)
	child.attach(item)
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
	case sub.EmbeddedLayout != nil:
		// The layout the name resolved to when the template was loaded.
		// Resolving it there rather than here is what keeps validation
		// and this from answering one name differently.
		//
		// An embedded layout shares the enclosing report's fonts and data,
		// so it shares the caches those names are keyed in.
		item = &unit{
			report:    embeddedReport(eng.report, sub.EmbeddedLayout),
			resolver:  eng.resolver,
			faces:     eng.faces,
			fontNames: eng.fontNames,
			blobs:     eng.blobs,
			published: eng.published,
			images:    eng.images,
		}
	default:
		return nil, fmt.Errorf("no embedded layout named %q is in scope here",
			sub.Embedded)
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
//
// The nested embedded layouts are carried across as they stand rather than
// merged with the host's. Nothing here looks a name up -- a subreport node
// already holds the layout its name resolved to at load -- so the list is
// a description of this layout and not a search path.
func embeddedReport(host *tmpl.Report, emb *tmpl.Embedded) *tmpl.Report {
	layout := *host.Layout
	layout.Styles = append(append([]*tmpl.Style{}, emb.Styles...), host.Layout.Styles...)
	layout.Embedded = emb.Embedded
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

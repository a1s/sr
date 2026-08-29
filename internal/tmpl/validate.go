package tmpl

import (
	"path/filepath"
	"strconv"

	"github.com/a1s/sr/internal/expr"
	"github.com/a1s/sr/internal/geom"
	"github.com/a1s/sr/internal/kdl"
	"go.starlark.net/starlark"
)

// namespace is the set of names one layout defines
//
// An embedded layout is its own namespace for parameters, records,
// variables, and groups, and shares the enclosing report's fonts and data.
type namespace struct {
	params  map[string]*Parameter
	vars    map[string]*Variable
	groups  map[string]*Group
	records *Records
	order   []string // group names, outermost first
}

func (psr *parser) validate(report *Report) {
	psr.uniqueNames(report)

	shared := &sharedNames{
		fonts: report.FontByName,
		data:  report.DataByName,
	}

	ns := psr.namespaceFor(report.Params, report.Variables, report.Records, report.Layout)
	report.GroupNames = ns.order

	if report.Layout != nil {
		psr.validateStyles(report.Layout.Styles, shared)
		psr.validateNamespace(ns, shared)
		psr.validateBody(&report.Layout.Body, ns, shared, false)
		if report.Layout.Body.Columns != nil {
			columns := report.Layout.Body.Columns
			width := report.Layout.Page.Width -
				report.Layout.LeftMargin - report.Layout.RightMargin
			if columns.Count > 0 {
				each := (width - float64(columns.Count-1)*columns.Gap) / float64(columns.Count)
				if each <= 0 {
					psr.errf(columns.Node, "count", "%d columns with a %g pt gap leaves no width in a %g pt frame", columns.Count, columns.Gap, width)
				}
			}
		}
		for _, embedded := range report.Layout.Embedded {
			psr.validateEmbedded(embedded, shared)
		}
	}

	psr.validateFinalUse(report, ns)
	psr.validateOutlineTargets(report)
	psr.validateSubreports(report)
}

type sharedNames struct {
	fonts map[string]*Font
	data  map[string]*DataBlob
}

func (psr *parser) uniqueNames(report *Report) {
	unique := func(kind string, nodes []*kdl.Node, names []string, into func(index int)) {
		seen := map[string]bool{}
		for index, name := range names {
			if name == "" {
				continue
			}
			if seen[name] {
				psr.errf(nodes[index], "", "duplicate %s %q", kind, name)
				continue
			}
			seen[name] = true
			into(index)
		}
	}
	nodes := func(count int, at func(index int) *kdl.Node) []*kdl.Node {
		out := make([]*kdl.Node, count)
		for index := range out {
			out[index] = at(index)
		}
		return out
	}

	names := make([]string, len(report.Params))
	for index, param := range report.Params {
		names[index] = param.Name
	}
	unique("parameter",
		nodes(len(report.Params), func(index int) *kdl.Node { return report.Params[index].Node }),
		names,
		func(index int) { report.ParamByName[report.Params[index].Name] = report.Params[index] },
	)

	names = make([]string, len(report.Variables))
	for index, variable := range report.Variables {
		names[index] = variable.Name
	}
	unique("variable",
		nodes(len(report.Variables), func(index int) *kdl.Node { return report.Variables[index].Node }),
		names,
		func(index int) { report.VarByName[report.Variables[index].Name] = report.Variables[index] },
	)

	names = make([]string, len(report.Fonts))
	for index, font := range report.Fonts {
		names[index] = font.Name
	}
	unique("font", nodes(len(report.Fonts), func(index int) *kdl.Node { return report.Fonts[index].Node }), names,
		func(index int) { report.FontByName[report.Fonts[index].Name] = report.Fonts[index] })

	names = make([]string, len(report.Data))
	for index, blob := range report.Data {
		names[index] = blob.Name
	}
	unique("data", nodes(len(report.Data), func(index int) *kdl.Node { return report.Data[index].Node }), names,
		func(index int) { report.DataByName[report.Data[index].Name] = report.Data[index] })

	if report.Layout != nil {
		seen := map[string]bool{}
		for _, embedded := range report.Layout.Embedded {
			if seen[embedded.Name] {
				psr.errf(embedded.Node, "", "duplicate embedded layout %q", embedded.Name)
			}
			seen[embedded.Name] = true
		}
	}
}

// namespaceFor collects the names one layout level defines.
func (psr *parser) namespaceFor(params []*Parameter, vars []*Variable, records *Records, layout *Layout) *namespace {
	ns := &namespace{
		params:  map[string]*Parameter{},
		vars:    map[string]*Variable{},
		groups:  map[string]*Group{},
		records: records,
	}
	for _, param := range params {
		ns.params[param.Name] = param
	}
	for _, variable := range vars {
		ns.vars[variable.Name] = variable
	}
	if layout != nil {
		psr.collectGroups(&layout.Body, ns)
	}
	return ns
}

func (psr *parser) collectGroups(body *Body, ns *namespace) {
	for group := body.Group; group != nil; group = group.Group {
		if _, dup := ns.groups[group.Name]; dup {
			psr.errf(group.Node, "", "duplicate group %q", group.Name)
			continue
		}
		ns.groups[group.Name] = group
		ns.order = append(ns.order, group.Name)
	}
}

// validateNamespace checks the names a layout level declares against the ones
// resolution puts first.
func (psr *parser) validateNamespace(ns *namespace, shared *sharedNames) {
	reserved := func(node *kdl.Node, kind, name string) {
		if name == "" {
			return
		}
		if expr.IsReserved(name) {
			psr.errf(node, "", "%s %q collides with a predefined name, a module, or a builtin, which resolution puts first", kind, name)
		}
	}
	for _, param := range ns.params {
		reserved(param.Node, "parameter", param.Name)
	}
	for _, variable := range ns.vars {
		reserved(variable.Node, "variable", variable.Name)
	}
	for _, group := range ns.groups {
		reserved(group.Node, "group", group.Name)
		// The derived names count too: a group called PAGE would produce a
		// PAGE_COUNT that already exists.
		for _, suffix := range []string{"_COUNT", "_PAGE_NUMBER"} {
			if expr.IsReserved(group.Name + suffix) {
				psr.errf(group.Node, "", "group %q would derive %s, which already exists", group.Name, group.Name+suffix)
			}
		}
	}
	for _, variable := range ns.vars {
		if variable.Iter == ScopeGroup && variable.IterGrp != "" {
			if _, ok := ns.groups[variable.IterGrp]; !ok {
				psr.errf(variable.Node, "itergrp", "no group named %q", variable.IterGrp)
			}
		}
		if variable.Reset == ScopeGroup && variable.ResetGrp != "" {
			if _, ok := ns.groups[variable.ResetGrp]; !ok {
				psr.errf(variable.Node, "resetgrp", "no group named %q", variable.ResetGrp)
			}
		}
	}
}

func (psr *parser) validateEmbedded(embedded *Embedded, shared *sharedNames) {
	seen := map[string]bool{}
	for _, param := range embedded.Params {
		if seen[param.Name] {
			psr.errf(param.Node, "", "duplicate parameter %q", param.Name)
			continue
		}
		seen[param.Name] = true
		embedded.ParamByName[param.Name] = param
	}
	seen = map[string]bool{}
	for _, variable := range embedded.Variables {
		if seen[variable.Name] {
			psr.errf(variable.Node, "", "duplicate variable %q", variable.Name)
			continue
		}
		seen[variable.Name] = true
		embedded.VarByName[variable.Name] = variable
	}
	ns := psr.namespaceFor(embedded.Params, embedded.Variables, embedded.Records, nil)
	psr.collectGroups(&embedded.Body, ns)
	embedded.GroupNames = ns.order
	psr.validateNamespace(ns, shared)
	psr.validateStyles(embedded.Styles, shared)
	psr.validateBody(&embedded.Body, ns, shared, false)
	for _, inner := range embedded.Embedded {
		psr.validateEmbedded(inner, shared)
	}
}

func (psr *parser) validateBody(body *Body, ns *namespace, shared *sharedNames, inColumns bool) {
	for _, section := range []*Section{body.Title, body.Summary, body.Header, body.Footer, body.Detail} {
		psr.validateSection(section, ns, shared, inColumns)
	}
	if body.Columns != nil {
		psr.validateStyles(body.Columns.Styles, shared)
		psr.validateSection(body.Columns.Header, ns, shared, true)
		psr.validateSection(body.Columns.Footer, ns, shared, true)
	}
	for group := body.Group; group != nil; group = group.Group {
		psr.validateStyles(group.Styles, shared)
		psr.validateSection(group.Title, ns, shared, inColumns)
		psr.validateSection(group.Summary, ns, shared, inColumns)
		psr.validateSection(group.Detail, ns, shared, inColumns)
		if group.Columns != nil {
			psr.validateStyles(group.Columns.Styles, shared)
			psr.validateSection(group.Columns.Header, ns, shared, true)
			psr.validateSection(group.Columns.Footer, ns, shared, true)
		}
	}
}

func (psr *parser) validateSection(section *Section, ns *namespace, shared *sharedNames, inColumns bool) {
	if section == nil {
		return
	}
	psr.validateStyles(section.Styles, shared)
	for _, sub := range section.Subreports {
		if inColumns {
			psr.errf(sub.Node, "", "a subreport may not appear inside a columns block")
		}
		if section.Kind == BandHeader || section.Kind == BandFooter {
			psr.errf(sub.Node, "",
				"a subreport emits bands of its own, and a %s band is measured and reserved before the page it belongs to is filled; put it on a title, summary or detail band",
				section.Kind)
		}
		// swapheader and swapfooter place a band outside the frame's ordinary
		// fill -- above the page header, below the page footer -- and a
		// subreport takes frame space, which there is none of on either side.
		if section.SwapHeader || section.SwapFooter {
			swap := "swapheader"
			if section.SwapFooter {
				swap = "swapfooter"
			}
			psr.errf(sub.Node, "",
				"%s places this band outside the frame's own fill, and a subreport takes frame space of its own, so the two cannot be combined",
				swap)
		}
	}
	for _, el := range section.Elements {
		psr.validateElement(el, ns, shared)
	}
	psr.warnCollapsingBand(section)
}

func (psr *parser) validateElement(el Element, ns *namespace, shared *sharedNames) {
	psr.validateStyles(el.Base().Styles, shared)
	switch typed := el.(type) {
	case *Field:
		psr.validateContent(typed.Node, &typed.Content, ns, shared)
		if typed.Float && !typed.Stretch && !typed.Vert.Size.Set {
			psr.errf(typed.Node, "float", "a floating element's height must come from the element: give it a height, or stretch=#true")
		}
	case *Barcode:
		psr.validateContent(typed.Node, &typed.Content, ns, shared)
	case *Image:
		if typed.Data != "" {
			if _, ok := shared.data[typed.Data]; !ok {
				psr.errf(typed.Node, "data", "no data blob named %q", typed.Data)
			}
		}
		if typed.Float && !typed.Vert.Size.Set && typed.Scale != ScaleGrow {
			psr.errf(typed.Node, "float", "a floating element's height must come from the element: give it a height, or scale=\"grow\"")
		}
	case *Xref:
		for _, inner := range typed.Elements {
			psr.validateElement(inner, ns, shared)
		}
	case *Line, *Rectangle:
		if el.Base().Float && !el.Base().Vert.Size.Set {
			psr.errf(el.Base().Node, "float", "a floating element's height must come from the element, not from the band")
		}
	}
}

func (psr *parser) validateContent(node *kdl.Node, content *Content, ns *namespace, shared *sharedNames) {
	if content.Data != "" {
		if _, ok := shared.data[content.Data]; !ok {
			psr.errf(node, "data", "no data blob named %q", content.Data)
		}
	}
	if content.EvalTime == "" {
		return
	}
	switch content.EvalTime {
	case "report", "page", "column":
	default:
		if _, ok := ns.groups[content.EvalTime]; !ok {
			psr.errf(node, "evaltime", "names neither report, page, column, nor a group defined here")
		}
	}
}

func (psr *parser) validateStyles(styles []*Style, shared *sharedNames) {
	for _, style := range styles {
		if style.HasFont {
			if _, ok := shared.fonts[style.Font]; !ok {
				psr.errf(style.Node, "font", "no font named %q", style.Font)
			}
		}
	}
}

// validateFinalUse enforces that FINAL and evaltime require each other and that
// FINAL appears in no property but a deferred expr.
func (psr *parser) validateFinalUse(report *Report, ns *namespace) {
	known := map[string]bool{}
	for _, name := range expr.PredefinedNames {
		known[name] = true
	}
	for _, name := range ns.order {
		known[name+"_COUNT"] = true
		known[name+"_PAGE_NUMBER"] = true
	}
	for name := range report.VarByName {
		known[name] = true
	}
	// An embedded layout's own variables and groups are reachable from within
	// it, so admit them too.
	if report.Layout != nil {
		var admit func(list []*Embedded)
		admit = func(list []*Embedded) {
			for _, embedded := range list {
				for _, variable := range embedded.Variables {
					known[variable.Name] = true
				}
				for _, name := range embedded.GroupNames {
					known[name+"_COUNT"] = true
					known[name+"_PAGE_NUMBER"] = true
				}
				admit(embedded.Embedded)
			}
		}
		admit(report.Layout.Embedded)
	}

	for _, site := range psr.programs {
		if site.prog == nil {
			continue
		}
		if !site.deferrable {
			if site.prog.UsesFinal {
				psr.errf(site.node, site.prop, "FINAL lives in the expr of a field or barcode with an evaltime, and nowhere else: everything here is evaluated when the band is measured, before the scope ends")
			}
			continue
		}
		if !site.prog.UsesFinal {
			psr.errf(site.node, "evaltime", "this expression never names FINAL, so deferring it would give the same answer in place")
		}
		for _, name := range site.prog.FinalNames {
			if !known[name] {
				psr.errf(site.node, "expr", "FINAL.%s is neither a predefined variable nor a declared variable; a parameter is constant and a record field belongs to a record, so reach one through FINAL.THIS", name)
			}
		}
	}
}

// validateOutlineTargets checks that every xref type="outline" has a reachable
// target. Both sides are expressions; the check applies where they are
// constant, which is the normal case and the only one decidable at load.
func (psr *parser) validateOutlineTargets(report *Report) {
	names := map[string]bool{}
	dynamic := false
	forEachSection(report, func(section *Section) {
		for _, outline := range section.Outlines {
			if outline.Name == nil {
				continue
			}
			if text, ok := constantString(outline.Name); ok {
				names[text] = true
			} else {
				dynamic = true
			}
		}
	})
	forEachSection(report, func(section *Section) {
		var walk func(els []Element)
		walk = func(els []Element) {
			for _, el := range els {
				xref, ok := el.(*Xref)
				if !ok {
					continue
				}
				walk(xref.Elements)
				if xref.Type != "outline" || xref.Target == nil {
					continue
				}
				target, ok := constantString(xref.Target)
				if !ok || dynamic {
					continue
				}
				if !names[target] {
					psr.errf(xref.Node, "target", "no outline is named %q", target)
				}
			}
		}
		walk(section.Elements)
	})
}

// subTarget is the layout a subreport runs, whichever way it was named.
type subTarget struct {
	// what names it in a diagnostic: the embedded layout's name, or the file.
	what   string
	params []*Parameter
	byName map[string]*Parameter
	body   *Body
	// page is the child's own page size, nil for an embedded layout,
	// which has none of its own and takes the enclosing report's.
	page *geom.PageSize
}

func (psr *parser) validateSubreports(report *Report) {
	embedded := map[string]*Embedded{}
	if report.Layout != nil {
		var index func(list []*Embedded)
		index = func(list []*Embedded) {
			for _, sub := range list {
				embedded[sub.Name] = sub
				index(sub.Embedded)
			}
		}
		index(report.Layout.Embedded)
	}
	forEachSection(report, func(section *Section) {
		for _, sub := range section.Subreports {
			target := psr.subreportTarget(sub, embedded)
			if target == nil {
				continue
			}
			psr.checkSubreportTarget(sub, target, report)
		}
	})
}

// subreportTarget resolves a subreport to the layout it runs,
// or nil when the reference is broken and has already been reported.
func (psr *parser) subreportTarget(sub *Subreport, embedded map[string]*Embedded) *subTarget {
	if sub.Embedded != "" {
		found, ok := embedded[sub.Embedded]
		if !ok {
			psr.errf(sub.Node, "embedded", "no embedded layout named %q", sub.Embedded)
			return nil
		}
		return &subTarget{
			what:   strconv.Quote(sub.Embedded),
			params: found.Params,
			byName: found.ParamByName,
			body:   &found.Body,
		}
	}
	if sub.Report == nil {
		// The template did not load, and that was reported where it failed.
		return nil
	}
	page := sub.Report.Layout.Page
	return &subTarget{
		what:   strconv.Quote(filepath.Base(sub.Template)),
		params: sub.Report.Params,
		byName: sub.Report.ParamByName,
		body:   &sub.Report.Layout.Body,
		page:   &page,
	}
}

// checkSubreportTarget applies the rules that need both sides:
// what inline forbids, and that the arguments and the parameters agree.
func (psr *parser) checkSubreportTarget(sub *Subreport, target *subTarget, report *Report) {
	if sub.Inline {
		body := target.body
		if body.Header != nil || body.Footer != nil {
			psr.errf(sub.Node, "inline",
				"an inline subreport shares the parent's pages, whose header and footer are already reserved; %s must not define them",
				target.what)
		}
		// A columns block reserves a frame across the pages it spans, and an
		// inline subreport does not own the pages it prints on -- the same
		// reason its header and footer are refused.
		if body.Columns != nil || hasGroupColumns(body) {
			psr.errf(sub.Node, "inline",
				"an inline subreport prints in the host's frame, and a columns block reserves a frame of its own across the pages it spans; %s must not open one",
				target.what)
		}
		// swapheader places a band outside the page header and swapfooter
		// below the page footer, and an inline subreport has neither.
		if body.Title != nil && body.Title.SwapHeader {
			psr.errf(sub.Node, "inline",
				"swapheader places a title above the page header, and an inline subreport prints on a page whose header is the host's; %s must not use it",
				target.what)
		}
		if body.Summary != nil && body.Summary.SwapFooter {
			psr.errf(sub.Node, "inline",
				"swapfooter places a summary below the page footer, and an inline subreport prints on a page whose footer is the host's; %s must not use it",
				target.what)
		}
		if target.page != nil && report.Layout != nil {
			mine := report.Layout.Page
			if target.page.Width != mine.Width || target.page.Height != mine.Height {
				psr.errf(sub.Node, "inline",
					"an inline subreport shares the parent's pages; %s is %g by %g pt and this report is %g by %g pt",
					target.what, target.page.Width, target.page.Height,
					mine.Width, mine.Height)
			}
		}
	}
	supplied := map[string]bool{}
	for _, arg := range sub.Args {
		if _, ok := target.byName[arg.Name]; !ok {
			psr.errf(arg.Node, "", "%s has no parameter named %q", target.what, arg.Name)
			continue
		}
		if supplied[arg.Name] {
			psr.errf(arg.Node, "", "duplicate arg %q", arg.Name)
		}
		supplied[arg.Name] = true
	}
	// A subreport has no command line to fall back on, so a parameter
	// with neither an arg nor a default has nothing left to be bound from.
	for _, param := range target.params {
		if supplied[param.Name] || !param.Required() {
			continue
		}
		psr.errf(sub.Node, "",
			"%s requires the parameter %q, which has no default and no arg here",
			target.what, param.Name)
	}
}

// hasGroupColumns reports whether any group in the body opens a columns block.
func hasGroupColumns(body *Body) bool {
	for group := body.Group; group != nil; group = group.Group {
		if group.Columns != nil {
			return true
		}
	}
	return false
}

// warnCollapsingBand reports a band that declares no height and holds only
// elements whose vertical extent comes from the band, which collapses to zero.
func (psr *parser) warnCollapsingBand(section *Section) {
	if section.Height.Set || len(section.Elements) == 0 {
		return
	}
	for _, el := range section.Elements {
		if hasOwnHeight(el) {
			return
		}
	}
	psr.warns = append(psr.warns, Diagnostic{
		File: psr.file, Line: section.Node.Line, Path: section.Node.Path(),
		Message: "this band declares no height and every element in it takes its height from the band, so the band collapses to nothing",
	})
}

// hasOwnHeight reports whether an element brings a height of its own, either
// declared or from its content.
func hasOwnHeight(el Element) bool {
	if el.Base().Vert.Size.Set {
		return true
	}
	switch typed := el.(type) {
	case *Field:
		return typed.Stretch
	case *Barcode:
		return true
	case *Image:
		return typed.Scale == ScaleGrow
	case *Xref:
		for _, inner := range typed.Elements {
			if hasOwnHeight(inner) {
				return true
			}
		}
	}
	return false
}

// constantString evaluates an expression that needs no names, which is how a
// literal target or outline name is read at load.
func constantString(prog *expr.Program) (string, bool) {
	if prog == nil || len(prog.Params) > 0 {
		return "", false
	}
	value, err := prog.Call(newThread(), nil)
	if err != nil {
		return "", false
	}
	return expr.Str(value), true
}

// ForEachSection visits every band in the report, including those in embedded
// layouts.
func ForEachSection(report *Report, fn func(*Section)) { forEachSection(report, fn) }

// forEachSection visits every band in the report, including those in embedded
// layouts.
func forEachSection(report *Report, fn func(*Section)) {
	if report.Layout == nil {
		return
	}
	visitBody(&report.Layout.Body, fn)
	var visitEmbedded func(list []*Embedded)
	visitEmbedded = func(list []*Embedded) {
		for _, embedded := range list {
			visitBody(&embedded.Body, fn)
			visitEmbedded(embedded.Embedded)
		}
	}
	visitEmbedded(report.Layout.Embedded)
}

func visitBody(body *Body, fn func(*Section)) {
	for _, section := range []*Section{body.Title, body.Summary, body.Header, body.Footer, body.Detail} {
		if section != nil {
			fn(section)
		}
	}
	if body.Columns != nil {
		for _, section := range []*Section{body.Columns.Header, body.Columns.Footer} {
			if section != nil {
				fn(section)
			}
		}
	}
	for group := body.Group; group != nil; group = group.Group {
		for _, section := range []*Section{group.Title, group.Summary, group.Detail} {
			if section != nil {
				fn(section)
			}
		}
		if group.Columns != nil {
			for _, section := range []*Section{group.Columns.Header, group.Columns.Footer} {
				if section != nil {
					fn(section)
				}
			}
		}
	}
}

// newThread makes a Starlark thread for load-time evaluation of constant
// expressions.
func newThread() *starlark.Thread { return &starlark.Thread{Name: "load"} }

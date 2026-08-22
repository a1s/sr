package tmpl

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/a1s/sr/internal/geom"
	"github.com/a1s/sr/internal/kdl"
)

// Load reads and validates a template file.
func Load(file string) (*Report, error) {
	nodes, err := kdl.ParseFile(file)
	if err != nil {
		return nil, err
	}
	return build(file, nodes)
}

// LoadString reads and validates a template from a string.
// The name is used in diagnostics and as the base for relative paths.
func LoadString(src, name string) (*Report, error) {
	nodes, err := kdl.ParseString(src, name)
	if err != nil {
		return nil, err
	}
	return build(name, nodes)
}

func build(file string, nodes []*kdl.Node) (*Report, error) {
	psr := &parser{file: file}
	if len(nodes) != 1 || nodes[0].Name != "report" {
		psr.errf(nil, "", "a template's root node is a single `report`")
		return nil, psr.diags
	}
	report := psr.parseReport(nodes[0])
	if report != nil {
		psr.validate(report)
		report.Warnings = psr.warns
	}
	if len(psr.diags) > 0 {
		return nil, psr.diags
	}
	return report, nil
}

func (psr *parser) parseReport(node *kdl.Node) *Report {
	psr.noArgs(node)
	pr := psr.props(node)
	report := &Report{
		File:        psr.file,
		Name:        pr.str("name", ""),
		Description: pr.str("description", ""),
		Version:     pr.str("version", ""),
		Author:      pr.str("author", ""),
		Node:        node,
		ParamByName: map[string]*Parameter{},
		VarByName:   map[string]*Variable{},
		FontByName:  map[string]*Font{},
		DataByName:  map[string]*DataBlob{},
	}
	base := pr.str("basedir", "")
	dir := filepath.Dir(psr.file)
	if base == "" {
		report.BaseDir = dir
	} else if filepath.IsAbs(base) {
		report.BaseDir = base
	} else {
		report.BaseDir = filepath.Join(dir, base)
	}
	pr.done()

	psr.allowChildren(node, "parameter", "records", "variable", "font", "data", "layout")

	for _, child := range node.ChildrenNamed("parameter") {
		if param := psr.parseParameter(child); param != nil {
			report.Params = append(report.Params, param)
		}
	}
	if rec := psr.atMostOne(node, "records"); rec != nil {
		report.Records = psr.parseRecords(rec)
	}
	for _, child := range node.ChildrenNamed("variable") {
		if variable := psr.parseVariable(child); variable != nil {
			report.Variables = append(report.Variables, variable)
		}
	}
	for _, child := range node.ChildrenNamed("font") {
		if font := psr.parseFont(child); font != nil {
			report.Fonts = append(report.Fonts, font)
		}
	}
	for _, child := range node.ChildrenNamed("data") {
		if blob := psr.parseData(child); blob != nil {
			report.Data = append(report.Data, blob)
		}
	}
	lay := node.ChildrenNamed("layout")
	switch len(lay) {
	case 0:
		psr.errf(node, "", "a report needs exactly one `layout`")
	case 1:
		report.Layout = psr.parseLayout(lay[0])
	default:
		psr.errf(lay[1], "", "a report has exactly one `layout`")
		report.Layout = psr.parseLayout(lay[0])
	}
	return report
}

func (psr *parser) parseParameter(node *kdl.Node) *Parameter {
	name := psr.name(node)
	pr := psr.props(node)
	param := &Parameter{
		Name:   name,
		Type:   enumProp(pr, "type", valueTypeNames, TypeString),
		Format: pr.str("format", ""),
		Prompt: pr.boolean("prompt", false),
		Node:   node,
	}
	if def, ok := pr.strOpt("default"); ok {
		param.Default, param.HasDefault = def, true
	}
	if src, ok := pr.strOpt("defaultexpr"); ok {
		param.DefaultExpr = psr.compile(node, "defaultexpr", src, false)
	}
	pr.done()
	psr.allowChildren(node)
	if param.HasDefault && param.DefaultExpr != nil {
		psr.errf(node, "default", "at most one of default and defaultexpr")
	}
	if param.Format != "" && !param.Type.IsTemporal() {
		psr.errf(node, "format",
			"a parsing layout applies only to a date or datetime parameter, not %s",
			param.Type)
	}
	return param
}

func (psr *parser) parseRecords(node *kdl.Node) *Records {
	psr.noArgs(node)
	psr.props(node).done()
	psr.allowChildren(node, "member")
	recs := &Records{ByName: map[string]*RecordMember{}, Node: node}
	for _, child := range node.ChildrenNamed("member") {
		name := psr.name(child)
		pr := psr.props(child)
		member := &RecordMember{
			Name:     name,
			Type:     enumProp(pr, "type", valueTypeNames, TypeString),
			Nullable: pr.boolean("nullable", false),
			Format:   pr.str("format", ""),
			Node:     child,
		}
		pr.done()
		psr.allowChildren(child)
		if member.Format != "" && !member.Type.IsTemporal() {
			psr.errf(child, "format",
				"a parsing layout applies only to a date or datetime member, not %s",
				member.Type)
		}
		if _, dup := recs.ByName[name]; dup {
			psr.errf(child, "", "duplicate member %q", name)
			continue
		}
		recs.ByName[name] = member
		recs.Members = append(recs.Members, member)
	}
	return recs
}

func (psr *parser) parseVariable(node *kdl.Node) *Variable {
	name := psr.name(node)
	pr := psr.props(node)
	variable := &Variable{
		Name:     name,
		Calc:     enumProp(pr, "calc", calcNames, CalcFirst),
		Iter:     enumProp(pr, "iter", scopeNames, ScopeDetail),
		IterGrp:  pr.str("itergrp", ""),
		Reset:    enumProp(pr, "reset", scopeNames, ScopeReport),
		ResetGrp: pr.str("resetgrp", ""),
		Node:     node,
	}
	src, ok := pr.strOpt("expr")
	if !ok {
		psr.errf(node, "expr", "required")
	} else {
		variable.Expr = psr.compile(node, "expr", src, false)
	}
	if init, ok := pr.strOpt("init"); ok {
		variable.Init = psr.compile(node, "init", init, false)
	}
	pr.done()
	psr.allowChildren(node)
	if variable.Iter == ScopeGroup && variable.IterGrp == "" {
		psr.errf(node, "itergrp", "iter=\"group\" needs the group's name")
	}
	if variable.Reset == ScopeGroup && variable.ResetGrp == "" {
		psr.errf(node, "resetgrp", "reset=\"group\" needs the group's name")
	}
	return variable
}

func (psr *parser) parseFont(node *kdl.Node) *Font {
	name := psr.name(node)
	pr := psr.props(node)
	font := &Font{
		Name:      name,
		Typeface:  pr.str("typeface", ""),
		File:      pr.str("file", ""),
		Data:      pr.str("data", ""),
		Bold:      pr.boolean("bold", false),
		Italic:    pr.boolean("italic", false),
		Underline: pr.boolean("underline", false),
		Node:      node,
	}
	font.Size = pr.integerRequired("size")
	pr.done()
	psr.allowChildren(node)

	sources := 0
	for _, text := range []string{font.Typeface, font.File, font.Data} {
		if text != "" {
			sources++
		}
	}
	if sources != 1 {
		psr.errf(node, "", "exactly one of typeface, file or data is required")
	}
	// bold and italic select a face only when resolving by typeface. With file
	// or data they select nothing, but they are not an error: they describe the
	// face the template named, and travel to the printout's font entry.
	if font.Size <= 0 {
		psr.errf(node, "size", "must be positive")
	}
	return font
}

func (psr *parser) parseData(node *kdl.Node) *DataBlob {
	name := psr.name(node)
	pr := psr.props(node)
	blob := &DataBlob{
		Name:     name,
		Encoding: pr.str("encoding", ""),
		Compress: pr.str("compress", ""),
		Node:     node,
	}
	if src, ok := pr.strOpt("expr"); ok {
		blob.Expr = psr.compile(node, "expr", src, false)
	}
	pr.done()
	psr.allowChildren(node, "content")
	if body := psr.atMostOne(node, "content"); body != nil {
		if len(body.Args) != 1 {
			psr.errf(body, "", "content takes one string argument")
		} else if text, ok := body.Args[0].Text(); ok {
			blob.Content, blob.HasBody = text, true
		} else {
			psr.errf(body, "", "content must be a string")
		}
	}
	if blob.Encoding != "" && blob.Encoding != "base64" {
		psr.errf(node, "encoding", "unknown encoding %q; want base64", blob.Encoding)
	}
	if blob.Compress != "" && blob.Compress != "zlib" && blob.Compress != "gzip" {
		psr.errf(node, "compress", "unknown compression %q; want zlib or gzip", blob.Compress)
	}
	if (blob.Expr != nil) == blob.HasBody {
		psr.errf(node, "", "exactly one of expr and a content child is required")
	}
	return blob
}

func (psr *parser) parseLayout(node *kdl.Node) *Layout {
	psr.noArgs(node)
	pr := psr.props(node)
	layout := &Layout{
		Landscape:    pr.boolean("landscape", false),
		LeftMargin:   pr.dimDefault("leftmargin", 0),
		RightMargin:  pr.dimDefault("rightmargin", 0),
		TopMargin:    pr.dimDefault("topmargin", 0),
		BottomMargin: pr.dimDefault("bottommargin", 0),
		Node:         node,
	}
	sizeName, hasName := pr.strOpt("pagesize")
	width, height := pr.dim("width"), pr.dim("height")
	pr.done()

	switch {
	case hasName:
		size, err := geom.LookupPageSize(sizeName)
		if err != nil {
			psr.errf(node, "pagesize", "%v", err)
		}
		layout.Page = size
		if width.Set || height.Set {
			psr.errf(node, "pagesize", "give either pagesize or both width and height, not both")
		}
	case width.Set && height.Set:
		layout.Page = geom.PageSize{Width: width.Value, Height: height.Value}
	default:
		psr.errf(node, "pagesize", "either pagesize, or both width and height, is required")
	}
	if layout.Landscape {
		layout.Page.Width, layout.Page.Height = layout.Page.Height, layout.Page.Width
	}

	psr.allowChildren(node, "style", "embedded", "title", "summary", "header", "footer", "columns", "group", "detail")
	layout.Styles = psr.parseStyles(node)
	for _, child := range node.ChildrenNamed("embedded") {
		if embedded := psr.parseEmbedded(child); embedded != nil {
			layout.Embedded = append(layout.Embedded, embedded)
		}
	}
	layout.Body = psr.parseBody(node, true)
	return layout
}

func (psr *parser) parseEmbedded(node *kdl.Node) *Embedded {
	name := psr.name(node)
	psr.props(node).done()
	embedded := &Embedded{
		Name:        name,
		Node:        node,
		ParamByName: map[string]*Parameter{},
		VarByName:   map[string]*Variable{},
	}
	psr.allowChildren(node, "parameter", "records", "variable", "style",
		"title", "summary", "header", "footer", "columns", "group", "detail", "embedded")
	for _, child := range node.ChildrenNamed("parameter") {
		if param := psr.parseParameter(child); param != nil {
			embedded.Params = append(embedded.Params, param)
		}
	}
	if rec := psr.atMostOne(node, "records"); rec != nil {
		embedded.Records = psr.parseRecords(rec)
	}
	for _, child := range node.ChildrenNamed("variable") {
		if variable := psr.parseVariable(child); variable != nil {
			embedded.Variables = append(embedded.Variables, variable)
		}
	}
	embedded.Styles = psr.parseStyles(node)
	for _, child := range node.ChildrenNamed("embedded") {
		if inner := psr.parseEmbedded(child); inner != nil {
			embedded.Embedded = append(embedded.Embedded, inner)
		}
	}
	embedded.Body = psr.parseBody(node, true)
	return embedded
}

// parseBody reads the bands a layout, embedded layout, or group holds.
func (psr *parser) parseBody(node *kdl.Node, allowHeaderFooter bool) Body {
	var body Body
	if title := psr.atMostOne(node, "title"); title != nil {
		body.Title = psr.parseSection(title, BandTitle)
	}
	if summary := psr.atMostOne(node, "summary"); summary != nil {
		body.Summary = psr.parseSection(summary, BandSummary)
	}
	if header := psr.atMostOne(node, "header"); header != nil {
		if allowHeaderFooter {
			body.Header = psr.parseSection(header, BandHeader)
		} else {
			psr.errf(header, "", "a group has no page header; use its columns block, or title")
		}
	}
	if footer := psr.atMostOne(node, "footer"); footer != nil {
		if allowHeaderFooter {
			body.Footer = psr.parseSection(footer, BandFooter)
		} else {
			psr.errf(footer, "", "a group has no page footer; use its columns block, or summary")
		}
	}
	if child := psr.atMostOne(node, "columns"); child != nil {
		body.Columns = psr.parseColumns(child)
	}
	group := psr.atMostOne(node, "group")
	detail := psr.atMostOne(node, "detail")
	switch {
	case group != nil && detail != nil:
		psr.errf(detail, "", "exactly one of group and detail at each level")
	case group != nil:
		body.Group = psr.parseGroup(group)
	case detail != nil:
		body.Detail = psr.parseSection(detail, BandDetail)
	default:
		psr.errf(node, "", "exactly one of group and detail is required here")
	}
	return body
}

func (psr *parser) parseColumns(node *kdl.Node) *Columns {
	psr.noArgs(node)
	pr := psr.props(node)
	columns := &Columns{
		Count: pr.integer("count", 0),
		Gap:   pr.dimDefault("gap", 0),
		Node:  node,
	}
	pr.done()
	psr.allowChildren(node, "style", "header", "footer")
	columns.Styles = psr.parseStyles(node)
	if header := psr.atMostOne(node, "header"); header != nil {
		columns.Header = psr.parseSection(header, BandHeader)
	}
	if footer := psr.atMostOne(node, "footer"); footer != nil {
		columns.Footer = psr.parseSection(footer, BandFooter)
	}
	if columns.Count < 1 {
		psr.errf(node, "count", "required, and at least 1")
	}
	return columns
}

func (psr *parser) parseGroup(node *kdl.Node) *Group {
	name := psr.name(node)
	pr := psr.props(node)
	group := &Group{
		Name:         name,
		KeepTogether: pr.boolean("keeptogether", false),
		MinRows:      pr.integer("minrows", 1),
		MinTailRows:  pr.integer("mintailrows", 1),
		Node:         node,
	}
	if src, ok := pr.strOpt("expr"); ok {
		group.Expr = psr.compile(node, "expr", src, false)
	} else {
		psr.errf(node, "expr", "required")
	}
	pr.done()
	psr.allowChildren(node, "style", "title", "summary", "columns", "group", "detail")
	group.Styles = psr.parseStyles(node)
	body := psr.parseBody(node, false)
	group.Title, group.Summary, group.Columns, group.Group, group.Detail = body.Title, body.Summary, body.Columns, body.Group, body.Detail
	if group.MinRows < 0 {
		psr.errf(node, "minrows", "must not be negative")
	}
	if group.MinTailRows < 0 {
		psr.errf(node, "mintailrows", "must not be negative")
	}
	return group
}

func (psr *parser) parseStyles(node *kdl.Node) []*Style {
	var out []*Style
	for _, child := range node.ChildrenNamed("style") {
		psr.noArgs(child)
		pr := psr.props(child)
		style := &Style{
			Color:   pr.color("color"),
			BgColor: pr.color("bgcolor"),
			Node:    child,
		}
		if font, ok := pr.strOpt("font"); ok {
			style.Font, style.HasFont = font, true
		}
		style.When = pr.expression("when")
		pr.done()
		psr.allowChildren(child)
		out = append(out, style)
	}
	return out
}

func (psr *parser) parseSection(node *kdl.Node, kind SectionKind) *Section {
	psr.noArgs(node)
	pr := psr.props(node)
	section := &Section{
		Kind:    kind,
		Split:   pr.boolean("split", false),
		Orphans: pr.integer("orphans", 1),
		Widows:  pr.integer("widows", 1),
		Node:    node,
	}
	// height is a dimension or the word "auto", which is the default.
	if value, ok := pr.raw("height"); ok {
		if text, isStr := value.Text(); isStr && text == "auto" {
			section.Height = geom.Unset()
		} else {
			section.Height = pr.dimValue("height", value)
		}
	}
	section.PrintWhen = pr.expression("printwhen")
	if kind == BandTitle {
		section.SwapHeader = pr.boolean("swapheader", false)
	}
	if kind == BandSummary {
		section.SwapFooter = pr.boolean("swapfooter", false)
	}
	pr.done()

	psr.allowChildren(node, "style", "eject", "outline", "field", "line", "rectangle",
		"image", "barcode", "xref", "subreport")
	section.Styles = psr.parseStyles(node)
	for _, child := range node.ChildrenNamed("eject") {
		section.Ejects = append(section.Ejects, psr.parseEject(child))
	}
	for _, child := range node.ChildrenNamed("outline") {
		section.Outlines = append(section.Outlines, psr.parseOutline(child))
	}
	section.Elements = psr.parseElements(node)
	order := 0
	for _, child := range node.ChildrenNamed("subreport") {
		sub := psr.parseSubreport(child)
		sub.DocOrder = order
		order++
		section.Subreports = append(section.Subreports, sub)
	}
	sort.SliceStable(section.Subreports, func(one, two int) bool {
		if section.Subreports[one].Seq != section.Subreports[two].Seq {
			return section.Subreports[one].Seq < section.Subreports[two].Seq
		}
		return section.Subreports[one].DocOrder < section.Subreports[two].DocOrder
	})

	if section.Orphans < 1 {
		psr.errf(node, "orphans", "must be at least 1")
	}
	if section.Widows < 1 {
		psr.errf(node, "widows", "must be at least 1")
	}
	return section
}

func (psr *parser) parseEject(node *kdl.Node) *Eject {
	psr.noArgs(node)
	pr := psr.props(node)
	eject := &Eject{
		Type:    enumProp(pr, "type", map[string]EjectType{"page": EjectPage, "column": EjectColumn}, EjectPage),
		Require: pr.dim("require"),
		Node:    node,
	}
	eject.When = pr.expression("when")
	pr.done()
	psr.allowChildren(node)
	return eject
}

func (psr *parser) parseOutline(node *kdl.Node) *Outline {
	psr.noArgs(node)
	pr := psr.props(node)
	outline := &Outline{
		Level:  pr.integer("level", 1),
		Closed: pr.boolean("closed", false),
		Node:   node,
	}
	if src, ok := pr.strOpt("title"); ok {
		outline.Title = psr.compile(node, "title", src, false)
	} else {
		psr.errf(node, "title", "required")
	}
	outline.Name = pr.expression("name")
	outline.When = pr.expression("when")
	pr.done()
	psr.allowChildren(node)
	if outline.Level < 1 {
		psr.errf(node, "level", "must be at least 1")
	}
	return outline
}

func (psr *parser) parseSubreport(node *kdl.Node) *Subreport {
	psr.noArgs(node)
	pr := psr.props(node)
	subreport := &Subreport{
		Template:  pr.str("template", ""),
		Embedded:  pr.str("embedded", ""),
		Inline:    pr.boolean("inline", false),
		OwnPageNo: pr.boolean("ownpageno", false),
		Node:      node,
	}
	subreport.Seq = pr.integerRequired("seq")
	if src, ok := pr.strOpt("data"); ok {
		subreport.Data = psr.compile(node, "data", src, false)
	} else {
		psr.errf(node, "data", "required")
	}
	subreport.When = pr.expression("when")
	pr.done()
	psr.allowChildren(node, "arg")
	for _, child := range node.ChildrenNamed("arg") {
		name := psr.name(child)
		apr := psr.props(child)
		arg := &Arg{Name: name, Node: child}
		if src, ok := apr.strOpt("value"); ok {
			arg.Value = psr.compile(child, "value", src, false)
		} else {
			psr.errf(child, "value", "required")
		}
		apr.done()
		psr.allowChildren(child)
		subreport.Args = append(subreport.Args, arg)
	}
	if (subreport.Template != "") == (subreport.Embedded != "") {
		psr.errf(node, "", "exactly one of template and embedded is required")
	}
	if subreport.Inline && subreport.OwnPageNo {
		psr.errf(node, "ownpageno", "inline shares the parent's pagination, so it cannot restart numbering")
	}
	return subreport
}

func (psr *parser) parseElements(node *kdl.Node) []Element {
	var out []Element
	for _, child := range node.Children {
		switch child.Name {
		case "field":
			out = append(out, psr.parseField(child))
		case "line":
			out = append(out, psr.parseLine(child))
		case "rectangle":
			out = append(out, psr.parseRectangle(child))
		case "image":
			out = append(out, psr.parseImage(child))
		case "barcode":
			out = append(out, psr.parseBarcode(child))
		case "xref":
			out = append(out, psr.parseXref(child))
		}
	}
	return out
}

// geometry reads the position and size properties.
//
// widthIsExtent is false for a line and a rectangle, whose `width` is the
// stroke width rather than a horizontal extent.
func (psr *parser) geometry(pr *props, widthIsExtent bool) (geom.Extent, geom.Extent) {
	var horiz, vert geom.Extent
	horiz.Near = pr.dim("left")
	if alias := pr.dim("x"); alias.Set {
		if horiz.Near.Set {
			psr.errf(pr.node, "x", "x is an alias for left; give one of them")
		}
		horiz.Near = alias
	}
	horiz.Far = pr.dim("right")
	if widthIsExtent {
		horiz.Size = pr.dim("width")
	}
	horiz.Max = pr.dim("maxwidth")

	vert.Near = pr.dim("top")
	if alias := pr.dim("y"); alias.Set {
		if vert.Near.Set {
			psr.errf(pr.node, "y", "y is an alias for top; give one of them")
		}
		vert.Near = alias
	}
	vert.Far = pr.dim("bottom")
	vert.Size = pr.dim("height")
	vert.Max = pr.dim("maxheight")

	if horiz.Count() > 2 {
		psr.errf(pr.node, "width", "at most two of left, right and width; the third follows")
	}
	if vert.Count() > 2 {
		psr.errf(pr.node, "height", "at most two of top, bottom and height; the third follows")
	}
	return horiz, vert
}

func (psr *parser) common(pr *props, widthIsExtent bool) Common {
	common := Common{Node: pr.node}
	common.Horiz, common.Vert = psr.geometry(pr, widthIsExtent)
	common.HAlign = enumProp(pr, "halign", map[string]geom.HAlign{
		"left": geom.HLeft, "center": geom.HCenter, "right": geom.HRight,
	}, geom.HLeft)
	common.VAlign = enumProp(pr, "valign", map[string]geom.VAlign{
		"top": geom.VTop, "center": geom.VCenter, "bottom": geom.VBottom,
	}, geom.VTop)
	common.Float = pr.boolean("float", false)
	common.PrintWhen = pr.expression("printwhen")
	common.Styles = psr.parseStyles(pr.node)
	return common
}

// content reads the expr / text / data trio a field or barcode takes.
func (psr *parser) content(pr *props) Content {
	var content Content
	content.EvalTime = pr.str("evaltime", "")
	content.Format = pr.str("format", "%s")
	if src, ok := pr.strOpt("expr"); ok {
		content.Expr = psr.compile(pr.node, "expr", src, content.EvalTime != "")
	}
	if text, ok := pr.strOpt("text"); ok {
		content.Text, content.HasText = text, true
	}
	content.Data = pr.str("data", "")
	return content
}

func (psr *parser) checkContent(node *kdl.Node, content *Content, needsPlaceholder bool) {
	sources := 0
	if content.Expr != nil {
		sources++
	}
	if content.HasText {
		sources++
	}
	if content.Data != "" {
		sources++
	}
	if !content.Deferred() {
		if sources != 1 {
			psr.errf(node, "", "exactly one of expr, text and data is required")
		}
		return
	}
	if content.Expr == nil {
		psr.errf(node, "evaltime", "a deferred element takes its content from expr, which is what gets deferred")
	}
	placeholders := sources
	if content.Expr != nil {
		placeholders--
	}
	if placeholders > 1 {
		psr.errf(node, "", "at most one placeholder beside expr")
	}
	if needsPlaceholder && placeholders == 0 {
		psr.errf(node, "text", "this element's size depends on its content, so a deferred value needs a placeholder to reserve space with")
	}
}

func (psr *parser) parseField(node *kdl.Node) *Field {
	psr.noArgs(node)
	pr := psr.props(node)
	field := &Field{}
	field.Common = psr.common(pr, true)
	field.Content = psr.content(pr)
	_, field.HasAlign = node.Prop("align")
	field.Align = enumProp(pr, "align", textAlignNames, AlignLeft)
	field.Stretch = pr.boolean("stretch", false)
	pr.done()
	psr.allowChildren(node, "style")
	psr.checkContent(node, &field.Content, field.Stretch)
	return field
}

func (psr *parser) parseLine(node *kdl.Node) *Line {
	psr.noArgs(node)
	pr := psr.props(node)
	line := &Line{}
	line.Common = psr.common(pr, false)
	line.PenWidth = pr.dimDefault("width", 0)
	line.Dash = enumProp(pr, "dash", dashNames, DashSolid)
	line.Backslant = pr.boolean("backslant", false)
	pr.done()
	psr.allowChildren(node, "style")
	if line.PenWidth < 0 {
		psr.errf(node, "width", "a stroke width must not be negative")
	}
	return line
}

func (psr *parser) parseRectangle(node *kdl.Node) *Rectangle {
	psr.noArgs(node)
	pr := psr.props(node)
	rect := &Rectangle{}
	rect.Common = psr.common(pr, false)
	rect.PenWidth = pr.dimDefault("width", 0)
	rect.Dash = enumProp(pr, "dash", dashNames, DashSolid)
	rect.Radius = pr.dimDefault("radius", 0)
	rect.Opaque = pr.boolean("opaque", true)
	rect.Stroke = pr.boolean("stroke", true)
	pr.done()
	psr.allowChildren(node, "style")
	if rect.PenWidth < 0 {
		psr.errf(node, "width", "a stroke width must not be negative")
	}
	return rect
}

func (psr *parser) parseImage(node *kdl.Node) *Image {
	psr.noArgs(node)
	pr := psr.props(node)
	image := &Image{}
	image.Common = psr.common(pr, true)
	image.File = pr.str("file", "")
	image.Data = pr.str("data", "")
	image.Type = pr.str("type", "")
	image.Scale = enumProp(pr, "scale", scaleNames, ScaleCut)
	image.Proportional = pr.boolean("proportional", true)
	image.Embed = pr.boolean("embed", true)
	pr.done()
	psr.allowChildren(node, "style", "content")
	if body := psr.atMostOne(node, "content"); body != nil {
		if len(body.Args) == 1 {
			if text, ok := body.Args[0].Text(); ok {
				image.Content, image.HasContent = text, true
			} else {
				psr.errf(body, "", "content must be a string")
			}
		} else {
			psr.errf(body, "", "content takes one string argument")
		}
	}
	sources := 0
	for _, present := range []bool{image.File != "", image.Data != "", image.HasContent} {
		if present {
			sources++
		}
	}
	if sources != 1 {
		psr.errf(node, "", "exactly one of file, data and a content child is required")
	}
	if !image.Embed && image.File == "" {
		// embed=#false writes a path into the printout instead of the bytes,
		// and only `file` supplies one.
		psr.errf(node, "embed",
			"embed=#false records a reference to the image file, so it needs file=; data and a content child are always embedded")
	}
	switch image.Type {
	case "", "png", "jpeg", "gif":
	default:
		psr.errf(node, "type", "unknown image type %q; want png, jpeg or gif", image.Type)
	}
	return image
}

func (psr *parser) parseBarcode(node *kdl.Node) *Barcode {
	psr.noArgs(node)
	pr := psr.props(node)
	barcode := &Barcode{}
	barcode.Common = psr.common(pr, true)
	barcode.Content = psr.content(pr)
	barcode.Type = pr.str("type", "")
	barcode.Module = pr.dimDefault("module", mustDim("10mil"))
	barcode.Vertical = pr.boolean("vertical", false)
	barcode.Grow = pr.boolean("grow", false)
	pr.done()
	psr.allowChildren(node, "style")
	if barcode.Type == "" {
		psr.errf(node, "type", "required")
	} else if !BarcodeTypes[barcode.Type] {
		psr.errf(node, "type", "unknown barcode type %q", barcode.Type)
	}
	if barcode.Module <= 0 {
		psr.errf(node, "module", "must be positive")
	}
	// A barcode's box always grows along the coding direction, so a deferred
	// one always needs a placeholder.
	psr.checkContent(node, &barcode.Content, true)
	return barcode
}

func (psr *parser) parseXref(node *kdl.Node) *Xref {
	psr.noArgs(node)
	pr := psr.props(node)
	xref := &Xref{}
	xref.Common = psr.common(pr, true)
	xref.Type = pr.str("type", "")
	if src, ok := pr.strOpt("target"); ok {
		xref.Target = psr.compile(node, "target", src, false)
	} else {
		psr.errf(node, "target", "required")
	}
	xref.Caption = pr.expression("caption")
	pr.done()
	psr.allowChildren(node, "style", "field", "line", "rectangle", "image", "barcode", "xref")
	xref.Elements = psr.parseElements(node)
	switch xref.Type {
	case "url", "outline":
	case "":
		psr.errf(node, "type", "required")
	default:
		psr.errf(node, "type", "unknown xref type %q; want url or outline", xref.Type)
	}
	return xref
}

func mustDim(text string) float64 {
	value, err := geom.ParseDim(text)
	if err != nil {
		panic(fmt.Sprintf("bad built-in dimension %q: %v", text, err))
	}
	return value
}

// Package tmpl holds the template model of doc/template.md: the node tree
// a KDL document parses into, and the validation that runs once at load,
// before any data is read.
package tmpl

import (
	"github.com/a1s/sr/internal/expr"
	"github.com/a1s/sr/internal/geom"
	"github.com/a1s/sr/internal/kdl"
)

// ValueType is a declared parameter or record member type.
type ValueType int

// Declared value types.
const (
	TypeString ValueType = iota
	TypeInt
	TypeDecimal
	TypeFloat
	TypeBool
	TypeDatetime
	TypeDate
	TypeObject
	TypeList
)

var valueTypeNames = map[string]ValueType{
	"string":   TypeString,
	"int":      TypeInt,
	"decimal":  TypeDecimal,
	"float":    TypeFloat,
	"bool":     TypeBool,
	"datetime": TypeDatetime,
	"date":     TypeDate,
	"object":   TypeObject,
	"list":     TypeList,
}

// String names the type.
func (kind ValueType) String() string {
	for name, known := range valueTypeNames {
		if known == kind {
			return name
		}
	}
	return "?"
}

// IsTemporal reports whether the type is date or datetime, which are the two
// that accept a `format` parsing layout.
func (kind ValueType) IsTemporal() bool { return kind == TypeDate || kind == TypeDatetime }

// CalcMode is a variable's accumulation mode.
type CalcMode int

// Accumulation modes.
const (
	CalcFirst CalcMode = iota
	CalcLast
	CalcCount
	CalcList
	CalcSet
	CalcChain
	CalcSum
	CalcAvg
	CalcMin
	CalcMax
	CalcStd
	CalcVar
)

var calcNames = map[string]CalcMode{
	"first": CalcFirst, "last": CalcLast, "count": CalcCount,
	"list": CalcList, "set": CalcSet, "chain": CalcChain,
	"sum": CalcSum, "avg": CalcAvg, "min": CalcMin, "max": CalcMax,
	"std": CalcStd, "var": CalcVar,
}

// String names the mode.
func (calc CalcMode) String() string {
	for name, known := range calcNames {
		if known == calc {
			return name
		}
	}
	return "?"
}

// Retains reports whether the mode has to keep individual values.
//
// Everything else is an incremental accumulator with constant memory.
func (calc CalcMode) Retains() bool {
	return calc == CalcList || calc == CalcSet || calc == CalcChain
}

// Scope is a variable's iteration or reset scope.
type Scope int

// Scopes.
const (
	ScopeReport Scope = iota
	ScopePage
	ScopeColumn
	ScopeGroup
	ScopeDetail
	ScopeItem
)

var scopeNames = map[string]Scope{
	"report": ScopeReport, "page": ScopePage, "column": ScopeColumn,
	"group": ScopeGroup, "detail": ScopeDetail, "item": ScopeItem,
}

// String names the scope.
func (scope Scope) String() string {
	for name, known := range scopeNames {
		if known == scope {
			return name
		}
	}
	return "?"
}

// TextAlign is a field's per-line text alignment.
type TextAlign int

// Text alignments.
const (
	AlignLeft TextAlign = iota
	AlignCenter
	AlignRight
	AlignJustified
)

var textAlignNames = map[string]TextAlign{
	"left": AlignLeft, "center": AlignCenter,
	"right": AlignRight, "justified": AlignJustified,
}

// String names the alignment, as the printout spells it.
func (align TextAlign) String() string {
	for name, known := range textAlignNames {
		if known == align {
			return name
		}
	}
	return "left"
}

// Dash is a stroke dash pattern.
type Dash int

// Dash patterns.
const (
	DashSolid Dash = iota
	DashDot
	DashDash
	DashDotDash
)

var dashNames = map[string]Dash{
	"solid": DashSolid, "dot": DashDot, "dash": DashDash, "dashdot": DashDotDash,
}

// String names the pattern, as the printout spells it.
func (dash Dash) String() string {
	for name, known := range dashNames {
		if known == dash {
			return name
		}
	}
	return "solid"
}

// ScaleMode is how an image meets its box.
type ScaleMode int

// Image scale modes.
const (
	ScaleCut ScaleMode = iota
	ScaleFill
	ScaleGrow
)

var scaleNames = map[string]ScaleMode{
	"cut": ScaleCut, "fill": ScaleFill, "grow": ScaleGrow,
}

// String names the mode.
func (scale ScaleMode) String() string {
	for name, known := range scaleNames {
		if known == scale {
			return name
		}
	}
	return "cut"
}

// SectionKind names one of the five bands.
type SectionKind int

// Band kinds.
const (
	BandTitle SectionKind = iota
	BandSummary
	BandHeader
	BandFooter
	BandDetail
)

// String names the band.
func (kind SectionKind) String() string {
	switch kind {
	case BandTitle:
		return "title"
	case BandSummary:
		return "summary"
	case BandHeader:
		return "header"
	case BandFooter:
		return "footer"
	}
	return "detail"
}

// Report is the root of a template.
type Report struct {
	File        string
	BaseDir     string
	Name        string
	Description string
	Version     string
	Author      string

	Params    []*Parameter
	Records   *Records
	Variables []*Variable
	Fonts     []*Font
	Data      []*DataBlob
	Layout    *Layout

	ParamByName map[string]*Parameter
	VarByName   map[string]*Variable
	FontByName  map[string]*Font
	DataByName  map[string]*DataBlob
	GroupNames  []string

	// Warnings are load-time diagnostics that do not stop a build.
	Warnings DiagnosticList

	Node *kdl.Node
}

// Parameter is a value supplied by the caller.
type Parameter struct {
	Name        string
	Type        ValueType
	Default     string
	HasDefault  bool
	DefaultExpr *expr.Program
	Format      string
	Prompt      bool
	Node        *kdl.Node
}

// Required reports whether the caller must supply a value.
func (param *Parameter) Required() bool { return !param.HasDefault && param.DefaultExpr == nil }

// RecordMember declares one member of the input records, from a `member` node.
//
// Neither Column nor Field: a Column is an area on the page and a Field is
// a band element that prints a value, and this package holds all three.
type RecordMember struct {
	Name     string
	Type     ValueType
	Nullable bool
	Format   string
	Node     *kdl.Node
}

// Records declares the members of the input records.
type Records struct {
	Members []*RecordMember
	ByName  map[string]*RecordMember
	Node    *kdl.Node
}

// Names lists the declared member names in declaration order.
func (records *Records) Names() []string {
	if records == nil {
		return nil
	}
	out := make([]string, 0, len(records.Members))
	for _, member := range records.Members {
		out = append(out, member.Name)
	}
	return out
}

// Variable is an accumulator updated as data is consumed.
type Variable struct {
	Name     string
	Expr     *expr.Program
	Init     *expr.Program
	Calc     CalcMode
	Iter     Scope
	IterGrp  string
	Reset    Scope
	ResetGrp string
	Node     *kdl.Node
}

// Font is a named font definition.
type Font struct {
	Name      string
	Typeface  string
	File      string
	Data      string
	Size      int
	Bold      bool
	Italic    bool
	Underline bool
	Node      *kdl.Node
}

// DataBlob is a named literal blob.
type DataBlob struct {
	Name     string
	Encoding string
	Compress string
	Expr     *expr.Program
	Content  string
	HasBody  bool
	Node     *kdl.Node
}

// Layout is page geometry and the root of the band tree.
type Layout struct {
	Page      geom.PageSize
	Landscape bool

	LeftMargin   float64
	RightMargin  float64
	TopMargin    float64
	BottomMargin float64

	Styles   []*Style
	Embedded []*Embedded
	Body     Body
	Node     *kdl.Node
}

// Body is the part of a layout that band containers share: the optional bands,
// an optional columns block, and exactly one of group or detail.
type Body struct {
	Title   *Section
	Summary *Section
	Header  *Section
	Footer  *Section
	Columns *Columns
	Group   *Group
	Detail  *Section
}

// Embedded is a subreport layout defined inline.
type Embedded struct {
	Name      string
	Params    []*Parameter
	Records   *Records
	Variables []*Variable
	Styles    []*Style
	Body      Body
	Embedded  []*Embedded

	ParamByName map[string]*Parameter
	VarByName   map[string]*Variable
	GroupNames  []string

	Node *kdl.Node
}

// Columns splits the enclosing frame.
type Columns struct {
	Count  int
	Gap    float64
	Styles []*Style
	Header *Section
	Footer *Section
	Node   *kdl.Node
}

// Group is a data-driven grouping level.
type Group struct {
	Name         string
	Expr         *expr.Program
	KeepTogether bool
	MinRows      int
	MinTailRows  int
	Styles       []*Style
	Title        *Section
	Summary      *Section
	Columns      *Columns
	Group        *Group
	Detail       *Section
	Node         *kdl.Node
}

// Section is one band.
type Section struct {
	Kind       SectionKind
	Height     geom.Opt // unset means height="auto"
	PrintWhen  *expr.Program
	Split      bool
	Orphans    int
	Widows     int
	SwapHeader bool
	SwapFooter bool

	Styles     []*Style
	Ejects     []*Eject
	Outlines   []*Outline
	Elements   []Element
	Subreports []*Subreport

	Node *kdl.Node
}

// Style is conditional formatting.
type Style struct {
	When    *expr.Program
	Font    string
	HasFont bool
	Color   *Color
	BgColor *Color
	Node    *kdl.Node
}

// EjectType is a page or column break.
type EjectType int

// Eject kinds.
const (
	EjectPage EjectType = iota
	EjectColumn
)

// String names the kind.
func (kind EjectType) String() string {
	if kind == EjectColumn {
		return "column"
	}
	return "page"
}

// Eject forces a break.
type Eject struct {
	Type    EjectType
	When    *expr.Program
	Require geom.Opt
	Node    *kdl.Node
}

// Outline is a document outline entry.
type Outline struct {
	Title  *expr.Program
	Level  int
	Name   *expr.Program
	When   *expr.Program
	Closed bool
	Node   *kdl.Node
}

// Subreport runs another template over a nested sequence.
type Subreport struct {
	Template string
	// Report is the template named by Template, loaded with this one.
	// Nil for a subreport that names an embedded layout instead.
	Report    *Report
	Embedded  string
	Seq       int
	Data      *expr.Program
	When      *expr.Program
	Inline    bool
	OwnPageNo bool
	Args      []*Arg
	DocOrder  int
	Node      *kdl.Node
}

// Arg supplies a value for a subreport parameter.
type Arg struct {
	Name  string
	Value *expr.Program
	Node  *kdl.Node
}

// Common holds what every body element and xref carries.
type Common struct {
	Horiz     geom.Extent
	Vert      geom.Extent
	HAlign    geom.HAlign
	VAlign    geom.VAlign
	Float     bool
	PrintWhen *expr.Program
	Styles    []*Style
	Node      *kdl.Node
}

// Base gives access to the shared properties.
func (common *Common) Base() *Common { return common }

// Element is a body element or an xref.
type Element interface {
	Base() *Common
	// Kind names the element for diagnostics and for the printout.
	Kind() string
}

// Content is where a field's or barcode's text comes from.
type Content struct {
	Expr     *expr.Program
	Text     string
	HasText  bool
	Data     string
	EvalTime string
	Format   string
}

// Deferred reports whether the element's expression is evaluated at the end
// of a scope rather than where the element sits.
func (content *Content) Deferred() bool { return content.EvalTime != "" }

// HasPlaceholder reports whether a deferred element carries
// content to be measured from before its value is known.
func (content *Content) HasPlaceholder() bool { return content.HasText || content.Data != "" }

// Field is text.
type Field struct {
	Common
	Content
	Align TextAlign
	// HasAlign records whether the template wrote align,
	// which is what lets halign supply the alignment when it did not.
	HasAlign bool
	Stretch  bool
}

// Kind names the element.
func (field *Field) Kind() string { return "field" }

// Line draws a rule corner to corner of its box.
type Line struct {
	Common
	PenWidth  float64
	Dash      Dash
	Backslant bool
}

// Kind names the element.
func (line *Line) Kind() string { return "line" }

// Rectangle draws a box.
type Rectangle struct {
	Common
	PenWidth float64
	Dash     Dash
	Radius   float64
	Opaque   bool
	Stroke   bool
}

// Kind names the element.
func (rect *Rectangle) Kind() string { return "rectangle" }

// Image draws a bitmap.
type Image struct {
	Common
	File         string
	Data         string
	Content      string
	HasContent   bool
	Type         string
	Scale        ScaleMode
	Proportional bool
	Embed        bool
}

// Kind names the element.
func (image *Image) Kind() string { return "image" }

// Barcode draws a symbol.
type Barcode struct {
	Common
	Content
	Type     string
	Module   float64
	Vertical bool
	Grow     bool
}

// Kind names the element.
func (barcode *Barcode) Kind() string { return "barcode" }

// Xref is a link region containing its own body elements.
type Xref struct {
	Common
	Type     string
	Target   *expr.Program
	Caption  *expr.Program
	Elements []Element
}

// Kind names the element.
func (xref *Xref) Kind() string { return "xref" }

// BarcodeTypes lists the symbologies the format accepts.
var BarcodeTypes = map[string]bool{
	"Code128": true, "Code39": true, "Code93": true, "2of5i": true,
	"DataMatrix": true, "Aztec": true,
	"QR-L": true, "QR-M": true, "QR-Q": true, "QR-H": true,
}

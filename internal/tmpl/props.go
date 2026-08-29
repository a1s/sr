package tmpl

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/a1s/sr/internal/expr"
	"github.com/a1s/sr/internal/geom"
	"github.com/a1s/sr/internal/kdl"
)

// Diagnostic is one validation message.
//
// Every one names the file, the node path, and, where the fault is
// in a property rather than in the node, the property.
type Diagnostic struct {
	File    string
	Line    int
	Path    string
	Prop    string
	Message string
}

// Error renders the diagnostic.
func (diag Diagnostic) Error() string {
	var buf strings.Builder
	fmt.Fprintf(&buf, "%s:%d: %s", path.Base(diag.File), diag.Line, diag.Path)
	if diag.Prop != "" {
		fmt.Fprintf(&buf, " %s=", diag.Prop)
	}
	fmt.Fprintf(&buf, ": %s", diag.Message)
	return buf.String()
}

// DiagnosticList is a set of validation messages.
type DiagnosticList []Diagnostic

// Error renders every diagnostic, one per line.
func (diags DiagnosticList) Error() string {
	parts := make([]string, len(diags))
	for index, diag := range diags {
		parts[index] = diag.Error()
	}
	return strings.Join(parts, "\n")
}

// programSite records where a compiled expression came from,
// so that the FINAL rules can be checked once the whole template is read.
type programSite struct {
	prog       *expr.Program
	node       *kdl.Node
	prop       string
	deferrable bool // the expr of a field or barcode carrying an evaltime
}

type parser struct {
	file string
	// stack is the templates already being loaded, outermost first,
	// so that a subreport naming one of them is a cycle rather than a hang.
	stack    []string
	diags    DiagnosticList
	warns    DiagnosticList
	programs []programSite
}

func (psr *parser) errf(node *kdl.Node, prop, format string, args ...any) {
	diag := Diagnostic{
		File:    psr.file,
		Path:    "<document>",
		Prop:    prop,
		Message: fmt.Sprintf(format, args...),
	}
	if node != nil {
		diag.Line, diag.Path = node.Line, node.Path()
	}
	psr.diags = append(psr.diags, diag)
}

// props reads a node's properties, tracking which were consumed
// so that an unrecognised one can be reported rather than ignored.
type props struct {
	psr  *parser
	node *kdl.Node
	used map[string]bool
}

func (psr *parser) props(node *kdl.Node) *props {
	return &props{psr: psr, node: node, used: map[string]bool{}}
}

func (pr *props) raw(key string) (kdl.Value, bool) {
	pr.used[key] = true
	value, ok := pr.node.Prop(key)
	return value, ok
}

// done reports every property the node carried that nothing read.
func (pr *props) done() {
	for _, key := range pr.node.PropNames() {
		if !pr.used[key] {
			pr.psr.errf(pr.node, key, "unknown property")
		}
	}
}

func (pr *props) str(key, def string) string {
	value, ok := pr.raw(key)
	if !ok {
		return def
	}
	text, isStr := value.Text()
	if !isStr {
		pr.psr.errf(pr.node, key, "want a string, got %s", value.Kind)
		return def
	}
	return text
}

func (pr *props) strOpt(key string) (string, bool) {
	value, ok := pr.raw(key)
	if !ok {
		return "", false
	}
	text, isStr := value.Text()
	if !isStr {
		pr.psr.errf(pr.node, key, "want a string, got %s", value.Kind)
		return "", false
	}
	return text, true
}

func (pr *props) boolean(key string, def bool) bool {
	value, ok := pr.raw(key)
	if !ok {
		return def
	}
	if value.Kind != kdl.KindBool {
		pr.psr.errf(pr.node, key, "want #true or #false, got %s", value.Kind)
		return def
	}
	return value.Bool
}

// integerRequired reads an integer property that has no default,
// reporting its absence.
func (pr *props) integerRequired(key string) int {
	if _, ok := pr.node.Prop(key); !ok {
		pr.psr.errf(pr.node, key, "required")
	}
	return pr.integer(key, 0)
}

func (pr *props) integer(key string, def int) int {
	value, ok := pr.raw(key)
	if !ok {
		return def
	}
	if value.Kind != kdl.KindInt || !value.Int.IsInt64() {
		pr.psr.errf(pr.node, key, "want an integer, got %s", value.Kind)
		return def
	}
	return int(value.Int.Int64())
}

// dim reads a dimension: a bare number in points, or a string with a unit.
func (pr *props) dim(key string) geom.Opt {
	value, ok := pr.raw(key)
	if !ok {
		return geom.Unset()
	}
	return pr.dimValue(key, value)
}

func (pr *props) dimValue(key string, value kdl.Value) geom.Opt {
	if num, isNum := value.Number(); isNum {
		return geom.Val(geom.Round(num))
	}
	if text, isStr := value.Text(); isStr {
		num, err := geom.ParseDim(text)
		if err != nil {
			pr.psr.errf(pr.node, key, "%v", err)
			return geom.Unset()
		}
		return geom.Val(num)
	}
	pr.psr.errf(pr.node, key, "want a dimension, got %s", value.Kind)
	return geom.Unset()
}

func (pr *props) dimDefault(key string, def float64) float64 {
	if opt := pr.dim(key); opt.Set {
		return opt.Value
	}
	return def
}

func (pr *props) color(key string) *Color {
	text, ok := pr.strOpt(key)
	if !ok {
		return nil
	}
	color, err := ParseColor(text)
	if err != nil {
		pr.psr.errf(pr.node, key, "%v", err)
		return nil
	}
	return &color
}

func enumKeys[Enum any](table map[string]Enum) string {
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, " ")
}

func enumProp[Enum any](pr *props, key string, table map[string]Enum, def Enum) Enum {
	text, ok := pr.strOpt(key)
	if !ok {
		return def
	}
	value, found := table[text]
	if !found {
		pr.psr.errf(pr.node, key,
			"unknown value %q; want one of: %s", text, enumKeys(table))
		return def
	}
	return value
}

// expression compiles a property holding a Starlark expression.
func (pr *props) expression(key string) *expr.Program {
	text, ok := pr.strOpt(key)
	if !ok {
		return nil
	}
	return pr.psr.compile(pr.node, key, text, false)
}

func (psr *parser) compile(node *kdl.Node, prop, source string, deferrable bool) *expr.Program {
	where := fmt.Sprintf("%s:%d %s %s", path.Base(psr.file), node.Line, node.Path(), prop)
	prog, err := expr.Compile(where, source)
	if err != nil {
		psr.errf(node, prop, "%v", err)
		return nil
	}
	psr.programs = append(psr.programs,
		programSite{prog: prog, node: node, prop: prop, deferrable: deferrable})
	return prog
}

// name reads a node's identifying positional argument.
func (psr *parser) name(node *kdl.Node) string {
	if len(node.Args) == 0 {
		psr.errf(node, "", "missing name")
		return ""
	}
	text, ok := node.Args[0].Text()
	if !ok {
		psr.errf(node, "", "name must be a string")
		return ""
	}
	if len(node.Args) > 1 {
		psr.errf(node, "", "want one positional argument, got %d", len(node.Args))
	}
	return text
}

// noArgs reports a node that carries positional arguments but takes none.
func (psr *parser) noArgs(node *kdl.Node) {
	if len(node.Args) > 0 {
		psr.errf(node, "", "takes no positional arguments")
	}
}

// allowChildren reports any child whose name is not in the allowed set.
func (psr *parser) allowChildren(node *kdl.Node, allowed ...string) {
	set := map[string]bool{}
	for _, name := range allowed {
		set[name] = true
	}
	for _, child := range node.Children {
		if !set[child.Name] {
			psr.errf(child, "", "unexpected node here; %s accepts: %s",
				node.Name, strings.Join(allowed, " "))
		}
	}
}

// atMostOne reports a repeated node that may appear only once.
func (psr *parser) atMostOne(node *kdl.Node, name string) *kdl.Node {
	kids := node.ChildrenNamed(name)
	if len(kids) == 0 {
		return nil
	}
	if len(kids) > 1 {
		psr.errf(kids[1], "", "at most one %s is allowed here", name)
	}
	return kids[0]
}

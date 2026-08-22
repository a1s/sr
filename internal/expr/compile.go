package expr

import (
	"fmt"
	"sort"
	"strings"

	"go.starlark.net/resolve"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// fileOptions fixes the dialect. Sets are enabled explicitly
// rather than inherited from a host default: calc="set" depends on them.
// Recursion and global reassignment stay off.
var fileOptions = &syntax.FileOptions{
	Set:               true,
	While:             false,
	TopLevelControl:   false,
	GlobalReassign:    false,
	LoadBindsGlobally: false,
	Recursion:         false,
}

// Program is one compiled template expression.
//
// Compilation happens once, at template load. The expression becomes
// a function whose parameters are exactly the names it references,
// so an evaluation costs one call with those values and nothing scales
// with the number of columns in the data.
type Program struct {
	// Source is the expression as written.
	Source string
	// Where names the template file, node path, and property, for diagnostics.
	Where string
	// Params are the free names, in the order the function takes them.
	Params []string
	// FinalNames are the names read through FINAL.
	FinalNames []string
	// UsesFinal reports whether FINAL appears at all.
	UsesFinal bool
	// UsesPosition reports whether the expression reads VERTICAL_POSITION
	// or VERTICAL_SPACE, which is what makes a band's measurement uncacheable.
	UsesPosition bool

	fn *starlark.Function
}

// Compile parses and compiles one expression.
//
// where is a human-readable location -- file, node path, property --
// that every diagnostic from this expression carries.
func Compile(where, source string) (*Program, error) {
	expr, err := fileOptions.ParseExpr(where, source, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}

	if name, pos := findBlocked(expr); name != "" {
		return nil, fmt.Errorf("%s: %s (at %s)", where, blockedUniversals[name], pos)
	}

	// Resolve with every name that is not a Starlark universal
	// treated as predeclared, so that resolution succeeds and marks
	// each identifier's scope. The free names are then the predeclared
	// ones the fixed environment does not supply -- and a comprehension's
	// loop variable, being local, is not among them.
	anyPredeclared := func(name string) bool { return !IsUniversal(name) }
	_, err = resolve.ExprOptions(fileOptions, expr, anyPredeclared, IsUniversal)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	params := freeNames(expr)

	prog := &Program{
		Source: source,
		Where:  where,
		Params: params,
	}
	for _, param := range params {
		if param == FinalName {
			prog.UsesFinal = true
		}
		for _, pos := range PositionNames {
			if param == pos {
				prog.UsesPosition = true
			}
		}
	}
	prog.FinalNames = finalNames(expr)

	// Wrap the expression in a function of its free names.
	// Parentheses let a multi-line expression survive intact.
	var src strings.Builder
	fmt.Fprintf(&src, "def _e(%s):\n    return (\n", strings.Join(params, ", "))
	src.WriteString(source)
	src.WriteString("\n    )\n")

	thread := &starlark.Thread{Name: "compile"}
	globals, err := starlark.ExecFileOptions(fileOptions,
		thread, where, src.String(), Predeclared)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	fn, ok := globals["_e"].(*starlark.Function)
	if !ok {
		return nil, fmt.Errorf("%s: internal error compiling expression", where)
	}
	prog.fn = fn
	return prog, nil
}

// Call evaluates the program. args must be positionally aligned with Params.
func (program *Program) Call(
	thread *starlark.Thread,
	args []starlark.Value,
) (starlark.Value, error) {
	value, err := starlark.Call(thread, program.fn, starlark.Tuple(args), nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", program.Where, err)
	}
	return value, nil
}

// freeNames collects the identifiers the expression takes from outside itself,
// which become the compiled function's parameters. It must run after the
// resolver, which is what distinguishes a free name from a comprehension's
// own loop variable.
func freeNames(expr syntax.Expr) []string {
	seen := map[string]bool{}
	var names []string
	syntax.Walk(expr, func(node syntax.Node) bool {
		id, ok := node.(*syntax.Ident)
		if !ok {
			return true
		}
		binding, ok := id.Binding.(*resolve.Binding)
		if !ok || binding.Scope != resolve.Predeclared {
			return true
		}
		if IsPredeclared(id.Name) || seen[id.Name] {
			return true
		}
		seen[id.Name] = true
		names = append(names, id.Name)
		return true
	})
	sort.Strings(names)
	return names
}

// findBlocked reports the first blocked universal
// named anywhere in the expression.
func findBlocked(expr syntax.Expr) (string, syntax.Position) {
	var name string
	var pos syntax.Position
	syntax.Walk(expr, func(node syntax.Node) bool {
		if name != "" {
			return false
		}
		if id, ok := node.(*syntax.Ident); ok {
			if _, blocked := blockedUniversals[id.Name]; blocked {
				name, pos = id.Name, id.NamePos
				return false
			}
		}
		return true
	})
	return name, pos
}

// finalNames collects every name read as FINAL.<name>.
func finalNames(expr syntax.Expr) []string {
	seen := map[string]bool{}
	var names []string
	syntax.Walk(expr, func(node syntax.Node) bool {
		dot, ok := node.(*syntax.DotExpr)
		if !ok {
			return true
		}
		id, ok := dot.X.(*syntax.Ident)
		if !ok || id.Name != FinalName {
			return true
		}
		if !seen[dot.Name.Name] {
			seen[dot.Name.Name] = true
			names = append(names, dot.Name.Name)
		}
		return true
	})
	sort.Strings(names)
	return names
}

package build

import (
	"fmt"

	"github.com/a1s/sr/internal/expr"
	"github.com/a1s/sr/internal/tmpl"
	"github.com/a1s/sr/internal/vars"
	"go.starlark.net/starlark"
)

// varState is one variable's definition and its accumulator.
type varState struct {
	def *tmpl.Variable
	acc *vars.Accumulator
}

// scopeContext is the name environment expressions resolve against.
//
// Per doc/expressions.md#names-in-scope: predefined variables, then modules
// and builtins, then parameters, then variables, then declared record fields.
//
// The modules and builtins are not here, they are the compiled function's
// fixed environment, so a name that reaches this point is one of the other
// four.
type scopeContext struct {
	thread *starlark.Thread

	params    map[string]starlark.Value
	varByName map[string]*varState
	varOrder  []*varState

	record     *expr.Record
	itemNumber int
	dataCount  int

	reportCount int
	pageCount   int
	columnCount int
	groupCount  map[string]int

	pageNumber      int
	columnNumber    int
	groupPageNumber map[string]int

	verticalPosition float64
	verticalSpace    float64

	buildTime starlark.Value

	// final is bound only while a deferred expression is being resolved.
	final *expr.Namespace
}

func newScopeContext(buildTime starlark.Value) *scopeContext {
	return &scopeContext{
		thread:          &starlark.Thread{Name: "sr"},
		params:          map[string]starlark.Value{},
		varByName:       map[string]*varState{},
		groupCount:      map[string]int{},
		groupPageNumber: map[string]int{},
		pageNumber:      1,
		columnNumber:    1,
		buildTime:       buildTime,
	}
}

// lookup resolves one name.
func (ctx *scopeContext) lookup(name string) (starlark.Value, error) {
	switch name {
	case "THIS":
		if ctx.record == nil {
			return starlark.None, nil
		}
		return ctx.record, nil
	case "ITEM_NUMBER":
		return starlark.MakeInt(ctx.itemNumber), nil
	case "DATA_COUNT":
		return starlark.MakeInt(ctx.dataCount), nil
	case "REPORT_COUNT":
		return starlark.MakeInt(ctx.reportCount), nil
	case "PAGE_COUNT":
		return starlark.MakeInt(ctx.pageCount), nil
	case "COLUMN_COUNT":
		return starlark.MakeInt(ctx.columnCount), nil
	case "PAGE_NUMBER":
		return starlark.MakeInt(ctx.pageNumber), nil
	case "COLUMN_NUMBER":
		return starlark.MakeInt(ctx.columnNumber), nil
	case "VERTICAL_POSITION":
		return starlark.Float(ctx.verticalPosition), nil
	case "VERTICAL_SPACE":
		return starlark.Float(ctx.verticalSpace), nil
	case "BUILD_TIME":
		return ctx.buildTime, nil
	case expr.FinalName:
		if ctx.final == nil {
			return nil, fmt.Errorf("FINAL is not bound here")
		}
		return ctx.final, nil
	}
	if value, ok := ctx.groupDerived(name); ok {
		return value, nil
	}
	if value, ok := ctx.params[name]; ok {
		return value, nil
	}
	if state, ok := ctx.varByName[name]; ok {
		return state.acc.Value(), nil
	}
	if ctx.record != nil {
		if value, err := ctx.record.Attr(name); err == nil && value != nil {
			return value, nil
		}
		return nil, fmt.Errorf(
			"%s is not a parameter, a variable, or a declared field of the current record", name)
	}
	return nil, fmt.Errorf("%s is not defined, and no record is current", name)
}

// groupDerived resolves <group>_COUNT and <group>_PAGE_NUMBER.
func (ctx *scopeContext) groupDerived(name string) (starlark.Value, bool) {
	if len(name) > len("_COUNT") && name[len(name)-len("_COUNT"):] == "_COUNT" {
		group := name[:len(name)-len("_COUNT")]
		if _, ok := ctx.groupCount[group]; ok {
			return starlark.MakeInt(ctx.groupCount[group]), true
		}
	}
	const suffix = "_PAGE_NUMBER"
	if len(name) > len(suffix) && name[len(name)-len(suffix):] == suffix {
		group := name[:len(name)-len(suffix)]
		if _, ok := ctx.groupPageNumber[group]; ok {
			return starlark.MakeInt(ctx.groupPageNumber[group]), true
		}
	}
	return nil, false
}

// args builds the argument list for a compiled expression.
func (ctx *scopeContext) args(prog *expr.Program) ([]starlark.Value, error) {
	if len(prog.Params) == 0 {
		return nil, nil
	}
	out := make([]starlark.Value, len(prog.Params))
	for index, name := range prog.Params {
		value, err := ctx.lookup(name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", prog.Where, err)
		}
		out[index] = value
	}
	return out, nil
}

// eval evaluates a compiled expression in this context.
func (ctx *scopeContext) eval(prog *expr.Program) (starlark.Value, error) {
	if prog == nil {
		return starlark.None, nil
	}
	args, err := ctx.args(prog)
	if err != nil {
		return nil, err
	}
	return prog.Call(ctx.thread, args)
}

// truth evaluates a condition.
//
// A missing condition is true, that makes `when` optional on a style and an eject.
func (ctx *scopeContext) truth(prog *expr.Program) (bool, error) {
	if prog == nil {
		return true, nil
	}
	value, err := ctx.eval(prog)
	if err != nil {
		return false, err
	}
	return expr.Truth(value), nil
}

// snapshot captures the values a deferred expression reads
// where the element sits, leaving FINAL unbound.
//
// The names come from compilation, which has already turned the expression
// into a function of exactly the names it uses, so this costs a lookup per name
// and no analysis. It is per element rather than per band: two fields in one footer
// may sit at the same place and name different things.
func (ctx *scopeContext) snapshot(prog *expr.Program) (map[string]starlark.Value, error) {
	out := make(map[string]starlark.Value, len(prog.Params))
	for _, name := range prog.Params {
		if name == expr.FinalName {
			continue
		}
		value, err := ctx.lookup(name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", prog.Where, err)
		}
		out[name] = value
	}
	return out, nil
}

// finalNamespace builds the values FINAL reads at the end of a scope:
// every predefined variable, and every variable accumulator.
//
// A parameter is constant and a record field belongs to a record rather than
// to a scope, so neither is here; a field is reached through FINAL.THIS.
func (ctx *scopeContext) finalNamespace() *expr.Namespace {
	values := map[string]starlark.Value{}
	for _, name := range expr.PredefinedNames {
		if name == expr.FinalName {
			continue
		}
		if value, err := ctx.lookup(name); err == nil {
			values[name] = value
		}
	}
	for group := range ctx.groupCount {
		values[group+"_COUNT"] = starlark.MakeInt(ctx.groupCount[group])
	}
	for group := range ctx.groupPageNumber {
		values[group+"_PAGE_NUMBER"] = starlark.MakeInt(ctx.groupPageNumber[group])
	}
	for name, state := range ctx.varByName {
		values[name] = state.acc.Value()
	}
	return expr.NewNamespace("FINAL", values)
}

// callWithFinal evaluates a deferred expression: everything but FINAL
// comes from the snapshot taken where the element sits, and FINAL
// from the values reached when the scope ended.
func (ctx *scopeContext) callWithFinal(
	prog *expr.Program,
	snap map[string]starlark.Value,
	final *expr.Namespace,
) (starlark.Value, error) {
	args := make([]starlark.Value, len(prog.Params))
	for index, name := range prog.Params {
		if name == expr.FinalName {
			args[index] = final
			continue
		}
		value, ok := snap[name]
		if !ok {
			return nil, fmt.Errorf("%s: %s was not captured", prog.Where, name)
		}
		args[index] = value
	}
	return prog.Call(ctx.thread, args)
}

// iterate folds every variable whose iter scope matches.
func (ctx *scopeContext) iterate(scope tmpl.Scope, group string) error {
	for _, state := range ctx.varOrder {
		if state.def.Iter != scope {
			continue
		}
		if scope == tmpl.ScopeGroup && state.def.IterGrp != group {
			continue
		}
		if err := ctx.fold(state); err != nil {
			return err
		}
	}
	return nil
}

func (ctx *scopeContext) fold(state *varState) error {
	value, err := ctx.eval(state.def.Expr)
	if err != nil {
		return fmt.Errorf("variable %q: %w", state.def.Name, err)
	}
	if err := state.acc.Check(value); err != nil {
		return fmt.Errorf("variable %q: %w", state.def.Name, err)
	}
	if err := state.acc.Fold(value); err != nil {
		return fmt.Errorf("variable %q: %w", state.def.Name, err)
	}
	return nil
}

// reset clears every variable whose reset scope matches,
// seeding it with init where one is given.
func (ctx *scopeContext) reset(scope tmpl.Scope, group string) error {
	for _, state := range ctx.varOrder {
		if state.def.Reset != scope {
			continue
		}
		if scope == tmpl.ScopeGroup && state.def.ResetGrp != group {
			continue
		}
		state.acc.Reset()
		if state.def.Init == nil {
			continue
		}
		value, err := ctx.eval(state.def.Init)
		if err != nil {
			return fmt.Errorf("variable %q init: %w", state.def.Name, err)
		}
		if err := state.acc.Fold(value); err != nil {
			return fmt.Errorf("variable %q init: %w", state.def.Name, err)
		}
	}
	return nil
}

// varSnapshot records every accumulator, so that a band which turns out
// not to fit can have its fold rolled back and reapplied after the eject.
type varSnapshot []vars.State

func (ctx *scopeContext) snapshotVars() varSnapshot {
	out := make(varSnapshot, len(ctx.varOrder))
	for index, state := range ctx.varOrder {
		out[index] = state.acc.Snapshot()
	}
	return out
}

func (ctx *scopeContext) restoreVars(snapshot varSnapshot) {
	for index, state := range ctx.varOrder {
		state.acc.Restore(snapshot[index])
	}
}

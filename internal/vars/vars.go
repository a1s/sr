// Package vars implements variable accumulation: the twelve calc modes.
//
// See doc/expressions.md#variables.
//
// Only list, set and chain retain individual values. Everything else is an
// incremental accumulator with constant memory, so a report over a hundred
// thousand rows does not hold a hundred thousand values per variable and does
// not recompute an aggregate on every read.
package vars

import (
	"fmt"
	"math"

	"github.com/a1s/sr/internal/expr"
	"github.com/a1s/sr/internal/tmpl"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// Accumulator folds values for one variable.
type Accumulator struct {
	calc tmpl.CalcMode

	count int
	sum   starlark.Value
	first starlark.Value
	last  starlark.Value
	least starlark.Value
	most  starlark.Value

	// Welford's accumulation for the sample statistics.
	mean    float64
	m2      float64
	samples int

	// values is what list, set and chain retain.
	// A set holds the distinct values in first-seen order.
	values []starlark.Value
}

// New builds an empty accumulator.
func New(calc tmpl.CalcMode) *Accumulator { return &Accumulator{calc: calc} }

// Retains reports whether this mode keeps individual values.
// Only list, set and chain do; everything else is constant memory.
func (a *Accumulator) Retains() bool { return a.calc.Retains() }

// State is a snapshot, taken so that a detail band which turns out not to fit
// can have its fold rolled back and reapplied after the eject.
type State struct {
	acc    Accumulator
	values int
}

// Snapshot records the accumulator's state.
func (acc *Accumulator) Snapshot() State {
	return State{acc: *acc, values: len(acc.values)}
}

// Restore undoes every fold since a snapshot.
func (acc *Accumulator) Restore(state State) {
	values := acc.values[:state.values]
	*acc = state.acc
	acc.values = values
}

// Reset empties the accumulator.
func (acc *Accumulator) Reset() {
	calc := acc.calc
	*acc = Accumulator{calc: calc}
}

// Fold folds one value in.
func (acc *Accumulator) Fold(value starlark.Value) error {
	if value == nil {
		return nil
	}
	acc.count++
	if acc.first == nil {
		acc.first = value
	}
	acc.last = value

	switch acc.calc {
	case tmpl.CalcList, tmpl.CalcChain:
		acc.values = append(acc.values, value)
	case tmpl.CalcSet:
		for _, seen := range acc.values {
			if eq, err := starlark.Equal(seen, value); err == nil && eq {
				return nil
			}
		}
		acc.values = append(acc.values, value)
	case tmpl.CalcSum, tmpl.CalcAvg:
		sum, err := add(acc.sum, value)
		if err != nil {
			return err
		}
		acc.sum = sum
	case tmpl.CalcMin:
		less, err := lessThan(value, acc.least)
		if err != nil {
			return err
		}
		if acc.least == nil || less {
			acc.least = value
		}
	case tmpl.CalcMax:
		greater, err := lessThan(acc.most, value)
		if err != nil {
			return err
		}
		if acc.most == nil || greater {
			acc.most = value
		}
	case tmpl.CalcStd, tmpl.CalcVar:
		num, err := asFloat(value)
		if err != nil {
			return err
		}
		acc.samples++
		delta := num - acc.mean
		acc.mean += delta / float64(acc.samples)
		acc.m2 += delta * (num - acc.mean)
	}
	return nil
}

// Value reads the accumulator.
//
// An empty accumulator reads as 0 for count; None for first, last, sum, avg,
// min, max, std and var; and an empty list, set or chain otherwise. sum of
// nothing is None rather than 0, so "no rows" stays distinguishable from "rows
// summing to zero".
func (acc *Accumulator) Value() starlark.Value {
	switch acc.calc {
	case tmpl.CalcCount:
		return starlark.MakeInt(acc.count)
	case tmpl.CalcFirst:
		return orNone(acc.first)
	case tmpl.CalcLast:
		return orNone(acc.last)
	case tmpl.CalcSum:
		return orNone(acc.sum)
	case tmpl.CalcMin:
		return orNone(acc.least)
	case tmpl.CalcMax:
		return orNone(acc.most)
	case tmpl.CalcAvg:
		if acc.count == 0 || acc.sum == nil {
			return starlark.None
		}
		return average(acc.sum, acc.count)
	case tmpl.CalcStd, tmpl.CalcVar:
		// Sample statistics, dividing by n-1, so a single value is as
		// undefined as none at all.
		if acc.samples < 2 {
			return starlark.None
		}
		variance := acc.m2 / float64(acc.samples-1)
		if acc.calc == tmpl.CalcVar {
			return starlark.Float(variance)
		}
		return starlark.Float(math.Sqrt(variance))
	case tmpl.CalcList:
		list := starlark.NewList(append([]starlark.Value(nil), acc.values...))
		list.Freeze()
		return list
	case tmpl.CalcSet:
		set := starlark.NewSet(len(acc.values))
		for _, value := range acc.values {
			_ = set.Insert(value)
		}
		set.Freeze()
		return set
	case tmpl.CalcChain:
		var out []starlark.Value
		for _, value := range acc.values {
			seq, ok := value.(starlark.Iterable)
			if !ok {
				// A non-sequence in a chain is a type error, surfaced where
				// the value is read rather than swallowed.
				continue
			}
			iter := seq.Iterate()
			var item starlark.Value
			for iter.Next(&item) {
				out = append(out, item)
			}
			iter.Done()
		}
		list := starlark.NewList(out)
		list.Freeze()
		return list
	}
	return starlark.None
}

// Check reports a value the mode cannot fold, so that the error
// names the variable rather than surfacing later as a wrong answer.
func (acc *Accumulator) Check(value starlark.Value) error {
	if acc.calc != tmpl.CalcChain {
		return nil
	}
	if _, ok := value.(starlark.Iterable); !ok {
		return fmt.Errorf(`calc="chain" concatenates sequences, and this value is %s`,
			value.Type())
	}
	return nil
}

func orNone(value starlark.Value) starlark.Value {
	if value == nil {
		return starlark.None
	}
	return value
}

// add sums exactly, so that decimals stay decimals.
func add(acc, value starlark.Value) (starlark.Value, error) {
	if acc == nil {
		return value, nil
	}
	switch total := acc.(type) {
	case starlark.Int:
		switch addend := value.(type) {
		case starlark.Int:
			return total.Add(addend), nil
		case starlark.Float:
			num, _ := starlark.AsFloat(total)
			return starlark.Float(num) + addend, nil
		case expr.Decimal:
			return decimalSum(addend, total, starlark.Right, acc, value)
		}
	case starlark.Float:
		num, err := asFloat(value)
		if err != nil {
			return nil, err
		}
		return total + starlark.Float(num), nil
	case expr.Decimal:
		return decimalSum(total, value, starlark.Left, acc, value)
	}
	return nil, cannotSum(acc, value)
}

// decimalSum adds through Decimal.Binary and refuses what it will not handle.
//
// Binary is a Starlark operator hook, so it answers an operand it does not
// know with (nil, nil): "not mine, try the other side". There is no other
// side here. Taking that nil as the new total would silently restart the sum.
// An int or float accumulator errors on the same input, and so must this one.
func decimalSum(dec expr.Decimal, operand starlark.Value, side starlark.Side,
	acc, value starlark.Value) (starlark.Value, error) {

	sum, err := dec.Binary(syntax.PLUS, operand, side)
	if err != nil {
		return nil, err
	}
	if sum == nil {
		return nil, cannotSum(acc, value)
	}
	return sum, nil
}

func cannotSum(acc, value starlark.Value) error {
	return fmt.Errorf("cannot sum %s and %s", acc.Type(), value.Type())
}

// average divides a sum by a count, keeping a decimal exact.
func average(sum starlark.Value, count int) starlark.Value {
	if exact, ok := sum.(expr.Decimal); ok {
		out, err := exact.Binary(syntax.SLASH, starlark.MakeInt(count), starlark.Left)
		if err == nil && out != nil {
			return out
		}
	}
	num, err := asFloat(sum)
	if err != nil {
		return starlark.None
	}
	return starlark.Float(num / float64(count))
}

func lessThan(value, operand starlark.Value) (bool, error) {
	if value == nil || operand == nil {
		return false, nil
	}
	return starlark.Compare(syntax.LT, value, operand)
}

func asFloat(value starlark.Value) (float64, error) {
	switch typed := value.(type) {
	case starlark.Float:
		return float64(typed), nil
	case starlark.Int:
		num, _ := starlark.AsFloat(typed)
		return num, nil
	case expr.Decimal:
		return typed.Float(), nil
	}
	return 0, fmt.Errorf("cannot use %s as a number", value.Type())
}

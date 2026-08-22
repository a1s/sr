package vars

import (
	"math"
	"testing"

	"github.com/a1s/sr/internal/expr"
	"github.com/a1s/sr/internal/tmpl"
	"go.starlark.net/starlark"
)

func dec(test *testing.T, text string) starlark.Value {
	test.Helper()
	parsed, err := expr.ParseDecimal(text)
	if err != nil {
		test.Fatal(err)
	}
	return parsed
}

func fold(test *testing.T, acc *Accumulator, values ...starlark.Value) {
	test.Helper()
	for _, value := range values {
		if err := acc.Fold(value); err != nil {
			test.Fatal(err)
		}
	}
}

func TestEmptyAccumulators(test *testing.T) {
	cases := []struct {
		calc tmpl.CalcMode
		want string
	}{
		{tmpl.CalcCount, "0"},
		{tmpl.CalcFirst, "None"},
		{tmpl.CalcLast, "None"},
		{tmpl.CalcSum, "None"},
		{tmpl.CalcAvg, "None"},
		{tmpl.CalcMin, "None"},
		{tmpl.CalcMax, "None"},
		{tmpl.CalcStd, "None"},
		{tmpl.CalcVar, "None"},
		{tmpl.CalcList, "[]"},
		{tmpl.CalcSet, "set([])"},
		{tmpl.CalcChain, "[]"},
	}
	for _, testCase := range cases {
		test.Run(testCase.calc.String(), func(test *testing.T) {
			if got := New(testCase.calc).Value().String(); got != testCase.want {
				test.Errorf("got %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestAllTwelveCalcModes(test *testing.T) {
	ints := []starlark.Value{
		starlark.MakeInt(3),
		starlark.MakeInt(1),
		starlark.MakeInt(4),
		starlark.MakeInt(1),
	}

	cases := []struct {
		calc   tmpl.CalcMode
		values []starlark.Value
		want   string
	}{
		{tmpl.CalcFirst, ints, "3"},
		{tmpl.CalcLast, ints, "1"},
		{tmpl.CalcCount, ints, "4"},
		{tmpl.CalcSum, ints, "9"},
		{tmpl.CalcMin, ints, "1"},
		{tmpl.CalcMax, ints, "4"},
		{tmpl.CalcList, ints, "[3, 1, 4, 1]"},
		// A set keeps distinct values in first-seen order.
		{tmpl.CalcSet, ints, "set([3, 1, 4])"},
		{tmpl.CalcChain, []starlark.Value{
			starlark.NewList([]starlark.Value{starlark.String("a"), starlark.String("b")}),
			starlark.NewList([]starlark.Value{starlark.String("c")}),
		}, `["a", "b", "c"]`},
	}
	for _, testCase := range cases {
		test.Run(testCase.calc.String(), func(test *testing.T) {
			acc := New(testCase.calc)
			fold(test, acc, testCase.values...)
			if got := acc.Value().String(); got != testCase.want {
				test.Errorf("got %q, want %q", got, testCase.want)
			}
		})
	}

	// avg of ints is a float, since / is float division.
	acc := New(tmpl.CalcAvg)
	fold(test, acc, ints...)
	if got := acc.Value().String(); got != "2.25" {
		test.Errorf("avg = %q, want 2.25", got)
	}

	// std and var are sample statistics over 3, 1, 4, 1: mean 2.25,
	// sum of squares 6.75, variance 6.75/3 = 2.25, std 1.5.
	for _, testCase := range []struct {
		calc tmpl.CalcMode
		want float64
	}{{tmpl.CalcVar, 2.25}, {tmpl.CalcStd, 1.5}} {
		acc := New(testCase.calc)
		fold(test, acc, ints...)
		num, ok := starlark.AsFloat(acc.Value())
		if !ok || math.Abs(num-testCase.want) > 1e-12 {
			test.Errorf("%s = %v, want %v", testCase.calc, acc.Value(), testCase.want)
		}
	}
}

func TestSampleStatisticsNeedTwoValues(test *testing.T) {
	for _, calc := range []tmpl.CalcMode{tmpl.CalcStd, tmpl.CalcVar} {
		acc := New(calc)
		fold(test, acc, starlark.MakeInt(5))
		if got := acc.Value(); got != starlark.None {
			test.Errorf("%s of one value = %v, want None", calc, got)
		}
	}
}

func TestDecimalAggregatesStayExact(test *testing.T) {
	values := []starlark.Value{dec(test, "1.50"), dec(test, "2.25"), dec(test, "0.25")}

	sum := New(tmpl.CalcSum)
	fold(test, sum, values...)
	if got := sum.Value().String(); got != "4.00" {
		test.Errorf("sum = %q, want 4.00", got)
	}
	if _, ok := sum.Value().(expr.Decimal); !ok {
		test.Errorf("sum over decimals must stay a decimal, got %s", sum.Value().Type())
	}

	// avg quantizes like /, to six fractional digits.
	avg := New(tmpl.CalcAvg)
	fold(test, avg, values...)
	if got := avg.Value().String(); got != "1.333333" {
		test.Errorf("avg = %q, want 1.333333", got)
	}

	for _, testCase := range []struct {
		calc tmpl.CalcMode
		want string
	}{{tmpl.CalcMin, "0.25"}, {tmpl.CalcMax, "2.25"}} {
		acc := New(testCase.calc)
		fold(test, acc, values...)
		if got := acc.Value().String(); got != testCase.want {
			test.Errorf("%s = %q, want %q", testCase.calc, got, testCase.want)
		}
	}

	// std and var produce floats even over decimals.
	sd := New(tmpl.CalcStd)
	fold(test, sd, values...)
	if _, ok := sd.Value().(starlark.Float); !ok {
		test.Errorf("std over decimals is a float, got %s", sd.Value().Type())
	}
}

func TestSumOfNothingIsNoneNotZero(test *testing.T) {
	acc := New(tmpl.CalcSum)
	if got := acc.Value(); got != starlark.None {
		test.Errorf("sum of nothing = %v, want None", got)
	}
	fold(test, acc, starlark.MakeInt(0))
	if got := acc.Value().String(); got != "0" {
		test.Errorf("sum of one zero = %q, want 0", got)
	}
}

// A detail band that is measured, folded in, and then found not to fit
// has its fold rolled back before the eject and reapplied after,
// so no value is counted twice.
func TestRollback(test *testing.T) {
	for _, calc := range []tmpl.CalcMode{
		tmpl.CalcSum, tmpl.CalcCount, tmpl.CalcMin, tmpl.CalcMax,
		tmpl.CalcFirst, tmpl.CalcLast, tmpl.CalcAvg, tmpl.CalcStd, tmpl.CalcVar,
		tmpl.CalcList, tmpl.CalcSet, tmpl.CalcChain,
	} {
		test.Run(calc.String(), func(test *testing.T) {
			seed := starlark.NewList([]starlark.Value{starlark.MakeInt(1)})
			second := starlark.NewList([]starlark.Value{starlark.MakeInt(2)})
			acc := New(calc)
			if calc == tmpl.CalcChain {
				fold(test, acc, seed)
			} else {
				fold(test, acc, starlark.MakeInt(1), starlark.MakeInt(2))
			}
			before := acc.Value().String()

			state := acc.Snapshot()
			if calc == tmpl.CalcChain {
				fold(test, acc, second)
			} else {
				fold(test, acc, starlark.MakeInt(99))
			}
			acc.Restore(state)

			if got := acc.Value().String(); got != before {
				test.Errorf("after rollback = %q, want %q", got, before)
			}

			// Reapplying after the eject counts the value once.
			if calc == tmpl.CalcChain {
				fold(test, acc, second)
			} else {
				fold(test, acc, starlark.MakeInt(99))
			}
			state2 := acc.Snapshot()
			after := acc.Value().String()
			acc.Restore(state2)
			if got := acc.Value().String(); got != after {
				test.Errorf("a snapshot with nothing after it must be a no-op: %q vs %q", got, after)
			}
		})
	}
}

func TestReset(test *testing.T) {
	acc := New(tmpl.CalcSum)
	fold(test, acc, starlark.MakeInt(5))
	acc.Reset()
	if got := acc.Value(); got != starlark.None {
		test.Errorf("after reset = %v, want None", got)
	}
	fold(test, acc, starlark.MakeInt(3))
	if got := acc.Value().String(); got != "3" {
		test.Errorf("after reset and one fold = %q, want 3", got)
	}
}

func TestChainRejectsANonSequence(test *testing.T) {
	acc := New(tmpl.CalcChain)
	if err := acc.Check(starlark.MakeInt(1)); err == nil {
		test.Error("want an error for a non-sequence in a chain")
	}
	if err := acc.Check(starlark.NewList(nil)); err != nil {
		test.Errorf("a list is a sequence: %v", err)
	}
}

func TestMixedNumericSum(test *testing.T) {
	acc := New(tmpl.CalcSum)
	fold(test, acc, starlark.MakeInt(1), starlark.Float(0.5))
	if got := acc.Value().String(); got != "1.5" {
		test.Errorf("int then float = %q, want 1.5", got)
	}
	// A decimal meeting a float is refused rather than silently converted.
	other := New(tmpl.CalcSum)
	fold(test, other, dec(test, "1.50"))
	if err := other.Fold(starlark.Float(0.5)); err == nil {
		test.Error("want an error for a decimal summed with a float")
	}
}

// Only list, set and chain retain individual values;
// everything else is an incremental accumulator with constant memory.
func TestOnlyThreeModesRetainValues(test *testing.T) {
	retaining := map[tmpl.CalcMode]bool{
		tmpl.CalcList: true, tmpl.CalcSet: true, tmpl.CalcChain: true,
	}
	for _, calc := range []tmpl.CalcMode{
		tmpl.CalcFirst, tmpl.CalcLast, tmpl.CalcCount, tmpl.CalcList, tmpl.CalcSet,
		tmpl.CalcChain, tmpl.CalcSum, tmpl.CalcAvg, tmpl.CalcMin, tmpl.CalcMax,
		tmpl.CalcStd, tmpl.CalcVar,
	} {
		acc := New(calc)
		if acc.Retains() != retaining[calc] {
			test.Errorf("%s retains = %v, want %v", calc, acc.Retains(), retaining[calc])
		}
		for index := 0; index < 100; index++ {
			if calc == tmpl.CalcChain {
				fold(test, acc, starlark.NewList(
					[]starlark.Value{starlark.MakeInt(index)},
				))
				continue
			}
			fold(test, acc, starlark.MakeInt(index))
		}
		if got := len(acc.values); (got > 0) != retaining[calc] {
			test.Errorf("%s kept %d values after 100 folds", calc, got)
		}
	}
}

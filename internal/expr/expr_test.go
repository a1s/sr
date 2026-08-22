package expr

import (
	"strings"
	"testing"
	gotime "time"

	startime "go.starlark.net/lib/time"
	"go.starlark.net/starlark"
)

func eval(test *testing.T, src string, vals map[string]starlark.Value) starlark.Value {
	test.Helper()
	prog, err := Compile("test", src)
	if err != nil {
		test.Fatalf("Compile(%q): %v", src, err)
	}
	args := make([]starlark.Value, len(prog.Params))
	for index, param := range prog.Params {
		value, ok := vals[param]
		if !ok {
			test.Fatalf("expression %q needs %q, which the test did not supply",
				src, param)
		}
		args[index] = value
	}
	got, err := prog.Call(&starlark.Thread{}, args)
	if err != nil {
		test.Fatalf("eval(%q): %v", src, err)
	}
	return got
}

func evalStr(test *testing.T, src string, vals map[string]starlark.Value) string {
	test.Helper()
	return Str(eval(test, src, vals))
}

func TestCompileFindsFreeNames(test *testing.T) {
	prog, err := Compile("test", "amount * qty + math.floor(rate)")
	if err != nil {
		test.Fatal(err)
	}
	want := []string{"amount", "qty", "rate"}
	if strings.Join(prog.Params, ",") != strings.Join(want, ",") {
		test.Errorf("Params = %v, want %v", prog.Params, want)
	}
}

// A comprehension's loop variable is bound by the comprehension, not free,
// so it must not become a parameter. invoices.kdl writes exactly this.
func TestCompileIgnoresComprehensionVariables(test *testing.T) {
	prog, err := Compile("test", "', '.join([format('#%05d', n) for n in invoice_nos])")
	if err != nil {
		test.Fatal(err)
	}
	if len(prog.Params) != 1 || prog.Params[0] != "invoice_nos" {
		test.Fatalf("Params = %v, want [invoice_nos]", prog.Params)
	}
	got := evalStr(test, prog.Source, map[string]starlark.Value{
		"invoice_nos": starlark.NewList(
			[]starlark.Value{starlark.MakeInt(7), starlark.MakeInt(42)}),
	})
	if got != "#00007, #00042" {
		test.Errorf("got %q", got)
	}
}

func TestCompileRejectsPrint(test *testing.T) {
	if _, err := Compile("test", "print('x')"); err == nil {
		test.Error("want an error for print")
	}
}

func TestNoClock(test *testing.T) {
	if _, ok := timeModule.Members["now"]; ok {
		test.Fatal("the time module must not offer now")
	}
	prog, err := Compile("test", "time.now()")
	if err != nil {
		test.Fatal(err)
	}
	if _, err := prog.Call(&starlark.Thread{}, nil); err == nil {
		test.Error("time.now must not be callable")
	}
	// The rest of the module is present.
	got := evalStr(test, `time.time(year=2005, month=5, day=24).format("02.01.2006")`, nil)
	if got != "24.05.2005" {
		test.Errorf("time.format = %q", got)
	}
}

func TestSetsAreEnabled(test *testing.T) {
	got := eval(test, "len(set([1, 2, 2, 3]))", nil)
	if got.String() != "3" {
		test.Errorf("got %v, want 3", got)
	}
}

func TestFinalNames(test *testing.T) {
	prog, err := Compile("test", "'Page %d of %d' % (PAGE_NUMBER, FINAL.PAGE_NUMBER)")
	if err != nil {
		test.Fatal(err)
	}
	if !prog.UsesFinal {
		test.Error("UsesFinal = false")
	}
	if len(prog.FinalNames) != 1 || prog.FinalNames[0] != "PAGE_NUMBER" {
		test.Errorf("FinalNames = %v", prog.FinalNames)
	}
	// PAGE_NUMBER is read where the element sits and FINAL is a separate name,
	// so both are parameters.
	if strings.Join(prog.Params, ",") != "FINAL,PAGE_NUMBER" {
		test.Errorf("Params = %v", prog.Params)
	}
}

func TestUsesPosition(test *testing.T) {
	prog, err := Compile("test", "VERTICAL_SPACE > 100")
	if err != nil {
		test.Fatal(err)
	}
	if !prog.UsesPosition {
		test.Error("UsesPosition = false")
	}
	prog, err = Compile("test", "amount")
	if err != nil {
		test.Fatal(err)
	}
	if prog.UsesPosition {
		test.Error("UsesPosition = true for an expression that does not read position")
	}
}

func TestDecimalArithmeticIsExact(test *testing.T) {
	want := func(text string) starlark.Value {
		dec, err := ParseDecimal(text)
		if err != nil {
			test.Fatal(err)
		}
		return dec
	}
	env := map[string]starlark.Value{
		"a": want("1.50"), "b": want("2.25"), "n": starlark.MakeInt(3),
	}
	cases := []struct{ src, want string }{
		{"a + b", "3.75"},   // two 2-place values give 2 places
		{"a * b", "3.3750"}, // multiplying them gives 4
		{"a - b", "-0.75"},
		{"a * n", "4.50"},     // decimal times int stays exact
		{"a / b", "0.666667"}, // quantized to 6, half away from zero
		{"str(a + b)", "3.75"},
		{"float(a) + 0.5", "2.0"},
		{"int(b)", "2"},
		{"quantize(a / b, 2)", "0.67"},
		{"decimal('19.99') + decimal('0.01')", "20.00"},
	}
	for _, testCase := range cases {
		if got := evalStr(test, testCase.src, env); got != testCase.want {
			test.Errorf("%s = %q, want %q", testCase.src, got, testCase.want)
		}
	}
}

func TestDecimalRejectsFloatMixing(test *testing.T) {
	dec, _ := ParseDecimal("1.50")
	prog, err := Compile("test", "a + f")
	if err != nil {
		test.Fatal(err)
	}
	_, err = prog.Call(&starlark.Thread{}, []starlark.Value{dec, starlark.Float(0.5)})
	if err == nil || !strings.Contains(err.Error(), "cannot mix decimal and float") {
		test.Errorf("want a mixing error, got %v", err)
	}
}

func TestDecimalComparison(test *testing.T) {
	left, _ := ParseDecimal("1.50")
	other, _ := ParseDecimal("1.500")
	env := map[string]starlark.Value{"a": left, "b": other}
	if evalStr(test, "a == b", env) != "True" {
		test.Error("1.50 == 1.500 must be true: scale is not identity")
	}
	if evalStr(test, "a < b", env) != "False" {
		test.Error("1.50 < 1.500 must be false")
	}
}

func TestFormatSpecTable(test *testing.T) {
	dec, _ := ParseDecimal("19.995")
	neg, _ := ParseDecimal("-4.5")
	cases := []struct {
		spec string
		args []starlark.Value
		want string
	}{
		{"%s", []starlark.Value{starlark.String("hi")}, "hi"},
		{"%q", []starlark.Value{starlark.String("hi")}, `"hi"`},
		{"%3d.", []starlark.Value{starlark.MakeInt(7)}, "  7."},
		{"%-5d|", []starlark.Value{starlark.MakeInt(7)}, "7    |"},
		{"%05d", []starlark.Value{starlark.MakeInt(42)}, "00042"},
		{"#%05d", []starlark.Value{starlark.MakeInt(42)}, "#00042"},
		{"%+d", []starlark.Value{starlark.MakeInt(42)}, "+42"},
		{"%x", []starlark.Value{starlark.MakeInt(255)}, "ff"},
		{"%X", []starlark.Value{starlark.MakeInt(255)}, "FF"},
		{"%o", []starlark.Value{starlark.MakeInt(8)}, "10"},
		{"%b", []starlark.Value{starlark.MakeInt(5)}, "101"},
		{"%c", []starlark.Value{starlark.MakeInt(65)}, "A"},
		{"%i", []starlark.Value{starlark.MakeInt(42)}, "42"},
		{"%%", nil, "%"},
		{"%.2f", []starlark.Value{starlark.Float(3.14159)}, "3.14"},
		{"%e", []starlark.Value{starlark.Float(1234.5)}, "1.234500e+03"},
		{"%.3g", []starlark.Value{starlark.Float(1234.5)}, "1.23e+03"},
		// A decimal formats exactly, rounding half away from zero.
		{"%.2f", []starlark.Value{dec}, "20.00"},
		{"%.2f", []starlark.Value{neg}, "-4.50"},
		{"%8.2f|", []starlark.Value{dec}, "   20.00|"},
		{"%-8.2f|", []starlark.Value{dec}, "20.00   |"},
		{"%08.2f", []starlark.Value{dec}, "00020.00"},
		{"%08.2f", []starlark.Value{neg}, "-0004.50"},
		{"%+.2f", []starlark.Value{dec}, "+20.00"},
		{"%d", []starlark.Value{dec}, "20"},
		// Several conversions, spread from a tuple.
		{"Total for %s, %s: %.2f", []starlark.Value{
			starlark.String("SMITH"), starlark.String("MARY"), dec,
		}, "Total for SMITH, MARY: 20.00"},
	}
	for _, testCase := range cases {
		got, err := Format(testCase.spec, testCase.args)
		if err != nil {
			test.Errorf("Format(%q): %v", testCase.spec, err)
			continue
		}
		if got != testCase.want {
			test.Errorf("Format(%q) = %q, want %q", testCase.spec, got, testCase.want)
		}
	}
}

func TestFormatArgumentCountMustMatch(test *testing.T) {
	_, err := Format("%s %s", []starlark.Value{starlark.String("a")})
	if err == nil {
		test.Error("want an error for too few arguments")
	}
	_, err = Format("%s", []starlark.Value{starlark.String("a"), starlark.String("b")})
	if err == nil {
		test.Error("want an error for too many arguments")
	}
}

func TestFormatArgsSpreadsATuple(test *testing.T) {
	tup := starlark.Tuple{starlark.String("a"), starlark.MakeInt(1)}
	if got := FormatArgs(tup); len(got) != 2 {
		test.Errorf("a tuple must spread, got %d args", len(got))
	}
	if got := FormatArgs(starlark.String("a")); len(got) != 1 {
		test.Errorf("a scalar is one argument, got %d", len(got))
	}
}

func TestStrftime(test *testing.T) {
	when := gotime.Date(2005, 5, 24, 22, 53, 30, 0, gotime.UTC)
	cases := []struct{ spec, want string }{
		{"%d.%m.%Y", "24.05.2005"},
		{"%Y-%m-%d %H:%M", "2005-05-24 22:53"},
		{"%y", "05"},
		{"%B %b", "May May"},
		{"%A %a", "Tuesday Tue"},
		{"%j", "144"},
		{"%I %p", "10 PM"},
		{"%S", "30"},
		{"100%%", "100%"},
	}
	for _, testCase := range cases {
		if got := Strftime(when, testCase.spec); got != testCase.want {
			test.Errorf("Strftime(%q) = %q, want %q",
				testCase.spec, got, testCase.want)
		}
	}
}

func TestTruthValues(test *testing.T) {
	zeroTime := startime.Time(gotime.Time{})
	epoch := startime.Time(gotime.Unix(0, 0).UTC())
	zeroDec, _ := ParseDecimal("0.00")
	oneDec, _ := ParseDecimal("0.01")
	rec := NewRecord(nil, map[string]starlark.Value{})

	cases := []struct {
		name string
		v    starlark.Value
		want bool
	}{
		{"None", starlark.None, false},
		{"True", starlark.True, true},
		{"False", starlark.False, false},
		{"0", starlark.MakeInt(0), false},
		{"1", starlark.MakeInt(1), true},
		{"0.0", starlark.Float(0), false},
		{"decimal 0.00", zeroDec, false},
		{"decimal 0.01", oneDec, true},
		{"empty string", starlark.String(""), false},
		{"string", starlark.String("x"), true},
		{"empty list", starlark.NewList(nil), false},
		{"zero time", zeroTime, false},
		{"unix epoch", epoch, true},
		{"empty record", rec, true},
	}
	for _, testCase := range cases {
		if got := Truth(testCase.v); got != testCase.want {
			test.Errorf("Truth(%s) = %v, want %v",
				testCase.name, got, testCase.want)
		}
	}
}

func TestRecordAccess(test *testing.T) {
	inner := NewRecord([]string{"last_name"}, map[string]starlark.Value{
		"last_name": starlark.String("SMITH"),
	})
	rec := NewRecord([]string{"amount", "customer", "odd name"}, map[string]starlark.Value{
		"amount":   starlark.MakeInt(5),
		"customer": inner,
		"odd name": starlark.String("here"),
	})
	env := map[string]starlark.Value{"THIS": rec}
	if got := evalStr(test, "THIS.amount", env); got != "5" {
		test.Errorf("THIS.amount = %q", got)
	}
	if got := evalStr(test, "THIS.customer.last_name", env); got != "SMITH" {
		test.Errorf("nested attribute = %q", got)
	}
	if got := evalStr(test, `THIS["odd name"]`, env); got != "here" {
		test.Errorf("subscript = %q", got)
	}
}

func TestIsReserved(test *testing.T) {
	for _, name := range []string{
		"PAGE_NUMBER",
		"THIS",
		"FINAL",
		"math",
		"time",
		"format",
		"len",
		"print",
	} {
		if !IsReserved(name) {
			test.Errorf("%q must be reserved", name)
		}
	}
	for _, name := range []string{"amount", "customer_amount", "region"} {
		if IsReserved(name) {
			test.Errorf("%q must not be reserved", name)
		}
	}
}

func TestArbitraryPrecisionIntegers(test *testing.T) {
	got := evalStr(test, "123456789012345678901234567890 + 1", nil)
	if got != "123456789012345678901234567891" {
		test.Errorf("got %q", got)
	}
}

func BenchmarkEvaluate(bench *testing.B) {
	prog, err := Compile("bench", "amount * qty + math.floor(rate)")
	if err != nil {
		bench.Fatal(err)
	}
	thread := &starlark.Thread{}
	args := []starlark.Value{starlark.MakeInt(3), starlark.MakeInt(4), starlark.Float(1.5)}
	bench.ReportAllocs()
	for bench.Loop() {
		if _, err := prog.Call(thread, args); err != nil {
			bench.Fatal(err)
		}
	}
}

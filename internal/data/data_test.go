package data

import (
	"strings"
	"testing"
	gotime "time"

	"github.com/a1s/sr/internal/tmpl"
	"github.com/shopspring/decimal"
	"go.starlark.net/starlark"
)

func TestReadJSONAcceptsBothForms(test *testing.T) {
	array := `[{"a":1},{"a":2}]`
	ndjson := "{\"a\":1}\n{\"a\":2}\n"
	for name, src := range map[string]string{"array": array, "ndjson": ndjson} {
		rows, err := ReadJSON(strings.NewReader(src))
		if err != nil {
			test.Fatalf("%s: %v", name, err)
		}
		if len(rows) != 2 {
			test.Errorf("%s: rows = %d, want 2", name, len(rows))
		}
	}
	rows, err := ReadJSON(strings.NewReader("  \n"))
	if err != nil || rows != nil {
		test.Errorf("empty input: %v, %v", rows, err)
	}
}

func TestConvertPerDeclaredType(test *testing.T) {
	cases := []struct {
		kind   tmpl.ValueType
		format string
		raw    any
		want   string
	}{
		{tmpl.TypeString, "", "hi", "hi"},
		{tmpl.TypeInt, "", "42", "42"},
		{tmpl.TypeInt, "", "123456789012345678901234567890", "123456789012345678901234567890"},
		{tmpl.TypeDecimal, "", "19.99", "19.99"},
		{tmpl.TypeDecimal, "", "0.10", "0.10"},
		{tmpl.TypeFloat, "", "1.5", "1.5"},
		{tmpl.TypeBool, "", true, "True"},
		{tmpl.TypeBool, "", "false", "False"},
		{tmpl.TypeDatetime, "", "2005-05-24T22:53:30Z", "2005-05-24 22:53:30 +0000 UTC"},
		{tmpl.TypeDate, "", "2005-05-24", "2005-05-24 00:00:00 +0000 UTC"},
		{tmpl.TypeDate, "02.01.2006", "24.05.2005", "2005-05-24 00:00:00 +0000 UTC"},
	}
	for _, testCase := range cases {
		got, err := Convert(testCase.raw, testCase.kind, testCase.format)
		if err != nil {
			test.Errorf("%s %v: %v", testCase.kind, testCase.raw, err)
			continue
		}
		text := got.String()
		if str, ok := starlark.AsString(got); ok {
			text = str
		}
		if !strings.Contains(text, testCase.want) {
			test.Errorf("%s %v = %q, want %q", testCase.kind,
				testCase.raw, text, testCase.want)
		}
	}

	// A decimal keeps its scale, which is what makes money exact.
	want, err := Convert("19.90", tmpl.TypeDecimal, "")
	if err != nil {
		test.Fatal(err)
	}
	if want.String() != "19.90" {
		test.Errorf("decimal = %q, want 19.90", want.String())
	}
}

func TestConvertRejectsBadText(test *testing.T) {
	cases := []struct {
		kind tmpl.ValueType
		raw  any
	}{
		{tmpl.TypeInt, "not a number"},
		{tmpl.TypeDecimal, "1.2.3"},
		{tmpl.TypeDate, "the fifth of May"},
		{tmpl.TypeBool, "maybe"},
		{tmpl.TypeObject, "not an object"},
		{tmpl.TypeList, "not a list"},
	}
	for _, testCase := range cases {
		if _, err := Convert(testCase.raw, testCase.kind, ""); err == nil {
			test.Errorf("%s %v: want an error", testCase.kind, testCase.raw)
		}
	}
}

func TestRecordCoercion(test *testing.T) {
	decl := &tmpl.Records{
		Members: []*tmpl.RecordMember{
			{Name: "n", Type: tmpl.TypeInt},
			{Name: "opt", Type: tmpl.TypeString, Nullable: true},
			{Name: "obj", Type: tmpl.TypeObject},
		},
	}
	rec, err := Record(map[string]any{
		"n":     "7",
		"opt":   nil,
		"obj":   map[string]any{"inner": "x"},
		"extra": "kept",
	}, decl, 0)
	if err != nil {
		test.Fatal(err)
	}
	if value, _ := rec.Attr("n"); value.String() != "7" {
		test.Errorf("n = %v", value)
	}
	if value, _ := rec.Attr("opt"); value != starlark.None {
		test.Errorf("a null in a nullable member becomes None, got %v", value)
	}
	// A member the template does not declare is not an error and stays reachable,
	// since data often carries members a report does not use.
	if value, _ := rec.Attr("extra"); value == nil {
		test.Error("an undeclared field must remain reachable")
	}
	obj, _ := rec.Attr("obj")
	if _, err := obj.(starlark.HasAttrs).Attr("inner"); err != nil {
		test.Errorf("nested object: %v", err)
	}
}

func TestNullInANonNullableMember(test *testing.T) {
	decl := &tmpl.Records{Members: []*tmpl.RecordMember{{Name: "n", Type: tmpl.TypeInt}}}
	_, err := Record(map[string]any{"n": nil}, decl, 3)
	if err == nil {
		test.Fatal("want an error")
	}
	for _, want := range []string{"record 3", `"n"`, "nullable"} {
		if !strings.Contains(err.Error(), want) {
			test.Errorf("diagnostic %q does not mention %q", err, want)
		}
	}
}

func TestParamText(test *testing.T) {
	cases := []struct {
		kind tmpl.ValueType
		text string
		want string
	}{
		{tmpl.TypeString, "anything at all", "anything at all"},
		{tmpl.TypeInt, "-42", "-42"},
		{tmpl.TypeDecimal, "0.00", "0.00"},
		{tmpl.TypeFloat, "1e3", "1000"},
		{tmpl.TypeBool, "TRUE", "True"},
		{tmpl.TypeDate, "2005-01-01", "2005-01-01"},
	}
	for _, testCase := range cases {
		got, err := ParamText(testCase.text, testCase.kind, "")
		if err != nil {
			test.Errorf("%s %q: %v", testCase.kind, testCase.text, err)
			continue
		}
		text := got.String()
		if str, ok := starlark.AsString(got); ok {
			text = str
		}
		if !strings.Contains(text, testCase.want) {
			test.Errorf("%s %q = %q, want %q", testCase.kind,
				testCase.text, text, testCase.want)
		}
	}
	if _, err := ParamText("not a date", tmpl.TypeDate, ""); err == nil {
		test.Error("want an error for text that does not parse")
	}
}

func TestStructFields(test *testing.T) {
	type row struct {
		Amount   string `sr:"amount"`
		Plain    int
		Hidden   string `sr:"-"`
		internal string
	}
	got := StructFields(row{Amount: "1.00", Plain: 2, Hidden: "no", internal: "no"})
	if got["amount"] != "1.00" {
		test.Errorf("tagged field = %v", got["amount"])
	}
	if got["Plain"] != 2 {
		test.Errorf("untagged field = %v", got["Plain"])
	}
	if _, ok := got["Hidden"]; ok {
		test.Error(`a field tagged "-" is skipped`)
	}
	if len(got) != 2 {
		test.Errorf("fields = %v", got)
	}
}

func TestMatchesType(test *testing.T) {
	want, _ := Convert("1.00", tmpl.TypeDecimal, "")
	if !MatchesType(want, tmpl.TypeDecimal) {
		test.Error("a decimal must match decimal")
	}
	if MatchesType(want, tmpl.TypeFloat) {
		test.Error("a decimal is not a float")
	}
	if !MatchesType(starlark.String("x"), tmpl.TypeString) {
		test.Error("a string must match string")
	}
}

// The library API takes records as Go maps or structs,
// so a decimal and a time arrive as themselves rather than as text.
func TestConvertAcceptsGoValues(test *testing.T) {
	when := gotime.Date(2005, 5, 24, 22, 53, 30, 0, gotime.UTC)
	money := decimal.RequireFromString("19.99")

	got, err := Convert(money, tmpl.TypeDecimal, "")
	if err != nil {
		test.Fatal(err)
	}
	if got.String() != "19.99" {
		test.Errorf("decimal = %q", got.String())
	}
	got, err = Convert(when, tmpl.TypeDatetime, "")
	if err != nil {
		test.Fatal(err)
	}
	if !strings.Contains(got.String(), "22:53:30") {
		test.Errorf("datetime = %q", got.String())
	}
	// A date drops the time of day, whichever form it arrived in.
	got, err = Convert(when, tmpl.TypeDate, "")
	if err != nil {
		test.Fatal(err)
	}
	if !strings.Contains(got.String(), "00:00:00") {
		test.Errorf("date = %q", got.String())
	}
	// A decimal reaching a string member becomes its plain text.
	got, err = Convert(money, tmpl.TypeString, "")
	if err != nil {
		test.Fatal(err)
	}
	if text, _ := starlark.AsString(got); text != "19.99" {
		test.Errorf("string = %q", text)
	}
}

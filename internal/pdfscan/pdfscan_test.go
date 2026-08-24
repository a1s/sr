package pdfscan

import (
	"testing"
)

// The lexer reads each kind of PDF object, and tells an integer
// followed by a generation and an R from three separate numbers.
func TestLexObjects(test *testing.T) {
	for _, row := range []struct {
		source string
		want   string
	}{
		{"/Name", "/Name"},
		{"/A#20B", "/A B"},
		{"42", "42"},
		{"-3.5", "-3.5"},
		{"true", "true"},
		{"null", "null"},
		{"(text)", "text"},
		{`(a \(nested\) case)`, "a (nested) case"},
		{`(octal \101)`, "octal A"},
		{"<414243>", "ABC"},
		// An odd number of digits pads the last byte with a zero.
		{"<41424>", "AB@"},
		{"12 0 R", "12 0 R"},
		{"[1 2 3]", "[1 2 3]"},
		{"[12 0 R /Name]", "[12 0 R /Name]"},
		{"[1 2 3] 4", "[1 2 3]"},
	} {
		value, err := newLexer([]byte(row.source)).object()
		if err != nil {
			test.Errorf("%q: %v", row.source, err)
			continue
		}
		if got := Text(value); got != row.want {
			test.Errorf("%q read as %s, want %s", row.source, got, row.want)
		}
	}
}

// A dictionary reads its keys, and a comment between them is skipped.
func TestLexDictionary(test *testing.T) {
	source := "<< /Type /Page % a comment\n /Count 3 /Parent 1 0 R >>"
	value, err := newLexer([]byte(source)).object()
	if err != nil {
		test.Fatal(err)
	}
	dict, ok := value.(Dict)
	if !ok {
		test.Fatalf("read as %T", value)
	}
	if dict["Type"] != Name("Page") {
		test.Errorf("Type = %v", dict["Type"])
	}
	if dict["Count"] != float64(3) {
		test.Errorf("Count = %v", dict["Count"])
	}
	if dict["Parent"] != (Ref{Num: 1}) {
		test.Errorf("Parent = %v", dict["Parent"])
	}
}

// Reading a file that is not a PDF fails rather than returning an empty
// document, which would make a test pass by finding nothing.
func TestReadRejectsRubbish(test *testing.T) {
	for _, source := range []string{
		"",
		"not a pdf at all",
		"%PDF-1.7\nstartxref\n9999\n%%EOF\n",
	} {
		if _, err := Read([]byte(source)); err == nil {
			test.Errorf("%q read as a PDF", source)
		}
	}
}

// Two matrices concatenate the way PDF's cm operator does,
// the first applied first.
func TestMatrixConcatenation(test *testing.T) {
	scale := matrix{2, 0, 0, 3, 0, 0}
	move := matrix{1, 0, 0, 1, 10, 20}

	left, top := scale.multiply(move).apply(1, 1)
	if left != 12 || top != 23 {
		test.Errorf("scale then move gave %v,%v, want 12,23", left, top)
	}
	left, top = move.multiply(scale).apply(1, 1)
	if left != 22 || top != 63 {
		test.Errorf("move then scale gave %v,%v, want 22,63", left, top)
	}
}

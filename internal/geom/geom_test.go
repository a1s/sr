package geom

import (
	"errors"
	"math"
	"testing"
)

func TestParseDim(test *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"35", 35},
		{"35pt", 35},
		{"1in", 72},
		{"1000mil", 72},
		{"7.5mil", 0.54},
		{"25.4mm", 72},
		{"2.54cm", 72},
		{"1cm", 28.346},
		{"5mm", 14.173},
		{" 1.5 cm ", 42.52},
		{"-10", -10},
	}
	for _, testCase := range cases {
		got, err := ParseDim(testCase.in)
		if err != nil {
			test.Errorf("ParseDim(%q): %v", testCase.in, err)
			continue
		}
		if got != testCase.want {
			test.Errorf("ParseDim(%q) = %v, want %v", testCase.in, got, testCase.want)
		}
	}
	for _, bad := range []string{"", "abc", "10furlongs", "mm"} {
		if _, err := ParseDim(bad); err == nil {
			test.Errorf("ParseDim(%q): want error", bad)
		}
	}
}

func TestRound(test *testing.T) {
	cases := []struct{ in, want float64 }{
		{1.0 / 3.0, 0.333},
		{2.0 / 3.0, 0.667},
		{-2.0 / 3.0, -0.667},
		{72, 72},
		{0.0005, 0.001},
	}
	for _, testCase := range cases {
		if got := Round(testCase.in); got != testCase.want {
			test.Errorf("Round(%v) = %v, want %v", testCase.in, got, testCase.want)
		}
	}
}

func TestFitsTolerance(test *testing.T) {
	if !Fits(20.0005, 20) {
		test.Error("a band within tolerance of the remaining space must fit")
	}
	if Fits(20.002, 20) {
		test.Error("a band beyond tolerance must not fit")
	}
}

// TestResolveSpecTable covers every row of the resolution table in
// doc/template.md#position-and-size-any-two-of-three, in a container running
// from 100 for 200 units.
func TestResolveSpecTable(test *testing.T) {
	const start, size = 100.0, 200.0
	cases := []struct {
		name    string
		extent  Extent
		pos, ex float64
	}{
		{"nothing: left=0 right=0, the container's full width",
			Extent{}, 100, 200},
		{"left alone: right=0, extend to the container's right edge",
			Extent{Near: Val(10)}, 110, 190},
		{"width alone: left=0",
			Extent{Size: Val(30)}, 100, 30},
		{"right alone: left=0",
			Extent{Far: Val(20)}, 100, 180},
		{"left and width, both explicit",
			Extent{Near: Val(10), Size: Val(30)}, 110, 30},
		{"left=10 right=0, extend to the right margin",
			Extent{Near: Val(10), Far: Val(0)}, 110, 190},
		{"left=10 right=4, extend to 4 short of the right margin",
			Extent{Near: Val(10), Far: Val(4)}, 110, 186},
		{"right=4 width=5, right edge 4 in from the right, 5 wide",
			Extent{Far: Val(4), Size: Val(5)}, 291, 5},
	}
	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			pos, ex, err := testCase.extent.Resolve(start, size)
			if err != nil {
				test.Fatalf("Resolve: %v", err)
			}
			if pos != testCase.pos || ex != testCase.ex {
				test.Errorf("Resolve = (%v, %v), want (%v, %v)",
					pos, ex, testCase.pos, testCase.ex)
			}
		})
	}
}

// The geometry table in doc/decisions.md gives the four
// migration examples as container-relative meanings.
// Reproduce them against a container at 100 of width 200.
func TestResolveMigrationTable(test *testing.T) {
	const start, size = 100.0, 200.0
	// right=4 width=5: right edge 4 in from the container's right edge (296),
	// so the left edge sits at 291 — 9 in from the right edge.
	pos, ex, err := Extent{Far: Val(4), Size: Val(5)}.Resolve(start, size)
	if err != nil {
		test.Fatal(err)
	}
	if pos != 291 || ex != 5 {
		test.Fatalf("got (%v, %v), want (291, 5)", pos, ex)
	}
	if right := Round(pos + ex); right != 296 {
		test.Fatalf("right edge %v, want 296 — 4 in from the container's 300", right)
	}
}

func TestResolveAllThreeIsAnError(test *testing.T) {
	ext := Extent{Near: Val(1), Far: Val(2), Size: Val(3)}
	if _, _, err := ext.Resolve(0, 100); !errors.Is(err, ErrOverSpecified) {
		test.Errorf("want ErrOverSpecified, got %v", err)
	}
}

func TestResolveNegativeFarOverflows(test *testing.T) {
	// A negative far offset means the box reaches past the container edge,
	// which is legal -- only the resulting page coordinates decide overflow.
	pos, ex, err := Extent{Near: Val(10), Far: Val(-20)}.Resolve(0, 100)
	if err != nil {
		test.Fatal(err)
	}
	if pos != 10 || ex != 110 {
		test.Errorf("got (%v, %v), want (10, 110)", pos, ex)
	}
}

func TestResolveCrossedEdgesClampToZero(test *testing.T) {
	_, ex, err := Extent{Near: Val(80), Far: Val(80)}.Resolve(0, 100)
	if err != nil {
		test.Fatal(err)
	}
	if ex != 0 {
		test.Errorf("extent %v, want 0", ex)
	}
}

func TestResolveMaxClamp(test *testing.T) {
	// sakila's group title: left=5mm width=55mm maxwidth=50mm.
	// The clamp is not part of the two-of-three count, so left
	// and width both still apply, and the near edge stays put.
	pos, ex, err := Extent{
		Near: Val(14.173),
		Size: Val(155.906),
		Max:  Val(141.732),
	}.Resolve(0, 500)
	if err != nil {
		test.Fatal(err)
	}
	if pos != 14.173 || ex != 141.732 {
		test.Errorf("got (%v, %v), want (14.173, 141.732)", pos, ex)
	}

	// Anchored by the far edge instead, the far edge is what stays put.
	pos, ex, err = Extent{Far: Val(10), Size: Val(100), Max: Val(40)}.Resolve(0, 200)
	if err != nil {
		test.Fatal(err)
	}
	if pos != 150 || ex != 40 {
		test.Errorf("got (%v, %v), want (150, 40)", pos, ex)
	}
	if right := Round(pos + ex); right != 190 {
		test.Errorf("right edge %v, want 190", right)
	}

	// A clamp above the resolved extent does nothing.
	_, ex, err = Extent{Near: Val(0), Size: Val(30), Max: Val(50)}.Resolve(0, 200)
	if err != nil {
		test.Fatal(err)
	}
	if ex != 30 {
		test.Errorf("extent %v, want 30", ex)
	}
}

func TestAlign(test *testing.T) {
	if got := AlignH(10, 100, 20, HLeft); got != 10 {
		test.Errorf("HLeft = %v, want 10", got)
	}
	if got := AlignH(10, 100, 20, HCenter); got != 50 {
		test.Errorf("HCenter = %v, want 50", got)
	}
	if got := AlignH(10, 100, 20, HRight); got != 90 {
		test.Errorf("HRight = %v, want 90", got)
	}
	if got := AlignV(10, 100, 20, VTop); got != 10 {
		test.Errorf("VTop = %v, want 10", got)
	}
	if got := AlignV(10, 100, 20, VCenter); got != 50 {
		test.Errorf("VCenter = %v, want 50", got)
	}
	if got := AlignV(10, 100, 20, VBottom); got != 90 {
		test.Errorf("VBottom = %v, want 90", got)
	}
}

func TestPageSizes(test *testing.T) {
	a4, err := LookupPageSize("A4")
	if err != nil {
		test.Fatal(err)
	}
	// 210mm x 297mm, the figures the printout header example carries.
	if math.Abs(a4.Width-595.276) > 0.001 || math.Abs(a4.Height-841.89) > 0.001 {
		test.Errorf("A4 = %v, want 595.276 x 841.89", a4)
	}
	letter, err := LookupPageSize("Letter")
	if err != nil {
		test.Fatal(err)
	}
	if letter.Width != 612 || letter.Height != 792 {
		test.Errorf("Letter = %v, want 612 x 792", letter)
	}
	if _, err := LookupPageSize("A0"); err == nil {
		test.Error("A0 is not in the accepted list; want an error")
	}
}

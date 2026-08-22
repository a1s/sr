package expr

import (
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// divScale is the number of fractional digits a decimal division produces,
// per doc/expressions.md#the-decimal-type.
const divScale = 6

// Decimal is an exact decimal value, declared by member type="decimal".
//
// Arithmetic between decimals, and between a decimal and an int, is exact;
// the result's scale is what exactness requires. Division quantizes to six
// fractional digits, rounding half away from zero. Mixing a decimal with a
// float is an error rather than a silent conversion.
type Decimal struct {
	Val decimal.Decimal
}

// NewDecimal wraps a decimal.Decimal as a Starlark value.
func NewDecimal(dec decimal.Decimal) Decimal { return Decimal{Val: dec} }

// ParseDecimal parses plain decimal text.
func ParseDecimal(text string) (Decimal, error) {
	dec, err := decimal.NewFromString(text)
	if err != nil {
		return Decimal{}, fmt.Errorf("bad decimal %q", text)
	}
	return Decimal{Val: dec}, nil
}

// String renders the value as plain decimal text, with no exponent.
//
// The scale is preserved rather than trimmed: exactness decides how many
// fractional digits a result has, so adding two 2-place values shows two
// places and multiplying them shows four.
func (dec Decimal) String() string {
	if exp := dec.Val.Exponent(); exp < 0 {
		return dec.Val.StringFixed(-exp)
	}
	return dec.Val.String()
}

// Type names the Starlark type.
func (dec Decimal) Type() string { return "decimal" }

// Freeze is a no-op: a Decimal is immutable.
func (dec Decimal) Freeze() {}

// Truth reports whether the value is non-zero.
func (dec Decimal) Truth() starlark.Bool { return starlark.Bool(!dec.Val.IsZero()) }

// Hash returns a hash of the value.
func (dec Decimal) Hash() (uint32, error) {
	return starlark.String(dec.String()).Hash()
}

// decimalOperand converts y to a decimal for arithmetic.
// A float operand is refused: the conversion has to be deliberate.
func decimalOperand(operand starlark.Value) (decimal.Decimal, bool, error) {
	switch typed := operand.(type) {
	case Decimal:
		return typed.Val, true, nil
	case starlark.Int:
		big := typed.BigInt()
		return decimal.NewFromBigInt(big, 0), true, nil
	case starlark.Float:
		return decimal.Decimal{}, false, fmt.Errorf(
			"cannot mix decimal and float; convert with float(d) or decimal(str(f))")
	}
	return decimal.Decimal{}, false, nil
}

// Binary implements decimal arithmetic.
func (dec Decimal) Binary(
	op syntax.Token,
	operand starlark.Value,
	side starlark.Side,
) (starlark.Value, error) {
	other, ok, err := decimalOperand(operand)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	left, right := dec.Val, other
	if side == starlark.Right {
		left, right = other, dec.Val
	}
	switch op {
	case syntax.PLUS:
		return Decimal{Val: left.Add(right)}, nil
	case syntax.MINUS:
		return Decimal{Val: left.Sub(right)}, nil
	case syntax.STAR:
		return Decimal{Val: left.Mul(right)}, nil
	case syntax.SLASH:
		if right.IsZero() {
			return nil, fmt.Errorf("decimal division by zero")
		}
		return Decimal{Val: left.DivRound(right, divScale)}, nil
	case syntax.SLASHSLASH:
		if right.IsZero() {
			return nil, fmt.Errorf("decimal division by zero")
		}
		whole := left.Div(right).Floor()
		return Decimal{Val: whole}, nil
	case syntax.PERCENT:
		if right.IsZero() {
			return nil, fmt.Errorf("decimal division by zero")
		}
		return Decimal{Val: left.Mod(right)}, nil
	}
	return nil, nil
}

// Cmp orders decimals against decimals and ints.
func (dec Decimal) CompareSameType(
	op syntax.Token,
	operand starlark.Value,
	depth int,
) (bool, error) {
	other, ok, err := decimalOperand(operand)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("cannot compare decimal with %s", operand.Type())
	}
	cmp := dec.Val.Cmp(other)
	switch op {
	case syntax.EQL:
		return cmp == 0, nil
	case syntax.NEQ:
		return cmp != 0, nil
	case syntax.LT:
		return cmp < 0, nil
	case syntax.LE:
		return cmp <= 0, nil
	case syntax.GT:
		return cmp > 0, nil
	case syntax.GE:
		return cmp >= 0, nil
	}
	return false, fmt.Errorf("unsupported comparison %s on decimal", op)
}

// Neg implements unary minus.
func (dec Decimal) Neg() starlark.Value { return Decimal{Val: dec.Val.Neg()} }

// Unary implements unary operators on a decimal.
func (dec Decimal) Unary(op syntax.Token) (starlark.Value, error) {
	switch op {
	case syntax.MINUS:
		return Decimal{Val: dec.Val.Neg()}, nil
	case syntax.PLUS:
		return dec, nil
	}
	return nil, nil
}

// Quantize rounds to places fractional digits, half away from zero.
func (dec Decimal) Quantize(places int32) Decimal {
	return Decimal{Val: dec.Val.Round(places)}
}

// Float converts to a float64, losing exactness.
func (dec Decimal) Float() float64 {
	num, _ := dec.Val.Float64()
	return num
}

// BigInt truncates toward zero.
func (dec Decimal) BigInt() *big.Int {
	return dec.Val.Truncate(0).BigInt()
}

var (
	_ starlark.Value      = Decimal{}
	_ starlark.HasBinary  = Decimal{}
	_ starlark.HasUnary   = Decimal{}
	_ starlark.Comparable = Decimal{}
)

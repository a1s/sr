# Expression environment

Template expressions are [Starlark](https://github.com/bazelbuild/starlark)
expressions. This document specifies the dialect, the names in scope,
the formatting mechanism, and variable accumulation semantics.

## Contents

- [Language](#language)
- [Determinism](#determinism)
- [Names in scope](#names-in-scope)
- [Predefined variables](#predefined-variables)
- [Modules and builtins](#modules-and-builtins)
- [The `decimal` type](#the-decimal-type)
- [Formatting](#formatting)
- [Variables](#variables)
- [Compilation](#compilation)

## Language

Starlark is Python-like, not Python. What matters for templates:

- **No `**` operator.** Use `math.pow(x, y)`.
- **No statements.** An expression is a single expression.
- **No classes, no exceptions.** `fail(msg)` aborts with a message.
- **Integers are arbitrary precision.** `123456789012345678901234567890 + 1` is
  exact.
- **`/` is float division**, `//` is floor division. `1/3` is a float.
- **Strings are immutable** and indexable by byte; `.elems()` and `.codepoints()`
  iterate.
- **`set` is available**, which `calc="set"` uses.
- **`%` interpolation has no flags, width, or precision.** See
  [Formatting](#formatting).

## Determinism

The same template over the same data produces the same printout. Two properties
guarantee it:

- **There is no clock.** `time.now` is not in scope. Use `BUILD_TIME`,
  which the engine sets once per run and a caller may set explicitly,
  making output bit-reproducible.
- **Iteration order is fixed.** Dictionaries preserve insertion order
  and `set` iterates in first-seen order.

## Names in scope

Resolution order, first match wins:

1. [Predefined variables](#predefined-variables) — `PAGE_NUMBER`, `THIS`,
   and the rest.
2. [Modules and builtins](#modules-and-builtins) — `math`, `time`, `format`, …
3. `parameter` names. A parameter's value has the type its
   [declaration](template.md#parameter) gives, whether it came
   from the caller as text, from `default`, or from `defaultexpr`.
4. `variable` names.
5. Declared fields of the current data record.

A record field shadowed by a variable of the same name is reachable as
`THIS.<name>`. Fields are always reachable that way, which is also how to
reach a field whose name is not a valid identifier, or one that
[`records`](template.md#records) does not declare.

```
amount                      # bare
THIS.amount                 # equivalent
customer.last_name          # nested object
film.title                  # nested object
THIS["odd name"]            # subscript
```

Nested objects come from nested JSON and are declared
`column ... type="object"`.

## Predefined variables

| Name | Type | Meaning |
|---|---|---|
| `THIS` | record | The current data record. |
| `ITEM_NUMBER` | int | 1-based index of the current record in the data sequence. Differs from `REPORT_COUNT` when details are suppressed by `printwhen`. |
| `DATA_COUNT` | int | Total number of records. |
| `REPORT_COUNT` | int | Detail sections printed since the start of the report. |
| `PAGE_COUNT` | int | Detail sections printed since the start of the page. |
| `COLUMN_COUNT` | int | Detail sections printed since the start of the column. |
| *group*`_COUNT` | int | Detail sections printed since the start of the named group. |
| `PAGE_NUMBER` | int | Current page number, 1-based. |
| `COLUMN_NUMBER` | int | Current column number, 1-based. |
| *group*`_PAGE_NUMBER` | int | Page number relative to the start of the named group, 1-based. |
| `VERTICAL_POSITION` | float, points | Top of the current section within the frame. |
| `VERTICAL_SPACE` | float, points | Space from the top of the current section to the nearest footer, or to the frame bottom if there is none. |
| `BUILD_TIME` | time | When this run started. Constant for the whole run. |

Counters read the value current at the moment the expression is evaluated,
so a `PAGE_COUNT` in a page footer reports that page's detail count.
Counts that are only final later — the total page count, a group's total —
need `evaltime`; see [layout.md](layout.md#deferred-evaluation).

## Modules and builtins

### Starlark builtins

Available: `abs` `all` `any` `bool` `bytes` `chr` `dict` `dir` `enumerate` `fail`
`float` `getattr` `hasattr` `hash` `int` `len` `list` `max` `min` `ord` `range`
`repr` `reversed` `set` `sorted` `str` `tuple` `type` `zip`.

`print` is not available — a template has nowhere to print to.
There is no `round` builtin; use `math.round` or [`format`](#formatting).

### `math`

`ceil` `floor` `round` `mod` `pow` `sqrt` `fabs` `exp` `log` `hypot` `copysign`
`remainder`, the trigonometric and hyperbolic functions, `degrees`, `radians`,
`gamma`, and the constants `pi` and `e`.

### `time`

Constructors: `time.time(year=, month=, day=, hour=, minute=, second=,
nanosecond=, location=)`, `time.parse_time(s, format=, location=)`,
`time.from_timestamp(sec, nsec=)`, `time.parse_duration(s)`.

`time.now` is **not** available; see [Determinism](#determinism).

A time value has `.year .month .day .hour .minute .second .nanosecond .unix
.unix_nano`, plus `.in_location(name)` and `.format(layout)`.

`.format` takes a **Go reference-time layout**:

```
rental_date.format("02.01.2006")        # → "24.05.2005"
```

For the familiar directives, use `strftime`.

### `strftime`

```
strftime(t, spec) -> string
```

Formats a time using `%Y %y %m %d %H %M %S %j %B %b %A %a %p %I %Z %z %%`.
Locale-independent — month and day names are English.

```
strftime(rental_date, '%d.%m.%Y')
```

### `format`

See [Formatting](#formatting).

## The `decimal` type

A column declared `type="decimal"` produces `decimal` values,
and the type is available to expressions.

```
decimal("19.99")            # from a string
decimal(5)                  # from an int
```

Arithmetic:

- `+` `-` `*` between decimals, or a decimal and an int, are **exact**.
  The result's scale is what exactness requires: adding two 2-place values
  gives 2 places, multiplying them gives 4.
- `/` between decimals produces a decimal quantized to 6 fractional digits,
  rounding half away from zero. Use `quantize` for a different scale.
- Comparisons and `min` `max` `sum` work as expected and stay exact.
- **Mixing a decimal with a float is an error.** Convert deliberately with
  `float(d)` or `decimal(str(f))`.

Helpers:

```
quantize(d, places)         # round to `places` fractional digits, half away from zero
float(d)                    # explicit, lossy
str(d)                      # plain decimal text, no exponent
int(d)                      # truncates toward zero
```

`calc="sum"`, `"avg"`, `"min"`, `"max"` over decimals produce decimals;
`avg` quantizes like `/`. `"std"` and `"var"` produce floats.

## Formatting

Numeric presentation happens in the engine, not inside expressions:
Starlark's `%` operator supports no flags, width, or precision,
and `.format()` rejects format specs.

### The `format=` property

`field format=` and `barcode format=` are applied by the engine to the
expression's result, using a `%`-style specification with the full set
of flags, width, and precision:

```kdl
field expr="amount" format="%.2f"
field expr="ITEM_NUMBER" format="%3d."
field expr="(customer.last_name, customer.first_name, customer_amount)"
      format="Total for %s, %s: %.2f"
```

A tuple result spreads across the conversions positionally;
the count must match exactly. The default is `"%s"`.

Supported conversions: `%s` `%q` `%d` `%i` `%o` `%x` `%X` `%b` `%e` `%E` `%f` `%g`
`%G` `%c` `%%`, each accepting the flags `-` `+` `#` `0` `' '`, a width, and a
precision. `%q` is a quoted string; `%i` is an alias for `%d`. There is no `%r`.

`%d` and the float conversions accept `decimal` values and format them exactly:
`%.2f` on a decimal rounds half away from zero without going through a float.

`%s` on a float uses the shortest representation that round-trips, so `1.0/3`
renders as `0.3333333333333333`. Give a precision.

### The `format` builtin

The same formatter, callable from an expression:

```
format("%.2f", amount)
format("%s, %s", customer.last_name, customer.first_name)
```

Use this rather than `%` whenever precision is involved.

### Starlark's own `%`

Available for plain conversions:

```
'%s, %s' % (customer.last_name, customer.first_name)
```

Supported: `%s` `%r` `%d` `%i` `%o` `%x` `%X` `%e` `%f` `%g` `%E` `%G` `%c` `%%`,
with no flags, width, or precision. The argument count must match.

Prefer `format`.

## Variables

A `variable` folds a value into an accumulator as records are consumed.

```kdl
variable "total_amount"    expr="amount" calc="sum"   reset="report"
variable "row_number"      expr="1"      calc="sum"   reset="page"
variable "customer_amount" expr="amount" calc="sum"   reset="group" resetgrp="customer"
variable "first_title"     expr="film.title" calc="first" reset="group" resetgrp="customer"
```

### `calc`

| `calc` | Result | Retains values |
|---|---|---|
| `first` | the first value folded since the last reset | no |
| `last` | the most recent value | no |
| `count` | number of values folded | no |
| `sum` | sum | no |
| `avg` | sum ÷ count | no |
| `min` / `max` | extremum | no |
| `std` / `var` | sample standard deviation / variance, as floats | no |
| `list` | all values, in order | **yes** |
| `set` | distinct values, in first-seen order | **yes** |
| `chain` | concatenation of values, each of which must be a sequence | **yes** |

Only `list`, `set`, and `chain` retain individual values. Everything else is an
incremental accumulator with constant memory.

An empty accumulator reads as `0` for `count`; `None` for `first`, `last`, `sum`,
`avg`, `min`, `max`, `std`, `var`; and an empty `list`, `set`, or `chain`
otherwise. `sum` of nothing is `None` rather than `0`, so "no rows" stays
distinguishable from "rows summing to zero" — write `total_amount or 0`
where the distinction does not matter.

### `iter` and `reset`

`iter` says when `expr` is evaluated and folded in.
`reset` says when the accumulator is cleared.

| Value | Fires |
|---|---|
| `report` | once, at the start (`iter`) or end (`reset`) of the report |
| `page` | at each page boundary |
| `column` | at each column boundary |
| `group` | at each break of the group named by `itergrp` / `resetgrp` |
| `detail` | once per printed detail section |
| `item` | once per data record, whether or not its detail prints |

`detail` is the default for `iter`, `report` for `reset`.

`detail` and `item` differ for records whose detail is suppressed by `printwhen`:
`iter="detail"` skips them, `iter="item"` counts them.

`init`, if given, is evaluated at each reset and folded in as the first value.

### Ordering against section printing

Within one record, the order is:

1. `THIS` and `ITEM_NUMBER` advance to the new record.
2. Group expressions are evaluated outermost-first; the first that changed
   determines the break level, and all groups nested inside it also break.
3. For each breaking group, innermost-first: its `summary` prints, using
   the **previous** record's values.
4. Variables reset for the scopes that just ended.
5. For each breaking group, outermost-first: variables iterate for that scope,
   then its `title` prints.
6. The `detail` section's variables iterate, then the detail prints.

Step 3 precedes step 4, which is what lets a group summary print that group's
own total.

A detail section that turns out not to fit and is deferred to the next frame has
its variable fold rolled back and reapplied after the eject, so a value is never
counted twice.

## Compilation

Each expression is parsed and compiled once, at template load, into a function
whose parameters are the names it references:

```
# expr="amount * qty + math.floor(rate)"
def _e(amount, qty, rate):
    return amount * qty + math.floor(rate)
```

Per evaluation the engine calls it with just the values that expression needs, so
cost does not scale with the number of columns in the data. Throughput is roughly
0.26 µs per evaluation on one core.

Starlark resolves names at compile time, so every name an expression can reference
must be known before compilation. That is what the
[`records`](template.md#records) declaration provides.
A field not declared there is reachable only as `THIS["name"]`.

### Error reporting

A compile error names the template file, the node path, the property,
and the position within the expression. A runtime error — a missing field,
a type mismatch, `fail()` — additionally names the record index and the section
being built.

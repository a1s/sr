# Design decisions

Why `sr` is shaped the way it is. Nothing here is needed to use the engine — the
reference documentation stands on its own. This file exists so that the reasoning
behind a rule is recoverable without re-deriving it, and so the reference docs
can state what is true without also arguing for it.

## Contents

- [Relationship to PythonReports](#relationship-to-pythonreports)
- [Why a new engine at all](#why-a-new-engine-at-all)
- [Specification before code](#specification-before-code)
- [Template syntax: KDL over TOML](#template-syntax-kdl-over-toml)
- [Data: JSON with declared types](#data-json-with-declared-types)
- [Parameters are typed, and `default` is text](#parameters-are-typed-and-default-is-text)
- [Printout: a separate format](#printout-a-separate-format)
- [Expressions: Starlark](#expressions-starlark)
- [Layout: measure before commit](#layout-measure-before-commit)
- [Geometry: two of three edges](#geometry-two-of-three-edges)
- [Smaller decisions](#smaller-decisions)
- [Spike results](#spike-results)
- [Inherited defects, and what replaced them](#inherited-defects-and-what-replaced-them)

## Relationship to PythonReports

`sr` is a clean break from [PythonReports](https://github.com/a1s/PythonReports),
not a port. No template compatibility, no converter, no migration path.

That engine is worth reading rather than porting. It encodes twenty years of
decisions about what a printable report actually needs — bands, group scoping,
deferred page counts, column ejects — and those decisions are mostly right.
What it lacks is the mechanism to implement several of them correctly,
and a specification of what it does.

Sizes, for scale:

| Area | Lines |
|---|---|
| `builder.py` — context, variables, layout, pagination, subreports | 2906 |
| `datatypes.py` — typed attributes, `Box` geometry, value types | 1606 |
| `template.py` / `printout.py` — schemata | 652 |
| `drivers.py` — word wrap, metrics abstraction | 393 |
| `segment_layout.py` — DAG solver for floating boxes | 243 |
| `pdf.py` — reportlab writer | 512 |
| `barcode.py` + bundled Aztec generator | 1675 |
| `design.py`, `editor/`, `Tk.py`, `wxPrint.py` | ~8700 |

Not carried over: the barcode implementations (`boombuler/barcode` covers
Code128, Code39, Code93, 2of5, QR, DataMatrix, Aztec), the Tk and wx designers,
the wx printer — self-documented as "malfunctional" — and charts, which were
planned but never implemented. That is roughly 10,000 lines that do not need to
exist, part of it still Python 2.

Kept, deliberately: the band and group model, variable scoping with
`calc`/`iter`/`reset`, deferred evaluation, document order as paint order,
first-match style and eject selection, the template-to-printout boundary,
and the floating-element DAG solver, which is ported nearly unchanged
along with its test cases.

## Why a new engine at all

Banded reporting has no small, dependency-free implementation. Jasper needs a
JVM. Crystal is effectively Windows-only. HTML through headless Chrome is bad at
exactly the things this format is good at: a group footer that knows its group's
total, a page footer that knows the document ran to 47 pages, column ejects,
band-relative geometry.

A single static binary that reads a template and JSON and writes PDF fills a real
gap. Go was chosen for that: static linking, no runtime to install, and adequate
font and PDF libraries.

## Specification before code

PythonReports' pagination behaviour existed only as code, in one 2,900-line file,
with essentially no tests — two assertion-free scripts, one of which needed a
pickle file that was not in the repository. The behaviour could not be read off
the source with confidence.

A port could have used its output as an oracle: generate printouts with the old
engine, freeze them, diff. `sr` is not a port and fixes behaviour rather than
matching it, so that oracle does not exist.

That leaves the specification as the definition of correct. Which means it has to
be written before the code rather than extracted from it afterwards, and the tests
derive from the specification rather than from recorded output. It also means the
rule that when specification and test disagree, the specification is what gets
examined first.

Verification therefore rests on: spec-derived unit tests, hand-authored printout
fixtures small enough to check by reading, layout invariants asserted on every
printout produced in tests, and a round-trip through PDF comparing extracted glyph
positions against the printout.

## Template syntax: KDL over TOML

TOML was the initial proposal. It cannot express this format cleanly, for two
reasons that are properties of the format rather than matters of taste.

**Paint order is document order across different element types.** Later elements
draw on top of earlier ones, so a filled `rectangle` must precede the fields that
sit on it. TOML gives `[[header.rectangle]]` and `[[header.field]]` — order
preserved within each array, destroyed between them. That is precisely the
information the paint-order rule consumes. Every non-trivial band in the reference
template depends on it.

**`style` and `eject` are first-match-wins on `when`**, in document order. The
reference template has two `outline` nodes in a group title that differ only by
`when="customers_count % 2"` and its negation.

Workarounds exist — one array of tables with a `kind` discriminator, or an
explicit `z` attribute — but they turn the template into a serialized AST,
which is worse to hand-edit than the format being replaced. Hand-editing is
the point of a template format.

KDL maps almost one-to-one onto the element tree: named nodes, ordered children
of mixed type, positional arguments plus named properties, comments, raw and
multi-line strings, and `/-` to comment out a node.

YAML was considered and rejected: ordered sequences of single-key maps have the
same serialized-AST smell as the TOML workaround. Keeping XML was the fallback if
KDL had proven unworkable.

### KDL v2 specifically

The format uses `#true`/`#false`/`#null`, raw strings, and triple-quoted
multi-line strings, so a v1 parser cannot read it. This matters more than
it should, because v1 parsers do not necessarily *reject* v2 input — see
[spike results](#kdl-parsers).

## Data: JSON with declared types

JSON has neither a date type nor a decimal type, and the reference template
needs both. The predecessor's template coerced in every expression:
`float(amount)`, `rental_date.strftime(...)`.

Declaring field types in the template does two jobs, and the second is why
the declaration is not merely a convenience:

1. Coercion happens once, at load. Otherwise every template repeats conversion
   calls and money silently becomes a binary float.
2. It makes compile-once expression evaluation possible. Starlark resolves names
   when it compiles, so the set of names an expression may reference has to be
   known before any record is read. The column list is that set.

NDJSON exists so a large dataset need not be one enormous JSON value. It does not
enable streaming: `DATA_COUNT`, every report-scoped aggregate, and keep-together
lookahead all need the full sequence, so the engine buffers it. Claiming otherwise
would be a promise the format cannot keep.

## Printout: a separate format

The template-to-printout boundary is the best structural idea in the predecessor
and is kept unchanged in spirit:

- **Renderers stay simple.** The PDF writer does no layout. A second renderer
  costs a traversal, not a reimplementation of pagination.
- **It is the test surface.** Layout correctness is checked by comparing
  printouts, which are exact and diffable, rather than PDFs, which are neither.
- **It is cacheable and archivable.** Re-rendering years later needs no access to
  the original data.
- **It localizes blame.** A wrong position is a layout bug; a correct position
  rendered wrong is a renderer bug.

Only the encoding changed. TOML was proposed for printouts too and is a poor fit:
a printout is an ordered heterogeneous list of thousands of small records,
machine-generated, whereas TOML is built for hand-written configuration. NDJSON
with one page per line suits the shape, and CBOR is available where size matters.

Two deliberate departures from the predecessor's printout:

- **Nested `xref` marks use page coordinates.** The old format shifted the origin
  for nested content, so renderers had to track an offset. Page-absolute
  throughout means a renderer can flatten marks recursively and draw in one pass.
- **The printout records which font file was actually resolved**, not just the
  typeface requested. This makes "it looks different on the build server"
  answerable from the artifact.

## Expressions: Starlark

The predecessor evaluated template expressions with Python's `eval()` in a
namespace that included `__import__` of any module, and a `data` block could
carry a pickle. A template was arbitrary code execution. Starlark removes that:
no imports, no file or network access, no reach into the host.

It also keeps the syntax familiar, so expressions transfer mostly verbatim —
field references, tuples, comparisons, comprehensions, `%` interpolation
for plain conversions.

Consequences accepted:

- **Hermetic, so no `datetime.now()`.** `BUILD_TIME` replaces it. This is an
  improvement: the old idiom made no two builds of the same report comparable,
  and a caller-set `BUILD_TIME` makes output bit-reproducible.
- **No date or decimal type in the language.** The engine provides them.
- **`import` of arbitrary modules is gone**, along with `path="math"`
  and friends. A fixed module set is the point.

### Formatting lives in the engine

Starlark's `%` operator supports no flags, width, or precision, and
`.format()` rejects format specs — verified, see [spike results](#starlark).
So `"%.2f"` cannot be evaluated inside an expression.

This does not affect the `format=` property, which is applied by the engine to the
expression's result and was never evaluated by the expression language in either
engine. It does mean the engine owns a formatter, and that expressions needing
precision call the engine's `format()` builtin rather than `%`.

`%i` is accepted as an alias for `%d` because Python and Starlark both take it
and old templates use it.

`strftime` exists because Starlark's `time.format` takes Go reference-time
layouts. Requiring every author to learn `"02.01.2006"` to replace
`'%d.%m.%Y'` is a cost with no benefit.

### Compile once

The predecessor re-parsed every expression from source on every evaluation; its
own source notes roughly 10 seconds per 1000 records. Compiling once at template
load, into a function whose parameters are the names that expression references,
measured 0.26 µs per evaluation — about 3.8 million per second on one core.
That moves expression evaluation from the top of the profile to below layout.

## Layout: measure before commit

This is the central decision, and most of the layout features follow from it.

The predecessor laid a band directly into the page, discovered it did not fit,
ejected, and retried **exactly once**. If it still did not fit, the content
overflowed and nothing objected. It had no keep-together, no orphan or widow
control, and it reserved header space from a *static* estimate of the template box
rather than the header's measured height — so a variable-height page header
silently shifted every band below it.

Those are not five independent defects. They are one: the engine could commit
but not trial. It half-admitted this, keeping a `rollback` on variables
so a detail band that turned out unprintable could undo its accumulator fold.

Separating measurement from commitment yields band splitting, keep-together,
orphan and widow control, measured header reservation, and correct deferred
evaluation from a single mechanism. The cost is that bands are measured more
than once, mitigated by caching measurement against resolved content and width.

### Deferred values that grow are an error

Re-flowing after a deferred substitution does not converge: a taller page footer
reduces the content area, which produces more pages, which widens the page count,
which can make the footer taller again. Reserving from a placeholder and rejecting
growth is the only rule that terminates. The predecessor substituted text without
re-measuring at all, so an oversized value was silently chopped.

### Overflow fails by default

A band that cannot fit an empty frame and cannot split is an error, not a silently
broken page. `--allow-overflow` downgrades it to a warning recorded in the
printout header, because a single oversized record should not necessarily fail a
nightly batch — but the default is to fail, because a silently broken report is
worse than a missing one.

### Frames are a tree

The predecessor used a chain in which each frame had at most one child, and had
to push position changes down across all descendants, with a source comment
explaining why one level was not enough. Deriving each frame's geometry from
its parent removes that class of bug.

### Footers sit at the frame bottom

The predecessor pushed page footers to the bottom but left column footers floating
immediately after content. One rule is easier to predict, and "immediately after
content" is what a group `summary` is for.

### No output-time shrink pass

The predecessor emitted the declared box, then ran a separate pass that trimmed
whitespace and re-aligned — changing final geometry after layout was supposedly
complete. Computing the content box during measurement makes that pass
unnecessary.

## Geometry: two of three edges

`Box` in the predecessor encoded "relative to the far edge" in the *sign* of `x`,
`y`, `width`, and `height`:

```python
if self.x < 0:  self.x += box.width + 1
self.x = round(self.x + box.x)
if self.width < 0:
    self.width = self.width + box.width + 1 + box.x - self.x
```

So `x=-10 width=-5` in a container at `X` of width `W` resolved to a left edge at
`X + W - 9` and a width of 5. Two problems:

1. **Off by one.** `-n` meant *n − 1* from the far edge; `-1` was flush. Writing
   "at the right margin" required `-1`, not `0`. The `+1` terms exist for that and
   are load-bearing.
2. **Negative `width` was not a width.** It constrained the *right edge*:
   `x=10 width=-5` meant "start at 10, extend to 4 short of the container's right
   edge", so the resulting width depended on the container. It resolved to a
   concrete 5 above only because `x` was also right-relative — the two numbers
   were two offsets from the same edge.

Naming the three edges and requiring exactly two removes both. It is CSS's model
for the same problem, minus the over-constraint rule.

### All three specified is an error

CSS 2.1 resolves the same over-constraint by ignoring `right` for left-to-right
text. Predictable, and not what `sr` does. Validation runs once at load, before
any data is read, so rejecting the template costs a diagnostic rather than a
surprise on page 40 — and silently discarding a constraint the author wrote
deliberately would replace one geometry trap with another. `maxwidth` covers
the intent that most resembles over-specification.

### `height="auto"` versus `bottom=0`

The predecessor wrote `height="-1"` for both "content-determined" and "extend to
the container's bottom edge", telling them apart by which kind of node carried
the attribute. They are different things and now have different spellings.

`halign` and `valign` were left alone. An earlier draft of this analysis claimed
they conflicted with explicit coordinates; they do not. `Box.place()`, which would
discard `x`, is dead code never called from the builder. The live path places the
declared box and then aligns the content-sized box inside it, which is already the
right semantics.

## Parameters are typed, and `default` is text

A parameter value supplied on a command line is always text. Without a declared
type there is nothing to convert it by, so `parameter` carries `type` from the same
vocabulary as [`records`](#data-json-with-declared-types) columns.

That fixes the conversion problem. It also exposes one in the inherited design,
where `default` was an *expression*:

```
parameter "period_start" default="2005-01-01"
```

As an expression that is arithmetic: 2005 − 1 − 1, so the default is `2003`.
Silently, with no type to check it against.

So `default` is text, parsed exactly as a `--param` value is. One textual form,
one parser, and `default="2005-01-01"` with `type="date"` does what it looks like.

Computed defaults are still needed — "the month containing `BUILD_TIME`" is a real
requirement — so they get a separate property, `defaultexpr`. Two properties is
more surface than one, but each is unambiguous, and the ambiguity being removed
produced wrong output rather than an error.

**A parameter with no default is required.** The predecessor made `default`
mandatory, which forced a meaningless value onto parameters that genuinely have
none. Omitting both properties now means the caller must supply a value, and
failing to is an error naming the parameter — better than a report silently running
against a placeholder date.

`arg`, which passes a value to a subreport parameter, stays an expression: it is
never text, so there is nothing to parse. Its result is type-checked against the
declaration like a `defaultexpr` result.

## Smaller decisions

**`printwhen` moved from `style` to the element.** In the predecessor it was a
style property, so it was evaluated on whichever style matched. Suppressing an
element under two alternative styles meant repeating the condition on both, and
adding a style alternative could silently change what was visible. This surfaced
while translating the reference template, where a `rectangle` carried a `style`
whose only job was to hold a `printwhen`.

**`eject when` and `require` are a conjunction, and selection is by `when` alone.**
This is the predecessor's actual behaviour, which its documentation described only
as "the first match will stop the search" — leaving open what happens when a node
carries both properties. `need_eject` is unambiguous: a `when`-false node is skipped
and the next tried, a `when`-true node stops the search whether or not it goes on to
eject, and `require` is only ever consulted on the selected node. Written down here
because the alternative readings are plausible enough to be "simplified" into later:
treating them as independent triggers, or continuing the search past a selected node
that declined to eject. Both would break `when` + `require` on one node, which is
the combination that expresses "start a new page for this group if there is not
enough room left".

**A band that does not fit ejects a column, not a page.** Also the predecessor's
behaviour. In a single-column frame the two are identical; in a multi-column one,
a band overflowing its column should move to the next column rather than abandon
the rest of the page. Escalation to a page eject when no column remains is already
part of the eject rules, so the narrower choice loses nothing. Only an explicit
`eject type="page"` forces a page.

**`pen` split into `width` and `dash`.** One attribute meaning either a stroke
width or a dash style, depending on whether the value parsed as a dimension, is a
type pun. `line width` still shadows the geometry vocabulary; that is accepted
because "pen width" and "line width" are the same phrase in every drawing tool.

**Font guessing kept, with an opt-out.** A template naming `"Helvetica"` should
work on a machine that has something close, without the author enumerating paths.
The reproducibility problem is solved by a strict mode that stops at explicitly
named files and fails loudly, plus recording the resolution in the printout.
Tests run strict, with fonts committed, so no test outcome depends on what is
installed.

**One metrics implementation, no fudge factors.** In the predecessor whichever
metrics library loaded last won, and the PIL path multiplied widths by 1.12 with
the comment "PIL is about 1.1 times smaller". Layout output therefore depended on
which Python packages happened to be installed.

**Incremental accumulators.** The predecessor kept every value in a list for all
twelve `calc` modes and recomputed the aggregate on each read — O(n) per read and
memory linear in record count regardless of mode. Only `list`, `set`, and `chain`
need to retain values.

**`sum` of nothing is null, not zero**, so "no rows" stays distinguishable from
"rows summing to zero".

**`ITEM_NUMBER` is 1-based.** The predecessor documented it as 0-based, which made
it inconsistent with every `_COUNT` beside it and meant templates wrote
`ITEM_NUMBER + 1`.

**Auto-height circularity resolved explicitly.** An element sized by `bottom`
stretches to the band's bottom edge, so in an auto-height band it cannot
contribute to the height it is measured against. Such elements are excluded from
the maximum and resolved afterwards. The predecessor handled this with several
special-case branches in its resizeable check.

**`mil` added to the dimension units.** Barcode module widths are conventionally
quoted in mils, and the predecessor carried them as a bare number on a separate
numeric scale. As a unit they are ordinary dimensions.

**Rounding is specified, not incidental.** Geometry rounds to 3 decimal places
after every computation. This is observable — it decides whether a band fits — so
an implementation that rounds only at output time will disagree about page breaks.

**Unsorted input is not an error.** A group breaks on a change between adjacent
records, so grouping needs no buffering and one pass. "The same key appears again
later" is legal, because a report may deliberately group by a repeating value such
as weekday. The printout records distinct group runs against distinct keys, so the
discrepancy is visible without the engine having to guess intent.

**`pickle` in data blocks removed.** It let a template execute code on load.

**A defect not reproduced:** `fill_summary` in the predecessor reads an unassigned
local, raising `NameError` for any report with `summary swapfooter`.

## Spike results

Throwaway programs validating the assumptions the specification rests on.
Two of them contradicted what was planned.

### Starlark

`go.starlark.net` at `v0.0.0-20260708150628-5395d018f003`.

String interpolation has no width or precision — the assumption that failed:

```
"%.2f" % 3.14159         → error: unknown conversion %.
"%5.1f" % 3.14159        → error: unknown conversion %5
"{:.2f}".format(3.14159) → error: format spec features not supported
                             in replacement fields: .2f
"%s, %s" % ("a", "b")    → "a, b"
"%d" % 42                → "42"
"%i" % 42                → "42"
"%x" % 255               → "ff"
"%c" % 65                → "A"
"%s" % ("a", "b")        → error: too many arguments for format string
```

Other confirmed behaviour:

- Integers are arbitrary precision: `123456789012345678901234567890 + 1` is exact.
- No `**` operator; `2 * 10 ** 3` is a syntax error.
- No `round` builtin; `math.round` exists.
- `set` is available without a feature flag, which `calc="set"` needs.
- `time.now` exists and had to be removed from the environment.
- `time.format` takes Go reference-time layouts:
  `time.time(year=2005, month=5, day=24).format("02.01.2006")` → `"24.05.2005"`.
- `starlarkstruct` gives attribute access, so `customer.last_name` works.
- Floats stringify at full precision: `"%s" % (1.0/3)` → `"0.3333333333333333"`.

Compile-once mechanism, validated: parse the expression, collect referenced
identifiers from the syntax tree excluding the attribute part of `x.y`, subtract
the fixed environment, generate a function whose parameters are what remains.

```
# expr = "amount * qty + math.floor(rate)"
# free names: [amount, math, qty, rate] → params: [amount, qty, rate]
def _e(amount, qty, rate):
    return amount * qty + math.floor(rate)
```

100,000 calls in 26.2 ms — 0.26 µs per evaluation, 3.8 M/sec on one core.

### KDL parsers

Two Go libraries tested against the reference template.

**`github.com/sblinch/kdl-go` — unusable, and quietly so.** Effectively a v1
parser that mis-parses v2 without complaining:

| Construct | Result |
|---|---|
| `node """…"""` | parsed as **three arguments**, not one multi-line string |
| `node #"raw"#` | error, in both argument and property position |
| `node #true` bare argument | error |
| `/-node` slashdash | error |
| `node prop=#true` | accepted |

The triple-quoted case is the dangerous one: an embedded base64 block parses
without error and yields nonsense.

**`github.com/calico32/kdl-go` v0.15.0 — works.** Documents support for KDL 1.0.0
and 2.0.0 and passing the upstream test suite for each. Verified: raw strings in
both argument and property position, triple-quoted multi-line strings with
dedenting, bare `#true`, `/-` slashdash, `#null`, and `\` line continuation.

It parses the reference template: 91 nodes, `report` carrying 4 properties and 14
children. Semantic checks — the embedded base64 block round-trips to a valid PNG,
raw-string expressions survive exactly, and `detail`'s children come back as
`[style rectangle field field field field line]`, confirming the paint-order
property that decided KDL over TOML.

Caveats: the library's public API is 0.x and documented as unstable, so it is
wrapped behind an internal interface. Its API is also not what a reader guesses —
`Name()`, `Arguments()`, `Properties()`, `Children()` are methods, and
`Children()` returns a `*Document`.

### Font metrics and PDF

**Not yet run.** The remaining technical risk, and the one that can invalidate
output rather than merely cost rework: word wrap is a greedy scan whose line
breaks flip on fractions of a point, so the measuring and rendering paths must
share font tables exactly.

To answer:

- Which of `golang.org/x/image/font/sfnt`, `github.com/go-text/typesetting`, and
  `github.com/tdewolff/canvas` gives advance widths, and which PDF writer can
  embed and subset the same font.
- Whether the chosen library can load a font by explicit path **and** report which
  file it resolved, which the font resolution chain must record in the printout.
- Round-trip: wrap a paragraph, render it, extract glyph positions from the PDF,
  confirm they match the printout.

Complex-script shaping is out of scope for the first version, as it was
in the predecessor. Choosing `go-text/typesetting` would leave the door open.

## Inherited defects, and what replaced them

| PythonReports | `sr` |
|---|---|
| Band taller than the frame overflows silently; one eject retry | Measure before commit; split, or fail with a diagnostic |
| No keep-together | `group keeptogether` |
| No orphan or widow control | `orphans` / `widows` on bands, `minrows` / `mintailrows` on groups |
| Header height estimated from the template box | Header measured; actual height reserved |
| Deferred value substituted without re-measuring; chopped if taller | Re-measured; growth is an error |
| Aggregates retain every value, recomputed per read | Incremental accumulators |
| Expressions re-parsed from source per evaluation | Compiled once at load |
| Geometry overloaded on sign, with an off-by-one | Named edges, two of three |
| `height="-1"` means two different things | `height="auto"` and `bottom=0` |
| `pen` means width or dash depending on parse | `width` and `dash` |
| `printwhen` rides on the matched style | A property of the element |
| `parameter default` is an expression, so `2005-01-01` means 2003 | `type` plus text `default`, with `defaultexpr` for computed ones |
| Every parameter must have a default | No default means the caller must supply one |
| Metrics depend on which library loaded last | One implementation |
| `eval()` and `__import__` in templates; pickle in data | Sandboxed Starlark; no pickle |
| `datetime.now()` in expressions | `BUILD_TIME` |
| `NameError` on `summary swapfooter` | — |
| Frames a single-child chain | Frames a tree |
| Output-time shrink pass alters final geometry | Content box computed during measurement |
| Nested xref marks in xref-relative coordinates | Page coordinates throughout |
| No specification, no engine tests | This documentation set; tests derived from it |

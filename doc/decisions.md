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
- [Printout: a separate format](#printout-a-separate-format)
- [Expressions: Starlark](#expressions-starlark)
- [Layout: measure before commit](#layout-measure-before-commit)
- [Geometry: two of three edges](#geometry-two-of-three-edges)
- [Parameters are typed, and `default` is text](#parameters-are-typed-and-default-is-text)
- [A band height is a minimum](#a-band-height-is-a-minimum)
- [Deferral is per name, not per element](#deferral-is-per-name-not-per-element)
- [Smaller decisions](#smaller-decisions)
- [Spike results](#spike-results)
- [Enumeration and matching are one step](#enumeration-and-matching-are-one-step)
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

A mark that lands outside the page's printable area is the same kind of failure
and gets the same treatment. That case reaches the printout's invariant 7, which
requires every mark inside the margins — and a negative `right` or `bottom`,
which the template format allows, can produce one. The two rules only agree if the
resulting mark is an overflow, so it is. Judging it on the final page coordinates
rather than on the declaration keeps the ordinary use legal: a box reaching past
a container in the middle of the page crosses no margin.

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

## A band height is a minimum

`height=12` on a detail band and `stretch=#true` on a field inside it are both
ordinary things to write, and together they over-constrain: the field's wrapped
text may need 24 points in a band declared 12. Three answers were available.

**Clip.** The band stays 12 and the extra lines are dropped. But `stretch` exists
precisely to say "do not drop lines", so this makes the two properties cancel
each other, and which one wins depends on where the author looks.

**Error.** Honest, and unusable: the first customer name in the data that wraps
to two lines takes the report down. A layout constraint that fails on ordinary
data is not a constraint, it is a bug waiting for a specific record.

**Grow**, which is what `sr` does. A declared `height` is a floor. The band is
as tall as the greater of the declared value and its content, so `height=12` reads
as "12 points unless something needs more", which is what the author almost always
means. `height="auto"` stops being a special case and becomes a floor of zero —
one rule instead of two.

The ability to say "exactly this tall" is not lost. A `field` without `stretch`
already truncates at a line boundary, so a fixed-size band is a band of
non-stretching fields. Nothing has to grow that the author did not ask to grow.

The cost is that a fixed-pitch layout — a pre-printed form, a sheet of labels —
depends on the author leaving `stretch` off rather than on the band height
enforcing it. That is the right place for the decision, since only the author
knows whether an over-long value should spill or be cut.

## Deferral is per name, not per element

`evaltime` exists because some values are not final when the band that shows them
is built: the page count, a group's total. The obvious implementation is to hold
the whole expression until the scope ends and evaluate it there.

That is what the predecessor did, and it breaks the most common use of the feature.
Written whole, `'Page %d of %d'` needs the page number *here* and the page count
*at the end*. Deferred whole, both come from the end, so every page reports the last
page's number. The idiom that does work — an immediate `PAGE_NUMBER` field beside
a deferred one — is two fields, cannot be centred as one string, and has to be
discovered rather than read.

Two ways out were tried, and the first was wrong.

**Rejected: the engine decides.** A deferred expression is evaluated against
a snapshot of where the element sits, and the engine substitutes the names
*the scope makes final* — `PAGE_COUNT` for a page, a group's `_COUNT` for a group,
variables by their `reset`. This works, and it needs a new predefined name for the
page total, since no existing counter means "pages". Calling it `TOTAL_PAGES` exposed
what was wrong with the whole approach: it is an exception, it invites a `TOTAL_` twin
for every future quantity, and it sits badly beside `DATA_COUNT`, which is also a total
and is not named that way. Behind the name was a worse problem — a per-scope table of
which quantities each scope resolves. That table is a list the engine has to keep and
the author has to consult, and it grows with every counter added.

**Adopted: the author decides.** A name reads its value where the element sits;
`FINAL.`*name* reads the value it reaches when the `evaltime` scope ends.
So the expression says which half of itself is deferred:

```
'Page %d of %d' % (PAGE_NUMBER, FINAL.PAGE_NUMBER)
```

There is no name for the page total, because it is not a new quantity — it is
`PAGE_NUMBER` at the end of the report, which is what the user's original instinct
said and what this spells. `FINAL.customer_COUNT`, `FINAL.total_amount`, and
`FINAL.THIS.region` follow from the same form with nothing added, and the per-scope
table is gone: the engine no longer knows what a page total is.

`FINAL` is a namespace rather than a function because a Starlark function's argument
is evaluated before the call, so `final(PAGE_NUMBER)` would receive the value it was
meant to defer. Reading it as an attribute needs no special form in the compiler: the
expression's free names are collected as usual, and `FINAL` is simply the one that is
bound late.

That late binding is nearly free because of [compile-once](#compile-once): each
expression is already a function of exactly the names it references, so the engine
snapshots those and binds `FINAL` at substitution, with no analysis at run time.
The mechanism that made evaluation fast is what makes this affordable.

Requiring the two together — `FINAL` only under an `evaltime`, `evaltime` only with
a `FINAL` — is what makes the feature hard to get wrong. Each alone is a mistake
rather than a harmless no-op, and both are caught at load. Under the rejected design
the original defect, `evaltime="report"` on an expression with no end-of-scope name
in it, was silently a no-op.

## Smaller decisions

**`printwhen` moved from `style` to the element.** In the predecessor it was a
style property, so it was evaluated on whichever style matched. Suppressing an
element under two alternative styles meant repeating the condition on both, and
adding a style alternative could silently change what was visible. This surfaced
while translating the reference template, where a `rectangle` carried a `style`
whose only job was to hold a `printwhen`.

Moving it also fixed the order of work when a band is measured. With `printwhen`
on `style`, the style walk had to run first to discover which `printwhen` applied,
so a suppressed element paid for a resolution that was then thrown away. As an
independent property it is tested first, and everything else — style, geometry,
content — is skipped for anything invisible.

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

**A split must divide content.** Two rules that were each right on their own
combined into a wrong one. A band's declared `height` is a
[minimum](#a-band-height-is-a-minimum), so a band is routinely taller than what
is drawn in it; and a cut is blocked only by the span of the *marks*, not of
the resolved boxes, so that an ordinary band of one-line fields can split at all.
Together they make every offset below a band's content a valid cut — and since
the split branch is tried before the eject branch, and takes the greatest cut
that fits, a 13 mm row with 11 pt of text in a 20 pt gap would split at 20:
all four fields in the head, and a tail of pure whitespace committed on the
next frame, pushing everything after it down.

Ejecting the band whole is better in every case of that shape, so a cut with all the
marks on one side of it is not a legal split point, and the split branch declines it.
Stated symmetrically — marks on both sides — because a cut whose head is empty
relocates a blank strip just as pointlessly.

The last-resort branch, for a band too tall for any frame, gives this up along with
`orphans` and `widows`: there, a blank tail beats not making progress.

**Eject-after-placement applies to a report's `title` only, not to a group's.**
The predecessor's rule was "for `title`, at the end of the section", which reads
as though it covers every band named `title`. For the report title it is right:
an `eject` there means "give the title a page of its own", so it has to fire after
the band is placed. For a group title the same rule makes an `eject` useless — the
title lands at the bottom of the outgoing column and the group's rows start in the
next one, which is the opposite of every reason to write it. The exception is
therefore scoped to the band directly under `layout` or `embedded`. This surfaced
while adding an `eject` to the reference template, which under the wider reading
would have done the reverse of what its comment claimed.

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

**Example fonts are committed and referenced by path.** The examples originally
named typefaces — `"Arial"`, `"Times New Roman"` — which reads well but cannot be
snapshot-tested: under `--strict-fonts`, which is how tests run, resolution stops
at explicitly named files and a typeface-only font fails. An acceptance example that
cannot run under the test configuration is not an acceptance example. The Go fonts
are BSD-3-Clause and redistributable, so Regular and Bold are committed under
`example/fonts` with their licence, and both templates reference them by path.
The typeface path is still reachable — swapping `file=` for `typeface=` is a one-line
change, noted in the template.

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
stretches to the band's bottom edge, so in a band with no declared height
it cannot contribute to the height it is measured against. Such elements are excluded
from the maximum and resolved afterwards. The predecessor handled this with several
special-case branches in its resizeable check.

The exclusion had to be narrowed once the reference template was checked against it.
Read literally, "its `bottom` was derived" catches a stretch field and abarcode too,
since neither declares a height — which collapsed the whole titleband of `sakila.kdl`
to the one element that did. The distinction that matters is not where the bottom edge
came from but whether the element has a height of its own: a stretch field has its
wrapped text, a barcode its symbol, a `grow` image its bitmap. Those three participate;
everything with a derived bottom and no intrinsic height does not.

**`stroke=#false` on a rectangle, rather than `color="none"`.** A background block
wants a fill and no outline. Documenting that as "give the style no `color`" does
not work, because unset style properties fall through outward, so a `color` set
at `layout` level still reaches the rectangle and draws a hairline round it — which
the reference template walked into. A sentinel colour value would put "absent" into
a type whose every other value is a colour. A boolean beside the existing `opaque`
switches the two halves of the drawing independently, and maps directly onto the
printout's already-optional `stroke` and `fill`.

**An inline subreport has no `header` or `footer`.** It shares the parent's pages,
whose header and footer are already reserved, so there is no frame of its own
to attach them to. The tempting alternative is to redefine `header` under `inline`
as "print once at the start", which is what a line-item table wants — but that
makes one node name mean per-page in one context and once-per-invocation in another,
and `title` and `summary` already mean once. So the bands stay per-page, `inline`
rejects them at validation, and the examples use `title`.

**`embed=#false` records a path relative to the printout.** The case for an absolute
path is that a relative one needs a base, and the printout would have to carry one —
a `basedir` field in the header, duplicating the template's. But the printout does
not need to carry it: the printout is a file, and a file has a location. Resolving
against the directory it was read from needs no header field, and cannot drift out
of step with the document the way a stored path could.

It is also what keeps the artifact movable, which is the whole reason the printout
exists as a separate document. An absolute path ties it to the machine that built
it, so archiving one is archiving something that may not render later. Relative
paths let a printout and its images be copied, checked in, or shipped as one tree.

The price is that the recorded path depends on where the printout is written,
so one printout serialized to two directories carries two different values.
Both are correct, which is why the rewrite happens at serialization rather than
at build time — at build time the destination is not known, and may not exist.
Where there is no destination directory, a pipe or an in-memory hand-off,
the working directory stands in.

**Font paths follow the same rule, split by provenance.** A first pass relativized
image paths and left `fonts.resolvedFile` absolute, on the grounds that a font is
a system resource. That is true of a font the engine *found*, and false of one the
template *named*: `font file="../fonts/Go-Bold.ttf"` is a project asset in exactly
the way `image file="logo.png"` is, and the reason to make one travel with the
printout is the reason to make the other.

So the rule is drawn on where the path came from, not on which field holds it.
A template-named path — `image file=` under `embed=#false`, or `font file=` — is
written relative to the printout. A path the engine discovered on the host is written
as it was opened, because a relative path to `C:/Windows/Fonts` would be both
grotesque and machine-specific anyway, and `resolvedFile` doubles as the record
of what was measured, where a diagnostic should say literally what it opened.

A renderer does not have to know any of that. It resolves a relative path against the
printout's directory and uses an absolute one as it stands, and the two cases fall out
without consulting `resolvedBy`. Provenance decides what the *writer* emits;
the *reader* only has to look at the path.

**Writing a printout has no filesystem side effects.** Making paths relative invites
the reading that the engine gathers what they point at — copies the font and the logo
next to the printout so the directory stands alone. It does not. Writing a printout
creates one file. A relative path reaching up out of that directory is the ordinary
case, and the tree the paths span is normally the project rather than the output
directory.

The alternative was considered and rejected for two reasons. A build step that writes
files nobody named is a surprise, and one that writes them next to a document it is
also writing has to decide what to do when a file of that name is already there —
overwrite, skip, or fail, none of them obviously right. And it would be redundant work
in the common case: the font is already reachable, which is exactly why a relative path
to it can be written at all. An asset the printout can already open is referenced,
not duplicated.

That leaves self-containment to the mechanisms that already provide it and make it
explicit: `embed=#true` for an image, a `data` node for a font. Both put bytes *into*
the document, which is a different operation from putting files beside it, and both
already deduplicate — two images from one source share one `data` entry.

The payoff is a property worth having: `--strict-fonts` stops resolution at step 1,
so every font in a strict printout is `explicit`, so every path in it is relative. A
strict build with `--build-time` fixed is byte-identical **across machines**, not just
across runs on one — which is what made the reproducibility claim worth making in the
first place.

**A character the resolved font lacks is a warning, not an error.** The `.notdef`
glyph is itself visible failure — an empty box on the page — and metrics are
unaffected, so nothing silently shifts. Erroring would make a report fail on a
single unusual character in one record, which is the same objection that made
[band overflow](#a-band-height-is-a-minimum) a growth rule rather than an error.
The warning is recorded in the printout header, so it is diagnosable from the
artifact.

**A name that collides with a predefined one is rejected.** Resolution puts
predefined names, modules, and builtins first, so a variable called `PAGE_NUMBER`
would simply be unreachable — a template that looks like it works and does not.
Group names are checked against their derived names too: a group called `PAGE`
would produce a `PAGE_COUNT` that already exists.

**`format` means two different things, and keeps one name.** On a `parameter`
or a `column` it is a date parsing layout; on a `field` or a `barcode` it is a `%`
format specification. They never appear on the same node and each reads naturally
in place, so renaming one to `parse` was judged more confusing than the overload —
but validation has to scope its rules to the node type, and an early draft of the
validation list did not, forbidding `format` on precisely the nodes that use it
most.

**The zero time is false.** Starlark's time value is falsy when it is Go's zero
time, which the engine inherits rather than overriding: it makes
`printwhen="return_date"` mean "there is a return date", the test templates
actually want to write. A stored timestamp that genuinely is 0001-01-01 is
indistinguishable from absent, so `printwhen="return_date != None"` is documented
for the case where that matters.

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
Each of the three overturned something that had been assumed.

### Starlark

`go.starlark.net` at `v0.0.0-20260708150628-5395d018f003`.

String interpolation has no width or precision — the assumption that failed:

```
"%.2f" % 3.14159         → error: unknown conversion %.
"%5.1f" % 3.14159        → error: unknown conversion %5
"%05d" % 42              → error: unknown conversion %0
"%-5d" % 42              → error: unknown conversion %-
"%+d" % 42               → error: unknown conversion %+
"{:.2f}".format(3.14159) → error: format spec features not supported
                             in replacement fields: .2f
"%s, %s" % ("a", "b")    → "a, b"
"%d of %d" % (3, 7)      → "3 of 7"
"%d" % 42                → "42"
"%i" % 42                → "42"
"%x" % 255               → "ff"
"%c" % 65                → "A"
"%s" % ("a", "b")        → error: too many arguments for format string
```

Every flag form fails, not only precision. Plain conversions with
several arguments work, which is why `'Page %d of %d' % (…)` is fine
and `'#%05d' % n` is not — a distinction the reference template got wrong
before this was checked.

`#` begins a comment, so `printwhen="#false"` is an **empty expression**, not a
boolean:

```
#false → error: got end of file, want primary expression
False  → False
```

That is a KDL-to-Starlark trap worth naming: `#false` is how KDL spells false, and
it is invalid in every property whose value is an expression. Both examples had one.

Other confirmed behaviour:

- Integers are arbitrary precision: `123456789012345678901234567890 + 1` is exact.
- No `**` operator; `2 * 10 ** 3` is a syntax error.
- No `round` builtin; `math.round` exists.
- `set` is a **dialect option**, `resolve.AllowSet`. It defaults to `true`
  in the pinned version, but with it false `set([1,2])` fails with "this Starlark
  dialect does not support sets". `calc="set"` depends on it, so the engine sets it
  explicitly rather than inheriting a default that a host or a version bump could
  change. `resolve.AllowRecursion` and `resolve.AllowGlobalReassign` both default
  to false and are left there.
- `time.now` exists and had to be removed from the environment.
- `time.format` takes Go reference-time layouts:
  `time.time(year=2005, month=5, day=24).format("02.01.2006")` → `"24.05.2005"`.
- `starlarkstruct` gives attribute access, so `customer.last_name` works. An empty
  struct is truthy, so record truth had to be defined as "not `None`".
- Floats stringify at full precision: `"%s" % (1.0/3)` → `"0.3333333333333333"`.
- **A zero time is falsy.** `bool(time.time(year=1, month=1, day=1))` → `False`,
  while `bool(time.from_timestamp(0))` — the Unix epoch — is `True`. Only Go's
  zero time is false. See [the note above](#smaller-decisions).
- Subtracting two times yields a duration, with `.hours` and the rest; the module
  supplies `time.hour` and friends as duration constants.
- Method sets, enumerated with `dir()` rather than assumed. `set` has **no**
  `update`, though it does support `|` `&` `-`. Freezing a value makes its mutating
  methods fail — `xs.append(2)` on a frozen list errors, `xs.index(1)` does not —
  which is how records and accumulators are protected from an expression that
  writes to them.
- Comprehensions, conditional expressions, and slicing all work, so the reference
  template's `', '.join([format('#%05d', n) for n in invoice_nos])` is valid.

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
dedenting, bare `#true`, `/-` slashdash on both a node and a single property,
`#null`, and `\` line continuation.

**Parse with the version pinned.** Its default is `VersionAuto`, and auto-detection
misreports: given a genuine v2 syntax error late in a document, it concluded the
document was v1 and reported a bogus error on the first `#true`, 190 lines earlier.
With `WithVersion(Version2)` the real error was named correctly. Since the format
*is* v2, there is nothing to detect — pinning it costs an option and removes
a class of misleading diagnostic.

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

Pinned at `github.com/tdewolff/font v0.0.0-20260424075104-b5eeb1e23189`,
`github.com/tdewolff/canvas v0.0.0-20260803134256-8e86b9abb917`,
`github.com/go-pdf/fpdf v0.9.0`, `github.com/signintech/gopdf v0.38.0`,
`golang.org/x/image v0.44.0`, `github.com/go-text/typesetting v0.3.4`.

Measured against `example/fonts/Go-Regular.ttf` at 8 pt — sakila's body size —
and against `arial.ttf`, which unlike the committed fonts has a `kern` table.

**The metrics source: `go-text/typesetting`.** All three candidates read the same
tables and, asked at a ppem equal to the font's units per em, agree exactly on
every glyph present in both fonts. That is the sanity check. It is also as far as
an ASCII probe set gets you, and the thing that decided this was found outside it.

**`tdewolff/font` returns the wrong glyph for Latin-1 characters in some fonts.**
Not a wrong width for the right glyph — the wrong glyph. In `verdana.ttf`:

| | `ä` | `é` | `ß` | `¤` | `§` |
|---|---|---|---|---|---|
| `x/image/font/sfnt` | 108 | 112 | 137 | 892 | 134 |
| `go-text/typesetting` | 108 | 112 | 137 | 892 | 134 |
| `tdewolff/font` | **197** | **202** | **192** | **134** | **137** |

Glyph 197 is `‰`. Over `"äéß¤§ café"` at 10 pt the string measures 55.0830 pt by
the first two and 64.9268 pt by the third — **17.9% wrong**, and set with the wrong
characters.

The mechanism is the `cmap` subtable choice. Verdana and Georgia carry
`(1,0)fmt0 (3,1)fmt4` — a 256-entry Macintosh table and a Windows Unicode table —
and `tdewolff` reads the Macintosh one for code points below U+0100, where the
byte values mean MacRoman rather than Latin-1. Arial (`(0,3) (1,0) (3,1) (3,10)`)
and Segoe UI (`(0,3) (3,1)`) both have a `(0,x)` Unicode subtable, it is preferred,
and they come out correct. So the defect appears exactly when a font has a
Macintosh subtable and no platform-0 subtable, which Verdana and Georgia are
two very common instances of.

Over U+00A0–U+2FFF, `go-text` and `sfnt` agree on **every** character in all four
fonts tested — 0 disagreements — while `tdewolff` differs on 91 in Verdana and 91
in Georgia. Two independent implementations agreeing against the third is as close
to an oracle as this gets. Separately, `tdewolff` maps the 32 C1 controls
U+0080–U+009F to real glyphs where the others return `.notdef`; harmless,
and the same root cause.

`go-text/typesetting` is therefore the metrics source. It returns font units from
`Face.HorizontalAdvance`, its `cmap` handling matches `sfnt` exactly, and it leaves
shaping available if that is ever wanted. `tdewolff/font` keeps a narrower role —
`SFNT.Subset` and table access — and must not be asked to map a rune to a glyph.
Since the chosen writer embeds and subsets on its own, that role may turn out
to be empty.

This is the second defect in this spike that an ASCII test cannot see, and it is
the same shape as the kerning one below: the first probe set here ran
`A V W i l m o y . , 0 9 % ’ — € Ж` and found perfect agreement, because it
contained nothing in U+00A0–U+00FF. Both reference templates are ASCII too. So
the rule for the test suite is not "test a font" but **test a range**: Latin-1
supplement and General Punctuation at minimum, against a font with a Macintosh
`cmap` subtable, on top of the `kern`-bearing font the kerning finding already
called for.

Everything below this point was measured with `tdewolff/font` before the defect was
known. None of it moves: the sample text is ASCII throughout, and in Go Regular,
Go Bold and Arial the disagreement is confined to the 32 C1 controls, which no
sample contains. The substitute bound table was affected and has been recomputed.

**On the wrap sweep, which does not disqualify anything.** `sfnt`'s only advance
API is `GlyphAdvance(buf, gid, ppem, hinting)`, returning 26.6 fixed point at the
requested ppem. Passing the text size — the obvious thing to do — quantises to
1/64 pt per glyph, +0.0078 pt on `A` at 8 pt, 0.15%. Sweeping five paragraphs
against box widths from 30 to 400 pt in 0.05 pt steps, at five sizes:

| size | wraps tested | different breaks | **different line count** |
|---|---|---|---|
| 7 pt | 37,000 | 177 (0.48%) | 50 (0.135%) |
| 8 pt | 37,000 | 193 (0.52%) | 53 (0.143%) |
| 9 pt | 37,000 | 166 (0.45%) | 46 (0.124%) |
| 10 pt | 37,000 | 62 (0.17%) | 14 (0.038%) |
| 12 pt | 37,000 | 111 (0.30%) | 26 (0.070%) |

7,400 box widths per paragraph, five paragraphs. A different word on a line is
cosmetic; a different line count changes the band's height, which changes what
fits in the frame, which changes the pagination of everything after it.

But this is a hazard of the obvious call, not a property of the library. Asking at
`ppem == unitsPerEm` makes the fixed-point result the font unit exactly — that is
how the agreement above was measured — and the caller then does the same single
multiply the other two need. So `sfnt` can be exact, and is ruled out only on
scope: no subsetting, and less of the table access the resolution chain wants than
`go-text` offers. What the sweep is worth recording for is the trap: an advance API
that takes a size looks like the one to use, and using it that way silently repaginates
the document.

**Kerning is a decision, and it has to be the same decision on both sides.**
This is the finding that mattered most, because it does not show up in the
committed fonts. The Go faces have neither a `kern` nor a `GPOS` table,
so shaping and a plain `hmtx` sum agree to within fixed-point noise.
Arial has a `kern` table, and then:

| text, Arial 8 pt | `hmtx` sum | HarfBuzz | `canvas` |
|---|---|---|---|
| `"To"` | 9.33594 | 8.46875 | 8.44922 |
| `"AV"` | 10.67188 | 10.09375 | 10.07812 |
| `"AWAY"` | 23.55859 | 22.39062 | 22.37109 |

Nearly 10% on `"To"`. And the two shaping paths do not agree with each other
either, since they round glyph positions differently.

The divergence is also **sparse and content-dependent**, which makes it worse
than a constant offset. Arial kerns uppercase and punctuation pairs and leaves
most lowercase pairs alone, so the sakila summary sentence measures identically
under both. A kerning-heavy line does not:

| line, Arial 8 pt | printout says | `canvas` measured | `canvas` drew | drawn delta |
|---|---|---|---|---|
| `"TAVATTA WAVY TOWN Yorick."` | 115.1172 | 109.5039 | 109.6320 | **−5.49 pt** |

Two things follow. First, `sr` does not kern, matching the predecessor, and the
renderer must be *told* so rather than left on its default —
`canvas`'s `SetFeatures("kern=0")` (also spelled `-kern` or `kern off`,
all three parse) brings it back to exact agreement, 0.00000 pt on every line
tested. Second, **no test using only the committed fonts can catch a kerning
mismatch.** A round-trip test against a `kern`-bearing font belongs in the
suite, and since it cannot be a committed desktop font, it needs a small
purpose-built face.

Complex-script shaping stays out of scope, as it was in the predecessor. The
performance figures below are a second reason: shaping is not a free upgrade
path.

**The writer: `go-pdf/fpdf`.** All three embed the font and subset it. They do
not agree about how wide the text they just drew is:

| | own measurement vs. exact | `"a中b"`, missing glyph | subset tag | output, Go-Regular |
|---|---|---|---|---|
| `signintech/gopdf` | **−0.07 … −0.14 pt per line** | **11.1120** (ours 14.8984) | absent | 8.9 kB |
| `go-pdf/fpdf` | −0.013 … +0.011 pt | 14.8960 | absent | 11.5 kB |
| `tdewolff/canvas` | 0, with `kern=0` | 14.8984 | `SUBSET+GoRegular` | 7.1 kB |

`gopdf` is rejected on both columns. It truncates rather than rounds when
converting advances to PDF's 1/1000 em units, so its `/W` array is up to
0.99/1000 em short per glyph and a drawn line comes out about a tenth of a point
narrower than the printout says — roughly ten times the floor the other two
reach. Worse, it drops a character the font has no glyph for instead of measuring
`.notdef`, which the specification requires it to keep
([Missing glyphs](template.md#missing-glyphs)); on a three-character string that
is a 25% width error, and it reports no error while doing it.

`canvas` is correct once kerning is off, produces the smallest and most
conformant output, and has `AddAnchor` / `AddLink` / `AddOutline` matching the
printout's `xref` and `outline` marks, plus a general path renderer that gives
rounded rectangles and dash patterns for free. It was still not chosen:

- Its coordinate space is millimetres throughout — the canvas size, the
  renderer's page-size argument, drawing coordinates, and `FontFace.TextWidth`'s
  return value, though `Font.Face(size)` takes its size in points — and every
  page it writes opens with a `2.8346457 0 0 2.8346457 0 0 cm` matrix to get
  back to PDF units. The printout is in points.
- Its text path shapes, and shaping is 59× slower than an `hmtx` sum
  (see below). Handing a renderer lines that are already wrapped and having it
  re-shape them is work that buys nothing.
- It pulls in a large dependency tree — a TeX engine, a Markdown parser, Brotli,
  a triangulation library — none of which a report renderer needs.

`fpdf` measures by summing per-rune widths with no shaping, so there is no
kerning to configure away, and it has `RoundedRect`, `SetDashPattern`, `Image`,
`Link` / `SetLink` / `LinkString` and `Bookmark`, covering the printout's mark
set.

It has two defects, and calling them cosmetic would be wrong: it omits the
six-character tag PDF requires on a subset `BaseFont`, which is a conformance
violation that PDF/A validators flag, and it names the font from the family string
the caller passed rather than the face's PostScript name. The reason they do not
decide against it is narrower than harmlessness — neither affects the round-trip
test or reproducibility, since both read glyph positions and `/W` rather than the
`BaseFont` string. `canvas` emits `SUBSET+GoRegular` correctly and is the better
citizen here.

**The round trip works, and the position error is PDF's own, not the writer's.**
Reading the generated files back and replaying the text operators:

| | line start x | baseline y | line width | worst per-glyph x |
|---|---|---|---|---|
| `fpdf` | exact | exact | −0.013 … +0.011 pt | +0.012 pt |
| `canvas`, `kern=0` | exact | exact | +0.002 … +0.011 pt | +0.012 pt |
| `canvas`, kerning on | exact | exact | −5.49 … −1.45 pt | −5.49 pt |

Line starts and baselines are exact — the writers put the text where they were
told. The residual *width* error is the same for both surviving writers and
belongs to the format, not to them: a PDF advances the pen inside a shown string
from the font dictionary's `/W` array, which is in 1/1000 em, and a
2048-unit-per-em font does not divide into that. Worst deviation between `/W` and
the original `hmtx`, over the glyphs actually used, was 0.24/1000 em with `fpdf`
and 0.17/1000 em with `canvas` — 0.002 pt and 0.001 pt at 8 pt. The worst
accumulation actually measured across a line was 0.012 pt; it is not a per-glyph
figure times a glyph count, since the per-glyph errors carry opposite signs and
partly cancel.

That is the floor on how closely a rendered line can match the printout. Unlike
a measurement error it cannot change a line break, because by then the lines are
fixed: the printout carries them already wrapped, and a renderer that re-wraps is
in violation ([printout.md](printout.md#text)). It shows only as
hundredth-of-a-point drift where a renderer positions from a line's width,
which is right-aligned and justified text.

**Extraction needs a purpose-built reader.** `rsc.io/pdf` cannot do this job:
for an `Identity-H` composite font — which is what all three writers emit — it
returns the raw two-byte CIDs as separate characters and reports every one as
zero width. The round trip above uses a content-stream reader written for the
spike: tokenise the stream, track `cm` / `Tm` / `Td` / `TD` / `T*` / `Tf` /
`Tc` / `Tw` / `Tz`, replay `Tj` and `TJ`, and map CIDs back to runes through
the `ToUnicode` CMap. **Stage 2 has to reimplement this as a test helper**;
it is about 400 lines and it is the only way the round-trip test in the
verification plan can exist.

One thing that reader must not do is parse the embedded subset as a font.
A `CIDFontType2` with `Identity-H` and an identity `CIDToGIDMap` needs no `cmap`
table, and none of the three writers emits one, so `tdewolff/font` rejects all
three subsets with `cmap: missing table`. That is correct on both sides.
The test compares `/W` against the *original* file.

**Font resolution must be ours.** `tdewolff/font` ships `FindSystemFonts`
and `SystemFonts.Match(name, style)`, which looked like
[host enumeration](template.md#host-enumeration) for free. It is not usable:

```
Match("Arial", Regular) -> C:\Windows\Fonts\ariblk.ttf     # Arial Black
```

The scan records `ariblk.ttf` under family `Arial`, style `Regular`, and since
it sorts after `arial.ttf` in the directory it overwrites the real entry — so
plain Arial becomes unreachable and every template asking for Arial gets Arial
Black. The mechanism is a filter that reads

```go
if platform != PlatformWindows && (language&0x00FF) != 0x0009 { continue }
```

where `||` was meant. Every Windows-platform name record passes regardless
of language, so the loop over name IDs 1/2/16/17 ends on whichever *localised*
subfamily string comes last in the file. For `ariblk.ttf` that is Portuguese
`"Normal"` at language `0x0c0c`, which parses as `Regular`. The result depends
on the order of localised names inside a font file, which is not something the
engine can control or predict.

Three narrower findings from the same test:

- Matching is case-sensitive: `Arial` hits, `arial` misses.
- `Helvetica`, `Times`, `Courier` and `Go` all miss, which confirms step 2
  is needed and that it is an **alias** table (`Helvetica` → `Arial`),
  not merely typeface-to-filename.
- It scans directories and does not read the Windows registry. That is the first
  of the two observations that later merged those into
  [one step](#enumeration-and-matching-are-one-step); the registry keeps its value
  for fonts installed by reference rather than by copy, and reading it is ours
  to write either way.

A fourth, and on its own disqualifying: the scan **matches the file extension
case-sensitively**, against `".ttf"` and `".otf"` only.

```go
switch filepath.Ext(path) {
case ".ttf", ".otf":
    getMetadata = getSFNTMetadata
    // TODO: handle .ttc, .woff, .woff2, .eot
}
```

`C:\Windows\Fonts` on this host holds 165 files named `*.ttf`, **234 named
`*.TTF`**, and 15 `*.ttc`. Only the first group is looked at, so 60% of the
installed fonts — which on a stock Windows install is most of the ones
a template would name, the faces that arrive with Office — are invisible,
along with every collection. Every failure in this path is swallowed with
`return nil`, so nothing says so: 414 files go in and 160 `(family, style)`
entries come out, with no error.

What is worth borrowing is `DefaultFontDirs()` — `C:\Windows\Fonts` and
`%LOCALAPPDATA%\Microsoft\Windows\Fonts` on this host, and the equivalents
elsewhere. Scanning is cheap enough that the chain needs no cache: 11–19 ms
for the files it does look at. `SystemFonts.Save` / `LoadSystemFonts` exist,
but reloading the resulting 10 kB file took 9 ms, which is no faster than
rescanning.

**The substitute face has two jobs, and `cour.ttf` was chosen for the first.**
The last resort has to be *found* — a name that is present on essentially every
host, which `cour.ttf` is on Windows — and it should be *wide*, so that text
overflows visibly rather than overlapping silently. Availability was the
predecessor's main criterion and it is the harder of the two, since a face
that is not there cannot be too narrow.

Measured, the width half is not achievable as stated, and the attempt to fix it
by picking a wider face fails in an instructive way.

**No face guarantees overflow.** That would require the substitute to be at least
as wide as the replaced face for every glyph, and nothing is. What can be asked
of a candidate is a *bound*: the worst ratio of substitute width to replaced
width, over the text it might be asked to set. That is its narrowest glyph divided
by the target's widest — and **the bound is only as good as the character range
quoted with it**, which a first pass at this left unsaid. Over ten desktop faces:

| admitted range | widest glyph in it | `cour.ttf` | `arial.ttf` |
|---|---|---|---|
| printable ASCII | 1.0762 em — Verdana `%` | 44% narrow | 82% narrow |
| Latin-1 | 1.0762 em — Verdana `%` | 44% narrow | 82% narrow |
| Latin-1 + punctuation, currency | 1.8027 em — Tahoma `‱` | 67% narrow | **100% narrow** |
| everything, spacing glyphs | 1.9941 em — Segoe UI `⸻` | 70% narrow | **100% narrow** |
| everything | as above | **100% narrow** | **100% narrow** |

Measured with `go-text`, after the `cmap` defect above invalidated the first run of
this table. The 44% figure quoted earlier was ASCII-only and said so nowhere; it
happens to survive to the end of Latin-1, and then does not. Widening the range
costs half of it: `‰` and `‱` are ordinary in a financial report, Roman numerals
(Times' `Ⅷ` at 1.6733 em) in a legal one, and two- and three-em dashes exist
precisely in order to be wide.

Admit *everything* and every candidate reaches zero, because **no real face is
uniform over a whole `cmap` and none should be** — a combining acute must not
advance the pen. Arial has 314 zero-advance glyphs, Courier New picks one up at
U+200C, and the Adwaita Mono that Linux's fontconfig hands back has them at U+055F
and U+200B. Zero advance is correct typography and fatal to a bound, so the bound
has to be quoted over spacing glyphs at most.

Within that, the gap is structural rather than marginal. Over spacing glyphs
Courier New and DejaVu Sans Mono hold 0.30 while Verdana falls to 0.082 and Arial
to 0 — a uniform advance has no narrow glyphs for the ratio to collapse on. A
proportional face is better only *on average*.

An average is what a first pass at this measured, and it misled. Ranking
candidates by their worst *string* ratio over seven strings and six target faces
put `verdana.ttf` at 0.999× and `DejaVuSans.ttf` at 0.971× against `cour.ttf` at
0.755×, which reads as a clear win for proportional. Widening the net to
seventeen strings and twelve faces reverses it. A ranking that flips with the
sample is not a ranking, and neither sample is privileged: what the template
asked for and what the data says are both unknown when the substitute is chosen.
Only the bound is sample-independent, which is why it is the figure to use.

Go Mono scores identically to Courier New, because the score belongs to the 0.6 em
advance rather than to the face — so embedding a monospaced face in the binary buys
availability, not width.

**Coverage is the third criterion, and it outranks the second.** A last resort
that lacks the character prints `.notdef`, which is the visible failure this section
is trying to arrange — except that it arrives one glyph at a time and for the wrong
reason. Counting spacing glyphs a face actually has:

| candidate | advance | spacing glyphs | bound over ASCII |
|---|---|---|---|
| `DejaVuSansMono.ttf` | 0.6021 em | 3159 | 44% narrow |
| `cour.ttf` | 0.6001 em | 2883 | 44% narrow |
| `consola.ttf` | 0.5498 em | 2343 | 49% narrow |
| `lucon.ttf` | 0.6025 em | **644** | 44% narrow |

That reverses the Windows fallback. Lucida Console was picked as the second
candidate for being 0.4% wider than Courier New; it has **less than a quarter** of
its coverage. Consolas is 8.7% narrower — five points of bound — for 3.6× the
glyphs, and 0.4% of width against 1,700 characters is not a trade worth making. The
second Windows candidate is `consola.ttf`.

Courier New stays first on all three criteria at once: on every Windows host,
uniform, and 2,883 glyphs.

**The decision: keep monospace, and give it company on the other two platforms.**
`cour.ttf` stands, for the reason it was originally chosen — it is on every
Windows host — with the width argument restated as a bound rather than a promise.
What it needs is a per-platform list, because Courier New reaches Linux only
through `ttf-mscorefonts-installer`, which is a licensing step rather than
a default, so on a plain Linux host the last resort previously found nothing
and the chain did not say what happened next. It now names candidates per platform
and fails with a diagnostic if none is found; see
[the substitute face](template.md#the-substitute-face).

**Checking that on Linux changed the Linux answer, and collapsed two steps of the
chain into one.**
The host was Arch under WSL, with fontconfig 2.17.1. It has **six font files, all
Adwaita** — GNOME's current default — and therefore no DejaVu, no Liberation and
no Noto. A last resort that named those three filenames would have found nothing,
and the failure would have been the one the new error path reports rather than a
substitute.

That is not an Arch quirk. Distributions agree on neither the filenames nor the
directories, and this one keeps its fonts in `/usr/share/fonts/Adwaita` with
`/usr/local/share/fonts` and both per-user directories absent. Naming files is
a Windows technique. So Linux asks fontconfig for the generic family `monospace`,
which is the platform's own answer to the question the last resort is asking, and
which returned `AdwaitaMono-Regular.ttf` on this host.

Measured, that face vindicates the bound argument and makes the candidate lists
matter less than expected:

| face | advance | bound | worst case |
|---|---|---|---|
| Adwaita Mono (Arch) | 0.6000 em | 0.558 | 44% narrow |
| Courier New (Windows) | 0.6001 em | 0.558 | 44% narrow |
| DejaVu Sans Mono | 0.6021 em | 0.559 | 44% narrow |
| Lucida Console (Windows) | 0.6025 em | 0.560 | 44% narrow |
| Adwaita Sans (Arch, proportional) | 0.2422 em | 0.225 | 77% narrow |

Four unrelated monospaced faces land within 0.0025 em of 0.6, so **the bound
does not depend on which one is found** — only on its being monospaced.
The proportional face from the same package sits at 77%, in line with every other
proportional candidate. That makes the exact contents of the per-platform lists
a detail and the monospace requirement the substantive rule.

Which is why the engine checks it. Asking fontconfig for `monospace` hands the
choice to the host, and the bound is the only reason this step prefers one face
to another, so the one property worth verifying is the one being relied on.
On the host above, fontconfig answered `sans-serif` with a *monospaced* face;
a configuration that returns the reverse is no less possible. The check warns
rather than fails: the substitute path is already the one where output is not
to be trusted, and a second-guessed guess is still better than an error
on a report the author may not care about.

The check needs a stated range, because a naive one fails on the very faces this
section recommends. Over a full `cmap` nothing is uniform: Lucida Console's `€`
is 0.6030 em against 0.6025 everywhere else, and Adwaita Mono and Consolas both carry
zero-advance glyphs. Over **Latin-1, spacing glyphs only**, every candidate named
here is uniform to the last unit — Lucida Console's outlier is `€` at U+20AC, just
outside — and a proportional face is caught immediately, since Arial's `'` is 0.19 em
against `%` at 1.02. So that is the range, with equality, not a tolerance.

**`fc-match` never reports a miss.** On this host it returned Adwaita Mono for
`Helvetica`, `Arial`, `Times New Roman`, `serif`, `sans-serif`, and for the
invented family `NoSuchFaceXYZ`. The chain used to say its third step was
"fontconfig on Linux"; had that meant letting fontconfig perform the match,
every typeface would have resolved there, the later steps would have been unreachable
on Linux, and the printout would have recorded a *found* face for what is really
a fallback — quietly answering a request for Helvetica with a monospaced face and
calling it a match. That is precisely the silent substitution the `resolvedBy`
field exists to expose. The rule is now explicit: the engine enumerates and matches
by family itself, and may not delegate the match to a platform matcher that answers
everything.

The same behaviour is exactly right for the last resort, where a guess is what is
wanted, and it is what Linux now uses there. One mechanism, unusable in one place
and ideal in the other, worth knowing before either gets written.

The macOS list is still unverified — no host to check it on — and is the point
at which `.ttc` support matters, since `Menlo.ttc` is a collection.

**Measurement is cheap; shaping is not.** Wrapping a 95-character paragraph into
three lines calls the width function 21 times:

| | per wrap |
|---|---|
| `hmtx` sum, no cache | 2.61 µs |
| `hmtx` sum, rune → advance cache | 2.34 µs |
| `canvas` shaping, `kern=0` | **154.14 µs** |

A rune cache is barely worth having, because the cost is per string rather than
per rune. Shaping is 59× slower, which at 100k rows with two stretch fields each
would put half a minute into measurement alone. This is the second reason not to
let a shaping renderer re-measure what the printout already settled.

**Leading is still unspecified.** [printout.md](printout.md#text) shows
`leading: 10.8` for a 9 pt font, which is 1.2 × size, but nothing states the
rule. The Go faces suggest a tighter value: at 8 pt, `hhea`
ascent + descent + lineGap is 9.2461 pt, or 1.1558 × size, and both `OS/2`
metric pairs give the same number. The choice is between a constant multiplier,
which is predictable and font-independent, and the font's own suggestion, which
looks right per face but makes line spacing change when a font is substituted.
Not decided here.

## Enumeration and matching are one step

The chain originally had five steps, with "OS font enumeration" and "a scan of
known font directories" as separate rungs and `resolvedBy` recording which one
answered. Two measurements collapsed them.

On Windows, the library that was meant to supply the enumeration step scans
directories and never touches the registry, so the two rungs were already
the same code path. On Linux, fontconfig's font list *is* the directory scan — its
configured directories are where the scan would look, and on the Arch host above
they were the single `/usr/share/fonts/Adwaita`. Neither platform has a case where
a face is found by one rung and not the other, except one that runs the other way
round: the Windows registry can name a font installed by reference, outside the
font directories, which a scan misses. That argues for reading the registry as
an additional *source*, not for keeping a separate step.

So there is one step, `host`, fed by every source the platform offers, and the
distinction the printout used to draw between `os` and `scan` is gone. It was
recording which internal mechanism fired, which is not a fact about the document:
`resolvedFile` already says what was opened, and that is the diagnostic anyone
actually wants. `resolvedBy` now has four values — `explicit`, `table`, `host`,
`substitute` — and each one now corresponds to something a reader might act on.
The one that matters is still `substitute`.

The honest consequence, recorded in the reference documentation rather than
buried: a 44% bound means text in a substituted font can still overlap.
The signal that a substitute was used is `resolvedBy: "substitute"` in the
printout header and the accompanying warning — machine-readable, unambiguous,
and available whatever the geometry does. The predecessor needed geometry as
the signal because it had no such output. `sr` does, so the geometry is a
belt-and-braces measure and is described as one.

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

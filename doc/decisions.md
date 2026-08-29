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
- [What building the engine settled](#what-building-the-engine-settled)
- [What building the renderer settled](#what-building-the-renderer-settled)
- [What building the command line settled](#what-building-the-command-line-settled)
- [What building subreports settled](#what-building-subreports-settled)

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
   known before any record is read. The member list is that set.

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
Read literally, "its `bottom` was derived" catches a stretch field and a barcode too,
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
or a `member` it is a date parsing layout; on a `field` or a `barcode` it is a `%`
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
- `Helvetica`, `Times`, `Courier` and `Go` all miss, which confirms the built-in
  table is needed and that it is an **alias** table (`Helvetica` → `Arial`),
  not typeface-to-filename. On macOS all three of those families are real,
  which is why the alias table ended up [after host enumeration rather than
  before](#macos-moves-the-alias-table) and not merely renamed.
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
and the face set quoted with it**, which a first pass at this left unsaid.
The widest glyph in each row below was found by searching twelve desktop faces:
Arial, Times New Roman, Verdana, Tahoma, Georgia, Segoe UI, Calibri, Trebuchet MS,
Comic Sans MS, Courier New, Consolas, Lucida Console. Within a face, only glyphs
that advance the pen are counted — a *spacing* glyph, as against one with zero
advance, which every row excludes for the reason below.

| admitted range | widest spacing glyph in it | `cour.ttf` | `arial.ttf` |
|---|---|---|---|
| printable ASCII | 1.0762 em — Verdana `%` | 44% narrow | 82% narrow |
| Latin-1 | 1.0869 em — Comic Sans MS `Æ` | 45% narrow | 82% narrow |
| Latin-1 + punctuation, currency | 1.8027 em — Tahoma `‱` | 67% narrow | 95% narrow |
| everything | 1.9941 em — Segoe UI `⸻` | 70% narrow | 96% narrow |

Measured with `go-text`, after the `cmap` defect above invalidated the first run
of this table. The 44% figure quoted earlier was ASCII-only and said so nowhere,
and it does not quite reach the end of Latin-1 — Comic Sans MS has a wider `Æ` there
than Verdana's `%`. Widening the range costs half of it: `‰` and `‱` are ordinary
in a financial report, Roman numerals (Times' `Ⅷ` at 1.6733 em) in a legal one,
and two- and three-em dashes exist precisely in order to be wide.

Which is why the face set is named too. Drop Comic Sans MS and the Latin-1 row
reads 44% again — the same sensitivity to the sample that the objection below
raises against ranking by strings, and no less real here. What does not move
is the ordering.

**Advance means `hmtx` throughout.** The engine sums `hmtx` and does not shape,
so that is the only notion of advance it has, and the bound is computed in it.
Every row above excludes zero-advance glyphs, because admitting them sends any
face that has one straight to 100% and measures nothing: Arial has 314, and 283
of them are combining or enclosing marks it is correct to give no advance.

That generalises badly, and the substitute candidates are where it breaks. Courier
New and DejaVu Sans Mono give a combining acute a full 0.6 em advance, so by `hmtx`
they are uniform across their whole `cmap` — arguable typography, but it is what
the tables say. Consolas has six zero-advance glyphs and they are format characters
rather than marks: U+000D, U+200C–U+200F and U+FEFF. Lucida Console has none and is
still not uniform, because its `€` is 0.6030 em against 0.6025 everywhere else.
Three faces, three different reasons, which is why the range belongs in the rule
rather than in a caveat on it.

Within that, the gap is structural rather than marginal. Over spacing glyphs
Courier New and DejaVu Sans Mono hold 0.30 while Verdana falls to 0.082 and Arial
to 0.042 — a uniform advance has no narrow glyphs for the ratio to collapse on. A
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
reason. Counting the characters a face covers — code points in the BMP, excluding
Unicode non-spacing marks, enclosing marks and format characters, since none
of those is a character a report sets on its own:

| candidate | advance | characters covered | bound over ASCII |
|---|---|---|---|
| `DejaVuSansMono.ttf` | 0.6021 em | 3159 | 44% narrow |
| `cour.ttf` | 0.6001 em | 2883 | 44% narrow |
| `consola.ttf` | 0.5498 em | 2343 | 49% narrow |
| `lucon.ttf` | 0.6025 em | **644** | 44% narrow |

The rule is quoted because it has to be. Courier New is 2883 under it,
3085 counting distinct glyphs, and 3180 counting either `cmap` entries
or glyphs with a non-zero `hmtx` advance — three defensible readings
of "coverage", 300 apart.

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
matter less than expected. Bounds over printable ASCII, against the same 1.0762 em
that row of the table above uses:

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

The check needs a stated range, because a naive one fails on two of the three faces
this section recommends, and fails differently on each. Over a full `cmap` Lucida
Console's `€` is 0.6030 em against 0.6025 everywhere else, while Consolas is uniform
in its spacing glyphs and carries six zero-advance format characters; Adwaita Mono
has zero-advance glyphs at U+055F and U+200B. Courier New and DejaVu Sans Mono pass
a naive check, which is luck rather than a property to rely on. Over **Latin-1,
spacing glyphs only**, every candidate named here is uniform to the last unit —
Lucida Console's outlier is `€` at U+20AC, just outside — and a proportional face is
caught immediately, since Arial's `'` is 0.19 em against `%` at 1.02. So that is the
range, with equality, not a tolerance.

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

The macOS list has since been checked on a host, and it held; what did not hold
is in [macOS](#macos-moves-the-alias-table).

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
actually wants. `resolvedBy` now has four values — `explicit`, `host`, `alias`,
`substitute` — and each one now corresponds to something a reader might act on.
The one that matters is still `substitute`. `alias` arrived later, with the macOS
findings below, and replaced the `table` this section originally listed.

The honest consequence, recorded in the reference documentation rather than
buried: a bound of 44% over printable ASCII, and worse over any wider range,
means text in a substituted font can still overlap.
The signal that a substitute was used is `resolvedBy: "substitute"` in the
printout header and the accompanying warning — machine-readable, unambiguous,
and available whatever the geometry does. The predecessor needed geometry as
the signal because it had no such output. `sr` does, so the geometry is a
belt-and-braces measure and is described as one.

### macOS moves the alias table

Host enumeration was then implemented to the letter of the specification and run
on macOS 26.5.2. 395 files, 810 faces, 381 families. Four things the reference
documentation asserted were wrong there, and the first is the reason
the chain has a different shape.

**A built-in alias table consulted before the host is a trap on the one platform
where the aliased-from names are real.** The Windows finding above — `Helvetica`,
`Times` and `Courier` all miss, so an alias table is needed — is sound on Windows
and generalised badly. macOS ships all three:

| Requested | Present on the host |
|---|---|
| `Helvetica` | `/System/Library/Fonts/Helvetica.ttc`, 6 faces |
| `Times` | `/System/Library/Fonts/Times.ttc` |
| `Courier` | `/System/Library/Fonts/Courier.ttc` |

With the alias table at step 2, a template asking for Helvetica got Arial
while real Helvetica sat in the system font directory — and the printout recorded
`resolvedBy: "table"`, which reads as a deliberate mapping rather than a fallback.
That is the same silent-substitution failure as fontconfig's matcher above,
reached by a different route: a step that always answers, placed before the step
that answers correctly.

The fix is ordering, not content. The host is searched first; an alias is what to
try when the machine has no such family, and each alias is then looked up on the
host in turn. It also settled a disagreement between the two documents, which had
the table as "typeface-to-filename" in one and "alias" in the other. Those are not
the same object and they fail differently: a filename table mapping `Helvetica` →
`arial.ttf` simply misses and falls through, harmlessly, while a family alias
resolves and wins. Only the alias spelling survives, and `resolvedBy` names it
`alias` rather than `table`.

**Style cannot come from the subfamily string.** The rule was that a face is
keyed by "the family and style it declares together", which reads as matching the
subfamily text. On macOS that text does not say `Italic`: Helvetica's slanted faces
are `Oblique` and `Bold Oblique`, and seven installed families —
`Avenir`, `Courier`, `DejaVu Sans`, `DejaVu Sans Mono`, `Galvji`, `Helvetica`,
`Mshtakan` — use a spelling the literal rule misses. So
`typeface="Helvetica" italic=#true` failed on the one platform where Helvetica
is genuinely installed.

`head.macStyle` and `OS/2.fsSelection` carry the italic bit correctly on all of
them, so style now comes from the bits. That change also dissolves a second gap
rather than needing its own rule: the old pair rule said family and style are
taken from name IDs 16/17 or from 1/2 and never mixed, with 16/17 winning when
present, and two faces on this host have 16 without 17 —

| | name 1 | name 2 | name 16 | name 17 |
|---|---|---|---|---|
| `NewYork.ttf` | `.New York` | `Regular` | `.New York` | — |
| `NewYorkItalic.ttf` | `.New York` | `Regular Italic` | `.New York` | — |

— so both keyed as `('.New York', 'Regular')`, the italic became unreachable,
and which one answered a lookup depended on directory order. With family from
a name record and style from the bits there is no pair to mix, and nothing to
decide about what "present" means when only half of it is.

The bits are not unconditionally authoritative either. On this host the string and
the bits disagree on 107 of 806 keys, mostly weight names a string test cannot
classify, and in both directions: `Avenir / Black` is not bold by its string and
bold by `macStyle`, while `.SF NS Mono / Light Italic` is italic by its string and
not by `macStyle`.

The precedence does not resolve that second case, and it is worth being plain
about why not. It ranks the *sources*, so a table that is present wins even
where the subfamily string contradicts it, and the string is reached only when
both tables are absent. `.SF NS Mono / Light Italic` therefore comes out
non-italic unless `OS/2.fsSelection` disagrees with `head.macStyle` on that face,
which was not measured — the spike recorded only `macStyle`. So the rule is chosen
on the argument that the bits are machine-readable and a weight string is not
classifiable, at a known cost on faces whose bits are wrong; it is not chosen
because it is right everywhere. Resolving it needs the `fsSelection` column
the spike did not collect, and a decision about whether a contradicting string
should override a present bit at all. Left open.

**Colliding keys needed a rule and had none.** Three keys collide on this host,
so 810 faces enumerate to 807 distinct keys: the two New York faces above,
`Arial Unicode MS` installed in two directories, and `Hoefler Text Ornaments`
keying as `Hoefler Text`. The last is the
interesting shape — an ornament face claiming its parent's family — and
in every case the answer was directory order, unstated. It is now stated:
first found wins, sources in tabulated order, files in Unicode order within
a directory.

**Collections are the platform, not a corner of it.** The earlier note
called macOS "the point at which `.ttc` support matters", which undersells it:

| | files | faces |
|---|---|---|
| `.ttf` | 227 | 227 |
| `.ttc` | **128** | **545** |
| `.otf` | 38 | 38 |
| no extension | 2 | 0, unparseable |

545 of 810 faces, 67%, come from collections, `Helvetica`, `Times`, `Courier`,
`Menlo`, `Lucida Grande` and `Hoefler Text` among them. Face-by-face `.ttc`
enumeration is a prerequisite for the platform working at all.

The same count also corrected "a `.ttc` candidate resolves to the regular face of
the collection", which was being read as face 0. `Menlo.ttc` happens to be Regular,
Bold, Italic, Bold Italic in that order, but `Helvetica.ttc` carries `Light` and
`Light Oblique` after the four standard faces, so a family match that ignored style
could land on either. The rule is the face whose style bits say neither bold nor
slanted.

**A permanent warning is a warning nobody reads.** The no-silent-drops rule made
three files on a stock macOS produce a warning on every run of every report:
`HelveLTMM` and `TimesLTMM`, Type 1 Multiple Master datafork fonts with
an `sfntVersion` that is neither TrueType nor OpenType, and
`Supplemental/NISC18030.ttf`, which parses as sfnt and has no `head` table.
None can be removed — they are under system integrity protection.

Two changes followed. A format the engine knows it does not support is *classified*
and skipped rather than warned about, leaving warnings for a file that claims to be
sfnt and then is not. And enumeration diagnostics were moved out of the printout's
warning list altogether: they describe the machine, not the document, and the list
they were sharing is where missing-glyph and overflow warnings live. Attaching an
unfixable warning to every report is how a reader learns to ignore that list.

The reviewer's observation that the first two files, having no extension, would be
skipped by an extension filter and satisfy the rule *by accident* is the reason the
rule now says explicitly that neither case is decided from the filename — that being
the same shortcut `tdewolff/font` was faulted for above.

**What held.** The substitute list is correct: all three candidates exist
at the stated paths, and the monospace property the engine must verify holds
for each, over Latin-1 spacing glyphs exactly as the rule specifies.

| | advance | distinct widths |
|---|---|---|
| `Monaco.ttf` | 0.6001 em | 1 |
| `Menlo.ttc` face 0 | 0.6021 em | 1 |
| `Supplemental/Courier New.ttf` | 0.6001 em | 1 |
| `Arial.ttf`, as a control | 0.1909–1.0151 em | **30** |

That is the "four unrelated monospaced faces within 0.0025 em of 0.6" claim
on a third platform — 0.6001, 0.6021, 0.6001 — so the Latin-1 range and equality
rather than a tolerance both survive contact with macOS, and a proportional face
is caught on the first comparison.

The *bound* does not transfer, and the table above does not measure it. A bound is
the substitute's narrowest advance over the widest advance of the face it replaces,
so it belongs to the target set, not to the substitute: against this host's Arial
alone it computes to 41% rather than 44%. What the widest Latin-1 glyph is across
381 macOS families was not measured, and with that many families it is likely wider
than the Windows set's, which would make the bound worse rather than equal.
The figure that carries over is the 0.6 em clustering.

One inaccuracy in the directory list cost nothing: only `/System/Library/Fonts`
has a `Supplemental` subdirectory, so "and their `Supplemental` subdirectories"
over-generalised from one case. The table now names the one that exists, and a
missing directory is explicitly not an error.

**Not verified.** Fonts installed on demand through Font Book land under
`/System/Library/AssetsV2/com_apple_MobileAsset_Font7` and `…_Font8`, outside
every directory the chain names, so a template asking for one falls through to the
substitute. The host had the asset catalogues but no downloaded payload, so whether
those paths are stable enough to scan is unanswered and needs a machine with one
installed. And all of this is one host on one OS version: the structure is
long-standing but the counts are not constants.

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

## What building the engine settled

The specification was written before the engine, which is what makes
these worth recording separately: each is a question the documents left
open or answered in a way that implementation showed to be wrong.

**Leading is a constant multiplier, 1.2 times the size.** The spike measured
the font's own suggestion at 1.156 times the size for the committed faces
and left the choice open. A constant wins on the property that matters to
a paginating engine: line spacing must not change when a typeface is
substituted, because that changes how many lines fit a band, which changes
what fits a frame, which repaginates everything after it. A per-face value
makes the substitute path alter pagination as well as appearance, and the
substitute path is already the one where output is not to be trusted.
See [layout.md](layout.md#text-metrics).

**`align` and `halign` on a field combine rather than compete.** The two
properties are documented separately — `halign` aligns content in the box,
`align` aligns lines in the field — and for a single-line field they
describe the same operation on the same box. The printout carries one box
and one alignment, and the box is the field's resolved box, which is what
[printout.md](printout.md#text)'s own example shows and what makes
`align="right"` right-align a number in a column. So `halign` supplies
the alignment when `align` was not written, and `align` wins when it was.
Reading the content box as "the widest line" instead would have made
`align` a no-op on every single-line field, which is most of them.

**An element declaring `bottom` and `height` is container-dependent too.**
The rule said an element is resolved after the band's height when "its `bottom`
was derived, and it has no height of its own", which reads an element with both
as participating. It cannot. Its top comes out as `height − bottom − ownHeight`,
so including its bottom edge in the maximum would require the height that maximum
is computing. Nor is anything lost by leaving it out: its far edge lands at
`height − bottom`, at or above the band's bottom edge for any non-negative
`bottom`, so it could never be the lowest edge anyway. Both reference templates
write such an element (`line width=2 bottom=2 height=0`, a rule two points above
the bottom edge), and under the old wording each was resolved in the first pass,
where the band height still reads zero, landing the line at −2. So the rule is
now drawn on the anchor rather than on the derivation.

**The floating partial order really is built from declared boxes.**
The rule says so, and the reason showed up as a bug: a `stretch` field declares
no height, so its measured height is not zero while its declared extent is.
Using the measured extent, a floating element two points below a one-line field
found nothing wholly above it and stayed where it was declared. With declared
extents the field spans nothing at offset 0, the floater depends on it, and the gap
propagates against the measured height — which is the whole point of the solver.

**Both reference templates needed `or 0` on a footer total.** A page footer is
measured when the page begins, which on the first page is before any record has
been consumed, and `sum` of nothing is `None` rather than `0`. Two of the three
footers in the examples formatted that `None` with `%.2f` and failed. The rule
is right — "no rows" and "rows summing to zero" are different — but its
consequence was under-documented, so [expressions.md](expressions.md#calc)
now names the case rather than leaving it as an aside.

**`embedded` belongs under `layout`, and the example was moved.** The template
format states it twice, in the document model and in `layout`'s child list;
`invoices.kdl` had it under `report`. The specification is the oracle, so the
example moved.

**Decimal comparison is decimal-to-decimal.** Starlark compares two values
of different types by returning unequal for `==` and raising for an ordered
comparison, and there is no hook a value type can use to change that.
So `amount > 0` does not work on a decimal member and `amount == 0` is
quietly false. Arithmetic between a decimal and an int is exact, so this is
an inconsistency, and it is the host dialect's rather than the format's —
the only honest response was to document it and name the spelling that works,
`decimal("0")`.

**Barcode quiet zones need a zero-width bar.** A 1-D stripe array alternates
starting with a bar, and a quiet zone is a leading space. The two are reconciled
by opening the array with a zero-width bar, which keeps the alternation
unambiguous and keeps the invariant that the runs sum to the box's extent.
A 1-D symbol also needs a bar height that does not come from its box,
since a band of barcodes sizes to them: fifteen per cent of the symbol length,
or a quarter of an inch, whichever is greater.

**Not built at this stage.** Subreports are staged last and the engine refuses
a template containing one, naming the node. (They are built now -- see
[what building them settled](#what-building-subreports-settled).) The measurement
cache the layout document describes is not implemented: it is a cost optimisation
rather than a behaviour, and a correct cache key has to capture every name a
band's expressions read, which is more machinery than the current throughput asks
for.

## What building the renderer settled

The [renderer specification](render.md) was written alongside the renderer
rather than before it, because Stage 0 wrote the printout format as the
renderer's contract and left the rest to whichever library was chosen. What
follows is what that choice turned out to cost, and what replaced it.

### The writer decision is reversed: the PDF is written here

The [spike](#font-metrics-and-pdf) chose `go-pdf/fpdf`, against
`signintech/gopdf` and `tdewolff/canvas`, on measured width accuracy
and missing-glyph handling. Reading `fpdf` against the printout format,
rather than against a sample paragraph, found five things the printout
says that it cannot express -- and three of them are in exactly the
features Stage 3 exists to deliver:

| | |
|---|---|
| `outline closed` | `Bookmark` writes `/Count 0` on every entry, which a reader takes as *collapsed*. Every parent comes out closed regardless of what the template asked. |
| `outline` and `xref` destination `x` | Both are written `/XYZ 0 y null`. The x the printout carries is discarded. |
| A destination on a page of its own size | The y is turned around using the *document's* page height rather than the destination page's, so a mixed-size document scrolls to the wrong place. |
| Coordinates finer than 0.01 pt | `Text` writes `Td` with two decimals. The printout's precision is three. |
| The subset tag on `BaseFont` | Omitted, which a PDF/A validator flags, and the face is named from the family string the caller passed rather than from its PostScript name. |

The last two were known at spike time and judged not to decide anything,
which was right on the evidence then available: neither affects the round trip
or reproducibility. The first three were not, and they are not cosmetic.
This project exists to remove encoding traps rather than install new ones,
and a renderer that silently drops a `closed` the author wrote is the same
class of defect as a geometry model that silently discards a constraint.

So the PDF is written directly: `internal/pdfw` for objects, streams, the
cross reference table and the content stream operators, `internal/sfnt` for
reading font tables and building subsets. Four consequences, all of them wanted:

- **Kerning cannot creep back in.** The spike's largest finding was that a
  renderer which shapes disagrees with the printout by several points on a
  kerning-heavy line, that the two shaping paths disagree with each other,
  and that no test using only the committed faces can catch it -- so a
  `kern`-bearing fixture face was called for. It is not needed. PDF advances
  the pen inside a shown string from the font dictionary's `/W` array; there is
  no kerning for a reader to apply and none for a writer to be told to switch off.
  Writing `/W` ourselves closes the hazard by construction rather than by
  configuration.
- **The width floor moves down by two orders of magnitude.** `/W` accepts real
  numbers. Writing three decimals of a thousandth of an em, instead of rounding
  to whole thousandths as all three candidate writers do, takes the worst
  per-glyph error from 0.24/1000 em to under 0.001/1000 em, so the 0.012 pt
  accumulation the spike measured across a line is gone. The spike called that
  error "PDF's own, not the writer's". It is the writer's after all; the format
  only invited it.
- **Reproducibility is structural.** No timestamp of its own, no map
  iteration in the output path: the same printout renders to the same bytes.
  `fpdf` writes a creation date unless told otherwise, which is one more thing
  to remember rather than a property of the design.
- **The dependency tree stays flat.** `tdewolff/font` was pinned by the spike
  for `SFNT.Subset`, with the note that the role might turn out to be empty.
  It is not empty, but it is not affordable either: `go list -deps` on a
  package importing it pulls in `net/http`, `github.com/andybalholm/brotli`,
  `github.com/golang/freetype`, and `golang.org/x/image/font/sfnt` --
  the third metrics implementation, the one the spike ruled out.
  A report renderer that links an HTTP client is not a report renderer.
  The subsetter replacing it is about 250 lines and adds nothing to `go.mod`.

The rejection of the other two writers stands, and so does everything the spike
measured about metrics: `go-text/typesetting` remains the only thing that maps
a character to a glyph, in the renderer as in the engine, for the `cmap` reason
the spike found. `tdewolff/font` is dropped entirely, which also removes the
standing hazard that something in the codebase might one day ask it for a glyph.

### Writing the subset is where the risk moved

A subsetter is small, but it is bytes in and bytes out, and a wrong offset
shows up as a glyph that is subtly the wrong shape rather than as an error.
Three things follow, and they are in the test suite rather than in a comment:

- **Composite glyphs are the failure mode the committed fonts cannot show.**
  Go Regular and Go Bold contain no composite glyph, so a subsetter that
  neglected to remap component indices would pass every test built on them --
  and break the accented letters most European text is made of, because a
  component index still pointing into the original face draws whatever glyph
  now sits at that number. The test builds a three-glyph font with a composite
  in it. This is the spike's "test a range, not a font" arriving from the other
  direction.
- **A component outside the face is an error.** Left alone it resolves to a
  blank, so a corrupt font would quietly lose accents instead of being reported.
- **Checksums are verified by recomputation**, both per table and the file-wide
  `checkSumAdjustment`: nothing else in this pipeline reads them, so a wrong one
  is invisible until some other tool looks.

What cannot be tested is a full independent parse of the subset. It omits
`cmap`, which an Identity-H composite font never reads, and every font library
refuses a face without one -- `go-text` says `missing table cmap`, the same
refusal the spike recorded for all three candidate writers' output. So the test
validates the container through an independent loader, and the outlines through
`loca` against the original face.

### The printout gained a face index

A `.ttc` holds several faces and `resolvedFile` alone does not say which one
was measured. On macOS two thirds of installed faces live in collections, so
a host-resolved font could be measured as face 3 and rendered as face 0 -- a
silently wrong typeface, with no diagnostic anywhere. `resolvedIndex` is now
written beside `resolvedFile`, absent for the ordinary single-face file.
The engine knew the index all along; nothing carried it across.

Writing it down exposed a second half of the same defect at the other end
of the chain. A template naming a collection by `file=` could only ever be
given its first face, because an explicit file was taken to *be* a face --
so the index the printout had just gained was unreachable from a template,
and a `.ttc` pinned with `bold=#true` produced the regular face and a warning
that it was not bold. The declared style now chooses among a collection's faces:
the first whose own style bits are exactly the ones declared, and the first face
when none is. Style and not position, because face 0 of a collection is not
reliably its regular one -- it is in `Menlo.ttc`, which is what makes the
assumption tempting.

### One code per character, not per glyph

The obvious subset is one entry per glyph, and it is wrong for text extraction.
Faces routinely map several characters to one glyph -- a non-breaking space and
a space, a hyphen and a soft hyphen -- so a glyph-keyed `ToUnicode` has to pick
one of them, and the text comes back out of the file as the wrong character.
Keying codes by character costs a duplicated glyph outline in the rare colliding
case and makes extraction exact.

It also settles the missing-glyph case the format
[already specified](template.md#missing-glyphs). A character the face lacks
gets a code of its own that draws the empty glyph, so the box is visible *and*
the character is still in the text. Keying by glyph would have collapsed every
missing character onto code 0 and lost all of them from the extracted text --
which is where `signintech/gopdf` was rejected for dropping the character
outright.

### Where the baseline sits had no answer, and needed one

The printout fixes a text mark's box and its leading and says nothing about the
baseline, which is correct: leading is normative because it decides pagination,
and baseline placement inside it decides nothing else. But a renderer cannot
decline to choose.

Anchoring to the ascender is the usual choice, and it is wrong here for the same
reason the leading is a constant. `ascender + descender` exceeds `1.2 × size` in
many faces, so anchoring at the top pushes the whole overhang down into the next
line's slot while leaving a gap above it. Centring the face's em extent in the
leading splits the overhang, so a substituted face does not collide on one side
only. Neither choice can move a page break, which makes this the one place in
the pipeline where a per-face value is safe.

### Which ascender: `hhea`, and only `hhea`

A face carries two vertical extents. `hhea` has one pair, OS/2 has
`sTypoAscender` and `sTypoDescender`, and a face can set `USE_TYPO_METRICS`
in `fsSelection` to ask readers to prefer the second. They are not the same
numbers, and on a face that sets the bit the difference moves a baseline by
about a point.

Shaping libraries honour the request, which is right for their purpose and
wrong here. The PDF font descriptor this renderer writes carries `hhea`'s ascent
and descent -- a reader that re-measures the text has only those -- so if the
baseline were placed from OS/2, the file would state one em extent and be laid
out to another. One table has to answer both questions. `hhea` is the one the
format [specifies](render.md#text), and it is read from the table directly rather
than through the shaping library, which would have returned the other pair.

How exposed is a report to this in practice? Less than the trap suggests, on the
evidence to hand: of the 165 faces installed on the machine this was written on,
17 set the bit -- the Cascadia and Sitka families, mostly -- and in every one of
the 17 the two pairs are *identical*. The bit exists so that a face whose glyphs
overflow the Latin line box can still state the line spacing its designer
intended, so the faces where the pairs genuinely differ are the ones a report
reaches for when it sets a script this machine has no font for.

That is why the regression test patches the bit into a committed face rather than
trusting a fixture to carry it: neither committed face sets it, and the faces
that would diverge cannot be committed here. The reason for choosing `hhea` does
not rest on how common the divergence is, though -- it is that the descriptor
states `hhea`, so anything else deciding the baseline makes the file contradict
itself.

### A justified line is drawn in pieces, and the pieces carry their spaces

Two ways to justify: word spacing, or explicit positions. Word spacing is not
available: PDF's `Tw` acts on the single-byte code 32, and an Identity-H
composite font has no single-byte codes at all, so a file relying on it would be
silently unjustified. Explicit positions are better anyway: each segment is
placed by an exact displacement from the one before, so its position does not
depend on the glyph advances before it, and the line lands on the box's far edge
rather than near it.

The segments have to carry their own whitespace rather than being words with
gaps jumped between them. Drawing words alone looks identical and extracts as
`onetwothree`. And the slack has to be measured against the sum of the segments
rather than against the whole line: each is measured and rounded on its own, so
their sum differs from the line's rounded width by a thousandth or two per join,
and measuring against the line leaves the last word that far off the edge.

### Bars are black, and the printout says nothing about it

A `barcode` element takes `style` children like every other element, and the
printout records no colour for it. That is not an omission -- a symbol that is
not dark on light does not scan -- but it is worth stating, because the template
accepts a `color` on a barcode and nothing comes of it.

### A vertical matrix symbol is rotated, not transposed

`vertical` swaps the coding direction, and for a 1-D symbol that is all
it means. For a matrix symbol the obvious implementation -- rows along X,
runs along Y -- is a transposition, which is a rotation *and* a mirror.
A rotated QR code scans; a mirrored one is a different symbol. So the rows
advance leftward from the box's right edge, which makes it a quarter turn
clockwise.

### How the renderer is verified, and what is left to a person

A printout fixture can be read: it is a handful of marks, written by hand from
the specification and compared against what the engine produced. A PDF cannot
be read that way, so the renderer's oracle is a reader written for the purpose.
`internal/pdfscan` parses the cross reference table, resolves objects, inflates
and replays content streams, and recovers text through each font's own
`ToUnicode` map. The [spike](#font-metrics-and-pdf) estimated 400 lines for it
and that is roughly what it is.

Everything the renderer decides is asserted against it: alignment against the
face's own measurement, the baseline formula, justified slack landing on the
box's far edge, dash patterns, stroke widths including the hairline, the
rectangle that draws nothing, barcode bars against the stripe list, the
rotation of a vertical matrix symbol, image placement and cropping, the
outline tree's shape and counts, both kinds of link, per-page sizes, and the
information dictionary. The two reference documents -- the sakila report, and
a paged report with a header, footer, deferred page count and justified text --
are compared line by line: 95 and 126 lines, each against the position
computed from [render.md](render.md)'s rules rather than from the renderer.

Two things that oracle cannot establish, and one of them is not automatable:

- **That a third-party reader agrees.** Xpdf's `pdftotext` -- an implementation
  sharing no code with this one -- reads the sakila PDF and recovers its text
  in reading order, which exercises the cross reference table, the page tree,
  the font dictionaries and the `ToUnicode` maps. That is as far as a
  text-extraction tool reaches.
- **That the glyphs look right.** Nothing available here rasterises a PDF,
  and a subsetter's characteristic failure is a glyph of the wrong shape
  rather than an error. Visual review is a human step, as the verification
  plan already says of the reference report.

**Rendering costs about 0.6 ms per page.** A hundred thousand rows over 1,429
pages render in 0.9 s, a thousand rows over 15 pages in 11 ms -- linear, with
the font subset built once for the document rather than once per page.

### The output file is not opened until the render succeeds

`WriteFile` renders the whole document into memory and writes the file
afterwards. The obvious order -- open the file, render into it -- truncates
the previous report to nothing before it knows whether the render works, and
the render can fail for a reason outside the document: it reads the font files
the printout resolved, and those are not part of it and can have moved since it
was written. Losing yesterday's report to a missing font is not an acceptable way
to report a missing font. The render is buffered regardless, since a PDF's cross
reference table cannot be written until the objects are, so the safe order costs
nothing.

### Not built at this stage

- **PostScript (CFF) outlines.** Refused, naming the font and the file.
  An `.otf` needs a CIDFontType0 descendant and a CID-keyed CFF, and
  a guess at it would produce files that fail on some readers only.
- **Subreports**, still, so the second reference template renders no further
  than it builds. (Built since; the renderer needed no change for them,
  because by the time it sees the document a subreport's bands are ordinary
  marks on ordinary pages.)

## What building the command line settled

The [command line](cli.md) was specified before it was written, like the rest,
and it is the one stage that is almost all interface and almost no algorithm.
What follows is what writing it forced a decision on.

### The exit code says whether the document exists, and nothing else

Three codes: 0, 1 for a run that failed, 2 for a command line that was wrong.
The distinction is worth the extra code because the two have different readers --
a person retypes a flag, a scheduler retries a build -- and because a script that
cannot tell them apart treats a typo as a broken report.

**Warnings do not enter it.** A build that substituted a font, met a character
its font lacks, or -- under `--allow-overflow` -- overran a band, still produced
the document, and the document says so: the warnings are in the [printout
header](printout.md#header-line). A caller who wants to fail on them tests that
list, which is still there tomorrow when someone opens the archived printout. An
exit code is gone the moment the process is.

### The document goes to stdout, everything about the run to stderr

Including the summary line, which is the one thing a person actually wants to
see. It goes to stderr because `--out -` exists: a rule with an exception for
"unless the output is a file" would mean the piped form works and the file form
prints one extra line, from a different stream, on a different day. One rule,
no exception.

### An extension that names no format is an error

`printout.WriteFile` falls back to NDJSON for anything that is not `.cbor`,
which is right for a library call where the caller has already decided.
On a command line it would write a printout to `report.txt` and report success,
and the person who typed `-o report` wanted a PDF. So the CLI resolves the format
itself, from `--format` or from `.pdf` / `.jsonl` / `.ndjson` / `.cbor`,
and refuses the rest.

That leaves `-o -`, which has no extension at all, and is why `--format` exists
as a flag rather than only as an override.

### A printout is read from a file, never from stdin

Reading one is not symmetrical with writing one, and the asymmetry is in the
format: a relative path inside a printout [resolves against the directory the
printout was read from](printout.md#paths). Standard input has no directory. The
alternative -- a `--base` flag for the case -- would add a way to get the base
wrong in exchange for a pipe nobody needs, since the thing on the other end of
that pipe is a file already.

### Flags may follow positional arguments, which the flag package will not do

`flag.Parse` stops at the first argument that is not a flag, so
`sr render out.srp.jsonl -o out.pdf` -- the order everyone types -- leaves
`-o` in the residue. The fix is six lines: parse, take one positional, parse
again from the rest. Which is a fair price for not taking a dependency on a
CLI framework to support four subcommands and eleven flags. The rest of that
price is help text written by hand rather than generated, and a short and
a long name registered as two flags writing one variable.

### `sr validate` resolves the fonts, or it checks nothing new

Template validation runs at load, so `sr validate` could have been
`LoadTemplate` and an exit code. But everything load checks is in the
document, and the interesting failure -- the one that differs between the
machine that wrote the template and the machine that builds it -- is font
resolution. And [enumeration diagnostics](template.md#host-enumeration)
are deliberately kept out of the printout, which leaves them nowhere to
be read at all if a check does not read them.

So a check resolves every declared font and reports what each one resolved to,
and by which step. Two consequences:

- **A font that does not resolve is collected, not returned.** One missing
  typeface must not hide the second one, because the person reading the report
  is about to go and install fonts.
- **A required parameter is not a failure.** A template whose values arrive at
  build time is a normal template, so the check binds what it can and leaves the
  rest unbound; a `defaultexpr` that reads an unbound parameter is left unbound
  in turn rather than reported. This is the one change the command line made
  inside the engine: parameter binding grew a strict flag, true for a build
  and false for a check.

### The library grew introspection rather than the CLI growing reach

`sr validate` prints the parameters, the page geometry, and the names
a template declares. All of it was reachable through `internal/tmpl`,
which the CLI can import, being in the same module -- and that would have
made the CLI the one front end able to see a template.

Instead `Info` and `CheckFonts` are public. The evidence that this is
the right side of the line is a property that was already in the format:
`parameter prompt=#true` means "an interactive front end should ask for this",
and until now nothing could read it. A format with a hint for front ends and
no way for a front end to read it was incomplete, not minimal.

### `sr inspect` checks the printout it dumps

It reads a printout and prints it, so it is the tool a person reaches for
when something looks wrong. Running the format's own
[invariants](printout.md#invariants) over what it just read costs nothing
next to the reading, and an inspector that displays a mark sitting outside
its page without remarking on it is worse than no inspector. The dump still
goes to stdout; the violation goes to stderr, and the exit code is 1.

### Smaller ones

- **A `--param` naming nothing is an error, and so is one given twice.**
  The engine refuses it, so a library caller is covered too;
  the CLI refuses it first, so the exit code says the command line was wrong.
- **A `--format` that contradicts a recognized extension warns.**
  Not an error, because overriding is what the flag is for; not silent,
  because it produces a file nothing will read back.
- **Unresolved fonts are reported under their own heading**, not among
  the check's warnings. They are why the exit code is 1, and a heading
  that says otherwise contradicts the code that follows it.
- **Everything is buffered before the output is opened**, for printouts
  as well as PDFs, which the [renderer already argued
  for](#the-output-file-is-not-opened-until-the-render-succeeds). Serializing
  a printout can fail on a path it cannot make relative, and losing yesterday's
  report to that is no better than losing it to a missing font.
- **No configuration file and no environment variable.** Every input is an
  argument, so a run is reproducible from the command line that produced it,
  and a report that came out wrong cannot be blamed on a file in someone's
  home directory.
- **A template naming a subreport passes the check, and the check lists the
  nodes.** They were a warning while the engine could not build them; now they
  are a fact about the template, under a heading of their own beside the fonts.

## What building subreports settled

Subreports were staged last because they are the most entangled part of the
design: a nested builder that has to be separate enough to have its own records,
variables and page numbering, and joined enough to write into the same printout
and, under `inline`, onto the same page. What follows is where that line ended
up being drawn.

### A subreport is a second engine, not a second mode

The engine already had everything a subreport needs -- a record loop, a frame
tree, a deferral table, a variable context. So a subreport is another instance
of it, with a host pointer, rather than a set of flags threaded through the one
instance. The state divides in three:

- **Per invocation**: the context, the records, the frames, the pending
  deferrals. A fresh engine each time the subreport runs.
- **Per template**: the resolved faces, the decoded images, the decoded blobs,
  and the map from a template's names to the printout's. These are keyed by names
  that belong to a template, and a subreport on a detail band runs once per
  record, so putting them here is what stops a font being read and parsed
  thousands of times.
- **Per document**: the printout, the page being filled, the font and data
  tables, the glyph warnings, the group statistics.

An `embedded` layout shares its host's per-template state, because it shares its
host's fonts and data by definition. A `template=` layout gets its own, because
it is a separate document with a base directory of its own.

### The child's names, the host's names, and one place they meet

The `arg` nodes. They are evaluated in the host's context and bound in the
child's, and nothing else crosses: the host's variables and record fields are
not visible inside the subreport, and the subreport's are not visible outside it.
That was already the format's design; building it confirmed there is no second
channel that wants to exist. A subreport parameter with neither an `arg` nor a
default became a load-time error at the same time, because unlike a report
parameter there is no command line for it to fall back on.

### One document, so one font table -- and names can collide

Two templates in one document can both define `font "body"`, meaning different
faces, and a mark refers to a font by name. So the name a template writes and the
name the printout publishes are two things, and the engine keeps a map between
them. Identical faces share one entry however many templates asked for them, and
a clash takes a suffix. Data blobs work the same way.

The face identity is canonicalised before comparison, because the two routes a
file is reached by differ in spelling: a host named relatively on the command
line resolves `../fonts/Go-Regular.ttf` relative to that, while a subreport is
loaded through the host's base directory and so resolves it absolutely. Compared
as written, one file looked like two faces and the reference example embedded
every font twice.

### An inline subreport is refused a frame of its own

The specification already refused it a `header` and a `footer`, on the grounds
that it does not own the pages it prints on. Building it found three more of the
same thing, and they are the same rule:

- **`columns`.** A columns block reserves a frame that spans page breaks, with
  its own header and footer re-placed on each. An inline subreport's frames are
  grafted onto the host's for the length of one invocation, and a frame that
  outlives the invocation has nobody left to measure its bands in the right
  context. Refused.
- **`swapheader` and `swapfooter`.** Both place a band outside the frame's
  ordinary fill -- above the page header, below the page footer on the last page.
  There is no fill position there for a subreport's bands to follow.
- **A subreport on a `header`, a `footer`, or a swapped band**, in any report.
  Those bands are measured and reserved before the page they bound is filled, so
  a subreport on one would emit bands into a frame that does not exist yet.

Each is a load-time error naming the node, so none of them is something a build
discovers half way through.

### Splicing needs no splice

"The child's pages go into the parent's page list at the point the subreport
occurs" sounds like an insertion into the middle of a list. It is not: everything
the host has built is already before that point, so closing the host's page,
letting the child append its own, and then opening the host's next page puts them
in exactly the right order. The consequence to state plainly is that a subreport
that paginates itself always ends the host's page, full or not -- that is what
"complete pages" means.

Page numbering falls out of the same arrangement. Without `ownpageno` the child
shares the host's counter, so it continues and the host resumes after it with no
arithmetic anywhere; with `ownpageno` the child gets a counter of its own and the
host's is simply untouched.

### A page can now differ from the document

A subreport that paginates itself may run at its own page size and margins, which
is the first time a printout has needed per-page geometry. The format always said
a page could carry overrides; the Go type carried only `width` and `height`, so
the margins were added. They are optional *independently*, and written even when
zero, because zero is a real margin: a page flush to the paper edge under a
header that insets is an override and has to be able to say so.

The reference example uses it. `region_sheet.kdl` is landscape and inset
differently from the register it sits inside, which is what a wide table wants
and what a subreport with pages of its own is free to choose.

### A suppressed band suppresses its subreports

`printwhen` on the host band decides both. A void invoice that does not print has
no line items to print either, and the reference example walked straight into it:
the void invoice's row was suppressed and its line items printed anyway, under
the previous invoice's heading.

Deciding it takes more than reading the same property twice. The band's own
measurement evaluates `printwhen` after setting `VERTICAL_POSITION` and
`VERTICAL_SPACE`, and a negative-seq subreport runs -- and may eject -- between
the gate and that measurement. A condition reading either of those,
or `PAGE_NUMBER`, answered one way at the gate and another at the band,
which reproduced the original bug with the halves swapped: a row's line items
printed and the row did not.

So the answer is taken once, at `bandPrints`, with the vertical context set
exactly as the measurement would set it, and passed into the measurement.
It also stands across the retries inside one placement, so a band cannot appear
or vanish half way through being placed. A header, a footer and the keep-together
lookahead still ask for themselves: each is a measurement of its own rather than
a placement.

The frame position the condition sees is the one before the band's negative-seq
subreports ran. That is the only self-consistent choice -- the gate has to be
answered before they run, because it decides whether they run at all.

### What the keep-together lookahead cannot see

`keeptogether` and `minrows` measure ahead by laying the coming bands out into a
scratch context. A subreport's bands are not among them: the child runs over a
sequence the host has not evaluated yet, and measuring it would mean running the
whole nested build twice, once to size it and once to print it.

Left as an estimate, and said so in the layout document. It is the only estimate
in the engine -- everything the host itself contributes is measured -- and the
alternative is a cost that scales with the nesting depth for a decision that is
a preference rather than a rule.

### An inline subreport's deferrals resolve when it ends

The awkward case: a field with `evaltime="page"` inside an inline subreport. Its
page is the host's, and the host's page may end long after the invocation does --
by which time the child's context has moved on to another invocation, so the
values `FINAL` would read are not the ones the field meant.

Resolving everything the child registered at the end of its invocation is the
answer. Its `report` scope ends there by definition, and once the invocation is
over it has no band left to contribute, so nothing it registered is genuinely
outstanding. A page break that happens *during* the invocation still resolves the
child's page and column deferrals along with the host's, because there both are
printing in the scope that just ended. A deferred value that has to read the
host's final page state belongs on a host band, where the host resolves it.

### An embedded name is resolved once, at load

The first implementation looked a `subreport embedded=` name up twice:
validation against a flat map of every layout in the document, the engine
against a pre-order walk of the same tree. Two searches over one tree,
and they did not agree -- the flat map took the last of a repeated name,
the walk took the first -- so a template could pass `sr validate` against
one layout and build against another. Where the two layouts happened to
declare matching parameters, it built the wrong one silently.

The fix is to search once, at load, and record the result on the subreport node.
There is then no second search to disagree with, and the engine holds a pointer
rather than a name.

Choosing which search to keep settled the scoping question the format had
left open. The lexical one: beside the layout the reference is written in,
then outward. A layout nested inside a *sibling* is out of scope, which is
what makes nesting mean anything -- a pre-order walk over the whole tree
makes every name global and the nesting decorative.

Shadowing is then refused rather than resolved. Lexical scope gives a nested
name that repeats an enclosing one a well-defined meaning, but not a readable one:
the reader has to trace the nesting to know which layout a name means. Two layouts
in one scope chain may not share a name, and neither may two siblings, which the
format already said for the top level and never checked below it. Two unrelated
layouts may each keep a private one of the same name, since neither is in the
other's scope and there is nothing to confuse.

### Recursion is bounded, not forbidden

A subreport may name the layout it is written in, which is how a template walks a
tree, and the data normally ends the walk. Nothing guarantees the data is finite,
so nesting stops at 32 with the node named. A `template=` cycle is different --
it is a property of the documents rather than of the data -- so that one is
refused at load, before any data is read.

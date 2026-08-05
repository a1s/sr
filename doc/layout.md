# Layout algorithm

How a [template](template.md) plus a data sequence become
a [printout](printout.md).

## Contents

- [Coordinates and rounding](#coordinates-and-rounding)
- [Measure, decide, commit](#measure-decide-commit)
- [Frames](#frames)
- [Building a band](#building-a-band)
- [Floating elements](#floating-elements)
- [Placing a band](#placing-a-band)
- [Band splitting](#band-splitting)
- [Ejects](#ejects)
- [Keeping content together](#keeping-content-together)
- [The record loop](#the-record-loop)
- [Deferred evaluation](#deferred-evaluation)
- [Subreports](#subreports)
- [Errors](#errors)

## Coordinates and rounding

Origin is the top-left of the page. X grows right, Y grows **down**.
All lengths are PostScript points.

Every computed coordinate and extent is rounded to 3 decimal places immediately
after the computation that produces it. This is normative: it decides whether
a band fits, so rounding only at output time gives different page breaks.

Comparisons against frame boundaries use a tolerance of 0.001 pt, so a band
whose height matches the remaining space exactly fits rather than ejecting.

## Measure, decide, commit

Every band goes through three steps:

```
measure(band, context) → Measurement       pure: no mutation, no emission
decide(measurement, frame)                 fits / split / eject / error
commit(measurement, frame)                 emit marks, advance, update variables
```

**Measure** resolves styles, evaluates expressions, wraps text, runs the floating
solver, and computes the band's height and its marks at band-relative coordinates.
It mutates nothing: no variable folds, no counters, no output.

**Decide** compares the measured height against the space remaining in the frame
and chooses one of: commit as-is, split, eject and re-measure, or fail.

**Commit** translates the marks to page coordinates, appends them to the page,
advances the frame's fill position, and applies variable iteration.

Band splitting, keep-together, orphan and widow control, measured header
reservation, and deferred evaluation are all consequences of this separation.

### Cost

A band is measured once when it fits, twice when an eject intervenes, and up to
`minrows + 1` times when a group is keeping content together. Measurement is the
expensive step because it wraps text.

Measurements are cached against resolved content and available width,
so re-measuring after an eject at the same width is free.

## Frames

A **frame** is a rectangular region that bands fill from the top down.

| Field | Meaning |
|---|---|
| `x`, `width` | Horizontal extent of the current column. |
| `top`, `bottom` | Vertical extent available to content, after header and footer reservation. |
| `columnCount`, `columnGap` | 1 and 0 for a non-column frame. |
| `column` | Current 0-based column index. |
| `header`, `footer` | Bands reserved at the frame's top and bottom. |
| `parent`, `children` | Tree links. |
| `fillY` | Where the next band goes. |

Frames form a tree, each frame's geometry derived from its parent's.

### Construction

Built once, before any data is read:

1. **Page frame.** The page box inset by the four margins. Its `header` and
   `footer` are `layout`'s. Their measured heights are reserved from `top`
   and `bottom` — see [reservation](#headerfooter-reservation).
2. **Column frames.** A `columns` node creates a child frame with
   `width = (parent.width - (count - 1) × gap) / count`, carrying `count` and
   `gap`. If that `columns` node has its own header or footer, they attach to
   this frame and a further child frame holds the content.
3. **Group levels.** For each `group`, from outermost in, its `columns` node — if
   any — creates frames by the same rule. The group's `title` and `summary` bands
   belong to the frame that *contains* the group's columns, so a group title spans
   all columns.
4. **Detail frame.** The innermost frame holds the `detail` band.

`title` and `summary` at layout level belong to the innermost non-column frame,
unless `swapheader` / `swapfooter` is set, which moves them to the page frame so
they sit outside the page header and footer.

### Header/footer reservation

A frame reserves space for its header and footer by measuring them.
Both are measured against the context as it stands when the frame begins.

A footer is placed flush against the frame's reserved bottom band — including
a column footer. For content that should follow immediately below the last band,
use a group `summary`.

Deferred values inside a header or footer are sized from their placeholder
content; see [deferred evaluation](#deferred-evaluation).

## Building a band

Given a band template and a context, measurement proceeds:

1. **Test the band's `printwhen`.** If false the band is dropped — no marks,
   no height — and none of the steps below run.
2. **Resolve the band's style.** Walk `style` nodes in document order, innermost
   scope first: the band's own, then its `columns`, then each enclosing `group`,
   then `layout`. The first whose `when` is true supplies `font`, `color`, and
   `bgcolor`. Unset properties fall through to the next match in the same walk.
3. **Resolve the band's geometry** against the frame, per
   [the two-of-three rule](template.md#position-and-size-any-two-of-three).
   An explicit `height` is known here; `height="auto"` is settled at step 6.
4. **For each element, in document order:**
   1. **Test its `printwhen`.** If false the element is dropped and the
      remaining sub-steps are skipped. It contributes no marks and no height,
      so a suppressed element neither shows nor pushes its followers down.
   2. **Resolve its style**, by the same outward walk as step 2 but starting
      at the element's own `style` children.
   3. **Resolve its geometry** within the band, applying `maxwidth` / `maxheight`
      clamps. In a band whose height is still unsettled, an extent that depends on
      the band's bottom edge is left for step 6.
   4. **Resolve its content:**
      - `field`: evaluate `expr` (or take `text` / `data`), apply `format`, wrap
        to the box width. With `stretch`, the box height grows to the wrapped text;
        without it, lines beyond the box are dropped at a line boundary.
      - `image`: decode and sniff the type, then apply `scale`. `cut` draws the
        image at natural size clipped to the box, and the retained region becomes
        the mark's `crop`. `fill` scales the image to the box: with `proportional=#true`
        the aspect ratio is preserved, so it is scaled to fit within the box
        and positioned by `halign` / `valign`, and with `#false` it is stretched
        to the box exactly. `grow` expands the box wherever the image exceeds it,
        so the image is drawn at natural size, neither scaled nor clipped.
        Only `fill` scales, so `proportional` is consulted only for `fill`;
        only `cut` produces a `crop`.
      - `barcode`: encode, obtaining stripe widths and a minimum symbol size;
        the box grows along the coding direction to at least that minimum.
      - `xref`: recurse — an xref is a container of elements and is measured as one.
      - A field or barcode with `evaltime` is measured from its placeholder content
        and [registered](#deferred-evaluation).
5. **Resolve floating elements.** See [below](#floating-elements).
6. **Determine band height.** With an explicit `height`, that value.
   With `height="auto"`, the maximum bottom edge over all elements
   that produced marks — zero if none did.

   An element whose vertical extent is **container-dependent** — its `bottom` was
   derived rather than its `height` given, so it stretches to the band's bottom
   edge — is excluded from that maximum and resolved afterwards against the height
   the other elements produced. If every element in an auto band is
   container-dependent, the band's height is zero and all of them collapse.
7. **Align content.** For each element, align its content box inside its resolved
   box per `halign` / `valign`; for a `field`, align each line per `align`.

The emitted mark's box is the content box from step 7, not the declared box.

## Floating elements

An element with `float=#true` has no fixed vertical position: it sits below
whatever lies above it, using measured heights rather than declared ones.

Resolution is a partial order, not declaration order:

1. Consider every element whose `top` and `height` are known and non-negative —
   positioned from the top rather than anchored to the bottom. Elements anchored by
   `bottom` do not participate.
2. Build the minimal DAG of "wholly above" relations from the **declared** boxes:
   - a non-floating element precedes a floating element it is wholly above;
   - a floating element precedes another floating element only when it starts
     earlier, which settles the case of a zero-height floater.

   Minimal means: if C is above both A and B, and B is above A, C depends on B
   only. Transitivity supplies the rest.
3. Walk the DAG in topological order, assigning each floating element
   `top = max(bottom edges of its predecessors) + gap`, where `gap` is that
   element's declared distance to the nearest predecessor.

Zero-height elements are skipped, so a suppressed field does not push its
followers down.

## Placing a band

```
measurement := measure(band, context)
available   := frame.bottom - frame.fillY

if measurement.height <= available + TOLERANCE:
    commit(measurement)

else if band allows splitting and a legal split point exists:
    split, commit the head, column eject, continue with the tail

else if measurement.height <= frame.height(empty):
    column eject, re-measure, commit

else:
    band cannot fit any frame → see Errors
```

`frame.height(empty)` is `bottom - top` for a fresh frame — the most a band could
ever get.

An eject a band triggers by not fitting is always a **column** eject. In a
single-column frame that is the same thing as a page eject, and in a multi-column
one it advances to the next column, escalating to a page eject only when no column
remains — see [which frames participate](#which-frames-participate). A band that
overflows its column should move to the next column, not skip the rest of the page.

An [`eject` node](template.md#eject) is the only way to force a page eject, via
`type="page"`.

After committing, `frame.fillY` advances by the band's height, and every descendant
frame's `top` moves down with it.

## Band splitting

A band with `split=#true` may break across frames.

### Legal split points

A cut at band-relative offset *y* is legal when, for every element that produced
marks:

- the element's vertical span does not strictly contain *y*, **or**
- the element is a `stretch` field and *y* falls on one of its line boundaries.

Barcodes, images, and band-spanning rectangles therefore block a cut anywhere
inside their span. A `stretch` field permits cuts between its lines.

`orphans` and `widows` constrain which line boundaries qualify: at least `orphans`
lines must remain above the cut and at least `widows` below it, per field. A field
with fewer than `orphans + widows` lines permits no internal cut and blocks like an
unsplittable element.

### Splitting

The greatest legal *y* not exceeding the available space is chosen. The head
commits: elements wholly above *y* as-is, split fields with their leading lines and
`lastLineJustified: true` so the renderer does not treat a continued line as a
paragraph end. Then a column eject, and the tail continues with the remaining
lines, its elements re-placed from the tail's top.

Non-splittable elements below the cut move to the tail whole.

A band that does not fit even an empty frame and does allow splitting is split
unconditionally, ignoring `orphans` and `widows`.

## Ejects

An eject ends the current page or column and starts the next.

### Which frames participate

Given the frame of the band that triggered the eject:

- **Page eject**: that frame and every ancestor, up to and including the page
  frame.
- **Column eject**: that frame and ancestors, stopping at the first frame that
  still has an unused column. If none does, the walk reaches the page frame and the
  column eject becomes a page eject.

### Sequence

1. **Footers, innermost first.** Each participating frame's footer is built and
   placed flush at its reserved bottom band. Footers are built against the
   **outgoing** context, so a page footer reports the page it belongs to.
2. **Resolve deferred values** for the scopes that just ended — `column` for a
   column eject, `page` and `column` for a page eject.
3. **Advance.** For a page eject: increment `PAGE_NUMBER`, reset `PAGE_COUNT` and
   `COLUMN_COUNT`, update each group's `_PAGE_NUMBER`, start a new page. For a
   column eject: reset `COLUMN_COUNT`, apply column-scoped variable resets and
   iterations, increment the ejecting frame's `column`, set its `x` to
   `parent.x + (width + gap) × column`, and reset every descendant frame to column
   0.
4. **Headers, outermost first** — the reverse of the footer order.

The two orders together are the order in which the bands appear on the page.

### `eject` nodes

A band's `eject` nodes are tested in document order. The first whose `when` is true
is selected, and the search stops there whether or not it ejects; a `when`-false
node is skipped and the next is tried. The selected node ejects unconditionally if
it has no `require`, and otherwise only when less than `require` remains in the
frame. Full table in [template.md](template.md#eject).

For `title`, ejects are evaluated **after** the band is placed. For every other
band, before.

## Keeping content together

Three mechanisms, from coarsest to finest.

### `group keeptogether`

Before committing a group's `title`, the group's whole extent — title, every
detail, summary — is measured, accumulating until either the group ends or the
total exceeds an empty frame. If it fits an empty frame but not the space
remaining, an eject happens first.

Accumulation stops at the empty-frame height, which bounds the lookahead cost
regardless of group size: a group that cannot fit one frame cannot be kept
together.

Lookahead is available because the data sequence is fully buffered.

### `group minrows` and `mintailrows`

`minrows` is the minimum number of detail rows that must share a frame with the
group title. Before committing the title, it plus the next `minrows` details are
measured; if the total does not fit, an eject happens first.

`mintailrows` is the minimum that must share a frame with the group summary. When
the record loop reaches the point where `mintailrows` details plus the summary
remain, they are measured together; if they do not fit, an eject happens before the
first of them.

Both are counted in rows, distinct from a band's line-counted `orphans` and
`widows`.

### `eject require`

Ejects when less than a given amount of space remains — "start this band with at
least this much room". Combine it with `when` on the same node to restrict it to the
records where it matters; see [`eject`](template.md#eject).

## The record loop

For each record, in this order:

1. `THIS` and `ITEM_NUMBER` advance.
2. Evaluate every group's `expr`, outermost first. The outermost that changed
   determines the break level; every group nested inside it breaks too.
3. For each breaking group, **innermost first**: place its `summary`, built against
   the **previous** record's context, so a group summary can print its own total.
4. Reset variables whose scope just ended.
5. For each breaking group, **outermost first**: iterate variables for that scope,
   then place its `title`.
6. Iterate `detail`-scoped variables, then place the `detail` band.

Full variable semantics are in
[expressions.md](expressions.md#variables).

### Records must be ordered by group key

A group breaks when its `expr` changes between **adjacent** records. It does not
collect records with equal keys from across the sequence, so the input must arrive
with each group's records contiguous — normally by sorting in the query that
produced the data. Grouping then costs one pass and no per-group buffering.

Unsorted input is not an error: a repeating key is legal, since a report may group
by a value such as weekday. It produces a group that opens more than once. The
printout header records the number of distinct group runs alongside the number of
distinct keys, so the discrepancy is visible.

### Rollback

A detail band that is measured, folded into variables, and then found not to fit
has its fold rolled back before the eject and reapplied after, so no value is
counted twice. Step 6 iterates variables before placing because the band's content
usually depends on them.

A band suppressed by `printwhen` does not iterate `detail`-scoped variables, but
does advance `ITEM_NUMBER`.

### Report structure

```
title
  [page header, first page]
  for each record: group titles / detail / group summaries
  [page footer, last page]
summary
```

`swapheader` on `title` places it above the first page header; `swapfooter` on
`summary` places it below the last page footer. Both attach the band to the page
frame instead of the inner frame.

## Deferred evaluation

A `field` or `barcode` with `evaltime` is not evaluated when its band is built. It
is registered against the named scope; when that scope ends, the real value is
computed and substituted. This is how a page footer prints the final page count.

### Placeholders

A placeholder is **required** exactly when the deferred element's own size depends
on its content: a `field` with `stretch=#true`, or any `barcode`. In both cases the
box grows to fit, so the space it will need cannot be known from the geometry alone.

Everywhere else the resolved box is content-independent — geometry resolution always
produces a complete box — so there is nothing to reserve and the placeholder is
optional.

The placeholder is the element's `text` or `data`, and the band is measured from it.
Validation rejects a deferred element that needs one and has none. See
[content sources](template.md#content-sources).

### Re-measurement

After substitution the element is re-measured:

- **Height unchanged or smaller** — accepted. The box stays as reserved, which
  shows as whitespace.
- **Height larger** — **error**, naming the field, its placeholder, and both
  heights.

Size placeholders for the worst case:

```kdl
field expr="'Page %d of %d' % (PAGE_NUMBER, PAGE_COUNT)" evaltime="report" \
      text="Page 999 of 999"
```

## Subreports

A `subreport` runs another template over a nested sequence, ordered within its band
by `seq` — negative before the band's content, non-negative after, ties on document
order.

A subreport is a nested builder with its own context: its own parameters, fed by
`arg` nodes, its own variables and groups, and its own record loop. It shares the
enclosing report's fonts and data blobs.

- **Non-inline** (default): the child builds complete pages, which are spliced into
  the parent's page list at the point the subreport occurs. `ownpageno` restarts
  page numbering inside the child; otherwise numbering continues from the parent
  and resumes after it.
- **Inline**: the child's bands are placed into the parent's current frame,
  continuing the parent's pagination. An inline subreport must match the parent's
  page size and inherits its margins, and its page headers and footers are the
  parent's — the child does not emit its own. `inline` and `ownpageno` are mutually
  exclusive.

A subreport may not appear inside a `columns` block.

## Errors

Each of these names the template node, the record index, and the measured values.

| Condition | Behaviour |
|---|---|
| Band taller than an empty frame, splitting not allowed | error |
| Band taller than an empty frame, splitting allowed | split unconditionally, ignoring `orphans` / `widows` |
| Deferred value taller than its placeholder | error |
| Header and footer reservations together exceed the frame | error |
| Barcode content not encodable in the selected type | error |
| Expression type mismatch or missing field | error |
| Column count so high that column width is non-positive | error, at template validation |

`--allow-overflow` downgrades the first row to a warning and places the band
anyway. The warning is recorded in the printout header, so an overflowing document
is identifiable from the artifact.

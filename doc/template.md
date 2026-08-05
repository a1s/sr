# Template format

A **template** describes how to turn a sequence of data records into a paginated
document. It is a [KDL](https://kdl.dev) v2 document whose root node is `report`,
conventionally in a file with a `.kdl` extension.

Applying a template to data produces a **printout** —
see [printout.md](printout.md). The rules governing how bands are placed
and paginated are in [layout.md](layout.md). Expressions are specified in
[expressions.md](expressions.md).

## Contents

- [Document model](#document-model)
- [KDL conventions](#kdl-conventions)
- [Value types](#value-types)
- [Geometry](#geometry)
- [Ordering rules](#ordering-rules)
- [Element reference](#element-reference)
- [Font resolution](#font-resolution)
- [Validation](#validation)

## Document model

A template has a declaration part and a layout part.

```
report
├── parameter*        values supplied by the caller
├── records?          declared types of the input record fields
├── variable*         accumulators evaluated as data is consumed
├── font*             named font definitions
├── data*             named literal blobs (images, font files)
└── layout            page geometry, then the band tree
    ├── style*
    ├── embedded*     reusable sub-layouts for subreports
    ├── title?
    ├── summary?
    ├── header?
    ├── footer?
    ├── columns?
    └── group? | detail?
```

Exactly one of `group` or `detail` is required at each level that can hold them
(`layout`, `group`, `embedded`). Groups nest one level at a time; the innermost
level holds the `detail`.

**Sections** (also called bands) are `title`, `summary`, `header`, `footer`,
and `detail`. All five take the same set of children:

```
<section>
├── style*            conditional formatting, first match wins
├── eject*            forced page or column break, first match wins
├── outline*          document outline (bookmark) entry
├── field*            text
├── line*
├── rectangle*
├── image*
├── barcode*
├── xref*             a link, containing its own body elements
└── subreport*
```

`field`, `line`, `rectangle`, `image`, and `barcode` are collectively
**body elements**. They are the only nodes that put marks on the page.

## KDL conventions

The format uses a consistent subset of KDL:

- **A node's identity is a positional argument** where it has one:
  `font "body"`, `variable "total"`, `group "customer"`, `parameter "start"`.
  Everything else is a named property.
- **Booleans are `#true` and `#false`.**
- **Literal text content is the `text` property**, not a child node:
  `field text="Total:"`.
- **Long blobs use multi-line strings**, which dedent to the closing delimiter:

  ```kdl
  data "logo" encoding="base64" {
    content """
      iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8
      z8DwHwAFAAH/q842iQAAAABJRU5ErkJggg==
      """
  }
  ```

- **`/-` comments out a whole node or property.**

  ```kdl
  /-rectangle width=0 { box y=2 }
  ```

### Parser requirements

The format uses KDL **v2** specifically: `#true` / `#false` / `#null`, raw
strings `#"…"#`, and triple-quoted multi-line strings. A v1 parser cannot
read it, and some v1 parsers accept v2 input without reading it correctly.

## Value types

### Dimension

A length. Bare numbers are PostScript points (1/72 inch).
Quoted strings may carry a unit suffix:

| Suffix | Unit |
|---|---|
| `pt` | point, 1/72 in (the default) |
| `mil` | 1/1000 in |
| `mm` | millimetre |
| `cm` | centimetre |
| `in` | inch |

`width=35` and `width="35pt"` are identical.

Internally all dimensions are points, rounded to 3 decimal places after
every computation. Rounding is normative: it decides whether a box fits.

### Color

Any of:

- `"#RRGGBB"`
- a name, case-insensitive: the 16 HTML 4.01 names plus `cyan`, `darkgray`,
  `lightgray`, `magenta`, `orange`, `pink`
- three comma-separated integers 0–255, or three floats 0–1: `"0,89,0"`
- a single integer, red in bits 16–23, green in 8–15, blue in 0–7

The canonical form, and what appears in a printout, is always `"#RRGGBB"`.

### Boolean

`#true` or `#false`.

### Integer

A KDL integer.

### Expression

A string holding a Starlark expression. See [expressions.md](expressions.md).
A string literal inside an expression needs its own quotes: `target="'top'"`,
not `target="top"`.

### Enumerations

| Type | Values |
|---|---|
| `align` (text) | `left` `center` `right` `justified` |
| `halign` | `left` `center` `right` |
| `valign` | `top` `center` `bottom` |
| `calc` | `count` `list` `set` `chain` `first` `last` `sum` `avg` `min` `max` `std` `var` |
| `iter` / `reset` | `report` `page` `column` `group` `detail` `item` |
| `eject type` | `page` `column` |
| `barcode type` | `Code128` `Code39` `Code93` `2of5i` `DataMatrix` `Aztec` `QR-L` `QR-M` `QR-Q` `QR-H` |
| `image scale` | `cut` `fill` `grow` |
| `xref type` | `outline` `url` |
| `dash` | `solid` `dot` `dash` `dashdot` |
| `encoding` | `base64` |
| `compress` | `zlib` `gzip` |

Page size names accepted by `layout pagesize=`:

- ISO 216: `A1` `A2` `A3` `A4` `A5` `A6` `B3` `B4` `B5` `B6`
- North American: `Letter` `Legal` `Ledger` `Executive` `Statement` `Quatro`
  `Royal` `BusinessCard`
- ISO 269 envelopes: `EnvelopeC3` `EnvelopeC4` `EnvelopeC5` `EnvelopeC6`
  `EnvelopeDL` `EnvelopeB4` `EnvelopeB5`
- North American envelopes: `Envelope#10` `EnvelopeA2` `EnvelopeA6` `EnvelopeA7`

All are given portrait (width × height); `landscape=#true` swaps them.

## Geometry

Every body element, every section, and every `xref` occupies a box
positioned within its container. The container is the section for a bod
y element, the frame for a section, and the `xref` for elements inside one.

### Position and size: any two of three

Horizontally: `left`, `right`, `width`. Vertically: `top`, `bottom`, `height`.
`right` and `bottom` are offsets measured **inward** from the container's far
edge. Specify any two per axis; the third is derived.

Missing values are filled in a fixed order — `left` then `right`, `top` then
`bottom` — until two of the three are known:

| Specified | Resolves as |
|---|---|
| nothing | `left=0 right=0` — the container's full width |
| `left` | `left`, `right=0` — extend to the container's right edge |
| `width` | `left=0`, `width` |
| `right` | `left=0`, `right` |
| any two | as given |
| all three | **error** |

Vertically the same, with `top`/`bottom`/`height`.

A negative `right` or `bottom` means the box extends past the container edge,
and is legal.

`x` and `y` are accepted as aliases for `left` and `top`.

`maxwidth` and `maxheight` clamp a resolved extent and are not part
of the two-of-three count:

```kdl
field left=10 right=10 maxwidth=50
```

### Section height

A section takes `height="auto"` (the default), meaning content-determined: the
band is as tall as the lowest mark it produces. An explicit `height` fixes it.

An element's `bottom=0` is a different thing: it means extend to the container's
bottom edge.

### Alignment

`halign` and `valign` align an element's **content inside its box**,
once the content's natural size is known. They do not move the box.

For a `field`, content is the wrapped text; for an `image`, the bitmap;
for a `barcode`, the symbol. `align` on a `field` is separate: it aligns
each line of text within the field, and adds `justified`.

### Floating elements

`float=#true` on a body element means its vertical position is not fixed: it is
pushed down below whatever elements lie above it, after their actual heights are
known. Placement is by partial order, not declaration order — see
[layout.md](layout.md#floating-elements).

## Ordering rules

Node order is significant in three places. Reordering siblings changes output.

1. **Body elements paint in document order.** Later elements draw on top of
   earlier ones. A filled `rectangle` must precede the fields that sit on it.
2. **`style` matches first-win.** Each section's `style` nodes are tested in
   document order and the first whose `when` is true supplies the formatting.
   Search continues outward: the section's own styles, then `columns`, then
   each enclosing `group`, then `layout`.
3. **`eject` selects first-win**, in document order: the first node whose
   `when` is true is selected and stops the search, even if its `require`
   then declines to eject. See [`eject`](#eject).

`outline` nodes with `when` conditions also resolve first-win.

`subreport` is ordered by its numeric `seq`, not by document position.
Negative `seq` runs before the section's own content, non-negative after.
Ties break on document order.

## Element reference

### `report`

Root node.

| Property | Type | Default |
|---|---|---|
| `name` | string | — |
| `description` | string | — |
| `version` | string | — |
| `author` | string | — |
| `basedir` | string | template's directory |

`basedir` is the base for resolving relative paths in `image file=`,
`font file=`, and `subreport template=`.

Children: `parameter*`, `records?`, `variable*`, `font*`, `data*`, `layout`
(exactly one).

### `parameter`

A value supplied by the caller, available to expressions by name.

```kdl
parameter "period_start" type="date" default="2005-01-01"
parameter "period_end"   type="date"
parameter "min_amount"   type="decimal" default="0.00"
parameter "title_suffix" default="draft"
parameter "as_of"        type="datetime" defaultexpr="BUILD_TIME"
parameter "due"          type="date" format="02.01.2006" default="31.12.2005"
```

| | Type | Default |
|---|---|---|
| *(arg 1)* | string, the name | required |
| `type` | `string` `int` `decimal` `float` `bool` `datetime` `date` `object` `list` | `string` |
| `default` | text, parsed per `type` | — |
| `defaultexpr` | expression | — |
| `format` | string, for parsing `datetime` / `date` | RFC 3339 |
| `prompt` | boolean | `#false` |

`type` gives the parameter's value type. Inside expressions the value has
that type, so a `date` parameter supports `.year` and `.unix` and a `decimal`
parameter stays exact in arithmetic.

At most one of `default` and `defaultexpr` may be given. A parameter with neither
is **required**: building without a value for it is an error naming the parameter.

- `default` is text in the same form the caller supplies on a command line,
  parsed per `type` — see [below](#parameter-values-as-text).
- `defaultexpr` is an expression, for a default that has to be computed.

Either is used only when the caller supplies no value.

`prompt` marks the parameter as one an interactive front end should ask about;
the engine ignores it.

#### Parameter values as text

A parameter value arriving as text — from `--param NAME=VALUE`, or from
`default` — is parsed according to `type`:

| `type` | Accepted text |
|---|---|
| `string` | any, verbatim |
| `int` | optional sign and decimal digits; arbitrary precision |
| `decimal` | optional sign, digits, optional `.` and fractional digits |
| `float` | as `decimal`, plus exponent notation, `inf`, `nan` |
| `bool` | `true` `false` `1` `0`, case-insensitive |
| `date` | RFC 3339 date, `2005-01-01`, or `format` if given |
| `datetime` | RFC 3339 timestamp, `2005-05-24T22:53:30Z`, or `format` if given |
| `object` | a JSON object |
| `list` | a JSON array |

`date` yields a time value with zero time of day.

Text that does not parse is an error naming the parameter, its declared type, and
the offending text.

A `defaultexpr` result, and a subreport [`arg`](#arg) value, must match the
declared `type`; a mismatch is an error naming the parameter.

### `records`

Declares the fields of the input records and their types.

```kdl
records {
  column "customer_id"  type="int"
  column "amount"       type="decimal"
  column "rental_date"  type="datetime"
  column "return_date"  type="datetime" nullable=#true
  column "customer"     type="object"
  column "film"         type="object"
}
```

Each `column` node:

| | Type | Default |
|---|---|---|
| *(arg 1)* | string, the field name | required |
| `type` | `string` `int` `decimal` `float` `bool` `datetime` `date` `object` `list` | `string` |
| `nullable` | boolean | `#false` |
| `format` | string, for parsing `datetime` / `date` | RFC 3339 |

Declared fields are coerced once, at load, and are the names expressions may
reference as bare identifiers.

A field present in the data but not declared is not an error — data often carries
columns a report does not use. It is not reachable as a bare name, but remains
reachable as `THIS["name"]`.

`type="object"` and `type="list"` pass nested JSON through; their contents are
not declared and are reached by attribute or subscript.

### Data input

Records come from JSON: either a single array document, or NDJSON with one record
per line. The engine buffers the whole dataset — `DATA_COUNT`, report-scoped
aggregates, and [keep-together](layout.md#keeping-content-together) lookahead
all require the full sequence.

The library API takes a Go slice directly; JSON is the CLI's front end.

### `variable`

An accumulator updated as data is consumed.

```kdl
variable "total_amount"    expr="amount" calc="sum" reset="report"
variable "row_number"      expr="1"      calc="sum" reset="page"
variable "customer_amount" expr="amount" calc="sum" reset="group" resetgrp="customer"
```

| | Type | Default |
|---|---|---|
| *(arg 1)* | string, the name | required |
| `expr` | expression | required |
| `init` | expression | — |
| `calc` | calc enum | `first` |
| `iter` | iter enum | `detail` |
| `itergrp` | string, a group name | — |
| `reset` | iter enum | `report` |
| `resetgrp` | string, a group name | — |

`iter` says when `expr` is evaluated and folded into the accumulator; `reset` says
when the accumulator is cleared. When either is `group`, the corresponding
`itergrp` / `resetgrp` names which group. `init`, if present, seeds the
accumulator as its first value.

Full semantics are in [expressions.md](expressions.md#variables).

### `font`

A named font definition, referenced by `style font=`.

```kdl
font "body"  typeface="Helvetica" size=9
font "bold"  typeface="Helvetica" size=9 bold=#true
font "title" typeface="Times"     size=18
font "mono"  file="fonts/DejaVuSansMono.ttf" size=8
```

| | Type | Default |
|---|---|---|
| *(arg 1)* | string, the name | required |
| `typeface` | string | — |
| `file` | string, path to a TTF/OTF | — |
| `data` | string, a `data` node name | — |
| `size` | integer, points | required |
| `bold` | boolean | `#false` |
| `italic` | boolean | `#false` |
| `underline` | boolean | `#false` |

Exactly one of `typeface`, `file`, or `data` is required. `file` and `data`
name a font resource directly; `typeface` goes through
[font resolution](#font-resolution).

`bold` and `italic` select a face when resolving by `typeface`.
With `file` or `data` they are an error — pass the bold face's own file.

`underline` is drawn by the renderer and does not affect metrics.

### `data`

A named literal blob: image bytes, a font file, or field text.

```kdl
data "logo" encoding="base64" compress="zlib" {
  content """
    eJzT0yMAAGTvBe8=
    """
}
data "footnote" expr="parameter_notice"
```

| | Type | Default |
|---|---|---|
| *(arg 1)* | string, the name | — |
| `encoding` | encoding enum | none — content is literal text |
| `compress` | compress enum | none |
| `expr` | expression | — |

Either a `content` child or `expr`, not both. With `expr`,
the blob is produced at build time from the expression's result.

### `layout`

Page geometry and the root of the band tree.

| Property | Type | Default |
|---|---|---|
| `pagesize` | page size name | — |
| `width` | dimension | — |
| `height` | dimension | — |
| `landscape` | boolean | `#false` |
| `leftmargin` | dimension | 0 |
| `rightmargin` | dimension | 0 |
| `topmargin` | dimension | 0 |
| `bottommargin` | dimension | 0 |

Either `pagesize` or both `width` and `height` is required.

Children: `style*`, `embedded*`, `title?`, `summary?`, `header?`, `footer?`,
`columns?`, and exactly one of `group?` / `detail?`.

### `style`

Conditional formatting. First match wins; see [ordering](#ordering-rules).

```kdl
style font="body" color="black"
style bgcolor="#F3EDE7" when="PAGE_COUNT % 2"
```

| Property | Type | Default |
|---|---|---|
| `when` | expression | `True` |
| `font` | string, a `font` name | — |
| `color` | color | — |
| `bgcolor` | color | — |

`when` selects the style. `font` must name a defined `font`;
an unknown name is a validation error.

Unset properties on the matched style fall through to the next match
in the same outward walk, so a band-level style setting only `bgcolor`
still inherits a `font` from `layout`.

### `printwhen`

Every section and every body element accepts `printwhen`, an expression that
suppresses it when false. A suppressed element produces no marks and contributes
no height, so a band whose `height="auto"` collapses when everything in it is
suppressed.

```kdl
detail printwhen="amount > 0" {
  rectangle width=0 printwhen="PAGE_COUNT % 2"
  field expr="film.title"
}
```

`printwhen` is evaluated independently of style matching.

### Content sources

`field` and `barcode` take their content from `expr`, `text`, or `data`.

**Without `evaltime`**, exactly one of the three.

**With `evaltime`**, `expr` is the content and is required — it is what gets
deferred. `text` or `data` may accompany it as a **placeholder**: the value the
element is measured from before the real one is known. At most one placeholder.

```kdl
// immediate — one source
field expr="amount" format="%.2f"
field text="Total:"

// deferred — expr is the content, text sizes the box until it resolves
field expr="'Page %d of %d' % (PAGE_NUMBER, PAGE_COUNT)" evaltime="report" \
      text="Page 999 of 999"
barcode type="2of5i" expr="PAGE_COUNT" evaltime="page" text="90"
```

A placeholder is **required** exactly when the element's own size depends
on its content, which is two cases:

- a `field` with `stretch=#true`, whose height grows to fit the wrapped text;
- any `barcode`, whose box grows along the coding direction to at least the
  symbol's minimum size.

Everywhere else the resolved box is independent of the content, so there is nothing
to reserve and a placeholder is optional. Note that this includes elements sized by
`bottom` — geometry resolution always yields a complete box, so "the box is
incomplete without knowing the content" never arises.

`image` and `data` have no deferred form. `image` takes exactly one of `file`,
`data`, or a `content` child; `data` exactly one of `expr` or a `content` child.

### `eject`

Forces a page or column break.

```kdl
eject type="page" when="customer_COUNT > 20"
eject require="3cm"
eject type="page" when="customer_COUNT > 20" require="3cm"
```

| Property | Type | Default |
|---|---|---|
| `type` | eject enum | `page` |
| `when` | expression | `True` |
| `require` | dimension | — |

`when` **selects** the node. `require` then decides whether the selected node
ejects.

A band's `eject` nodes are tested in document order. The first whose `when` is true
is selected and the search stops there — later nodes are never considered, whether
or not the selected node ejects. A node whose `when` is false is skipped and the
next is tried.

Once a node is selected:

| `require` | Result |
|---|---|
| absent | eject |
| present | eject only if less than `require` remains in the frame |

So the two combine as a conjunction, and `require` is reachable only through a true
`when`:

| `when` | `require` | Result |
|---|---|---|
| false | any | not selected; the next `eject` node is tried |
| true or absent | absent | eject |
| true or absent | present | eject if remaining space < `require` |

The three examples above therefore mean: eject whenever the customer has more than
20 rows; eject whenever less than 3cm remains; and eject only when both hold.

For `title`, eject is evaluated at the **end** of the section.
For every other section, at the beginning.

A band that does not fit the space remaining ejects on its own, without any `eject`
node — see [layout.md](layout.md#placing-a-band).

### Sections: `title`, `summary`, `header`, `footer`, `detail`

| Property | Type | Default | Applies to |
|---|---|---|---|
| `height` | dimension or `"auto"` | `"auto"` | all |
| `printwhen` | expression | — | all |
| `split` | boolean | `#false` | all |
| `orphans` | integer, text lines | 1 | all, when `split` |
| `widows` | integer, text lines | 1 | all, when `split` |
| `swapheader` | boolean | `#false` | `title` only |
| `swapfooter` | boolean | `#false` | `summary` only |

`split=#true` allows the band to break across frames, continuing wrapped text of
`stretch` fields on the next. `orphans` and `widows` are minimum line counts at a
break. Elements that cannot split — barcodes, images, band-spanning rectangles —
prevent a break in their vertical span.

`swapheader` on `title` places the title above the page header on the first page
rather than below it; `swapfooter` on `summary` does the same at the bottom.

`title` prints once before any data, `summary` once after. `header` and `footer`
repeat per frame. `detail` prints once per data record.

### `columns`

Splits the enclosing frame into columns.

```kdl
columns count=2 gap="5mm" {
  header height=20 { ... }
  footer height=12 { ... }
}
```

| Property | Type | Default |
|---|---|---|
| `count` | integer | required |
| `gap` | dimension | 0 |

Children: `style*`, `header?`, `footer?`.

Column width is `(frame width - (count - 1) × gap) / count`.

A `subreport` may not appear inside a column.

### `group`

A data-driven grouping level.

```kdl
group "customer" expr="customer_id" keeptogether=#true minrows=2 {
  title { ... }
  summary { ... }
  detail { ... }
}
```

| | Type | Default |
|---|---|---|
| *(arg 1)* | string, the name | required |
| `expr` | expression | required |
| `keeptogether` | boolean | `#false` |
| `minrows` | integer, detail rows | 1 |
| `mintailrows` | integer, detail rows | 1 |

The group breaks whenever `expr` changes value between adjacent records,
so records must arrive ordered by the group key — see
[layout.md](layout.md#records-must-be-ordered-by-group-key).
A break invalidates all nested groups.

`keeptogether=#true` places the whole group on one frame if it fits on an empty
one. `minrows` is the minimum number of detail rows that must follow the group
title in the same frame; `mintailrows` the minimum that must precede the group
summary. These are counted in rows, distinct from a section's line-counted
`orphans` / `widows`.

Children: `style*`, `title?`, `summary?`, `columns?`, and exactly one of `group?`
/ `detail?`.

Defining a group named `X` makes `X_COUNT` and `X_PAGE_NUMBER` available to
expressions.

### `field`

Text.

```kdl
field expr="amount" format="%.2f" align="right" width="15mm"
field text=" Film title" width="35mm" valign="center"
field expr="notes" stretch=#true left=0 right=0
```

| Property | Type | Default |
|---|---|---|
| `expr` | expression | — |
| `text` | string, literal content | — |
| `data` | string, a `data` node name | — |
| `evaltime` | `report` `page` `column` or a group name | — |
| `align` | text align enum | `left` |
| `format` | string, a `%` format | `"%s"` |
| `stretch` | boolean | `#false` |

Plus the geometry and alignment properties from [Geometry](#geometry),
`printwhen`, and `style*` children.

Content comes from `expr`, `text`, or `data` — see
[content sources](#content-sources). `format` is applied to the `expr` result,
so a tuple-valued expression spreads across several conversions:

```kdl
field expr="(customer.last_name, customer.first_name, customer_amount)"
      format="Total for %s, %s: %.2f"
```

`evaltime` defers evaluation until the named scope is complete — this is
how a page footer prints the final page count. See
[layout.md](layout.md#deferred-evaluation).

`stretch=#true` grows the box's height to fit wrapped text.
Without it, text that does not fit is truncated at a line boundary.

### `line`

```kdl
line width=1 left=0 right=0 height=0
line dash="dot" left=0 right=0
```

| Property | Type | Default |
|---|---|---|
| `width` | dimension, stroke width | 0 (hairline) |
| `dash` | dash enum | `solid` |
| `backslant` | boolean | `#false` |

Plus geometry, `printwhen`, and `style*`.

The line runs corner to corner of its box: top-left to bottom-right,
or bottom-left to top-right when `backslant=#true`.

`width` on a `line` is the **stroke width**, not a geometry extent.
Use `left`/`right` for a line's horizontal extent.

### `rectangle`

```kdl
rectangle width=0 radius=2
rectangle width=1 opaque=#false
```

| Property | Type | Default |
|---|---|---|
| `width` | dimension, stroke width | 0 (hairline) |
| `dash` | dash enum | `solid` |
| `radius` | dimension, corner radius | 0 |
| `opaque` | boolean | `#true` |

Plus geometry, `printwhen`, and `style*`.

The stroke uses the style's `color`, the fill its `bgcolor`. `opaque=#false`
suppresses the fill. A `width` of 0 draws a hairline; to draw fill only, set
`opaque=#true` and give the style no `color`.

### `image`

```kdl
image file="logo.png" scale="fill" width="2cm" height="1cm"
image data="TheLarch" scale="grow"
```

| Property | Type | Default |
|---|---|---|
| `file` | string, path relative to `basedir` | — |
| `data` | string, a `data` node name | — |
| `type` | `png` `jpeg` `gif` | sniffed from content |
| `scale` | scale enum | `cut` |
| `proportional` | boolean | `#true` |
| `embed` | boolean | `#true` |

Plus geometry, alignment, `printwhen`, and `style*`.

Exactly one of `file` or `data`, or a `content` child.

`scale`: `cut` crops to the box, `fill` scales to the box, `grow` grows the box to
the image. `proportional` preserves aspect ratio when scaling. `embed=#false`
records a file reference in the printout instead of the bytes.

`type` is optional and sniffed from the content; give it to override.

### `barcode`

```kdl
barcode type="Code128" text="Code 128" valign="top"
barcode type="QR-Q" expr="PAGE_COUNT" evaltime="page" text="90" grow=#true
```

| Property | Type | Default |
|---|---|---|
| `type` | barcode enum | required |
| `expr` | expression | — |
| `text` | string, literal content | — |
| `data` | string, a `data` node name | — |
| `evaltime` | as for `field` | — |
| `format` | string, a `%` format | `"%s"` |
| `module` | dimension, narrow bar width | `"10mil"` |
| `vertical` | boolean | `#false` |
| `grow` | boolean | `#false` |

Plus geometry, alignment, `printwhen`, and `style*`.

Content comes from `expr`, `text`, or `data` — see
[content sources](#content-sources). A barcode's size is always content-dependent,
so a deferred barcode always needs a placeholder.

`format` is applied to the `expr` result before encoding, which is how a numeric
value gets a fixed width:

```kdl
barcode type="2of5i" expr="order_id" format="%08d"
```

The box always grows along the coding direction — vertically when
`vertical=#true`, horizontally otherwise — to at least the symbol's minimum size.
`grow=#true` additionally expands the symbol to use the available box: for 2-D
types this recomputes `module`, for 1-D types it grows the bar height.

Characters the selected type cannot encode are an error.

Barcodes are always embedded in the printout, as stripe geometry
rather than a bitmap; see [printout.md](printout.md#barcode).

### `xref`

A link region that contains its own body elements.

```kdl
xref type="url" target="'https://dev.mysql.com/doc/sakila/en/'" \
     right=0 height="1cm" halign="right" {
  field align="right" text="DVD rental payments" { style font="title" }
}
```

| Property | Type | Default |
|---|---|---|
| `type` | xref enum | required |
| `target` | expression | required |
| `caption` | expression | — |

Plus geometry and body-element children (`field`, `line`, `rectangle`, `image`,
`barcode`, and nested `xref`).

`type="url"` links to the expression's string result. `type="outline"` links
to an `outline` whose `name` matches. An `xref` with no explicit width sizes
to its content.

### `outline`

An entry in the document's outline (bookmark) tree.

```kdl
outline title="'DVD rental payments'" name="'top'" closed=#true
outline title="'%s, %s' % (customer.last_name, customer.first_name)" level=2
```

| Property | Type | Default |
|---|---|---|
| `title` | expression | required |
| `level` | integer | 1 |
| `name` | expression | — |
| `when` | expression | — |
| `closed` | boolean | `#false` |

`name` makes the entry a target for `xref type="outline"`. `closed=#true` renders
the entry collapsed.

### `subreport`

Runs another template over a nested data sequence.

```kdl
subreport seq=10 template="detail_lines.kdl" data="THIS.lines" {
  arg "heading" value="film.title"
}
```

| Property | Type | Default |
|---|---|---|
| `template` | string, path relative to `basedir` | — |
| `embedded` | string, an `embedded` node name | — |
| `seq` | integer | required |
| `data` | expression yielding a sequence | required |
| `when` | expression | — |
| `inline` | boolean | `#false` |
| `ownpageno` | boolean | `#false` |

Exactly one of `template` or `embedded`.

`seq` orders subreports within a section: negative before the section's own
content, non-negative after. `inline=#true` splices the subreport's bands into the
current frame instead of starting fresh pages; an inline subreport must match the
parent's page size and inherits its margins. `ownpageno=#true` restarts page
numbering inside the subreport, and is incompatible with `inline`.

A `subreport` may not appear inside a `columns` block.

Children: `arg*`.

### `arg`

Supplies a value for a subreport `parameter`.

| | Type | Default |
|---|---|---|
| *(arg 1)* | string, the parameter name | required |
| `value` | expression | required |

`value` is an expression, not text, so it is not parsed per the parameter's `type`.
Its result must already match that type.

### `embedded`

A subreport layout defined inline, referenced by `subreport embedded=`.

| | Type | Default |
|---|---|---|
| *(arg 1)* | string, the name | required |

Children: `parameter*`, `variable*`, `style*`, `title?`, `summary?`, `header?`,
`footer?`, `columns?`, exactly one of `group?` / `detail?`, and nested `embedded*`.

An `embedded` layout is its own namespace for parameter, variable, and group
names, but shares the enclosing report's `font` and `data` definitions.

## Font resolution

A `font` node naming a `typeface` is resolved by trying, in order:

1. An explicit `file` or `data` on the `font` node. If either is present,
   resolution ends there and failure is an error.
2. A built-in typeface-to-filename table covering the common desktop faces.
3. OS font enumeration: the Windows registry, fontconfig on Linux, the standard
   directories on macOS.
4. A scan of known font directories.
5. A last-resort substitute, deliberately wider than most faces so that text
   overflows visibly rather than overlapping silently.

**Strict mode** stops after step 1 and fails with the unresolved typeface named.
Enable it with `--strict-fonts` on the CLI, or the equivalent library option.

The printout records which file and face were actually measured,
and which step of the chain produced them — see
[printout.md](printout.md#fonts).

## Validation

Validation runs once, at load, before any data is read. It checks:

- Node nesting and cardinality.
- Required properties present; property values in range for their type.
- Exactly one of `group` / `detail` at each level that takes them.
- `layout` has `pagesize`, or both `width` and `height`.
- Unique names within a namespace: `parameter`, `variable`, `group`, `font`,
  `data`, `embedded`. An `embedded` layout is a separate namespace for parameters,
  variables, and groups.
- Every `style font=` names a defined `font`.
- Every `itergrp` / `resetgrp` names a defined `group`, and every `evaltime` names
  `report`, `page`, `column`, or a defined group.
- Every `xref type="outline"` has a reachable target `outline name=`.
- Per axis, at most two of `left`/`right`/`width` and at most two of
  `top`/`bottom`/`height`.
- [Content sources](#content-sources) are consistent: on a `field` or `barcode`
  without `evaltime`, exactly one of `expr` / `text` / `data`; with `evaltime`,
  `expr` plus at most one placeholder. `evaltime` without `expr` is an error.
  On `image`, exactly one of `file` / `data` / `content`; on `data`, exactly
  one of `expr` / `content`.
- At most one of `default` / `defaultexpr` on a `parameter`, and any `default`
  parses as its declared `type`.
- `format` appears only on a `parameter` or `column` whose type is `date` or
  `datetime`.
- Every `arg` names a `parameter` of the subreport it belongs to.
- Every deferred `stretch` field and every deferred `barcode` has a placeholder.
- `subreport` has exactly one of `template` / `embedded`, is not inside `columns`,
  and does not combine `inline` with `ownpageno`.
- `font` does not combine `file` or `data` with `bold` or `italic`.
- `columns count` does not make the column width non-positive.
- Expressions parse. Name resolution is not checked at load, since undeclared
  record fields are reached dynamically.

It warns when an `height="auto"` band contains only elements whose vertical extent
is container-dependent, since such a band collapses to zero height.

Every diagnostic names the file, the node path, and the property.

# Printout format

A **printout** is the result of applying a template to data: a sequence of pages,
each holding a flat list of absolutely-positioned marks in paint order.

Nothing in a printout is evaluable. Every expression has been evaluated, every box
resolved to absolute coordinates, every string wrapped to lines, every barcode
encoded to stripe widths, every font resolved to a specific file. A renderer
needs no data, no template, and no expression evaluator.

Printouts are machine-generated and not intended for hand editing.

## Contents

- [Encoding](#encoding)
- [Header line](#header-line)
- [Page lines](#page-lines)
- [Marks](#marks)
- [Invariants](#invariants)
- [Example](#example)

## Encoding

Two encodings carry the same data model:

- **NDJSON** (default, `.srp.jsonl`): one JSON object per line, LF-terminated.
  The first line is the [header](#header-line); every following line is a
  [page](#page-lines).
- **CBOR** (`.srp.cbor`): the same objects as a CBOR sequence, in the same order.
  Smaller, for large printouts.

The in-memory structure is the primary artifact — the engine hands it to a renderer
directly, and serializes only when asked.

### Units and number format

Every length is a number of PostScript points (1/72 inch), rounded to at most 3
decimal places. Colors are `"#RRGGBB"` strings.

Numbers serialize in the shortest form that round-trips. Integral values are
written without a fractional part, so `72` not `72.0`.

## Header line

```json
{
  "sr": 1,
  "kind": "header",
  "report": {
    "name": "DVD rental payments",
    "description": "Payments by customer",
    "version": "1.0",
    "author": "als"
  },
  "built": "2026-08-04T09:12:44Z",
  "engine": "sr 0.1.0",
  "strictFonts": false,
  "pages": 37,
  "groupRuns": { "customer": 599 },
  "groupKeys": { "customer": 599 },
  "page": {
    "width": 595.276, "height": 841.89,
    "leftMargin": 42.52, "rightMargin": 42.52,
    "topMargin": 28.35, "bottomMargin": 28.35
  },
  "fonts": [ … ],
  "data": { … }
}
```

| Field | Meaning |
|---|---|
| `sr` | Format version. `1` for this specification. |
| `kind` | Always `"header"`. |
| `report` | Metadata from the template's `report` node. Omitted fields are absent, not null. |
| `built` | RFC 3339, the run's `BUILD_TIME`. |
| `engine` | Name and version of the producing engine. |
| `strictFonts` | Whether font guessing was disabled for this run. |
| `pages` | Number of page lines that follow. |
| `groupRuns` | Per group, how many times it opened. |
| `groupKeys` | Per group, how many distinct key values it saw. A `groupRuns` value larger than its `groupKeys` value means the input was not ordered by that group's key. |
| `page` | Default page geometry, inherited by every page that does not override it. |
| `fonts` | Resolved font table. |
| `data` | Shared blobs, keyed by name. |
| `warnings` | Present only when `--allow-overflow` suppressed an error. An array of objects with `kind`, `node`, `record`, and `message`. |

### `fonts`

One entry per distinct font used, sorted by `name`.

```json
{
  "name": "body",
  "size": 9,
  "bold": false, "italic": false, "underline": false,
  "requested": "Helvetica",
  "resolvedFile": "C:/Windows/Fonts/arial.ttf",
  "resolvedFace": "Arial",
  "resolvedBy": "os"
}
```

`requested` is the template's `typeface`; `resolvedFile` and `resolvedFace`
are what was measured. `resolvedBy` is the step of the
[resolution chain](template.md#font-resolution) that produced it — one of
`explicit`, `table`, `os`, `scan`, `substitute`. A value of `substitute` means
text may overflow.

An embedded font names a `data` entry via `"resolvedData": "<name>"` instead of
`resolvedFile`.

### `data`

Blobs referenced by more than one mark, so image bytes are stored once
no matter how many pages show the logo.

```json
"data": {
  "logo": { "encoding": "base64", "content": "iVBORw0KGgo…" }
}
```

`encoding` is `"base64"` for binary, absent for literal text.
Content is stored decompressed.

## Page lines

```json
{
  "kind": "page",
  "number": 1,
  "marks": [ … ]
}
```

| Field | Meaning |
|---|---|
| `kind` | Always `"page"`. |
| `number` | 1-based page number as printed. Not necessarily the line's ordinal — a subreport with `ownpageno` restarts numbering. |
| `width`, `height`, `*Margin` | Optional. Present only when they differ from the header's `page` defaults. |
| `marks` | Array of [marks](#marks) in paint order. |

Pages appear in output order. Sections are flattened: a detail band's rectangle,
four fields, and rule become five entries in `marks`, with no record of which band
produced them.

## Marks

Every mark has `kind` and a `box`. The box is four required numbers — absolute,
with non-negative width and height, no alignment and no relative encoding:

```json
"box": { "x": 42.52, "y": 411.02, "width": 184.25, "height": 10 }
```

`x`/`y` are the top-left corner, measured from the top-left of the page. Y grows
downward. A renderer targeting a Y-up coordinate system flips once, at the page
level.

### `text`

```json
{
  "kind": "text",
  "box": { "x": 184.25, "y": 411.02, "width": 42.52, "height": 10 },
  "font": "body",
  "color": "#000000",
  "align": "right",
  "leading": 10.8,
  "lines": ["4.99"]
}
```

| Field | Meaning |
|---|---|
| `font` | Name of a header `fonts` entry. |
| `color` | Stroke colour of the glyphs. |
| `align` | `left` `center` `right` `justified`. |
| `leading` | Baseline-to-baseline distance. |
| `lines` | Wrapped lines, in order. Never empty. |
| `lastLineJustified` | Optional, default `false`. Only meaningful with `align: "justified"`. |

Text arrives already wrapped. A renderer must not re-wrap, and needs font metrics
only to place glyphs within a line.

`lastLineJustified` is `true` when a [split band](layout.md#band-splitting)
continues onto the next frame, so the line is not the paragraph's last and should
be justified.

### `line`

```json
{
  "kind": "line",
  "box": { "x": 42.52, "y": 421, "width": 510.24, "height": 0 },
  "width": 0.5,
  "dash": "dot",
  "color": "#808080",
  "backslant": false
}
```

The line runs from the box's top-left corner to its bottom-right,
or bottom-left to top-right when `backslant` is `true`. `width` is
the stroke width; `0` means a hairline — the thinnest the device draws.

### `rectangle`

```json
{
  "kind": "rectangle",
  "box": { "x": 42.52, "y": 400, "width": 510.24, "height": 20 },
  "width": 1,
  "dash": "solid",
  "stroke": "#000000",
  "fill": "#F3EDE7",
  "radius": 2
}
```

`stroke` and `fill` are independently optional. Absent `stroke` means
no outline is drawn regardless of `width`; absent `fill` means the interior
is untouched. The template's `opaque=#false` resolves to an absent `fill`.

### `image`

```json
{
  "kind": "image",
  "box": { "x": 42.52, "y": 100, "width": 56.7, "height": 28.35 },
  "type": "png",
  "data": "logo",
  "crop": { "x": 0, "y": 0, "width": 120, "height": 60 }
}
```

| Field | Meaning |
|---|---|
| `type` | `png` `jpeg` `gif`. Always present. |
| `data` | Name of a header `data` entry. |
| `file` | Path, when the template set `embed=#false`. Mutually exclusive with `data`. |
| `crop` | Optional source-pixel rectangle, present when `scale="cut"` clipped the image. |

`box` is the final drawn rectangle and `crop` says which source pixels fill it;
scaling is whatever maps one onto the other. The template's `scale` and
`proportional` do not appear.

### `barcode`

```json
{
  "kind": "barcode",
  "box": { "x": 42.52, "y": 60, "width": 90.7, "height": 36 },
  "type": "Code128",
  "value": "Code 128",
  "module": 10,
  "vertical": false,
  "stripes": [2, 1, 2, 2, 2, 2, 1, 1, 4]
}
```

`stripes` is the encoded geometry, in modules:

- **1-D types**: a flat array of alternating bar and space widths, starting with a
  bar. Quiet zones are included as leading and trailing spaces.
- **2-D types**: an array of rows, each an array of alternating dark and light run
  lengths, starting with dark.

`module` is the narrow-element width in points, after any `grow` adjustment.
`value` is the encoded string, recorded so a reader can verify the encoding without
re-deriving it. A renderer draws filled rectangles and does no encoding.

### `outline`

```json
{
  "kind": "outline",
  "title": "Smith, John",
  "level": 2,
  "name": "cust-148",
  "closed": false,
  "x": 42.52,
  "y": 200
}
```

An outline entry has a point rather than a box — it names a scroll position.
`name` is present only when some `xref` targets it. Entries appear in the order
they must appear in the outline tree; `level` gives the nesting.

### `xref`

```json
{
  "kind": "xref",
  "box": { "x": 300, "y": 60, "width": 210, "height": 28.35 },
  "type": "url",
  "target": "https://dev.mysql.com/doc/sakila/en/",
  "caption": "Sakila documentation",
  "marks": [ … ]
}
```

`type` is `url` or `outline`; for `outline`, `target` matches an outline mark's
`name`. `caption` is optional hover text.

Nested `marks` use **page coordinates**, not coordinates relative to the xref box,
so a renderer can flatten them recursively and draw in one pass. The xref box is
purely a hit region.

## Invariants

A valid printout satisfies all of these. They are asserted on every printout
produced in the test suite.

1. `sr` is a version the reader understands.
2. The number of page lines equals the header's `pages`.
3. Every `font` referenced by a `text` mark exists in the header's `fonts`.
4. Every `data` referenced by an `image` or a font exists in the header's `data`.
5. Every `xref` with `type: "outline"` has a `target` matching some outline mark's
   `name`, somewhere in the printout.
6. Every box has non-negative `width` and `height`.
7. Every mark lies within its page's printable area — inside the margins — to
   within the 3-decimal rounding tolerance, unless the header carries an overflow
   warning.
8. Every `text` mark has at least one line, and `lines` count times `leading`
   does not exceed the box height by more than the rounding tolerance.
9. `stripes` sums, times `module`, equal the box extent along the coding
   direction, within tolerance.
10. Outline `level` never jumps by more than one from the previous entry.

## Example

A one-page printout with one font, a rule, and two detail rows:

```json
{"sr":1,"kind":"header","report":{"name":"Minimal"},"built":"2026-08-04T09:12:44Z","engine":"sr 0.1.0","strictFonts":true,"pages":1,"page":{"width":595.276,"height":841.89,"leftMargin":42.52,"rightMargin":42.52,"topMargin":28.35,"bottomMargin":28.35},"fonts":[{"name":"body","size":9,"bold":false,"italic":false,"underline":false,"requested":"DejaVuSans","resolvedFile":"testdata/fonts/DejaVuSans.ttf","resolvedFace":"DejaVu Sans","resolvedBy":"explicit"}],"data":{}}
{"kind":"page","number":1,"marks":[
  {"kind":"text","box":{"x":42.52,"y":28.35,"width":200,"height":10.8},"font":"body","color":"#000000","align":"left","leading":10.8,"lines":["Film title"]},
  {"kind":"line","box":{"x":42.52,"y":39.15,"width":510.24,"height":0},"width":0.5,"color":"#000000","dash":"solid","backslant":false},
  {"kind":"text","box":{"x":42.52,"y":39.15,"width":200,"height":10.8},"font":"body","color":"#000000","align":"left","leading":10.8,"lines":["ACADEMY DINOSAUR"]},
  {"kind":"text","box":{"x":42.52,"y":49.95,"width":200,"height":10.8},"font":"body","color":"#000000","align":"left","leading":10.8,"lines":["ACE GOLDFINGER"]}
]}
```

The page line is shown wrapped for readability. In a real file it is one line.

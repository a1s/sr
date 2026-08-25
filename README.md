# sr — structural reporting

A banded report formatter. Apply a template to a sequence of records
and get a paginated document: bands, groups, page and column headers
and footers, running aggregates, and page footers that know the final
page count.

A Go library with a CLI over it. Template plus JSON in, PDF out,
one static binary.

> **Status:** the engine, the PDF renderer and the command line are implemented.
> One thing named below is not built yet: **subreports**, which a build refuses
> with the offending node named, and a template check reports as such.
> Everything else in these documents is implemented and tested.

## Documentation

| | |
|---|---|
| [doc/template.md](doc/template.md) | Template format: nodes, properties, geometry, ordering rules, font resolution |
| [doc/expressions.md](doc/expressions.md) | Expression language, predefined names, formatting, variable semantics |
| [doc/layout.md](doc/layout.md) | Layout and pagination |
| [doc/printout.md](doc/printout.md) | The intermediate document a renderer consumes |
| [doc/render.md](doc/render.md) | PDF rendering: what a renderer decides, and what it must not |
| [doc/cli.md](doc/cli.md) | The command line: subcommands, flags, streams, exit codes |
| [doc/decisions.md](doc/decisions.md) | Why the design is what it is, and what it replaces |
| [example/sakila/](example/sakila/) | Reference template and dataset |
| [example/invoices/](example/invoices/) | Second example: subreports, region grouping, the remaining variable modes |
| [example/fonts/](example/fonts/) | Fonts committed so examples resolve identically everywhere |

The Go packages are `github.com/a1s/sr` for the library,
`github.com/a1s/sr/printout` for the document a renderer reads,
and `github.com/a1s/sr/pdf` for the renderer.

## How it works

Three artifacts and one language:

- A **template** is a [KDL](https://kdl.dev) v2 document describing page geometry
  and a tree of bands. Bands are `title`, `summary`, `header`, `footer`, and
  `detail`; groups nest around the detail band.
- **Data** is JSON or NDJSON. Field types are declared in the template, which is
  what turns `"19.99"` into an exact decimal and `"2005-05-24T22:53:30Z"` into a
  time value.
- A **printout** is the engine's output: pages of absolutely-positioned marks
  with nothing evaluable left in them. Renderers do no layout.
- **Expressions** are [Starlark](https://github.com/bazelbuild/starlark) —
  sandboxed, with no imports, filesystem, or network.

Layout is speculative: every band is measured before it is committed, then placed,
split, or deferred to the next frame. Band splitting, keep-together, orphan and
widow control, and correct deferred page counts all follow from that.

## Command line

```bash
make build      # or: go install github.com/a1s/sr/cmd/sr@latest
```

```bash
sr build --template sakila.kdl --data payments.jsonl --out report.pdf
```

| Flag | |
|---|---|
| `-t`, `--template` | Template file. Required. |
| `-d`, `--data` | JSON array or NDJSON file, `-` for standard input. Omit for a template with no records. |
| `-o`, `--out` | Output path, `-` for standard output. Extension selects the format: `.pdf`, `.srp.jsonl`, `.srp.cbor`. |
| `--format` | `pdf`, `jsonl` or `cbor`, when the extension does not say. |
| `--param NAME=VALUE` | Supply a report parameter. Repeatable. `VALUE` is text, parsed per the parameter's declared type. |
| `--build-time` | RFC 3339. Fixes `BUILD_TIME` for reproducible output. |
| `--strict-fonts` | Resolve only explicitly named font files; fail otherwise. |
| `--allow-overflow` | Downgrade oversized-band errors to warnings recorded in the printout. |
| `--uncompressed` | Leave PDF streams uncompressed, to read the file in an editor. |
| `-v`, `--verbose` | Report host font diagnostics. |

Other subcommands:

```bash
sr validate sakila.kdl             # check a template without data
sr inspect report.srp.jsonl        # dump a printout as readable text
sr render report.srp.jsonl -o report.pdf
```

`build` writing a printout and `render` reading one back produce the same PDF as
`build` writing PDF directly, which is what makes printouts worth archiving.

The document goes to standard output and everything about the run to standard
error, so `-o -` pipes. Exit 0 is success, 1 a run that failed, 2 a mistake in
the command line; warnings never change it, because they travel in the printout
header where archiving keeps them. Full reference: [doc/cli.md](doc/cli.md).

## Library

```go
package main

import (
    "time"

    "github.com/a1s/sr"
)

func main() {
    tpl, err := sr.LoadTemplate("sakila.kdl")
    if err != nil {
        panic(err)
    }

    rows := []map[string]any{
        {
            "customer_id": 1,
            "amount":      sr.Decimal("2.99"),
            "rental_date": time.Date(2005, 5, 24, 22, 53, 30, 0, time.UTC),
            "customer":    map[string]any{"first_name": "MARY", "last_name": "SMITH"},
            "film":        map[string]any{"title": "ACADEMY DINOSAUR"},
        },
    }

    out, err := tpl.Build(rows,
        sr.WithParam("period_start", time.Date(2005, 1, 1, 0, 0, 0, 0, time.UTC)),
        sr.WithBuildTime(time.Date(2026, 8, 4, 9, 12, 44, 0, time.UTC)),
        sr.StrictFonts(),
    )
    if err != nil {
        panic(err)
    }

    if err := sr.WritePDF(out, "report.pdf"); err != nil {
        panic(err)
    }
}
```

`sr.WritePDF` is `pdf.WriteFile`, re-exported for the common case
of building and rendering in one place. Use the `pdf` package directly
to render to a writer, or to a printout read back from a file:

```go
out, err := printout.ReadFile("report.srp.jsonl")
err = pdf.Write(out, os.Stdout)
```

A printout also serializes to NDJSON or CBOR with `out.WriteFile(path)`,
which is the artifact worth archiving: rendering a printout read back
gives the same PDF, byte for byte, as rendering the one it was built from.

Records may be `map[string]any` or structs; struct fields map to declared members
by name, or by an `sr:"..."` tag. The CLI is a thin shell over this API.

A loaded template also describes itself, which is what a front end needs
before it has any data -- the parameters to ask for, the page geometry,
the names declared:

```go
info := tpl.Info()                          // parameters, page, groups, fonts, …
check, err := tpl.CheckFonts()              // what each typeface resolved to
```

`CheckFonts` is the one check that reads the machine rather than the document,
and it is what `sr validate` reports. A font that does not resolve comes back
in `check.Failures` rather than as an error, so one missing typeface does not
hide the next.

## Running the examples

```bash
sr build -t example/sakila/sakila.kdl -d example/sakila/payments.jsonl -o sakila.pdf
```

Narrowed to one month, using the template's `date` parameters:

```bash
sr build -t example/sakila/sakila.kdl -d example/sakila/payments.jsonl -o june.pdf --param period_start=2005-06-01 --param period_end=2005-07-01
```

The second example uses a subreport, so it checks but does not yet build:

```bash
sr validate example/invoices/invoices.kdl
```

```bash
sr build -t example/invoices/invoices.kdl -d example/invoices/invoices.jsonl -o invoices.pdf
```

[sakila.kdl](example/sakila/sakila.kdl) — a payment list grouped by customer
in two columns: every band type, a group with its own title and summary, column
header and footer, stretch fields, a floating element, six barcode types, an
embedded image and a referenced one, both kinds of cross-reference, conditional
outline entries, a deferred page count, justified text, and typed parameters.

[invoices.kdl](example/invoices/invoices.kdl) — invoices by region with line
items: an inline subreport with an `arg` and its own `records`, a group using
`keeptogether` with `minrows` and `mintailrows`, a `summary` with `swapfooter`,
an image with `embed=#false`, a compressed `data` blob, `iter="item"` against
`iter="detail"`, and the `calc` modes sakila leaves out.

Between them the two templates use every node in the format and all twelve `calc`
modes. They do not exhaust every property. What they leave out, so that reading
them as a reference does not mislead:

| | |
|---|---|
| Properties | `layout width` / `height` / `landscape`, `font typeface` / `data` / `bold` / `italic`, `line backslant`, `rectangle opaque`, `image type`, `barcode data`, `data expr`, `maxheight`, `format` as a date-parse layout on either `parameter` or `member`, `subreport template` / `ownpageno` |
| Spellings | the `x` / `y` aliases for `left` / `top` — both examples use `left` and `top` throughout |
| Enumerations | barcode types `Code93`, `DataMatrix`, `QR-M`, `QR-H`; `image scale="cut"` and `"grow"`; `dash="dash"` |
| Scopes | `iter="report"` / `"page"` / `"column"`; `reset="detail"` / `"item"` |

A subreport in its own file — `subreport template=` with `ownpageno` — needs
a third template, which neither of these is.

Both reference the [committed fonts](example/fonts/) by path, so they build
identically on any machine and work under `--strict-fonts`. Swap `file=` for
`typeface=` to use whatever the machine has instead.

## Reproducible output

The same template over the same data produces byte-identical printouts, and
byte-identical PDFs, when `--build-time` is fixed and `--strict-fonts` is set --
**on any machine**, not just across runs on one. Rendering adds no timestamp
of its own: a PDF's creation date is the run's `BUILD_TIME`. Without the first,
the run timestamp differs; without the second, output depends on which fonts
are installed.

Nothing machine-specific survives in a strict printout. Strict mode admits only fonts
the template named by path, and every path the template named — fonts and
`embed=#false` images alike — is written relative to the printout rather than
absolute. So a printout and the files it points at move as one tree. The header
records which font file each typeface resolved to and by which step, so a difference
is diagnosable from the artifact.

## License

MIT. See [LICENSE](LICENSE).

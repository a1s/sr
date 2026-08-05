# sr — structural reporting

A banded report formatter. Apply a template to a sequence of records
and get a paginated document: bands, groups, page and column headers
and footers, running aggregates, and page footers that know the final
page count.

A Go library with a CLI over it. Template plus JSON in, PDF out,
one static binary.

> **Status:** the specification and reference example are complete;
> the engine is not yet implemented. The documents below describe
> the system as specified.

## Documentation

| | |
|---|---|
| [doc/template.md](doc/template.md) | Template format: nodes, properties, geometry, ordering rules, font resolution |
| [doc/expressions.md](doc/expressions.md) | Expression language, predefined names, formatting, variable semantics |
| [doc/layout.md](doc/layout.md) | Layout and pagination |
| [doc/printout.md](doc/printout.md) | The intermediate document a renderer consumes |
| [doc/decisions.md](doc/decisions.md) | Why the design is what it is, and what it replaces |
| [example/sakila/](example/sakila/) | Reference template and dataset |
| [example/invoices/](example/invoices/) | Second example: subreports, region grouping, the remaining variable modes |
| [example/fonts/](example/fonts/) | Fonts committed so examples resolve identically everywhere |

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
sr build --template sakila.kdl --data payments.jsonl --out report.pdf
```

| Flag | |
|---|---|
| `-t`, `--template` | Template file. Required. |
| `-d`, `--data` | JSON array or NDJSON file. Omit for a template with no records. |
| `-o`, `--out` | Output path. Extension selects the format: `.pdf`, `.srp.jsonl`, `.srp.cbor`. |
| `--param NAME=VALUE` | Supply a report parameter. Repeatable. `VALUE` is text, parsed per the parameter's declared type. |
| `--build-time` | RFC 3339. Fixes `BUILD_TIME` for reproducible output. |
| `--strict-fonts` | Resolve only explicitly named font files; fail otherwise. |
| `--allow-overflow` | Downgrade oversized-band errors to warnings recorded in the printout. |

Other subcommands:

```bash
sr validate -t sakila.kdl          # check a template without data
sr inspect report.srp.jsonl        # dump a printout as readable text
sr render report.srp.jsonl -o report.pdf
```

`build` writing a printout and `render` reading one back produce the same PDF as
`build` writing PDF directly, which is what makes printouts worth archiving.

## Library

```go
package main

import (
    "os"
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

    printout, err := tpl.Build(rows,
        sr.WithParam("period_start", time.Date(2005, 1, 1, 0, 0, 0, 0, time.UTC)),
        sr.WithBuildTime(time.Date(2026, 8, 4, 9, 12, 44, 0, time.UTC)),
        sr.StrictFonts(),
    )
    if err != nil {
        panic(err)
    }

    out, _ := os.Create("report.pdf")
    defer out.Close()
    if err := printout.WritePDF(out); err != nil {
        panic(err)
    }
}
```

Records may be `map[string]any` or structs; struct fields map to declared columns
by name, or by an `sr:"..."` tag. The CLI is a thin shell over this API.

## Running the examples

```bash
sr build -t example/sakila/sakila.kdl -d example/sakila/payments.jsonl -o sakila.pdf
```

Narrowed to one month, using the template's `date` parameters:

```bash
sr build -t example/sakila/sakila.kdl -d example/sakila/payments.jsonl -o june.pdf --param period_start=2005-06-01 --param period_end=2005-07-01
```

```bash
sr build -t example/invoices/invoices.kdl -d example/invoices/invoices.jsonl -o invoices.pdf
```

Between them the two templates use every node and property in the format.

[sakila.kdl](example/sakila/sakila.kdl) — a payment list grouped by customer
in two columns: every band type, a group with its own title and summary, column
header and footer, stretch fields, a floating element, six barcode types, an
embedded image and a referenced one, both kinds of cross-reference, conditional
outline entries, deferred page counts, justified text, and typed parameters.

[invoices.kdl](example/invoices/invoices.kdl) — invoices by region with line
items: an inline subreport with an `arg` and its own `records`, a group using
`keeptogether` with `minrows` and `mintailrows`, a `summary` with `swapfooter`,
an image with `embed=#false`, `iter="item"` against `iter="detail"`, and the `calc`
modes sakila leaves out. All twelve `calc` modes and every `iter` / `reset` scope
appear across the pair.

Both reference the [committed fonts](example/fonts/) by path, so they build
identically on any machine and work under `--strict-fonts`. Swap `file=` for
`typeface=` to use whatever the machine has instead.

## Reproducible output

Two runs of the same template over the same data produce byte-identical printouts
when `--build-time` is fixed and `--strict-fonts` is set. Without the first, the
run timestamp differs; without the second, output depends on which fonts are
installed. The printout header always records which font file each typeface
resolved to, so a difference is diagnosable from the artifact.

## License

MIT. See [LICENSE](LICENSE).

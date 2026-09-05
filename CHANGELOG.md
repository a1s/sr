# Changelog

## [0.1.0] - 2026-09-05

First release.

### Engine

- Banded report engine with `title`, `summary`, `header`, `footer`,
  and `detail` bands.
- Group scoping, keep-together, orphan/widow control, balanced columns.
- Deferred evaluation, so the first page can print the final page count.
- Running variables with twelve `calc` modes for data aggregation.

### Templates and data

- [KDL](https://kdl.dev) v2 templates.
- JSON or NDJSON input, with field types declared in the template.
- Sandboxed [Starlark](https://github.com/bazelbuild/starlark) expressions:
  no imports, no filesystem, no network.
- Typed report parameters, with defaults and default expressions.
- Two kinds of subreports: an inline `embedded` block with its own records,
  and a `template=` reference to a separate file that paginates onto pages
  of its own geometry.
- Embedded and referenced images, with relative paths in printouts.
- One- and two-dimensional barcodes, including QR, Aztec, Code 128.

### Printout and renderer

- Printout: a renderer-neutral, absolutely-positioned, archive-friendly document.
- Same printout round-trips to a byte-identical PDF.
- Printout embeds images and data blobs; fonts and non-embedded images
  are relative references, so a printout and the files it points at
  move as one tree.
- PDF output with no layout decisions of its own.
- Reproducible: same template + same data + `--build-time` + `--strict-fonts`
  produce same bytes on any machine.

### Command line

- `sr build`, `sr validate`, `sr render`, `sr inspect`, `sr version`.
- Document on stdout, diagnostics on stderr, three exit codes.

### Library

- `github.com/a1s/sr` for the engine, `github.com/a1s/sr/printout` for
  the document a renderer reads, `github.com/a1s/sr/pdf` for the renderer.

### Documentation

- The template format, the expression environment, layout and pagination,
  the printout format, PDF rendering, and the command line are each specified
  in [doc/](https://github.com/a1s/sr/tree/v0.1.0/doc).
- Two worked examples, [sakila](https://github.com/a1s/sr/tree/v0.1.0/example/sakila)
  and [invoices](https://github.com/a1s/sr/tree/v0.1.0/example/invoices),
  which between them use every node in the format and all twelve `calc` modes.

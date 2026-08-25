package cli

// usageAll is what "sr help" prints.
const usageAll = `sr -- structural reporting: a template plus data in, a PDF out

usage: sr <command> [flags]

commands:
  build     apply a template to data and write a PDF or a printout
  validate  check a template without data
  render    render a printout to PDF
  inspect   dump a printout as readable text
  version   print the version
  help      print this, or one command's flags

Run "sr help <command>" for a command's flags.
The full reference is doc/cli.md.
`

const usageBuild = `usage: sr build -t TEMPLATE [-d DATA] -o OUT [flags]

Applies a template to a sequence of records and writes a PDF or a printout.

  -t, --template FILE     template file (required)
  -d, --data FILE         JSON array or NDJSON file, "-" for standard input;
                          omit for a template with no records
  -o, --out FILE          output file, "-" for standard output (required)
      --format NAME       pdf, jsonl or cbor; defaults to the --out extension,
                          and is required when --out is "-"
      --param NAME=VALUE  a report parameter, parsed per its declared type;
                          repeatable
      --build-time TIME   RFC 3339; fixes BUILD_TIME
      --strict-fonts      resolve only fonts the template names by file or data
      --allow-overflow    record an oversized band as a warning, not an error
      --uncompressed      leave PDF streams uncompressed
  -v, --verbose           report host font diagnostics

The output format comes from --format, or from what --out ends in: .pdf,
.jsonl or .ndjson, .cbor. Any other extension is an error rather than a guess.

--build-time together with --strict-fonts makes the output byte-identical
on any machine.
`

const usageValidate = `usage: sr validate -t TEMPLATE [flags]

Loads and validates a template, resolves the fonts it declares, and reports
what it found. No data is read. The template may also be the sole argument.

      --param NAME=VALUE  a report parameter; repeatable
  -t, --template FILE     template file
      --strict-fonts      resolve only fonts the template names by file or data
  -q, --quiet             print nothing on success
  -v, --verbose           include host font diagnostics

A parameter with no value, no default and no defaultexpr is listed as
required rather than failing the check. Exit 1 means the template did not
load, or a font it declares did not resolve.
`

const usageRender = `usage: sr render PRINTOUT -o OUT [flags]

Renders a printout to PDF. The renderer does no layout, so
this produces the same PDF as the build that wrote the printout.

  -o, --out FILE          PDF file, "-" for standard output (required)
      --uncompressed      leave PDF streams uncompressed

The printout's extension chooses the encoding to read: .cbor is CBOR,
anything else NDJSON. It is a file rather than a stream, because
the paths inside it resolve against the directory it was read from.
`

const usageInspect = `usage: sr inspect PRINTOUT [flags]

Dumps a printout as text, one mark per line, in paint order,
and checks it against the format's invariants.

      --pages RANGE       pages to dump: 1,4-6,10- and -3 are all ranges
      --summary           the header only, no pages

A violated invariant is reported on standard error and makes the exit code 1.
`

const usageVersion = `usage: sr version

Prints the version, which is what a printout header's engine field carries.
`

// usageByCommand indexes the per-command help for "sr help <command>"
// and for a command's own -h.
var usageByCommand = map[string]string{
	"build":    usageBuild,
	"validate": usageValidate,
	"render":   usageRender,
	"inspect":  usageInspect,
	"version":  usageVersion,
	"help":     usageAll,
}

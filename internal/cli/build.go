package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/a1s/sr"
	"github.com/a1s/sr/pdf"
	"github.com/a1s/sr/printout"
)

// format is what a build writes.
type format int

// The three output formats, per doc/printout.md's encodings plus PDF.
const (
	formatPDF format = iota
	formatNDJSON
	formatCBOR
)

// cmdBuild applies a template to data.
func cmdBuild(env Env, args []string) error {
	var (
		template, dataPath, out string
		formatName, buildTime   string
		params                  paramList
		strictFonts             bool
		allowOverflow           bool
		uncompressed            bool
		verbose                 bool
	)
	set := newFlags("build")
	stringFlag(set, &template, "template", "t", "template file")
	stringFlag(set, &dataPath, "data", "d", "JSON or NDJSON records file")
	stringFlag(set, &out, "out", "o", "output file")
	stringFlag(set, &formatName, "format", "", "pdf, jsonl or cbor")
	stringFlag(set, &buildTime, "build-time", "", "RFC 3339 BUILD_TIME")
	paramFlag(set, &params)
	boolFlag(set, &strictFonts, "strict-fonts", "", "resolve only named font files")
	boolFlag(set, &allowOverflow, "allow-overflow", "", "warn instead of failing")
	boolFlag(set, &uncompressed, "uncompressed", "", "leave PDF streams uncompressed")
	boolFlag(set, &verbose, "verbose", "v", "report host font diagnostics")

	rest, err := parse(env, set, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("build", "unexpected argument %q; build takes flags only", rest[0])
	}
	if template == "" {
		return usagef("build", "--template is required")
	}
	if out == "" {
		return usagef("build", "--out is required")
	}
	kind, err := chooseFormat("build", formatName, out)
	if err != nil {
		return err
	}

	tpl, err := sr.LoadTemplate(template)
	if err != nil {
		return err
	}
	notes := newStream(env.Err)
	for _, warning := range tpl.Warnings() {
		notes.line("warning: %s", warning)
	}

	options := []sr.Option{}
	for _, name := range params.names() {
		options = append(options, sr.WithTextParam(name, params.values[name]))
	}
	if buildTime != "" {
		when, err := time.Parse(time.RFC3339, buildTime)
		if err != nil {
			return usagef("build",
				"--build-time %q is not an RFC 3339 time", buildTime)
		}
		options = append(options, sr.WithBuildTime(when))
	}
	if strictFonts {
		options = append(options, sr.StrictFonts())
	}
	if allowOverflow {
		options = append(options, sr.AllowOverflow())
	}
	if verbose {
		options = append(options, sr.WithDiagnostics(func(text string) {
			notes.line("font: %s", text)
		}))
	}

	doc, err := buildDocument(env, tpl, dataPath, options)
	if err != nil {
		return err
	}
	if err := writeDocument(env, doc, kind, out, uncompressed); err != nil {
		return err
	}
	report(notes, doc, out)
	return nil
}

// buildDocument reads the records, if there are any, and builds.
func buildDocument(env Env, tpl *sr.Template, dataPath string, options []sr.Option) (
	*printout.Printout, error) {
	if dataPath == "" {
		return tpl.Build(nil, options...)
	}
	if dataPath == "-" {
		return tpl.BuildJSON(env.In, options...)
	}
	file, err := os.Open(dataPath)
	if err != nil {
		return nil, err
	}
	defer file.Close() // nolint:errcheck
	return tpl.BuildJSON(file, options...)
}

// chooseFormat reads the output format from the flag, or from the extension.
//
// An extension that names no format is an error rather than a default:
// a printout written to a file called report.txt would look like a success.
func chooseFormat(command, name, out string) (format, error) {
	switch strings.ToLower(name) {
	case "pdf":
		return formatPDF, nil
	case "jsonl", "ndjson":
		return formatNDJSON, nil
	case "cbor":
		return formatCBOR, nil
	case "":
	default:
		return 0, usagef(command, "--format %q is not pdf, jsonl or cbor", name)
	}
	if out == "-" {
		return 0, usagef(command,
			"--format is required when --out is \"-\", since there is no extension to read it from")
	}
	switch strings.ToLower(filepath.Ext(out)) {
	case ".pdf":
		return formatPDF, nil
	case ".jsonl", ".ndjson":
		return formatNDJSON, nil
	case ".cbor":
		return formatCBOR, nil
	}
	return 0, usagef(command,
		"%s names no output format; use .pdf, .jsonl or .cbor, or say --format", out)
}

// writeDocument renders or serializes the printout to the output.
//
// The whole document is produced in memory before the file is opened,
// as pdf.WriteFile does and for the same reason: a write that fails
// must not have truncated yesterday's report first.
func writeDocument(env Env, doc *printout.Printout, kind format, out string, uncompressed bool) error {
	dir := ""
	if out != "-" {
		dir = filepath.Dir(out)
	}
	var body bytes.Buffer
	var err error
	switch kind {
	case formatPDF:
		var options []pdf.Option
		if uncompressed {
			options = append(options, pdf.Uncompressed())
		}
		err = pdf.Write(doc, &body, options...)
	case formatNDJSON:
		err = doc.WriteNDJSON(&body, dir)
	case formatCBOR:
		err = doc.WriteCBOR(&body, dir)
	}
	if err != nil {
		return err
	}
	if out == "-" {
		stream := newStream(env.Out)
		stream.write(body.Bytes())
		return stream.err
	}
	return os.WriteFile(out, body.Bytes(), 0o644)
}

// report writes the warnings and the one-line summary to standard error.
//
// It is standard error because standard output may be the document itself,
// and because a summary is about the run rather than part of the result.
func report(notes *stream, doc *printout.Printout, out string) {
	for _, warning := range doc.Header.Warnings {
		notes.line("warning: %s", describeWarning(warning))
	}
	where := out
	if out == "-" {
		where = "standard output"
	}
	parts := []string{
		count(len(doc.Pages), "page"),
		count(len(doc.Header.Fonts), "font"),
	}
	if len(doc.Header.Warnings) > 0 {
		parts = append(parts, count(len(doc.Header.Warnings), "warning"))
	}
	notes.line("%s: %s", where, strings.Join(parts, ", "))
}

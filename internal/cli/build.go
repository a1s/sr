package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/a1s/sr"
	"github.com/a1s/sr/pdf"
	"github.com/a1s/sr/printout"
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

	// Every command-line check runs before any work, so that a mistyped flag
	// is not reported after a template has loaded and warned about itself.
	if len(rest) > 0 {
		return usagef("build", "unexpected argument %q; build takes flags only", rest[0])
	}
	if template == "" {
		return usagef("build", "--template is required")
	}
	if out == "" {
		return usagef("build", "--out is required")
	}
	kind, mismatch, err := chooseFormat("build", formatName, out)
	if err != nil {
		return err
	}
	when, err := readBuildTime("build", buildTime)
	if err != nil {
		return err
	}

	notes := newStream(env.Err)
	if mismatch != "" {
		notes.line("warning: %s", mismatch)
	}

	tpl, err := sr.LoadTemplate(template)
	if err != nil {
		return err
	}
	if err := checkParams("build", &params, tpl.Info()); err != nil {
		return err
	}
	for _, warning := range tpl.Warnings() {
		notes.line("warning: %s", warning)
	}

	options := []sr.Option{}
	for _, name := range params.names() {
		options = append(options, sr.WithTextParam(name, params.values[name]))
	}
	if !when.IsZero() {
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
	body, err := encode(doc, kind, out, uncompressed)
	if err != nil {
		return err
	}
	if err := deliver(env, out, body); err != nil {
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

// readBuildTime parses --build-time. The zero time means it was not given.
func readBuildTime(command, text string) (time.Time, error) {
	if text == "" {
		return time.Time{}, nil
	}
	when, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return time.Time{}, usagef(command,
			"--build-time %q is not an RFC 3339 time", text)
	}
	return when, nil
}

// format is what a build writes.
type format int

// The three output formats, per doc/printout.md's encodings plus PDF.
const (
	formatPDF format = iota
	formatNDJSON
	formatCBOR
)

// formatExtensions maps a recognized extension to the format it names.
var formatExtensions = map[string]format{
	".pdf":    formatPDF,
	".jsonl":  formatNDJSON,
	".ndjson": formatNDJSON,
	".cbor":   formatCBOR,
}

// formatNames maps the --format spellings to the same formats.
var formatNames = map[string]format{
	"pdf":    formatPDF,
	"jsonl":  formatNDJSON,
	"ndjson": formatNDJSON,
	"cbor":   formatCBOR,
}

// chooseFormat reads the output format from the flag, or from the extension.
//
// An extension that names no format is an error rather than a default:
// a printout written to a file called report.txt would look like a success.
//
// The second result is a warning for the case the flag and a recognized
// extension disagree. It is the same trap the error above closes --
// a file nothing will read back, because a reader chooses the encoding from
// the extension -- so it cannot pass silently; and it is a warning rather
// than an error because overriding the extension is what the flag is for.
func chooseFormat(command, name, out string) (format, string, error) {
	flagged, given := formatNames[strings.ToLower(name)]
	if name != "" && !given {
		return 0, "", usagef(command, "--format %q is not pdf, jsonl or cbor", name)
	}
	if out == "-" {
		if !given {
			return 0, "", usagef(command,
				"--format is required when --out is \"-\", "+
					"since there is no extension to read it from")
		}
		return flagged, "", nil
	}
	suffix := strings.ToLower(filepath.Ext(out))
	named, known := formatExtensions[suffix]
	switch {
	case !given && !known:
		return 0, "", usagef(command,
			"%s names no output format; use .pdf, .jsonl or .cbor, or say --format", out)
	case !given:
		return named, "", nil
	case known && named != flagged:
		return flagged, contradiction(name, out), nil
	}
	return flagged, "", nil
}

// contradiction phrases the warning for a --format that disagrees with a
// recognized extension.
func contradiction(name, out string) string {
	return fmt.Sprintf(
		"--format %s writes %s to %s, and render and inspect read the encoding "+
			"from the extension, so nothing will read it back",
		strings.ToLower(name), strings.ToUpper(name), out)
}

// encode renders or serializes the printout.
//
// The whole document is produced in memory before anything is opened,
// as pdf.WriteFile does and for the same reason: a write that fails
// must not have truncated yesterday's report first. Serializing a printout
// can fail too, on a path it cannot make relative to the output.
func encode(doc *printout.Printout, kind format, out string, uncompressed bool) ([]byte, error) {
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
		return nil, err
	}
	return body.Bytes(), nil
}

// report writes the warnings and the one-line summary to standard error.
//
// It is standard error because standard output may be the document itself,
// and because a summary is about the run rather than part of the result.
func report(notes *stream, doc *printout.Printout, out string) {
	for _, warning := range doc.Header.Warnings {
		notes.line("warning: %s", describeWarning(warning))
	}
	parts := []string{
		count(len(doc.Pages), "page"),
		count(len(doc.Header.Fonts), "font"),
	}
	if len(doc.Header.Warnings) > 0 {
		parts = append(parts, count(len(doc.Header.Warnings), "warning"))
	}
	notes.line("%s: %s", destination(out), strings.Join(parts, ", "))
}

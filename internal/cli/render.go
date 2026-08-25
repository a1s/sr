package cli

import (
	"bytes"
	"os"

	"github.com/a1s/sr/pdf"
	"github.com/a1s/sr/printout"
)

// cmdRender renders a printout that was written earlier.
func cmdRender(env Env, args []string) error {
	var (
		out          string
		uncompressed bool
	)
	set := newFlags("render")
	stringFlag(set, &out, "out", "o", "PDF file")
	boolFlag(set, &uncompressed, "uncompressed", "", "leave PDF streams uncompressed")

	rest, err := parse(env, set, args)
	if err != nil {
		return err
	}
	switch {
	case len(rest) == 0:
		return usagef("render", "a printout file is required")
	case len(rest) > 1:
		return usagef("render", "one printout at a time; %q is a second", rest[1])
	case rest[0] == "-":
		return usagef("render",
			"a printout is read from a file, not from standard input: "+
				"the paths inside it resolve against the directory it came from")
	}
	if out == "" {
		return usagef("render", "--out is required")
	}

	doc, err := printout.ReadFile(rest[0])
	if err != nil {
		return err
	}
	var options []pdf.Option
	if uncompressed {
		options = append(options, pdf.Uncompressed())
	}
	// Rendered whole before the output is touched, which is why this does not
	// hand the writer to the renderer even when the output is a file.
	var body bytes.Buffer
	if err := pdf.Write(doc, &body, options...); err != nil {
		return err
	}
	if out == "-" {
		document := newStream(env.Out)
		document.write(body.Bytes())
		if document.err != nil {
			return document.err
		}
	} else if err := os.WriteFile(out, body.Bytes(), 0o644); err != nil {
		return err
	}
	notes := newStream(env.Err)
	for _, warning := range doc.Header.Warnings {
		notes.line("warning: %s", describeWarning(warning))
	}
	where := out
	if out == "-" {
		where = "standard output"
	}
	notes.line("%s: %s", where, count(len(doc.Pages), "page"))
	return nil
}

package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/a1s/sr"
)

// cmdValidate checks a template without data.
func cmdValidate(env Env, args []string) error {
	var (
		template    string
		params      paramList
		strictFonts bool
		quiet       bool
		verbose     bool
	)
	set := newFlags("validate")
	stringFlag(set, &template, "template", "t", "template file")
	paramFlag(set, &params)
	boolFlag(set, &strictFonts, "strict-fonts", "", "resolve only named font files")
	boolFlag(set, &quiet, "quiet", "q", "print nothing on success")
	boolFlag(set, &verbose, "verbose", "v", "include host font diagnostics")

	rest, err := parse(env, set, args)
	if err != nil {
		return err
	}
	switch {
	case len(rest) > 1:
		return usagef("validate", "one template at a time; %q is a second", rest[1])
	case len(rest) == 1 && template != "":
		return usagef("validate",
			"the template is given twice, as %q and as --template %q",
			rest[0], template)
	case len(rest) == 1:
		template = rest[0]
	case template == "":
		return usagef("validate", "--template is required")
	}

	tpl, err := sr.LoadTemplate(template)
	if err != nil {
		return err
	}
	options := []sr.Option{}
	for _, name := range params.names() {
		options = append(options, sr.WithTextParam(name, params.values[name]))
	}
	if strictFonts {
		options = append(options, sr.StrictFonts())
	}
	fonts, err := tpl.CheckFonts(options...)
	if err != nil {
		return err
	}

	if !quiet {
		out := newStream(env.Out)
		writeCheck(out, template, tpl.Info(), tpl.Warnings(), fonts, verbose)
		if out.err != nil {
			return out.err
		}
	}
	if len(fonts.Failures) > 0 {
		// The report already lists them; the error is what sets the exit code,
		// and it names the count so a quiet run says something.
		return fmt.Errorf("%s did not resolve", count(len(fonts.Failures), "font"))
	}
	return nil
}

// writeCheck prints the template report of doc/cli.md.
func writeCheck(
	out *stream,
	path string,
	info sr.Info,
	loaded []string,
	fonts *sr.FontCheck,
	verbose bool,
) {
	out.line("template %s", path)
	line := []string{}
	if info.Name != "" {
		line = append(line, "report "+strconv.Quote(info.Name))
	}
	if info.Version != "" {
		line = append(line, "version "+info.Version)
	}
	if info.Author != "" {
		line = append(line, "by "+info.Author)
	}
	if len(line) > 0 {
		out.line("  %s", strings.Join(line, " "))
	}
	if info.Description != "" {
		out.line("  %s", info.Description)
	}
	out.line("  %s", describeGeometry(info.Page))

	counts := []string{count(info.Columns, "column")}
	for _, item := range []struct {
		number int
		noun   string
	}{
		{len(info.Groups), "group"},
		{len(info.Members), "member"},
		{len(info.Variables), "variable"},
		{len(info.Fonts), "font"},
		{len(info.Data), "data blob"},
	} {
		if item.number > 0 {
			counts = append(counts, count(item.number, item.noun))
		}
	}
	out.line("  %s", strings.Join(counts, ", "))

	if len(info.Parameters) > 0 {
		out.line("parameters")
		items := make([][]string, 0, len(info.Parameters))
		for _, param := range info.Parameters {
			items = append(items, paramFields(param))
		}
		writeRows(out, items)
	}
	if len(fonts.Fonts) > 0 {
		out.line("fonts")
		items := make([][]string, 0, len(fonts.Fonts))
		for _, entry := range fonts.Fonts {
			items = append(items, fontFields(entry))
		}
		writeRows(out, items)
	}
	problems := append([]string{}, loaded...)
	problems = append(problems, fonts.Warnings...)
	for _, node := range info.Subreports {
		problems = append(problems, node+
			": subreports are not implemented yet, so this template validates but will not build")
	}
	problems = append(problems, fonts.Failures...)
	if len(problems) > 0 {
		out.line("warnings")
		for _, problem := range problems {
			out.line("  %s", problem)
		}
	}
	if verbose && len(fonts.Diagnostics) > 0 {
		out.line("diagnostics")
		for _, diagnostic := range fonts.Diagnostics {
			out.line("  %s", diagnostic)
		}
	}
	if len(fonts.Failures) == 0 {
		out.line("ok")
	}
}

// paramFields renders one declared parameter.
//
// The parameter's name, type, and where its value is to come from.
func paramFields(param sr.Parameter) []string {
	parts := []string{param.Name, param.Type}
	switch {
	case param.HasDefault:
		parts = append(parts, "default "+strconv.Quote(param.Default))
	case param.HasDefaultExpr:
		parts = append(parts, "defaultexpr")
	default:
		parts = append(parts, "required")
	}
	if param.Prompt {
		parts = append(parts, "prompt")
	}
	return parts
}

// writeRows prints an indented, padded table.
func writeRows(out *stream, items [][]string) {
	for _, line := range rows(items) {
		out.line("  %s", line)
	}
}

package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// result is what one command line did.
type result struct {
	out  string
	err  string
	code int
}

// run executes one command line with buffers for the three streams,
// which is the whole point of Run taking an Env.
func run(test *testing.T, stdin string, args ...string) result {
	test.Helper()
	var out, errs bytes.Buffer
	code := Run(Env{
		Args: args,
		In:   strings.NewReader(stdin),
		Out:  &out,
		Err:  &errs,
	})
	return result{out: out.String(), err: errs.String(), code: code}
}

// fontFile is the committed regular face, as an absolute path with forward
// slashes so that a template written into a temporary directory resolves it
// under strict fonts on every platform.
func fontFile(test *testing.T) string {
	test.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "example", "fonts", "Go-Regular.ttf"))
	if err != nil {
		test.Fatal(err)
	}
	return filepath.ToSlash(abs)
}

// fixture writes a template and a dataset into a temporary directory
// and returns the directory.
//
// The template is the smallest one that exercises a parameter,
// a record member and a font: two rows print as two lines of text.
func fixture(test *testing.T) (dir, template, data string) {
	test.Helper()
	dir = test.TempDir()
	template = filepath.Join(dir, "tiny.kdl")
	source := fmt.Sprintf(`report name="Tiny" version="1" author="als" {
  parameter "note" default="hello"
  parameter "wanted" type="int"
  records { member "n" type="int" }
  font "body" file=%q size=10
  layout pagesize="A5" leftmargin=20 rightmargin=20 topmargin=20 bottommargin=20 {
    style font="body" color="black"
    title height=12 {
      field expr="note" left=0 width=200 height=12
    }
    detail height=12 {
      field expr="n" format="row %%d" left=0 width=200 height=12
    }
  }
}`, fontFile(test))
	if err := os.WriteFile(template, []byte(source), 0o644); err != nil {
		test.Fatal(err)
	}
	data = filepath.Join(dir, "rows.jsonl")
	if err := os.WriteFile(data, []byte("{\"n\": 1}\n{\"n\": 2}\n"), 0o644); err != nil {
		test.Fatal(err)
	}
	return dir, template, data
}

// read is os.ReadFile with the test's error handling.
func read(test *testing.T, path string) []byte {
	test.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		test.Fatal(err)
	}
	return raw
}

// The four exit codes a caller can see, and the two streams they arrive on.
func TestHelpAndVersion(test *testing.T) {
	cases := []struct {
		name string
		args []string
		code int
		// out is a fragment expected on standard output, err one on standard error.
		out, err string
	}{
		{name: "no command", args: nil, code: ExitUsage, err: "a command is required"},
		{name: "help", args: []string{"help"}, code: ExitOK, out: "usage: sr <command>"},
		{name: "--help", args: []string{"--help"}, code: ExitOK, out: "usage: sr <command>"},
		{name: "help build", args: []string{"help", "build"}, code: ExitOK,
			out: "usage: sr build"},
		{name: "build -h", args: []string{"build", "-h"}, code: ExitOK,
			out: "usage: sr build"},
		{name: "build --help", args: []string{"build", "--help"}, code: ExitOK,
			out: "--allow-overflow"},
		{name: "version", args: []string{"version"}, code: ExitOK, out: "sr "},
		{name: "--version", args: []string{"--version"}, code: ExitOK, out: "sr "},
		{name: "unknown command", args: []string{"frobnicate"}, code: ExitUsage,
			err: `unknown command "frobnicate"`},
		{name: "help for an unknown command", args: []string{"help", "frobnicate"},
			code: ExitUsage, err: `unknown command "frobnicate"`},
		{name: "a flag where a command belongs", args: []string{"--strict-fonts"},
			code: ExitUsage, err: "flags come after the command"},
		{name: "an unknown flag", args: []string{"build", "--nope"},
			code: ExitUsage, err: "not defined"},
	}
	for _, item := range cases {
		test.Run(item.name, func(test *testing.T) {
			got := run(test, "", item.args...)
			if got.code != item.code {
				test.Errorf("exit = %d, want %d; stderr %q",
					got.code, item.code, got.err)
			}
			if item.out != "" && !strings.Contains(got.out, item.out) {
				test.Errorf("stdout = %q, want it to contain %q", got.out, item.out)
			}
			if item.err != "" && !strings.Contains(got.err, item.err) {
				test.Errorf("stderr = %q, want it to contain %q", got.err, item.err)
			}
		})
	}
	// A usage error points at the command's own help, not the general one.
	got := run(test, "", "build", "--nope")
	if !strings.Contains(got.err, `sr help build`) {
		test.Errorf("stderr = %q, want it to name \"sr help build\"", got.err)
	}
}

// A build writes the document to standard output and everything about the run
// to standard error, so that --out - can be piped.
func TestBuildStreams(test *testing.T) {
	_, template, data := fixture(test)
	got := run(test, "", "build", "-t", template, "-d", data,
		"-o", "-", "--format", "jsonl", "--strict-fonts",
		"--param", "wanted=1", "--build-time", "2026-08-04T09:12:44Z")
	if got.code != ExitOK {
		test.Fatalf("exit = %d, stderr %q", got.code, got.err)
	}
	if !strings.HasPrefix(got.out, `{"sr":1,"kind":"header"`) {
		test.Errorf("stdout starts %q", got.out[:min(60, len(got.out))])
	}
	if lines := strings.Count(got.out, "\n"); lines != 2 {
		test.Errorf("NDJSON lines = %d, want 2: a header and one page", lines)
	}
	if want := "standard output: 1 page, 1 font\n"; got.err != want {
		test.Errorf("stderr = %q, want %q", got.err, want)
	}
}

// The three output formats, and the extension that names none of them.
func TestBuildFormats(test *testing.T) {
	dir, template, data := fixture(test)
	common := []string{"build", "-t", template, "-d", data,
		"--strict-fonts", "--param", "wanted=1"}

	for _, item := range []struct {
		name, file, prefix string
	}{
		{name: "pdf", file: "out.pdf", prefix: "%PDF-"},
		{name: "ndjson", file: "out.srp.jsonl", prefix: `{"sr":1`},
		{name: "ndjson by its other extension", file: "out.ndjson", prefix: `{"sr":1`},
	} {
		test.Run(item.name, func(test *testing.T) {
			path := filepath.Join(dir, item.file)
			got := run(test, "", append(append([]string{}, common...), "-o", path)...)
			if got.code != ExitOK {
				test.Fatalf("exit = %d, stderr %q", got.code, got.err)
			}
			if body := read(test, path); !bytes.HasPrefix(body, []byte(item.prefix)) {
				test.Errorf("%s starts %q, want %q", item.file, body[:8], item.prefix)
			}
		})
	}

	// CBOR is a sequence of maps, so the first byte is a map header.
	test.Run("cbor", func(test *testing.T) {
		path := filepath.Join(dir, "out.srp.cbor")
		got := run(test, "", append(append([]string{}, common...), "-o", path)...)
		if got.code != ExitOK {
			test.Fatalf("exit = %d, stderr %q", got.code, got.err)
		}
		body := read(test, path)
		if len(body) == 0 || body[0]&0xe0 != 0xa0 {
			test.Errorf("first CBOR byte = %#x, want a map header", body[0])
		}
	})

	// --format overrides the extension, which is what makes an unusual
	// name usable rather than a reason to rename the file.
	test.Run("format overrides the extension", func(test *testing.T) {
		path := filepath.Join(dir, "out.pdf")
		got := run(test, "", append(append([]string{}, common...),
			"-o", path, "--format", "jsonl")...)
		if got.code != ExitOK {
			test.Fatalf("exit = %d, stderr %q", got.code, got.err)
		}
		if body := read(test, path); !bytes.HasPrefix(body, []byte(`{"sr":1`)) {
			test.Errorf("body starts %q", body[:8])
		}
	})
}

// Every way of getting the command line wrong, and the code it produces.
func TestBuildUsage(test *testing.T) {
	dir, template, data := fixture(test)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "no template", args: []string{"build", "-o", "x.pdf"},
			want: "--template is required"},
		{name: "no output", args: []string{"build", "-t", template},
			want: "--out is required"},
		{name: "a positional argument", args: []string{"build", template, "-o", "x.pdf"},
			want: "build takes flags only"},
		{name: "an extension that names no format",
			args: []string{"build", "-t", template, "-o", filepath.Join(dir, "out.txt")},
			want: "names no output format"},
		{name: "an unknown format",
			args: []string{"build", "-t", template, "-o", "x.pdf", "--format", "ps"},
			want: `--format "ps" is not pdf, jsonl or cbor`},
		{name: "stdout without a format",
			args: []string{"build", "-t", template, "-o", "-"},
			want: "--format is required"},
		{name: "a parameter without a value",
			args: []string{"build", "-t", template, "-o", "x.pdf", "--param", "note"},
			want: "expects NAME=VALUE"},
		{name: "a parameter given twice",
			args: []string{"build", "-t", template, "-o", "x.pdf",
				"--param", "note=a", "--param", "note=b"},
			want: "given twice"},
		{name: "a build time that is not RFC 3339",
			args: []string{"build", "-t", template, "-o", "x.pdf",
				"-d", data, "--build-time", "yesterday"},
			want: "not an RFC 3339 time"},
	}
	for _, item := range cases {
		test.Run(item.name, func(test *testing.T) {
			got := run(test, "", item.args...)
			if got.code != ExitUsage {
				test.Errorf("exit = %d, want %d; stderr %q",
					got.code, ExitUsage, got.err)
			}
			if !strings.Contains(got.err, item.want) {
				test.Errorf("stderr = %q, want it to contain %q", got.err, item.want)
			}
		})
	}
}

// A run that fails on its work, rather than on its arguments, exits 1.
func TestBuildFailures(test *testing.T) {
	dir, template, _ := fixture(test)
	out := filepath.Join(dir, "out.pdf")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "a template that is not there",
			args: []string{"build", "-t", filepath.Join(dir, "nope.kdl"), "-o", out},
			want: "nope.kdl"},
		{name: "a dataset that is not there",
			args: []string{"build", "-t", template, "-o", out,
				"-d", filepath.Join(dir, "nope.jsonl")},
			want: "nope.jsonl"},
		{name: "a required parameter with no value",
			args: []string{"build", "-t", template, "-o", out},
			want: `parameter "wanted" is required`},
	}
	for _, item := range cases {
		test.Run(item.name, func(test *testing.T) {
			got := run(test, "", item.args...)
			if got.code != ExitFail {
				test.Errorf("exit = %d, want %d; stderr %q",
					got.code, ExitFail, got.err)
			}
			if !strings.Contains(got.err, item.want) {
				test.Errorf("stderr = %q, want it to contain %q", got.err, item.want)
			}
		})
	}
	if _, err := os.Stat(out); err == nil {
		test.Error("a failed build must not have written the output file")
	}
}

// Records arrive on standard input for "-", and parameters as text.
func TestBuildStdinAndParams(test *testing.T) {
	dir, template, _ := fixture(test)
	path := filepath.Join(dir, "out.srp.jsonl")
	got := run(test, "{\"n\": 7}\n",
		"build", "-t", template, "-d", "-", "-o", path,
		"--param", "note=from the command line", "--param", "wanted=3",
		"--strict-fonts")
	if got.code != ExitOK {
		test.Fatalf("exit = %d, stderr %q", got.code, got.err)
	}
	body := string(read(test, path))
	for _, want := range []string{"from the command line", "row 7"} {
		if !strings.Contains(body, want) {
			test.Errorf("the printout does not carry %q", want)
		}
	}
}

// Building a printout and rendering it later gives the same PDF as building
// the PDF directly, which is what makes a printout worth archiving.
func TestPrintoutRoundTrip(test *testing.T) {
	dir, template, data := fixture(test)
	common := []string{"-t", template, "-d", data, "--strict-fonts",
		"--build-time", "2026-08-04T09:12:44Z", "--param", "wanted=1"}
	direct := filepath.Join(dir, "direct.pdf")
	got := run(test, "", append([]string{"build"}, append(common, "-o", direct)...)...)
	if got.code != ExitOK {
		test.Fatalf("build to PDF: exit %d, stderr %q", got.code, got.err)
	}
	printout := filepath.Join(dir, "doc.srp.jsonl")
	got = run(test, "", append([]string{"build"}, append(common, "-o", printout)...)...)
	if got.code != ExitOK {
		test.Fatalf("build to a printout: exit %d, stderr %q", got.code, got.err)
	}
	// The flags follow the positional argument here on purpose:
	// the flag package alone would leave them unparsed.
	rendered := filepath.Join(dir, "rendered.pdf")
	got = run(test, "", "render", printout, "-o", rendered)
	if got.code != ExitOK {
		test.Fatalf("render: exit %d, stderr %q", got.code, got.err)
	}
	if want := "1 page"; !strings.Contains(got.err, want) {
		test.Errorf("stderr = %q, want it to contain %q", got.err, want)
	}
	if !bytes.Equal(read(test, direct), read(test, rendered)) {
		test.Error("rendering the printout gave a different PDF than building one directly")
	}
}

// A fixed build time and strict fonts make the output byte-identical.
func TestReproducible(test *testing.T) {
	dir, template, data := fixture(test)
	paths := []string{filepath.Join(dir, "one.pdf"), filepath.Join(dir, "two.pdf")}
	for _, path := range paths {
		got := run(test, "", "build", "-t", template, "-d", data, "-o", path,
			"--strict-fonts", "--build-time", "2026-08-04T09:12:44Z",
			"--param", "wanted=1")
		if got.code != ExitOK {
			test.Fatalf("exit = %d, stderr %q", got.code, got.err)
		}
	}
	if !bytes.Equal(read(test, paths[0]), read(test, paths[1])) {
		test.Error("two runs with a fixed build time produced different files")
	}
}

// Rendering reads a file, because the paths inside a printout resolve
// against the directory it came from.
func TestRenderUsage(test *testing.T) {
	dir, template, data := fixture(test)
	printout := filepath.Join(dir, "doc.srp.jsonl")
	if got := run(test, "", "build", "-t", template, "-d", data, "-o", printout,
		"--strict-fonts", "--param", "wanted=1"); got.code != ExitOK {
		test.Fatalf("exit = %d, stderr %q", got.code, got.err)
	}
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{name: "no printout", args: []string{"render", "-o", "x.pdf"},
			code: ExitUsage, want: "a printout file is required"},
		{name: "two printouts",
			args: []string{"render", printout, printout, "-o", "x.pdf"},
			code: ExitUsage, want: "one printout at a time"},
		{name: "standard input", args: []string{"render", "-", "-o", "x.pdf"},
			code: ExitUsage, want: "not from standard input"},
		{name: "no output", args: []string{"render", printout},
			code: ExitUsage, want: "--out is required"},
		{name: "a printout that is not there",
			args: []string{"render", filepath.Join(dir, "nope.srp.jsonl"), "-o", "x.pdf"},
			code: ExitFail, want: "nope.srp.jsonl"},
	}
	for _, item := range cases {
		test.Run(item.name, func(test *testing.T) {
			got := run(test, "", item.args...)
			if got.code != item.code {
				test.Errorf("exit = %d, want %d; stderr %q",
					got.code, item.code, got.err)
			}
			if !strings.Contains(got.err, item.want) {
				test.Errorf("stderr = %q, want it to contain %q", got.err, item.want)
			}
		})
	}
	// A PDF on standard output is unambiguous, so it needs no format flag.
	got := run(test, "", "render", printout, "-o", "-")
	if got.code != ExitOK {
		test.Fatalf("exit = %d, stderr %q", got.code, got.err)
	}
	if !strings.HasPrefix(got.out, "%PDF-") {
		test.Errorf("stdout starts %q", got.out[:min(8, len(got.out))])
	}
}

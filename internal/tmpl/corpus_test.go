package tmpl

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The corpus lives beside the engine rather than inside a test file
// so that adding a validation case is writing a template, and so that
// the other implementation of this specification can read the same cases.
// Its convention is testdata/templates/README.md.
const corpusDir = "../../testdata/templates"

// expectedFailures are the `expect:` markers in broken/ that this engine
// knowingly does not meet, by case and then by the marker's text,
// each with the reason.
//
// The corpus is shared with s-reports, so a case is not edited to suit
// this engine. A marker listed here is logged when it fails rather than
// failing the test, and fails the test once it passes, so that the entry
// goes when the engine catches up.
var expectedFailures = map[string]map[string]string{
	"barcode-charset.kdl": {
		"2of5i encodes characters rather than bytes, so it takes no eci": noECI,
	},
	"expression-syntax.kdl": {"there are no lambdas": keepsLambda},
}

// The reasons the markers in expectedFailures fail here.
const (
	noECI = "eci is not implemented: github.com/boombuler/barcode " +
		"writes no ECI, so it is an unknown property here"
	keepsLambda = "this engine keeps Starlark's lambda, " +
		"which doc/expressions.md does not take away"
)

// expectations are the markers a case states in its header comment.
type expectations struct {
	errors   []string
	warnings []string
}

// readExpectations reads the `expect:` and `warn:` markers
// from the comment block a template opens with.
//
// The block ends at the first line that is not a comment, so
// a marker further down the file is text rather than an expectation.
func readExpectations(test *testing.T, path string) expectations {
	test.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		test.Fatalf("reading %s: %v", path, err)
	}
	var want expectations
	for _, line := range strings.Split(string(source), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "//") {
			break
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "//"))
		switch {
		case strings.HasPrefix(rest, "expect:"):
			want.errors = append(want.errors,
				strings.TrimSpace(strings.TrimPrefix(rest, "expect:")))
		case strings.HasPrefix(rest, "warn:"):
			want.warnings = append(want.warnings,
				strings.TrimSpace(strings.TrimPrefix(rest, "warn:")))
		}
	}
	return want
}

// cases lists the templates in one corpus directory, leaving out the
// `.inc.kdl` files another case pulls in through `subreport template=`.
func cases(test *testing.T, dir string) []string {
	test.Helper()
	entries, err := os.ReadDir(filepath.Join(corpusDir, dir))
	if err != nil {
		test.Fatalf("reading %s: %v", dir, err)
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".kdl") || strings.HasSuffix(name, ".inc.kdl") {
			continue
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		test.Fatalf("no cases in %s", dir)
	}
	return out
}

// messages reduces a load's outcome to the diagnostics it produced.
func messages(report *Report, err error) (errs, warns []string) {
	if err != nil {
		var list DiagnosticList
		if diags, ok := err.(DiagnosticList); ok {
			list = diags
		}
		if list == nil {
			errs = append(errs, err.Error())
		}
		for _, diag := range list {
			errs = append(errs, diag.Message)
		}
	}
	if report != nil {
		for _, diag := range report.Warnings {
			warns = append(warns, diag.Message)
		}
	}
	return errs, warns
}

func containsAny(haystack []string, needle string) bool {
	for _, item := range haystack {
		if strings.Contains(item, needle) {
			return true
		}
	}
	return false
}

// TestBrokenTemplates holds every case in broken/ to the rule it isolates.
//
// A case must fail, and must fail with the message it names: a template
// refused for some other reason is not testing what it says it is.
func TestBrokenTemplates(test *testing.T) {
	names := cases(test, "broken")
	for name := range expectedFailures {
		if !slices.Contains(names, name) {
			test.Errorf("expectedFailures lists %s, which is not a case", name)
		}
	}
	for _, name := range names {
		test.Run(name, func(test *testing.T) {
			path := filepath.Join(corpusDir, "broken", name)
			want := readExpectations(test, path)
			if len(want.errors) == 0 {
				test.Fatalf("%s states no expect: marker", name)
			}
			for expected := range expectedFailures[name] {
				if !slices.Contains(want.errors, expected) {
					test.Errorf("expectedFailures lists %q, which is not a marker here",
						expected)
				}
			}
			report, err := Load(path)
			errs, _ := messages(report, err)
			if len(errs) == 0 {
				test.Fatalf("loaded clean; want %q", want.errors[0])
			}
			for _, expected := range want.errors {
				found := containsAny(errs, expected)
				reason, known := expectedFailures[name][expected]
				switch {
				case known && found:
					test.Errorf("%q now passes; remove it from expectedFailures",
						expected)
				case known:
					test.Logf("expected failure: no diagnostic contains %q: %s",
						expected, reason)
				case !found:
					test.Errorf("no diagnostic contains %q; got %q",
						expected, errs)
				}
			}
		})
	}
}

// TestValidTemplates holds every case in valid/ to loading, and to saying
// exactly what it says it says.
//
// A file naming no warning is a file that must warn about nothing, which
// is what keeps a new diagnostic from appearing everywhere unnoticed.
func TestValidTemplates(test *testing.T) {
	for _, name := range cases(test, "valid") {
		test.Run(name, func(test *testing.T) {
			path := filepath.Join(corpusDir, "valid", name)
			want := readExpectations(test, path)
			report, err := Load(path)
			errs, warns := messages(report, err)
			if len(errs) > 0 {
				test.Fatalf("did not load: %q", errs)
			}
			for _, expected := range want.warnings {
				if !containsAny(warns, expected) {
					test.Errorf("no warning contains %q; got %q", expected, warns)
				}
			}
			if len(warns) != len(want.warnings) {
				test.Errorf("got %d warnings, want %d: %q",
					len(warns), len(want.warnings), warns)
			}
		})
	}
}

// TestExamplesLoadSilently holds the committed examples to loading clean.
//
// They are what a reader copies from, so a warning in one
// is a warning the next template inherits.
func TestExamplesLoadSilently(test *testing.T) {
	paths, err := filepath.Glob("../../example/*/*.kdl")
	if err != nil || len(paths) == 0 {
		test.Fatalf("no example templates found: %v", err)
	}
	for _, path := range paths {
		test.Run(filepath.Base(path), func(test *testing.T) {
			report, err := Load(path)
			errs, warns := messages(report, err)
			if len(errs) > 0 {
				test.Fatalf("did not load: %q", errs)
			}
			if len(warns) > 0 {
				test.Errorf("loaded with warnings: %q", warns)
			}
		})
	}
}

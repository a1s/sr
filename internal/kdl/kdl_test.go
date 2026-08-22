package kdl

import "testing"

func TestParseReferenceTemplates(test *testing.T) {
	for _, path := range []string{
		"../../example/sakila/sakila.kdl",
		"../../example/invoices/invoices.kdl",
	} {
		nodes, err := ParseFile(path)
		if err != nil {
			test.Fatalf("%s: %v", path, err)
		}
		if len(nodes) != 1 || nodes[0].Name != "report" {
			test.Fatalf("%s: want one report node, got %d", path, len(nodes))
		}
		test.Logf("%s: report has %d props, %d children", path,
			len(nodes[0].PropNames()), len(nodes[0].Children))
	}
}

package cli

import "os"

// deliver writes a finished document to a path, or to standard output for "-".
//
// The document arrives complete, which is the rule both commands that produce
// one follow: nothing is opened until there is something to put in it.
func deliver(env Env, out string, body []byte) error {
	if out == "-" {
		document := newStream(env.Out)
		document.write(body)
		return document.err
	}
	return os.WriteFile(out, body, 0o644)
}

// destination names the output in a message.
func destination(out string) string {
	if out == "-" {
		return "standard output"
	}
	return out
}

// Command sr applies a report template to data and writes a PDF.
//
// It is a shell over the library and decides nothing on its own.
// Its reference is doc/cli.md.
//
//	sr build -t sakila.kdl -d payments.jsonl -o report.pdf
//	sr validate sakila.kdl
//	sr render report.srp.jsonl -o report.pdf
//	sr inspect report.srp.jsonl
package main

import (
	"os"

	"github.com/a1s/sr/internal/cli"
)

func main() {
	os.Exit(cli.Run(cli.Env{
		Args: os.Args[1:],
		In:   os.Stdin,
		Out:  os.Stdout,
		Err:  os.Stderr,
	}))
}

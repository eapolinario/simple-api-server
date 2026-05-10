/*
Copyright 2026 Eduardo Apolinario.
*/

// Binary simple-apiserver serves the tasks.example.com API group
// through the Kubernetes aggregation layer. Wiring lives in
// pkg/apiserver; this file owns only option parsing and process
// lifecycle (signal handling, exit codes, logger init).
package main

import (
	"os"

	"k8s.io/component-base/cli"
)

func main() {
	cmd := NewCommand(os.Stdout, os.Stderr)
	os.Exit(cli.Run(cmd))
}

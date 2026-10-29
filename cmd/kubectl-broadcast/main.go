package main

import (
	"os"

	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/kumarprabhashanand/pod-broadcast/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], genericiooptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr}))
}

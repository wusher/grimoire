package main

import (
	"context"
	"os"

	"github.com/wusher/grimoire/internal/grimoire"
)

func main() {
	cli := grimoire.NewCLI(os.Stdin, os.Stdout, os.Stderr)
	os.Exit(cli.Run(context.Background(), os.Args[1:]))
}

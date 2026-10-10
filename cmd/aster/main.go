package main

import (
	"context"
	"fmt"
	"os"

	"github.com/laojianzi/aster/internal/app"
	"github.com/laojianzi/aster/internal/credentialvault"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == credentialvault.HelperFlag {
		os.Exit(credentialvault.Serve(os.Stdin, os.Stdout))
	}
	if err := app.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

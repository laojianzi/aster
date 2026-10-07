package main

import (
	"context"
	"fmt"
	"os"

	"github.com/laojianzi/aster/internal/app"
)

func main() {
	if err := app.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

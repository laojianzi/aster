package app

import (
	"context"
	"fmt"
)

func Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fmt.Println("Aster bootstrap: native desktop initialization pending")
	return nil
}

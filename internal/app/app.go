package app

import (
	"context"
	"fmt"

	"github.com/egoist/mygo"
	uiworkbench "github.com/laojianzi/aster/internal/ui"
)

func Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	mygo.App.WhenReady(uiworkbench.Open)
	if err := mygo.App.Run(); err != nil {
		return fmt.Errorf("run native application: %w", err)
	}
	return nil
}

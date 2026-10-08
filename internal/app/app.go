package app

import (
	"context"
	"fmt"

	"github.com/egoist/mygo"
	uiworkbench "github.com/laojianzi/aster/internal/ui"
)
func Run(ctx context.Context)error{
	if err:=ctx.Err();err!=nil{return err}
	var workbench *uiworkbench.Workbench
	mygo.App.WhenReady(func(){workbench=uiworkbench.Open(ctx)})
	err:=mygo.App.Run()
	if workbench!=nil{workbench.Close()}
	if err!=nil{return fmt.Errorf("run native application: %w",err)}
	return nil
}

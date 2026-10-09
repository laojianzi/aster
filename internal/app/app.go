package app

import (
	"context"
	"fmt"
	"os"

	"github.com/egoist/mygo"
	uiworkbench "github.com/laojianzi/aster/internal/ui"
	"github.com/laojianzi/aster/internal/workspace"
)

const maxWorkspaces = 4

func Run(parent context.Context) error {
	if err := parent.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	smoke := os.Getenv("ASTER_NATIVE_SMOKE") == "1"
	var windows *workspace.Manager
	var startupErr error
	// This probe is owned by the same main thread as window callbacks.
	probe := newWindowProbe()
	windows = workspace.New(ctx, maxWorkspaces, func(child context.Context, slot int, closed func()) (workspace.Resource, error) {
		opts := uiworkbench.WindowOptions{Number: slot, NewWorkspace: windows.Open, Closed: func() {
			if smoke {
				probe.Closed(slot)
			}
			closed()
		}}
		if smoke {
			opts.SmokeFrame = func(frames int, closeWindow func()) { probe.Frame(slot, frames, closeWindow) }
		}
		return uiworkbench.Open(child, opts), nil
	}, func() {
		if smoke && probe.Complete() {
			fmt.Println("ASTER_NATIVE_SMOKE_OK")
		}
		mygo.App.Quit()
	})
	mygo.App.WhenReady(func() {
		startupErr = windows.Open()
		if startupErr == nil && smoke {
			startupErr = windows.Open()
		}
		if startupErr != nil {
			mygo.App.Quit()
		}
	})
	quitWatcherDone := make(chan struct{})
	go func() { defer close(quitWatcherDone); <-ctx.Done(); mygo.App.Quit() }()
	err := mygo.App.Run()
	cancel()
	<-quitWatcherDone
	windows.Shutdown() // never wait for worker shutdown on the running UI loop
	if startupErr != nil {
		return fmt.Errorf("open native workspace: %w", startupErr)
	}
	if err != nil {
		return fmt.Errorf("run native application: %w", err)
	}
	return nil
}

// The actual-OS smoke probe proves two windows paint, then closes the first and
// requires fresh frames from the survivor before closing it. It reads no cluster
// config and is not OS input, IME, accessibility or GPU-driver qualification.
type windowProbe struct {
	frames           map[int]int
	closing          map[int]bool
	closed           map[int]bool
	survivorBaseline int
	survived         bool
}

func newWindowProbe() *windowProbe {
	return &windowProbe{frames: map[int]int{}, closing: map[int]bool{}, closed: map[int]bool{}}
}
func (p *windowProbe) Frame(slot, frame int, closeWindow func()) {
	if p.frames[slot] == 0 && frame > 0 {
		fmt.Printf("ASTER_NATIVE_WINDOW_RENDERED:%d\n", slot)
	}
	p.frames[slot] = frame
	if slot == 1 && p.frames[1] > 0 && p.frames[2] > 0 && !p.closing[1] {
		p.closing[1] = true
		closeWindow()
	}
	// Two more observed frames are required, not merely an already queued frame.
	if slot == 2 && p.closed[1] && frame > p.survivorBaseline+1 && !p.closing[2] {
		p.survived = true
		p.closing[2] = true
		fmt.Println("ASTER_NATIVE_OTHER_WINDOW_LIVE")
		closeWindow()
	}
}
func (p *windowProbe) Closed(slot int) {
	p.closed[slot] = true
	if slot == 1 {
		p.survivorBaseline = p.frames[2]
	}
}
func (p *windowProbe) Complete() bool {
	return p.closed[1] && p.closed[2] && p.survived && p.frames[1] > 0 && p.frames[2] > 0
}

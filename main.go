package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"hookspot/cmd"
)

func main() {
	err := executeWithSignalContext(cmd.ExecuteContext)
	if err != nil {
		if exitCode := cmd.HandleError(os.Stderr, err); exitCode != 0 {
			os.Exit(exitCode)
		}
	}
}

func executeWithSignalContext(execute func(context.Context) error) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	watcherDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		stop()
		close(watcherDone)
	}()

	err := execute(ctx)
	stop()
	<-watcherDone
	return err
}

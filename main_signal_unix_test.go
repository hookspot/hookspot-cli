//go:build !windows

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

const signalFixtureEnvironment = "HOOKSPOT_TEST_SIGNAL_MODE"

func TestSignalFixture(t *testing.T) {
	mode := os.Getenv(signalFixtureEnvironment)
	if mode == "" {
		return
	}
	err := executeWithSignalContext(func(ctx context.Context) error {
		fmt.Fprintln(os.Stdout, "ready")
		if mode == "normal" {
			return nil
		}
		<-ctx.Done()
		fmt.Fprintln(os.Stdout, "canceled")
		if mode == "blocked" {
			select {}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFirstInterruptAllowsGracefulCleanup(t *testing.T) {
	fixture := startSignalFixture(t, "graceful")
	waitForFixtureLine(t, fixture.scanner, "ready")
	if err := fixture.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	waitForFixtureLine(t, fixture.scanner, "canceled")
	if err := fixture.wait(t); err != nil {
		t.Fatalf("graceful fixture exit = %v", err)
	}
}

func TestLaterInterruptForcesExitDuringBlockedCleanup(t *testing.T) {
	fixture := startSignalFixture(t, "blocked")
	waitForFixtureLine(t, fixture.scanner, "ready")
	if err := fixture.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	waitForFixtureLine(t, fixture.scanner, "canceled")

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

forceSignals:
	for {
		select {
		case <-fixture.exited:
			break forceSignals
		case <-ticker.C:
			if err := fixture.command.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Fatal(err)
			}
		}
	}

	err := fixture.waitErr
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("blocked fixture exit = %v, want signal exit", err)
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGINT {
		t.Fatalf("blocked fixture status = %#v, want SIGINT", exitErr.Sys())
	}
}

func TestNormalReturnJoinsSignalWatcher(t *testing.T) {
	fixture := startSignalFixture(t, "normal")
	waitForFixtureLine(t, fixture.scanner, "ready")
	if err := fixture.wait(t); err != nil {
		t.Fatalf("normal fixture exit = %v", err)
	}
}

type signalFixture struct {
	command *exec.Cmd
	scanner *bufio.Scanner
	exited  chan struct{}
	waitErr error
}

func startSignalFixture(t *testing.T, mode string) *signalFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSignalFixture$")
	command.Env = append(os.Environ(), signalFixtureEnvironment+"="+mode)
	stdout, childStdout, err := os.Pipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	command.Stdout = childStdout
	if err := command.Start(); err != nil {
		_ = stdout.Close()
		_ = childStdout.Close()
		cancel()
		t.Fatal(err)
	}
	if err := childStdout.Close(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = stdout.Close()
		cancel()
		t.Fatal(err)
	}

	fixture := &signalFixture{
		command: command,
		scanner: bufio.NewScanner(stdout),
		exited:  make(chan struct{}),
	}
	go func() {
		fixture.waitErr = command.Wait()
		close(fixture.exited)
	}()
	t.Cleanup(func() {
		cancel()
		<-fixture.exited
		_ = stdout.Close()
	})
	return fixture
}

func (f *signalFixture) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-f.exited:
		return f.waitErr
	case <-time.After(4 * time.Second):
		t.Fatal("signal fixture did not exit")
		return nil
	}
}

func waitForFixtureLine(t *testing.T, scanner *bufio.Scanner, want string) {
	t.Helper()
	if !scanner.Scan() {
		t.Fatalf("signal fixture ended before %q: %v", want, scanner.Err())
	}
	if got := scanner.Text(); got != want {
		t.Fatalf("signal fixture line = %q, want %q", got, want)
	}
}

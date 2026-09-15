package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"golang.org/x/term"
)

const maxLoginKeyBytes = 64 * 1024

type credentialReadResult struct {
	value string
	err   error
}

// credentialReader is intentionally one-shot. Cancellation may leave one
// bounded byte reader blocked until process exit, so it must never be replaced.
type credentialReader struct {
	started atomic.Bool
}

var processCredentialReader credentialReader

func readLoginKey(ctx context.Context, in io.Reader, out io.Writer) (string, error) {
	return processCredentialReader.read(ctx, in, out)
}

func (r *credentialReader) read(ctx context.Context, in io.Reader, out io.Writer) (value string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !r.started.CompareAndSwap(false, true) {
		return "", errors.New("CLI key input was already used")
	}

	var restore func() error
	if file, ok := in.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		restore, err = disableInputEcho(int(file.Fd()))
		if err != nil {
			return "", fmt.Errorf("disable terminal echo: %w", err)
		}
		defer func() {
			if restoreErr := restore(); restoreErr != nil {
				value = ""
				err = fmt.Errorf("restore terminal: %w", restoreErr)
			}
		}()
	}

	if _, err := fmt.Fprint(out,
		"Enter your hookspot CLI key (Account settings > CLI key): "); err != nil {
		return "", fmt.Errorf("write CLI key prompt: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	done := make(chan credentialReadResult, 1)
	go func() {
		line, readErr := bufio.NewReader(io.LimitReader(in, maxLoginKeyBytes+1)).ReadString('\n')
		if len(line) > maxLoginKeyBytes {
			done <- credentialReadResult{err: errors.New("CLI key input is too long")}
			return
		}
		if errors.Is(readErr, io.EOF) {
			readErr = nil
		}
		done <- credentialReadResult{value: strings.TrimSpace(line), err: readErr}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-done:
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return result.value, result.err
	}
}

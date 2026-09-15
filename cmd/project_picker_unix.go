//go:build linux || darwin

package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func prepareProjectPickerInput(input *os.File) (func() error, error) {
	flags, err := unix.FcntlInt(input.Fd(), unix.F_GETFL, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.SetNonblock(int(input.Fd()), true); err != nil {
		return nil, err
	}
	return func() error {
		_, err := unix.FcntlInt(input.Fd(), unix.F_SETFL, flags)
		return err
	}, nil
}

func prepareProjectPickerOutput(*os.File) (func() error, error) {
	return func() error { return nil }, nil
}

func readProjectPickerKey(ctx context.Context, input *os.File) ([]byte, error) {
	return parseProjectPickerKey(ctx, func(readContext context.Context) (byte, error) {
		return readProjectPickerByte(readContext, input)
	}, projectPickerEscapeTimeout)
}

func readProjectPickerByte(ctx context.Context, input *os.File) (byte, error) {
	var buffer [1]byte
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		count, err := unix.Read(int(input.Fd()), buffer[:])
		if count == 1 {
			return buffer[0], nil
		}
		if err == nil && count == 0 {
			return 0, io.EOF
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EWOULDBLOCK) {
			return 0, err
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

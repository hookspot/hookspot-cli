//go:build windows

package cmd

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	consoleKeyEvent       = 0x0001
	virtualKeyReturn      = 0x0d
	virtualKeyEscape      = 0x1b
	virtualKeyC           = 0x43
	virtualKeyUp          = 0x26
	virtualKeyDown        = 0x28
	leftControlPressed    = 0x0008
	rightControlPressed   = 0x0004
	projectPickerWaitTime = 20
)

var readConsoleInput = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleInputW")

type consoleInputRecord struct {
	eventType uint16
	padding   uint16
	event     [16]byte
}

func prepareProjectPickerInput(input *os.File) (func() error, error) {
	handle := windows.Handle(input.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return nil, err
	}
	pickerMode := mode &^ windows.ENABLE_VIRTUAL_TERMINAL_INPUT
	if err := windows.SetConsoleMode(handle, pickerMode); err != nil {
		return nil, err
	}
	return func() error { return windows.SetConsoleMode(handle, mode) }, nil
}

func prepareProjectPickerOutput(output *os.File) (func() error, error) {
	handle := windows.Handle(output.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return nil, err
	}
	pickerMode := mode | windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	if err := windows.SetConsoleMode(handle, pickerMode); err != nil {
		return nil, err
	}
	return func() error { return windows.SetConsoleMode(handle, mode) }, nil
}

func readProjectPickerKey(ctx context.Context, input *os.File) ([]byte, error) {
	handle := windows.Handle(input.Fd())
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		event, err := windows.WaitForSingleObject(handle, projectPickerWaitTime)
		if err != nil {
			return nil, err
		}
		if event == uint32(windows.WAIT_TIMEOUT) {
			continue
		}
		if event != uint32(windows.WAIT_OBJECT_0) {
			return nil, fmt.Errorf("unexpected console input wait result %d", event)
		}

		record, err := readProjectPickerConsoleRecord(handle)
		if err != nil {
			return nil, err
		}
		key := projectPickerKeyFromConsoleRecord(record)
		if len(key) != 0 {
			return key, nil
		}
	}
}

func readProjectPickerConsoleRecord(handle windows.Handle) (consoleInputRecord, error) {
	var record consoleInputRecord
	var recordsRead uint32
	result, _, callErr := readConsoleInput.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&record)),
		1,
		uintptr(unsafe.Pointer(&recordsRead)),
	)
	if result == 0 {
		if callErr == nil || errors.Is(callErr, windows.ERROR_SUCCESS) {
			return consoleInputRecord{}, errors.New("ReadConsoleInputW failed")
		}
		return consoleInputRecord{}, callErr
	}
	if recordsRead != 1 {
		return consoleInputRecord{}, io.ErrNoProgress
	}
	return record, nil
}

func projectPickerKeyFromConsoleRecord(record consoleInputRecord) []byte {
	if record.eventType != consoleKeyEvent {
		return nil
	}
	keyDown := binary.LittleEndian.Uint32(record.event[0:4]) != 0
	virtualKeyCode := binary.LittleEndian.Uint16(record.event[6:8])
	unicodeChar := binary.LittleEndian.Uint16(record.event[10:12])
	controlKeyState := binary.LittleEndian.Uint32(record.event[12:16])
	if !keyDown {
		return nil
	}
	controlPressed := controlKeyState&(leftControlPressed|rightControlPressed) != 0
	if unicodeChar == 3 || controlPressed && virtualKeyCode == virtualKeyC {
		return []byte{3}
	}
	switch virtualKeyCode {
	case virtualKeyUp:
		return []byte("\x1b[A")
	case virtualKeyDown:
		return []byte("\x1b[B")
	case virtualKeyReturn:
		return []byte{'\r'}
	case virtualKeyEscape:
		return []byte{'\x1b'}
	}
	if unicodeChar != 0 {
		return []byte(string(rune(unicodeChar)))
	}
	return nil
}

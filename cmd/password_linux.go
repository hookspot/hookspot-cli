//go:build linux

package cmd

import "golang.org/x/sys/unix"

const readTermios = unix.TCGETS
const writeTermios = unix.TCSETS

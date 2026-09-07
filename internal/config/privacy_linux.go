//go:build linux

package config

import "os"

func validatePlatformAccess(*os.File, bool) error { return nil }

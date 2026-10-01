// Package config owns Hookspot configuration files.
package config

// Options select the configuration file.
type Options struct {
	ExplicitPath    string
	ExplicitPathSet bool
	Local           bool
	Intent          Intent
	// Prefix names the default config subfolder; empty uses the root folder.
	Prefix string
}

// Intent describes why a command opens a store. Ordinary commands read an
// existing explicitly selected file; login may create one.
type Intent uint8

const (
	Read Intent = iota
	LoginCreate
)

// Overrides are command flags. Nil means the flag was not supplied; a pointer
// to an empty string is an explicit empty value and does not fall through.
type Overrides struct {
	CLIKey      *string
	Project     *string
	NeedProject bool
}

// Config contains resolved values for one command invocation.
type Config struct {
	CLIKey           string
	Project          string
	OrganizationSlug string
	ProjectSlug      string
}

// Package config owns environment-specific Hookspot configuration.
package config

// Options select the immutable environment and configuration file.
type Options struct {
	Environment     string
	ExplicitPath    string
	ExplicitPathSet bool
	Intent          Intent
}

// Intent describes why a command opens a store. Ordinary commands read an
// existing explicitly selected file; login and migration may create one.
type Intent uint8

const (
	Read Intent = iota
	LoginCreate
	MigrationCreate
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

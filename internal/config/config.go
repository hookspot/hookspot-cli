package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds the resolved hookspot settings.
type Config struct {
	CLIKey   string
	Project  string
	LogLevel string
}

// New returns a viper instance configured with hookspot's defaults,
// environment variable bindings, and config file location. If configFile
// is empty, it defaults to $HOME/.config/hookspot/config.toml.
func New(configFile string) (*viper.Viper, error) {
	v := viper.New()
	v.SetEnvPrefix("HOOKSPOT")
	v.AutomaticEnv()
	v.SetDefault("log_level", "info")

	if configFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		configFile = filepath.Join(home, ".config", "hookspot", "config.toml")
	}

	v.SetConfigFile(configFile)
	v.SetConfigType("toml")

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) && !os.IsNotExist(err) {
			return nil, err
		}
	}

	return v, nil
}

// Load resolves the current configuration from v.
func Load(v *viper.Viper) Config {
	return Config{
		CLIKey:   v.GetString("cli_key"),
		Project:  v.GetString("project"),
		LogLevel: v.GetString("log_level"),
	}
}

// Save writes v's settings to configFile (or the default location if
// empty), creating parent directories as needed.
func Save(v *viper.Viper, configFile string) error {
	if configFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		configFile = filepath.Join(home, ".config", "hookspot", "config.toml")
	}

	if err := os.MkdirAll(filepath.Dir(configFile), 0o700); err != nil {
		return err
	}

	if err := v.WriteConfigAs(configFile); err != nil {
		return err
	}

	return os.Chmod(configFile, 0o600)
}

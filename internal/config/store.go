package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const currentSchemaVersion = 1

type persistedRecord struct {
	SchemaVersion int    `toml:"schema_version"`
	Environment   string `toml:"environment"`
	CLIKey        string `toml:"cli_key,omitempty"`
	Project       string `toml:"project,omitempty"`
}

// Store owns the persisted record for one immutable environment.
type Store struct {
	environment string
	path        string
	managedDir  bool
	exists      bool
	record      persistedRecord
	write       func(string, []byte, bool) error
}

// New selects and reads the environment-specific configuration record.
func New(opts Options) (*Store, error) {
	if !validEnvironment(opts.Environment) {
		return nil, fmt.Errorf("unknown configuration environment %q", opts.Environment)
	}
	path, managed, err := selectPath(opts)
	if err != nil {
		return nil, err
	}
	if strings.ContainsRune(path, '\x00') {
		return nil, errors.New("config path contains a NUL byte")
	}
	path, err = canonicalConfigPath(path)
	if err != nil {
		return nil, err
	}
	store := &Store{
		environment: opts.Environment,
		path:        path,
		managedDir:  managed,
		record: persistedRecord{
			SchemaVersion: currentSchemaVersion,
			Environment:   opts.Environment,
		},
		write: writeFileAtomic,
	}

	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if !managed && opts.Intent == Read {
			return nil, errors.New("selected config file does not exist")
		}
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect config file: %w", err)
	}
	if opts.Intent == MigrationCreate {
		return nil, errors.New("migration destination already exists")
	}
	if err := validateConfigFileType(path, info); err != nil {
		return nil, err
	}
	contents, err := readValidatedConfig(path, info)
	if err != nil {
		return nil, err
	}
	var record persistedRecord
	if err := toml.Unmarshal(contents, &record); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}
	if record.SchemaVersion != currentSchemaVersion {
		if record.SchemaVersion == 0 {
			return nil, errors.New("config file has no schema marker; explicit migration is required")
		}
		return nil, fmt.Errorf("unsupported config schema %d", record.SchemaVersion)
	}
	if record.Environment != opts.Environment {
		return nil, fmt.Errorf("config environment is %q, binary environment is %q", record.Environment, opts.Environment)
	}
	store.record = record
	store.exists = true
	return store, nil
}

func readValidatedConfig(path string, info os.FileInfo) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config file: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened config file: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, errors.New("config file changed while it was being opened")
	}
	if err := validateConfigFile(path, file, info); err != nil {
		return nil, err
	}
	contents, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	return contents, nil
}

// Path returns the selected configuration file path.
func (s *Store) Path() string { return s.path }

// Resolve applies flag and environment precedence without mutating the store.
func (s *Store) Resolve(flags Overrides) (Config, error) {
	cfg := Config{}
	if flags.CLIKey != nil {
		cfg.CLIKey = *flags.CLIKey
	} else if value, set := os.LookupEnv(s.scopedName("CLI_KEY")); set {
		cfg.CLIKey = value
	} else if value, set := os.LookupEnv("HOOKSPOT_CLI_KEY"); set {
		if err := requireLegacyAssertion(s.environment); err != nil {
			return Config{}, err
		}
		cfg.CLIKey = value
	} else {
		cfg.CLIKey = s.record.CLIKey
	}

	if !flags.NeedProject {
		return cfg, nil
	}
	if flags.Project != nil {
		cfg.Project = *flags.Project
		return cfg, nil
	}
	organization, organizationSet := os.LookupEnv(s.scopedName("ORGANIZATION_SLUG"))
	project, projectSet := os.LookupEnv(s.scopedName("PROJECT_SLUG"))
	if organizationSet || projectSet {
		if organization == "" || project == "" || !organizationSet || !projectSet {
			return Config{}, fmt.Errorf("%s and %s must both be set and nonempty", s.scopedName("ORGANIZATION_SLUG"), s.scopedName("PROJECT_SLUG"))
		}
		cfg.OrganizationSlug = organization
		cfg.ProjectSlug = project
		return cfg, nil
	}

	organization, organizationSet = os.LookupEnv("HOOKSPOT_ORGANIZATION_SLUG")
	project, projectSet = os.LookupEnv("HOOKSPOT_PROJECT_SLUG")
	if organizationSet || projectSet {
		if err := requireLegacyAssertion(s.environment); err != nil {
			return Config{}, err
		}
		if organization == "" || project == "" || !organizationSet || !projectSet {
			return Config{}, errors.New("HOOKSPOT_ORGANIZATION_SLUG and HOOKSPOT_PROJECT_SLUG must both be set and nonempty")
		}
		cfg.OrganizationSlug = organization
		cfg.ProjectSlug = project
		return cfg, nil
	}

	cfg.Project = s.record.Project
	return cfg, nil
}

// SaveCLIKey persists only the authenticated key owned by this store.
func (s *Store) SaveCLIKey(key string) error {
	record := s.record
	record.CLIKey = key
	return s.persist(record, !s.exists)
}

// SaveProject persists only the project UID owned by this store.
func (s *Store) SaveProject(uid string) error {
	record := s.record
	record.Project = uid
	return s.persist(record, !s.exists)
}

// ClearCLIKey removes only the persisted key, preserving the project.
func (s *Store) ClearCLIKey() error {
	if !s.exists || s.record.CLIKey == "" {
		return nil
	}
	record := s.record
	record.CLIKey = ""
	return s.persist(record, !s.exists)
}

// EnvironmentCLIKeyActive reports whether logout leaves a nonempty scoped or
// correctly asserted legacy key active. Explicit flags are intentionally
// irrelevant because they do not outlive this invocation.
func (s *Store) EnvironmentCLIKeyActive() bool {
	if value, set := os.LookupEnv(s.scopedName("CLI_KEY")); set {
		return value != ""
	}
	value, set := os.LookupEnv("HOOKSPOT_CLI_KEY")
	return set && value != "" && requireLegacyAssertion(s.environment) == nil
}

// Import publishes a legacy key and project together without overwriting a
// destination that already exists.
func (s *Store) Import(key, project string) error {
	if s.exists {
		return errors.New("destination config already exists")
	}
	record := s.record
	record.CLIKey = key
	record.Project = project
	return s.persist(record, true)
}

func (s *Store) persist(record persistedRecord, noOverwrite bool) error {
	contents, err := toml.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode config file: %w", err)
	}
	if err := prepareConfigDirectory(s.path, s.managedDir); err != nil {
		return err
	}
	if err := s.write(s.path, contents, noOverwrite); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	s.record = record
	s.exists = true
	return nil
}

func selectPath(opts Options) (string, bool, error) {
	if opts.ExplicitPathSet {
		if opts.ExplicitPath == "" {
			return "", false, errors.New("explicit config path is empty")
		}
		return opts.ExplicitPath, false, nil
	}
	if path, set := os.LookupEnv(scopedName(opts.Environment, "CONFIG_FILE")); set {
		if path == "" {
			return "", false, fmt.Errorf("%s config path is empty", strings.ToLower(opts.Environment))
		}
		return path, false, nil
	}
	if path, set := os.LookupEnv("HOOKSPOT_CONFIG_FILE"); set {
		if err := requireLegacyAssertion(opts.Environment); err != nil {
			return "", false, err
		}
		if path == "" {
			return "", false, errors.New("legacy config path is empty")
		}
		return path, false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".config", "hookspot", opts.Environment, "config.toml"), true, nil
}

func requireLegacyAssertion(environment string) error {
	asserted, set := os.LookupEnv("HOOKSPOT_ENVIRONMENT")
	if !set || asserted == "" {
		return errors.New("legacy HOOKSPOT variables require HOOKSPOT_ENVIRONMENT to match this binary")
	}
	if asserted != environment {
		return fmt.Errorf("HOOKSPOT_ENVIRONMENT is %q, binary environment is %q", asserted, environment)
	}
	return nil
}

func (s *Store) scopedName(suffix string) string {
	return scopedName(s.environment, suffix)
}

func scopedName(environment, suffix string) string {
	return "HOOKSPOT_" + strings.ToUpper(environment) + "_" + suffix
}

func validEnvironment(environment string) bool {
	return environment == "dev" || environment == "stage" || environment == "prod"
}

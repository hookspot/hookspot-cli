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
	if opts.Local {
		return newLocalStore(opts)
	}
	path, managed, err := selectPath(opts)
	if err != nil {
		return nil, err
	}
	return newStoreAt(opts.Environment, path, managed, opts.Intent)
}

func newStoreAt(environment, path string, managed bool, intent Intent) (*Store, error) {
	if strings.ContainsRune(path, '\x00') {
		return nil, errors.New("config path contains a NUL byte")
	}
	path, err := canonicalConfigPath(path)
	if err != nil {
		return nil, err
	}
	store := &Store{
		environment: environment,
		path:        path,
		managedDir:  managed,
		record: persistedRecord{
			SchemaVersion: currentSchemaVersion,
			Environment:   environment,
		},
		write: writeFileAtomic,
	}

	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if !managed && intent == Read {
			return nil, errors.New("selected config file does not exist")
		}
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect config file: %w", err)
	}
	if intent == MigrationCreate {
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
	if record.Environment != environment {
		return nil, fmt.Errorf("config environment is %q, binary environment is %q", record.Environment, environment)
	}
	store.record = record
	store.exists = true
	return store, nil
}

func newLocalStore(opts Options) (*Store, error) {
	if opts.ExplicitPathSet || configPathEnvironmentSet() {
		return nil, errors.New("--local cannot be combined with --config or a CONFIG_FILE environment override")
	}

	localPath, err := localConfigPath(opts.Environment)
	if err != nil {
		return nil, err
	}
	localPath, err = canonicalConfigPath(localPath)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(localPath); err == nil {
		return newStoreAt(opts.Environment, localPath, true, opts.Intent)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect local config file: %w", err)
	}

	globalPath, err := globalConfigPath(opts.Environment)
	if err != nil {
		return nil, err
	}
	global, err := newStoreAt(opts.Environment, globalPath, true, Read)
	if err != nil {
		return nil, err
	}
	return &Store{
		environment: opts.Environment,
		path:        localPath,
		managedDir:  true,
		record: persistedRecord{
			SchemaVersion: currentSchemaVersion,
			Environment:   opts.Environment,
			CLIKey:        global.record.CLIKey,
			Project:       global.record.Project,
		},
		write: writeFileAtomic,
	}, nil
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

// SavedProject returns the project UID persisted in the selected record.
func (s *Store) SavedProject() string { return s.record.Project }

// Resolve applies flag and environment precedence without mutating the store.
func (s *Store) Resolve(flags Overrides) (Config, error) {
	cfg := Config{}
	if flags.CLIKey != nil {
		cfg.CLIKey = *flags.CLIKey
	} else if value, set := os.LookupEnv("HOOKSPOT_CLI_KEY"); set {
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
	organization, organizationSet := os.LookupEnv("HOOKSPOT_ORGANIZATION_SLUG")
	project, projectSet := os.LookupEnv("HOOKSPOT_PROJECT_SLUG")
	if organizationSet || projectSet {
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

// EnvironmentCLIKeyActive reports whether logout leaves a nonempty
// HOOKSPOT_CLI_KEY active. Explicit flags are intentionally irrelevant
// because they do not outlive this invocation.
func (s *Store) EnvironmentCLIKeyActive() bool {
	value, set := os.LookupEnv("HOOKSPOT_CLI_KEY")
	return set && value != ""
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
	if path, set := os.LookupEnv("HOOKSPOT_CONFIG_FILE"); set {
		if path == "" {
			return "", false, errors.New("HOOKSPOT_CONFIG_FILE is empty")
		}
		return path, false, nil
	}
	localPath, err := localConfigPath(opts.Environment)
	if err != nil {
		return "", false, err
	}
	if _, err := os.Lstat(localPath); err == nil {
		return localPath, true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("inspect local config file: %w", err)
	}
	globalPath, err := globalConfigPath(opts.Environment)
	return globalPath, true, err
}

func configPathEnvironmentSet() bool {
	_, set := os.LookupEnv("HOOKSPOT_CONFIG_FILE")
	return set
}

func localConfigPath(environment string) (string, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("find current directory: %w", err)
	}
	return filepath.Join(workingDirectory, ".hookspot", environment, "config.toml"), nil
}

func globalConfigPath(environment string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".config", "hookspot", environment, "config.toml"), nil
}

func validEnvironment(environment string) bool {
	return environment == "dev" || environment == "prod"
}

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"hookspot/internal/api"
	"hookspot/internal/config"
)

func selectionProjects() []api.Project {
	return []api.Project{
		{UID: "proj_storefront", Name: "Storefront", Organization: api.Organization{UID: "org_acme", Name: "Acme Inc."}},
		{UID: "proj_payments", Name: "Payments", Organization: api.Organization{UID: "org_acme", Name: "Acme Inc."}},
		{UID: "proj_billing", Name: "Billing", Organization: api.Organization{UID: "org_other", Name: "Other"}},
	}
}

func TestProjectCandidatesMatchExactNamesIgnoringCase(t *testing.T) {
	projects := selectionProjects()
	tests := []struct {
		name    string
		args    []string
		wantUID []string
		wantErr bool
	}{
		{name: "all", wantUID: []string{"proj_storefront", "proj_payments", "proj_billing"}},
		{name: "organization with spaces", args: []string{"aCmE iNc."}, wantUID: []string{"proj_storefront", "proj_payments"}},
		{name: "organization and project", args: []string{"ACME INC.", "pAyMeNtS"}, wantUID: []string{"proj_payments"}},
		{name: "no substring organization match", args: []string{"Acme"}, wantErr: true},
		{name: "unknown pair", args: []string{"Acme Inc.", "Unknown"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := projectCandidates(projects, test.args)
			if (err != nil) != test.wantErr {
				t.Fatalf("projectCandidates() error = %v, wantErr %v", err, test.wantErr)
			}
			if test.wantErr {
				return
			}
			if len(got) != len(test.wantUID) {
				t.Fatalf("candidate count = %d, want %d: %+v", len(got), len(test.wantUID), got)
			}
			for index, uid := range test.wantUID {
				if got[index].UID != uid {
					t.Fatalf("candidate %d UID = %q, want %q", index, got[index].UID, uid)
				}
			}
		})
	}

	duplicate := append(selectionProjects(), api.Project{
		UID: "proj_duplicate", Name: "PAYMENTS", Organization: api.Organization{UID: "org_duplicate", Name: "ACME INC."},
	})
	if _, err := projectCandidates(duplicate, []string{"Acme Inc.", "Payments"}); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("duplicate exact pair error = %v", err)
	}
	unicodeProjects := []api.Project{{UID: "proj_unicode", Name: "Réception", Organization: api.Organization{Name: "München 合作"}}}
	matched, err := projectCandidates(unicodeProjects, []string{"mÜNCHEN 合作", "RÉCEPTION"})
	if err != nil || len(matched) != 1 || matched[0].UID != "proj_unicode" {
		t.Fatalf("Unicode exact match = %+v, %v", matched, err)
	}
}

func TestSelectProjectMarksAndDefaultsSavedProject(t *testing.T) {
	projects := selectionProjects()[:2]
	prompt := func(ctx context.Context, in io.Reader, out io.Writer, options []string, defaultIndex int) (int, error) {
		if err := ctx.Err(); err != nil {
			t.Fatal(err)
		}
		wantOptions := []string{"Acme Inc. | Storefront", "Acme Inc. | Payments (current)"}
		if strings.Join(options, "\n") != strings.Join(wantOptions, "\n") {
			t.Fatalf("options = %q, want %q", options, wantOptions)
		}
		if defaultIndex != 1 {
			t.Fatalf("default index = %d, want 1", defaultIndex)
		}
		return 0, nil
	}

	selected, err := selectProject(context.Background(), strings.NewReader(""), io.Discard, projects, "proj_payments", prompt)
	if err != nil {
		t.Fatal(err)
	}
	if selected.UID != "proj_storefront" {
		t.Fatalf("selected UID = %q, want proj_storefront", selected.UID)
	}
}

func TestSelectProjectHandlesSoleCandidateAndPromptFailures(t *testing.T) {
	projects := selectionProjects()
	promptCalled := false
	selected, err := selectProject(context.Background(), nil, nil, projects[:1], "", func(context.Context, io.Reader, io.Writer, []string, int) (int, error) {
		promptCalled = true
		return 0, nil
	})
	if err != nil || selected.UID != "proj_storefront" || promptCalled {
		t.Fatalf("sole selection = %+v, err = %v, promptCalled = %v", selected, err, promptCalled)
	}

	wantErr := errors.New("prompt failed")
	if _, err := selectProject(context.Background(), nil, nil, projects[:2], "", func(context.Context, io.Reader, io.Writer, []string, int) (int, error) {
		return 0, wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("prompt error = %v, want %v", err, wantErr)
	}
	for _, index := range []int{-1, 2} {
		if _, err := selectProject(context.Background(), nil, nil, projects[:2], "", func(context.Context, io.Reader, io.Writer, []string, int) (int, error) {
			return index, nil
		}); err == nil || !strings.Contains(err.Error(), "invalid project selection") {
			t.Fatalf("index %d error = %v", index, err)
		}
	}
}

func TestProjectPickerArrowKeysEnterAndCancellation(t *testing.T) {
	tests := []struct {
		name      string
		current   int
		key       []byte
		wantIndex int
		wantDone  bool
		wantErr   error
	}{
		{name: "down", current: 0, key: []byte("\x1b[B"), wantIndex: 1},
		{name: "down wraps", current: 2, key: []byte("\x1b[B"), wantIndex: 0},
		{name: "up", current: 2, key: []byte("\x1b[A"), wantIndex: 1},
		{name: "up wraps", current: 0, key: []byte("\x1b[A"), wantIndex: 2},
		{name: "SS3 down", current: 0, key: []byte("\x1bOB"), wantIndex: 1},
		{name: "SS3 up", current: 2, key: []byte("\x1bOA"), wantIndex: 1},
		{name: "enter", current: 1, key: []byte("\r"), wantIndex: 1, wantDone: true},
		{name: "escape", current: 1, key: []byte("\x1b"), wantIndex: 1, wantErr: context.Canceled},
		{name: "control c", current: 1, key: []byte{3}, wantIndex: 1, wantErr: context.Canceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index, done, err := applyProjectPickerKey(test.current, 3, test.key)
			if index != test.wantIndex || done != test.wantDone || !errors.Is(err, test.wantErr) {
				t.Fatalf("apply key = (%d, %v, %v), want (%d, %v, %v)", index, done, err, test.wantIndex, test.wantDone, test.wantErr)
			}
		})
	}
}

func TestProjectPickerParserBoundsEscapeAndPrioritizesControlC(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  []byte
	}{
		{name: "CSI up", input: []byte("\x1b[A"), want: []byte("\x1b[A")},
		{name: "SS3 down", input: []byte("\x1bOB"), want: []byte("\x1bOB")},
		{name: "control c after escape", input: []byte{'\x1b', 3}, want: []byte{3}},
		{name: "control c inside CSI", input: []byte{'\x1b', '[', 3}, want: []byte{3}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index := 0
			key, err := parseProjectPickerKey(context.Background(), func(context.Context) (byte, error) {
				if index >= len(test.input) {
					return 0, io.EOF
				}
				value := test.input[index]
				index++
				return value, nil
			}, 5*time.Millisecond)
			if err != nil || !bytes.Equal(key, test.want) {
				t.Fatalf("parsed key = %q, error = %v, want %q", key, err, test.want)
			}
		})
	}

	started := time.Now()
	reads := 0
	key, err := parseProjectPickerKey(context.Background(), func(ctx context.Context) (byte, error) {
		if reads == 0 {
			reads++
			return '\x1b', nil
		}
		<-ctx.Done()
		return 0, ctx.Err()
	}, 10*time.Millisecond)
	if err != nil || !bytes.Equal(key, []byte{'\x1b'}) {
		t.Fatalf("bare escape = %q, error = %v", key, err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("bare escape parser took %s", elapsed)
	}
}

func TestProjectPickerViewBoundsRowsAndUnicodeWidth(t *testing.T) {
	options := []string{
		"Acme | Zero",
		"Acme | One",
		"Acme | Two",
		"Acme | Three",
		"Acme | Four",
		"Acme | Five",
		"Acme | Six",
		"Acme | Seven",
		"München 合作 | A project name that cannot fit",
		"Acme | Nine",
	}
	view, err := newProjectPickerView(18, 4, len(options), 8)
	if err != nil {
		t.Fatal(err)
	}
	if view.rows != 3 || view.start > 8 || view.start+view.rows <= 8 {
		t.Fatalf("initial viewport = start %d, rows %d; selected project is not visible", view.start, view.rows)
	}

	var output bytes.Buffer
	if err := view.render(&output, options, 8); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "cannot fit") || !strings.Contains(output.String(), "…") {
		t.Fatalf("long option was not truncated: %q", output.String())
	}
	for _, line := range strings.Split(output.String(), "\r\n") {
		line = strings.TrimPrefix(line, "\r\x1b[2K")
		if terminalTextWidth(line) > 17 {
			t.Fatalf("rendered line width = %d, want at most 17: %q", terminalTextWidth(line), line)
		}
		if !utf8.ValidString(line) {
			t.Fatalf("rendered line is invalid UTF-8: %q", line)
		}
	}

	output.Reset()
	if err := view.render(&output, options, 9); err != nil {
		t.Fatal(err)
	}
	if view.start+view.rows <= 9 {
		t.Fatalf("updated viewport = start %d, rows %d; selected project is not visible", view.start, view.rows)
	}
	if strings.Contains(output.String(), "\x1b[10A") || !strings.Contains(output.String(), "\x1b[2A") {
		t.Fatalf("redraw did not move by visible rows only: %q", output.String())
	}

	for _, value := range []string{strings.Repeat("\u231a", 8), strings.Repeat("\U0001f200", 8)} {
		truncated := truncateTerminalText(value, 7)
		if got := []rune(truncated); len(got) != 3 || got[len(got)-1] != '…' || terminalTextWidth(truncated) > 7 {
			t.Fatalf("wide-symbol truncation = %q, runes = %U, width = %d", truncated, got, terminalTextWidth(truncated))
		}
	}
	keycap := "1\ufe0f\u20e3"
	if got := terminalTextWidth(keycap); got != 2 {
		t.Fatalf("keycap width = %d, want 2", got)
	}
	if truncated := truncateTerminalText(strings.Repeat(keycap, 8), 6); terminalTextWidth(truncated) > 6 || !strings.HasSuffix(truncated, "…") {
		t.Fatalf("keycap truncation = %q, width = %d", truncated, terminalTextWidth(truncated))
	}
}

func TestPromptProjectRequiresTerminalInputAndOutput(t *testing.T) {
	for _, test := range []struct {
		name string
		in   io.Reader
		out  io.Writer
	}{
		{name: "non-terminal input", in: strings.NewReader("\r"), out: os.Stdout},
		{name: "non-terminal output", in: os.Stdin, out: &bytes.Buffer{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := promptProject(context.Background(), test.in, test.out, []string{"one", "two"}, 0)
			if err == nil || !strings.Contains(err.Error(), "interactive terminal") || !strings.Contains(err.Error(), "ORGANIZATION PROJECT or PROJECT_UID") {
				t.Fatalf("prompt error = %v", err)
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := promptProject(ctx, strings.NewReader(""), io.Discard, []string{"one", "two"}, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled prompt error = %v", err)
	}
}

func TestProjectUseSupportsInteractiveNameAndUIDForms(t *testing.T) {
	projects := selectionProjects()
	tests := []struct {
		name         string
		args         []string
		listed       []api.Project
		direct       *api.Project
		direct404    string
		wantRequests string
		wantUID      string
		environment  map[string]string
	}{
		{
			name: "no arguments selects sole project despite incomplete slug override", listed: projects[:1],
			wantRequests: "/cli/projects", wantUID: "proj_storefront",
			environment: map[string]string{"HOOKSPOT_ORGANIZATION_SLUG": "irrelevant"},
		},
		{
			name: "organization with spaces goes directly to exact name filtering", args: []string{"acme inc."}, listed: projects[1:2],
			wantRequests: "/cli/projects", wantUID: "proj_payments",
		},
		{
			name: "UID-shaped organization falls back after not found", args: []string{"Acme"}, listed: []api.Project{{UID: "proj_acme", Name: "Default", Organization: api.Organization{Name: "Acme"}}}, direct404: "Acme",
			wantRequests: "/cli/projects/Acme,/cli/projects", wantUID: "proj_acme",
		},
		{
			name: "organization and project pair", args: []string{"ACME INC.", "PAYMENTS"}, listed: projects,
			wantRequests: "/cli/projects", wantUID: "proj_payments",
		},
		{
			name: "project UID resolves directly", args: []string{"proj_payments"}, direct: &projects[1],
			wantRequests: "/cli/projects/proj_payments", wantUID: "proj_payments",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := make([]string, 0, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				if r.Header.Get("X-CLI-KEY") != "test-key" {
					t.Errorf("X-CLI-KEY = %q", r.Header.Get("X-CLI-KEY"))
				}
				switch {
				case r.URL.Path == "/cli/projects":
					_ = json.NewEncoder(w).Encode(test.listed)
				case test.direct != nil && r.URL.Path == "/cli/projects/"+test.direct.UID:
					_ = json.NewEncoder(w).Encode(test.direct)
				case test.direct404 != "" && r.URL.Path == "/cli/projects/"+test.direct404:
					http.NotFound(w, r)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			configPath := filepath.Join(t.TempDir(), "config.toml")
			if err := writeCommandFixture(configPath, []byte("schema_version = 1\nenvironment = 'dev'\ncli_key = 'test-key'\nproject = 'old-project'\n")); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"--config", configPath, "project", "use"}, test.args...)
			result := runCommandProcessEnvironment(t, "", developmentMetadata(server.URL), test.environment, args...)
			if result.err != nil {
				t.Fatalf("project use failed: %v\n%s", result.err, result.stderr)
			}
			if got := strings.Join(requests, ","); got != test.wantRequests {
				t.Fatalf("requests = %q, want %q", got, test.wantRequests)
			}
			contents, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(contents), "project = '"+test.wantUID+"'") || !strings.Contains(string(contents), "cli_key = 'test-key'") {
				t.Fatalf("unexpected saved config:\n%s", contents)
			}
			if !strings.Contains(result.stdout, "Active project set to ") || strings.Contains(result.stdout, "Saved to ") {
				t.Fatalf("unexpected stdout = %q", result.stdout)
			}
		})
	}
}

func TestProjectUseRejectsInvalidArgumentsBeforeConfiguration(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "empty organization", args: []string{""}},
		{name: "empty project", args: []string{"Acme", ""}},
		{name: "too many", args: []string{"one", "two", "three"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			missingConfig := filepath.Join(t.TempDir(), "missing.toml")
			args := append([]string{"--config", missingConfig, "project", "use"}, test.args...)
			result := runCommandProcess(t, "", developmentMetadata("http://127.0.0.1:1"), args...)
			if result.err == nil || !strings.Contains(result.stderr, "project selection") {
				t.Fatalf("invalid arguments result = %v, stderr = %q", result.err, result.stderr)
			}
			if _, err := os.Stat(missingConfig); !os.IsNotExist(err) {
				t.Fatalf("configuration was accessed or created: %v", err)
			}
		})
	}
}

func TestProjectUseErrorsLeaveStoredSelectionUntouched(t *testing.T) {
	projects := selectionProjects()
	tests := []struct {
		name       string
		args       []string
		status     int
		listed     []api.Project
		wantOutput string
	}{
		{name: "no projects", status: http.StatusOK, listed: []api.Project{}, wantOutput: "no projects"},
		{name: "multiple candidates without terminal", status: http.StatusOK, listed: projects[:2], wantOutput: "interactive terminal"},
		{name: "unknown organization with spaces", args: []string{"Missing Org"}, status: http.StatusOK, listed: projects, wantOutput: "no projects match"},
		{name: "unknown UID-shaped identifier after fallback", args: []string{"missing_uid"}, status: http.StatusOK, listed: projects, wantOutput: "no projects match"},
		{name: "duplicate exact pair", args: []string{"Acme Inc.", "Payments"}, status: http.StatusOK, listed: append(projects, api.Project{UID: "proj_duplicate", Name: "payments", Organization: api.Organization{Name: "ACME INC."}}), wantOutput: "multiple projects match"},
		{name: "list API failure", status: http.StatusInternalServerError, wantOutput: "Hookspot API is unavailable"},
		{name: "direct API failure does not fall back", args: []string{"proj_failure"}, status: http.StatusInternalServerError, wantOutput: "Hookspot API is unavailable"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := make([]string, 0, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				if r.URL.Path == "/cli/projects" && test.status == http.StatusOK {
					_ = json.NewEncoder(w).Encode(test.listed)
					return
				}
				if test.status == http.StatusOK {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(test.status)
			}))
			defer server.Close()

			configPath := filepath.Join(t.TempDir(), "config.toml")
			original := "schema_version = 1\nenvironment = 'dev'\ncli_key = 'test-key'\nproject = 'old-project'\n"
			if err := writeCommandFixture(configPath, []byte(original)); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"--config", configPath, "project", "use"}, test.args...)
			result := runCommandProcess(t, "", developmentMetadata(server.URL), args...)
			if result.err == nil || !strings.Contains(result.stderr, test.wantOutput) {
				t.Fatalf("error result = %v, stderr = %q", result.err, result.stderr)
			}
			contents, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(contents) != original {
				t.Fatalf("config changed on error:\n%s", contents)
			}
			if test.name == "direct API failure does not fall back" && strings.Join(requests, ",") != "/cli/projects/proj_failure" {
				t.Fatalf("non-404 requests = %v, want direct request only", requests)
			}
		})
	}
}

func TestProjectUseLocalFailureDoesNotCreateRecordOrChangeGlobal(t *testing.T) {
	home := t.TempDir()
	working := t.TempDir()
	globalPath := filepath.Join(home, ".config", "hookspot", "dev", "config.toml")
	original := "schema_version = 1\nenvironment = 'dev'\ncli_key = 'global-key'\nproject = 'proj_storefront'\n"
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeCommandFixture(globalPath, []byte(original)); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(selectionProjects()[:2])
	}))
	defer server.Close()
	environment := map[string]string{"HOME": home, "USERPROFILE": home}

	result := runCommandProcessDirectoryEnvironment(t, working, "", developmentMetadata(server.URL), environment, "project", "use", "--local")
	if result.err == nil || !strings.Contains(result.stderr, "interactive terminal") {
		t.Fatalf("nonterminal local result = %v, stderr = %q", result.err, result.stderr)
	}
	if _, err := os.Stat(filepath.Join(working, ".hookspot", "dev", "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("local config was created on failed selection: %v", err)
	}
	global, err := os.ReadFile(globalPath)
	if err != nil || string(global) != original {
		t.Fatalf("global config changed: %v\n%s", err, global)
	}
}

func TestPersistProjectSelectionHonorsCanceledContext(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	original := "schema_version = 1\nenvironment = 'dev'\nproject = 'old-project'\n"
	if err := writeCommandFixture(configPath, []byte(original)); err != nil {
		t.Fatal(err)
	}
	store, err := config.New(config.Options{Environment: "dev", ExplicitPath: configPath, ExplicitPathSet: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := persistProjectSelection(ctx, store, selectionProjects()[0], &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("persist error = %v, want context cancellation", err)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != original {
		t.Fatalf("config changed after cancellation:\n%s", contents)
	}
}

func TestProjectUseLocalCreatesRecordFromPersistedValuesOnly(t *testing.T) {
	project := selectionProjects()[1]
	tests := []struct {
		name         string
		global       string
		environment  map[string]string
		flags        []string
		wantLocalKey string
		forbiddenKey string
		globalExists bool
	}{
		{
			name:         "copies persisted global key instead of environment key",
			global:       "schema_version = 1\nenvironment = 'dev'\ncli_key = 'persisted-key'\nproject = 'old-project'\n",
			environment:  map[string]string{"HOOKSPOT_CLI_KEY": "environment-key"},
			wantLocalKey: "persisted-key", forbiddenKey: "environment-key", globalExists: true,
		},
		{
			name:  "does not persist flag key when global record is absent",
			flags: []string{"--cli-key", "flag-key"}, forbiddenKey: "flag-key",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			working := t.TempDir()
			globalPath := filepath.Join(home, ".config", "hookspot", "dev", "config.toml")
			if test.global != "" {
				if err := os.MkdirAll(filepath.Dir(globalPath), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := writeCommandFixture(globalPath, []byte(test.global)); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(globalPath)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/cli/projects/proj_payments" {
					t.Errorf("path = %q", r.URL.Path)
				}
				wantKey := test.forbiddenKey
				if wantKey == "" {
					wantKey = test.wantLocalKey
				}
				if got := r.Header.Get("X-CLI-KEY"); got != wantKey {
					t.Errorf("X-CLI-KEY = %q, want %q", got, wantKey)
				}
				_ = json.NewEncoder(w).Encode(project)
			}))
			defer server.Close()

			environment := map[string]string{"HOME": home, "USERPROFILE": home}
			for key, value := range test.environment {
				environment[key] = value
			}
			args := append([]string{}, test.flags...)
			args = append(args, "project", "use", "--local", "proj_payments")
			result := runCommandProcessDirectoryEnvironment(t, working, "", developmentMetadata(server.URL), environment, args...)
			if result.err != nil {
				t.Fatalf("project use --local failed: %v\n%s", result.err, result.stderr)
			}
			localPath := filepath.Join(working, ".hookspot", "dev", "config.toml")
			contents, err := os.ReadFile(localPath)
			if err != nil {
				t.Fatal(err)
			}
			text := string(contents)
			if !strings.Contains(text, "project = 'proj_payments'") || test.wantLocalKey != "" && !strings.Contains(text, "cli_key = '"+test.wantLocalKey+"'") || test.forbiddenKey != "" && strings.Contains(text, test.forbiddenKey) {
				t.Fatalf("unexpected local config:\n%s", text)
			}
			if result.stdout != "Active project set to Acme Inc. | Payments\n" {
				t.Fatalf("unexpected stdout: %q", result.stdout)
			}
			after, err := os.ReadFile(globalPath)
			if test.globalExists {
				if err != nil || string(after) != string(before) {
					t.Fatalf("global config changed: %v\n%s", err, after)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("global config was created: %v", err)
			}
		})
	}
}

func TestProjectUseUpdatesAutomaticLocalUnlessExplicitConfigWins(t *testing.T) {
	home := t.TempDir()
	working := t.TempDir()
	globalPath := filepath.Join(home, ".config", "hookspot", "dev", "config.toml")
	localPath := filepath.Join(working, ".hookspot", "dev", "config.toml")
	explicitPath := filepath.Join(t.TempDir(), "explicit.toml")
	for path, contents := range map[string]string{
		globalPath:   "schema_version = 1\nenvironment = 'dev'\ncli_key = 'global-key'\nproject = 'global-project'\n",
		localPath:    "schema_version = 1\nenvironment = 'dev'\ncli_key = 'local-key'\nproject = 'local-project'\n",
		explicitPath: "schema_version = 1\nenvironment = 'dev'\ncli_key = 'explicit-key'\nproject = 'explicit-project'\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := writeCommandFixture(path, []byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := strings.TrimPrefix(r.URL.Path, "/cli/projects/")
		_ = json.NewEncoder(w).Encode(api.Project{UID: uid, Name: uid, Organization: api.Organization{Name: "Acme"}})
	}))
	defer server.Close()
	environment := map[string]string{"HOME": home, "USERPROFILE": home}

	result := runCommandProcessDirectoryEnvironment(t, working, "", developmentMetadata(server.URL), environment, "project", "use", "project_local_new")
	if result.err != nil {
		t.Fatalf("automatic local update failed: %v\n%s", result.err, result.stderr)
	}
	localAfter, err := os.ReadFile(localPath)
	if err != nil || !strings.Contains(string(localAfter), "project_local_new") || !strings.Contains(string(localAfter), "local-key") {
		t.Fatalf("local config was not updated: %v\n%s", err, localAfter)
	}
	globalAfter, _ := os.ReadFile(globalPath)
	if !strings.Contains(string(globalAfter), "global-project") {
		t.Fatalf("global config changed:\n%s", globalAfter)
	}

	localBeforeExplicit := append([]byte(nil), localAfter...)
	result = runCommandProcessDirectoryEnvironment(t, working, "", developmentMetadata(server.URL), environment, "--config", explicitPath, "project", "use", "project_explicit_new")
	if result.err != nil {
		t.Fatalf("explicit update failed: %v\n%s", result.err, result.stderr)
	}
	explicitAfter, _ := os.ReadFile(explicitPath)
	if !strings.Contains(string(explicitAfter), "project_explicit_new") || !strings.Contains(string(explicitAfter), "explicit-key") {
		t.Fatalf("explicit config was not updated:\n%s", explicitAfter)
	}
	localAfterExplicit, _ := os.ReadFile(localPath)
	if string(localAfterExplicit) != string(localBeforeExplicit) {
		t.Fatalf("explicit update changed local config:\n%s", localAfterExplicit)
	}
}

func TestProjectUseLocalIsolatesFreshProcessesByWorkingDirectory(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	folderA := filepath.Join(root, "folder A")
	folderB := filepath.Join(root, "folder B")
	for _, folder := range []string{folderA, folderB} {
		if err := os.MkdirAll(folder, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	globalPath := filepath.Join(home, ".config", "hookspot", "dev", "config.toml")
	globalBefore := []byte("schema_version = 1\nenvironment = 'dev'\ncli_key = 'fixture-key'\nproject = 'global_g'\n")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeCommandFixture(globalPath, globalBefore); err != nil {
		t.Fatal(err)
	}

	projects := []api.Project{
		{UID: "local_a", Name: "Project 1", Organization: api.Organization{UID: "org_a", Name: "asd1"}},
		{UID: "local_b", Name: "Project 2", Organization: api.Organization{UID: "org_a", Name: "asd1"}},
		{UID: "local_c", Name: "Project 3", Organization: api.Organization{UID: "org_a", Name: "asd1"}},
	}
	requests := make(chan string, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Path
		if r.Header.Get("X-CLI-KEY") != "fixture-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/cli/projects" {
			_ = json.NewEncoder(w).Encode(projects)
			return
		}
		for _, project := range projects {
			if r.URL.Path == "/cli/projects/"+project.UID {
				_ = json.NewEncoder(w).Encode(project)
				return
			}
			if r.URL.Path == "/cli/projects/"+project.UID+"/sources" {
				_, _ = w.Write([]byte("[]"))
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	drainRequests := func() []string {
		var paths []string
		for {
			select {
			case path := <-requests:
				paths = append(paths, path)
			default:
				return paths
			}
		}
	}
	environment := map[string]string{"HOME": home, "USERPROFILE": home}
	run := func(folder string, args ...string) (commandResult, []string) {
		drainRequests()
		result := runCommandProcessDirectoryEnvironment(t, folder, "", developmentMetadata(server.URL), environment, args...)
		return result, drainRequests()
	}
	localPath := func(folder string) string {
		return filepath.Join(folder, ".hookspot", "dev", "config.toml")
	}
	read := func(path string) []byte {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return contents
	}
	requireProject := func(path, uid string) []byte {
		contents := read(path)
		if !bytes.Contains(contents, []byte("project = '"+uid+"'")) || !bytes.Contains(contents, []byte("cli_key = 'fixture-key'")) {
			t.Fatalf("unexpected config at %s:\n%s", path, contents)
		}
		return contents
	}
	requireGlobalUnchanged := func() {
		if after := read(globalPath); !bytes.Equal(after, globalBefore) {
			t.Fatalf("global config changed:\n%s", after)
		}
	}
	requireFreshListen := func(folder, uid string) {
		result, paths := run(folder, "listen")
		if result.err == nil || !strings.Contains(result.stderr, "no matching connections found") {
			t.Fatalf("fresh listen in %s = %v, stderr %q", folder, result.err, result.stderr)
		}
		wantPaths := "/cli/projects/" + uid + ",/cli/projects/" + uid + "/sources"
		if got := strings.Join(paths, ","); got != wantPaths {
			t.Fatalf("fresh listen requests in %s = %q, want %q", folder, got, wantPaths)
		}
	}

	for _, test := range []struct {
		folder string
		name   string
		uid    string
	}{
		{folder: folderA, name: "Project 1", uid: "local_a"},
		{folder: folderB, name: "Project 2", uid: "local_b"},
	} {
		result, paths := run(test.folder, "project", "use", "--local", "asd1", test.name)
		if result.err != nil {
			t.Fatalf("project use --local in %s failed: %v\n%s", test.folder, result.err, result.stderr)
		}
		if result.stdout != "Active project set to asd1 | "+test.name+"\n" {
			t.Fatalf("stdout in %s = %q", test.folder, result.stdout)
		}
		if got := strings.Join(paths, ","); got != "/cli/projects" {
			t.Fatalf("requests in %s = %q", test.folder, got)
		}
		requireProject(localPath(test.folder), test.uid)
		requireGlobalUnchanged()
	}

	for _, test := range []struct {
		folder string
		uid    string
	}{
		{folder: folderA, uid: "local_a"},
		{folder: folderB, uid: "local_b"},
	} {
		requireFreshListen(test.folder, test.uid)
	}

	folderBBefore := read(localPath(folderB))
	result, _ := run(folderA, "project", "use", "asd1", "Project 3")
	if result.err != nil {
		t.Fatalf("automatic local switch in %s failed: %v\n%s", folderA, result.err, result.stderr)
	}
	folderAAfter := requireProject(localPath(folderA), "local_c")
	if after := read(localPath(folderB)); !bytes.Equal(after, folderBBefore) {
		t.Fatalf("switch in %s changed %s", folderA, folderB)
	}
	requireGlobalUnchanged()
	requireFreshListen(folderA, "local_c")

	result, _ = run(folderB, "project", "use", "asd1", "Project 1")
	if result.err != nil {
		t.Fatalf("automatic local switch in %s failed: %v\n%s", folderB, result.err, result.stderr)
	}
	requireProject(localPath(folderB), "local_a")
	if after := read(localPath(folderA)); !bytes.Equal(after, folderAAfter) {
		t.Fatalf("switch in %s changed %s", folderB, folderA)
	}
	requireGlobalUnchanged()
	requireFreshListen(folderB, "local_a")
}

func TestProjectUseLocalRejectsCustomConfigBeforeAPI(t *testing.T) {
	for _, test := range []struct {
		name        string
		args        []string
		environment map[string]string
	}{
		{name: "explicit flag", args: []string{"--config", "config.toml"}},
		{name: "environment variable", environment: map[string]string{"HOOKSPOT_CONFIG_FILE": "config.toml"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			working := t.TempDir()
			configPath := filepath.Join(working, "config.toml")
			original := "schema_version = 1\nenvironment = 'dev'\ncli_key = 'test-key'\nproject = 'old-project'\n"
			if err := writeCommandFixture(configPath, []byte(original)); err != nil {
				t.Fatal(err)
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			environment := map[string]string{"HOME": home, "USERPROFILE": home}
			for key, value := range test.environment {
				environment[key] = value
			}
			args := append([]string{}, test.args...)
			args = append(args, "project", "use", "--local", "proj_new")
			result := runCommandProcessDirectoryEnvironment(t, working, "", developmentMetadata(server.URL), environment, args...)
			if result.err == nil || !strings.Contains(result.stderr, "--local cannot be combined") {
				t.Fatalf("conflict result = %v, stderr = %q", result.err, result.stderr)
			}
			if requests != 0 {
				t.Fatalf("API requests = %d, want 0", requests)
			}
			contents, err := os.ReadFile(configPath)
			if err != nil || string(contents) != original {
				t.Fatalf("custom config changed: %v\n%s", err, contents)
			}
		})
	}
}

func TestProjectDisplayName_OnlyShowsOrganizationAndProject(t *testing.T) {
	project := api.Project{
		UID:          "proj_payments",
		Name:         "Payments",
		Slug:         "payments",
		Organization: api.Organization{UID: "org_acme", Name: "Acme", Slug: "acme"},
	}

	if got, want := projectDisplayName(project), "Acme | Payments"; got != want {
		t.Fatalf("projectDisplayName() = %q, want %q", got, want)
	}
}

func TestProjectDisplayNameEscapesBackendControls(t *testing.T) {
	project := api.Project{
		Name:         "Payments\nInjected\x1b",
		Organization: api.Organization{Name: "Acme\tPrompt"},
	}
	want := `Acme\tPrompt | Payments\nInjected\x1b`
	if got := projectDisplayName(project); got != want {
		t.Fatalf("projectDisplayName() = %q, want %q", got, want)
	}
}

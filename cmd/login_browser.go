package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"charm.land/lipgloss/v2"

	"hookspot/internal/api"
	"hookspot/internal/cards"
	"hookspot/internal/endpoint"
)

const (
	browserLoginPollInterval = 2 * time.Second
	browserLoginDefaultTTL   = 10 * time.Minute
	browserLoginMaxTTL       = 15 * time.Minute
)

// loginAPI is the subset of the Hookspot API used by the browser login flow.
type loginAPI interface {
	StartLogin(ctx context.Context, deviceName string) (*api.LoginAttempt, error)
	PollLogin(ctx context.Context, pollToken string) (*api.LoginResult, error)
}

// loginStore persists an approved login.
type loginStore interface {
	SaveLogin(key, projectUID string) error
}

// browserLoginDeps carries the browser flow's inputs so it runs without the
// package-level store or endpoint.
type browserLoginDeps struct {
	api          loginAPI
	endpoint     endpoint.Base
	openBrowser  func(string) error
	store        loginStore
	pollInterval time.Duration
	out          io.Writer
}

// runBrowserLogin creates a login attempt, opens the browser, and polls until
// the user approves, the attempt expires, or ctx is cancelled.
func runBrowserLogin(ctx context.Context, deps browserLoginDeps) error {
	deviceName, err := os.Hostname()
	if err != nil {
		deviceName = ""
	}

	attempt, err := deps.api.StartLogin(ctx, deviceName)
	if err != nil {
		var apiErr *api.Error
		if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusMethodNotAllowed) {
			return newCommandError("this Hookspot server does not support browser login", "Run 'hookspot login -i' or set HOOKSPOT_CLI_KEY.")
		}
		return fmt.Errorf("start browser login: %w", err)
	}

	browserURL := deps.endpoint.API("cli/login/" + attempt.BrowserToken)
	if browserURL == nil {
		return newCommandError("the Hookspot server returned an invalid browser login token", "Run 'hookspot login' again.")
	}

	opened := deps.openBrowser(browserURL.String()) == nil
	lipgloss.Fprintln(deps.out, cards.Login(attempt.Code, browserURL.String(), opened, cards.Width(deps.out)))

	deadline := time.NewTimer(browserLoginTTL(attempt.ExpiresIn))
	defer deadline.Stop()
	ticker := time.NewTicker(deps.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return loginExpiredError()
		case <-ticker.C:
		}

		result, err := deps.api.PollLogin(ctx, attempt.PollToken)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			var apiErr *api.Error
			if errors.As(err, &apiErr) {
				if apiErr.StatusCode == http.StatusNotFound {
					return loginExpiredError()
				}
				if apiErr.StatusCode < http.StatusInternalServerError && apiErr.StatusCode != http.StatusTooManyRequests {
					return fmt.Errorf("poll browser login: %w", err)
				}
			}
			// Network errors, rate limits, and rolling-deploy 5xx responses are
			// transient; keep polling until the deadline.
			continue
		}
		if result.Status != "approved" {
			continue
		}
		if result.User.CLIKey == "" {
			return newCommandError("the Hookspot server approved the login without a CLI key", "Run 'hookspot login' again.")
		}

		projectUID := ""
		if result.Project != nil {
			projectUID = result.Project.UID
		}
		if err := deps.store.SaveLogin(result.User.CLIKey, projectUID); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		lipgloss.Fprintln(deps.out, cards.Done("Logged in as", result.User.Email))
		if result.Project != nil {
			lipgloss.Fprintln(deps.out, cards.Done("Active project set to", projectDisplayName(*result.Project)))
		} else {
			fmt.Fprintln(deps.out, "No active project set; run 'hookspot project use' to select one.")
		}
		return nil
	}
}

// loginExpiredError covers a sign-up whose email confirmation outlasted the
// attempt: the account now exists, so a fresh login succeeds.
func loginExpiredError() error {
	return newCommandError("login attempt expired", "Run 'hookspot login' again; this also works if you just created your account.")
}

// browserLoginTTL clamps the server-provided lifetime so a bad value can not
// keep the CLI polling indefinitely.
func browserLoginTTL(expiresIn int) time.Duration {
	if expiresIn <= 0 {
		return browserLoginDefaultTTL
	}
	ttl := time.Duration(expiresIn) * time.Second
	if ttl > browserLoginMaxTTL {
		return browserLoginMaxTTL
	}
	return ttl
}

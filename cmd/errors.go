package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"charm.land/lipgloss/v2"

	"hookspot/internal/api"
	"hookspot/internal/cards"
	"hookspot/internal/ws"
)

// commandError carries user-facing recovery guidance through wrapped errors.
// Commands return it; only HandleError decides how it is presented.
type commandError struct {
	message string
	hint    string
	cause   error
}

func (e *commandError) Error() string {
	if e.cause == nil {
		return e.message
	}
	if e.message == "" {
		return e.cause.Error()
	}
	return e.message + ": " + e.cause.Error()
}

func (e *commandError) Unwrap() error { return e.cause }

func newCommandError(message, hint string) error {
	return &commandError{message: message, hint: hint}
}

func wrapCommandError(message, hint string, cause error) error {
	if cause == nil {
		return nil
	}
	return &commandError{message: message, hint: hint, cause: cause}
}

func loginRequiredError() error {
	return newCommandError(
		"not logged in",
		"Run 'hookspot login' or set HOOKSPOT_CLI_KEY.",
	)
}

// HandleError is the single presentation boundary for fatal command errors. It
// writes at most one message and returns the process exit code. Long-running
// commands should consume recoverable errors before they reach this function.
func HandleError(out io.Writer, err error) int {
	if err == nil || errors.Is(err, context.Canceled) {
		return 0
	}

	message, hint := fatalErrorMessage(err)
	if cards.Terminal(out) {
		lipgloss.Fprintln(out, cards.Error(message, hint, cards.Width(out)))
		return 1
	}
	fmt.Fprintln(out, cards.Sanitize(message))
	if hint != "" {
		fmt.Fprintln(out)
		fmt.Fprintln(out, cards.Sanitize(hint))
	}
	return 1
}

func fatalErrorMessage(err error) (string, string) {
	var commandErr *commandError
	if errors.As(err, &commandErr) {
		return commandErr.Error(), commandErr.hint
	}

	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Outdated:
			return apiErr.Message, upgradeCommand()
		case apiErr.StatusCode >= 300 && apiErr.StatusCode < 400:
			return fmt.Sprintf("Hookspot API redirect blocked: %s %s returned %s", apiErr.Method, apiErr.URL, apiErr.Status()),
				"Redirects are not followed to protect the Hookspot CLI key. Install the correct Hookspot release."
		case apiErr.StatusCode == http.StatusUnauthorized:
			return "authentication failed: the Hookspot CLI key was rejected", "Check the key, then run 'hookspot login' again or update HOOKSPOT_CLI_KEY."
		case apiErr.StatusCode == http.StatusForbidden:
			return "authorization failed: the Hookspot CLI key cannot access this resource", "Check that the key belongs to the selected project and has the required access."
		case apiErr.StatusCode == http.StatusNotFound:
			return err.Error(), projectNotFoundHint()
		case apiErr.StatusCode == http.StatusTooManyRequests:
			return "Hookspot API rate limit exceeded", "Wait briefly and try the command again."
		case apiErr.StatusCode >= 500:
			return fmt.Sprintf("Hookspot API is unavailable (%s)", apiErr.Status()), "Try again shortly. If the problem continues, check the Hookspot service status."
		default:
			return err.Error(), ""
		}
	}

	var sessionErr *ws.SessionError
	if errors.As(err, &sessionErr) {
		switch sessionErr.Kind {
		case ws.SessionAuthentication:
			return "authentication failed: the WebSocket session was rejected", "Run 'hookspot login' again or update HOOKSPOT_CLI_KEY."
		case ws.SessionProtocol:
			return err.Error(), "The server sent an invalid WebSocket message. Retry the command; if it continues, report the error."
		case ws.SessionHandler:
			return err.Error(), "The delivery could not be processed. Check the error and retry the command."
		case ws.SessionNotFound:
			return "project not found: the WebSocket channel join was rejected", projectNotFoundHint()
		case ws.SessionOutdated:
			return sessionErr.Err.Error(), upgradeCommand()
		}
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		detail := strings.TrimSpace(urlErr.Err.Error())
		if detail == "" {
			detail = "network request failed"
		}
		return "cannot reach the Hookspot API: " + detail, "Check your network connection and the configured Hookspot server URL."
	}

	return err.Error(), ""
}

// projectNotFoundHint also covers configs from older CLI versions, which may
// hold an unprefixed project UID that the server no longer accepts.
func projectNotFoundHint() string {
	return "The project may have been deleted, your access removed, or its UID saved by an older Hookspot CLI. Select a project with 'hookspot project use', 'hookspot login', --project, or HOOKSPOT_ORGANIZATION_SLUG and HOOKSPOT_PROJECT_SLUG."
}

# Native release procedures

These versioned checklists are immutable once used. `native-manual` records an
operator assertion; it does not execute a checklist. For a behavioral/manual
check, record any failed or ambiguous procedure as `--result fail`, and rerun
the whole procedure before recording a later pass. Failed smoke collections
remain diagnostic records rather than `native-manual` results. Every behavioral
procedure requires a successful `native_release_v1` observation for the same
binary hash first.

## native_release_v1

1. Run the supplied smoke collector against the exact retained archive and
   target on a matching host, recording truthful host, provenance, and execution
   values.
2. Pass only when version/help exit 0, neither times out nor has capture failure,
   help is nonempty, stderr is empty, all nine version fields and receipt,
   archive, and binary hashes match, and aggregation classifies the observation
   as native. Emulated and unknown observations are not native passes.

## network_release_v1

1. Use the receipt's exact `builder_platform` binary, an isolated explicit
   config, and dedicated key, project, named source, and harmless unique
   webhook in the same environment; use normal OS TLS validation.
2. Pass login, project list/use, one named-source listen session, the exact
   triggered request, and the expected service-side CLI response.
3. Assert no key disclosure and no unexpected redirect, authentication, TLS,
   or reconnect error, then perform one clean Ctrl-C. Do not override the
   endpoint, install a custom CA, or disable TLS. The pass boundary is all
   assertions succeeding; it remains unverified until approved URLs and these
   dedicated inputs exist.

## Windows procedures

`windows_config_private_v1`:

1. On a native matching host and local NTFS scratch path, use the exact binary
   and create a new explicit config with a dedicated key.
2. Assert a regular non-reparse file, current-user owner, protected DACL with
   only current-user/SYSTEM file allow ACEs, and an unchanged existing parent
   descriptor; change the selected project and recheck.
3. In a disposable junction/reparse-chain control, require rejection before
   any Hookspot contact or config create/change.
4. In a separate disposable parent-rights control, grant untrusted delete or
   ACL-mutation rights and require rejection before any Hookspot contact or
   config create/change. Pass only when every assertion succeeds; unsupported
   privilege/filesystem is unverified.

`windows_password_input_v1`:

1. From a fresh path and real Windows console, with no key flag/env and an unused
   config, enter a dedicated valid key; assert it is never echoed, echo is
   restored, and login succeeds.
2. From another fresh path, press Ctrl-C at the prompt; assert return within
   five seconds, no config, no key output, and restored echo. Redirected/IDE
   input does not satisfy it. Pass requires both success and cancellation paths.

`windows_signal_cancel_v1`:

1. Use the exact binary with a dedicated project/source and prove the WebSocket
   session with one unique delivery.
2. Press Ctrl-C once; require status 0 within five seconds with no panic/stack/
   error noise, then require a fresh invocation to start normally. Pass requires
   both runs; this does not cover later interrupts while output is blocked.

Used procedure IDs are immutable: add `v2` rather than editing a used checklist.
Secrets are process-only and never appear in argv or logs. Parameterize target,
architecture, host kind, lowercase operator ID, and procedure; do not copy
machine-specific native claims. The synthetic `.invalid` TLS/Phoenix test writes
records with ID `local-tls-phoenix-v1`; that ID is not a real publication
procedure. Its test-local records are never publication inputs and never
satisfy the real `network_release_v1` procedure.

# What developers need from a local webhook relay CLI, and what Hookspot should cover

- **Date:** 2026-09-24.
- **For:** the Hookspot product owner.
- **Decision it supports:** what to add to `docs/plans/20260924-local-webhook-setup.md` ("the plan"), what goes to follow-up plans, and what to never build.
- **Inputs:** the plan and research notes 00, 01, 02, and T1–T6 in `tmp/research_notes/`. This report adds no new research. It merges and ranks what the notes found.
- **Note (2026-09-24, after this report):** the plan was trimmed to fixes of existing functionality. The "Where in the plan" column and task numbers below refer to the pre-trim plan. Dropped features (source auto-create, `hookspot sources`, dashboard links) and the §4b roadmap are tracked in `docs/backlog/`.

## Executive summary

1. The plan already beats its peers on the hard parts of setup. `listen` never prompts, creation is safe under concurrency, and Hookspot forwards bodies and multi-value headers more faithfully than Hookdeck, Svix, or smee. The gaps come after the first event: knowing when the listener is ready, learning about missed events, and replaying a request.
2. **Add now 1 (CLI, S):** print `Ready` only after the channel join. After a reconnect, say how long the CLI was offline and that requests in the gap weren't delivered, with a dashboard link.
3. **Add now 2 (CLI, S):** show next steps when `listen` creates or connects a source: the URL is permanent, paste it into the provider now, verify with the secret the provider shows for *this* endpoint, and here is a `curl` self-test.
4. **Add now 3 (CLI, S):** add dashboard links to the banner and to every request line. Until a CLI retry command exists, this is how a person replays a specific request.
5. **Add now 4 (backend, S):** remove `plug :accepts, ["json"]` from the ingest pipeline. It returns 406 to webhooks whose `Accept` header doesn't allow JSON, and those requests are never stored (reproduced).
6. **Add now 5 (CLI, S each):** assign a stream (stdout or stderr) to every new output line, name the project in every banner, accept `--forward-to 3000`, stop sending the local hop through `HTTP_PROXY`, and print the `Location` of 3xx responses.
7. **Follow-up 1:** `hookspot requests retry <uid>`, over a new `/cli` route that reuses the dashboard's retry. Replay has the strongest demand, and this closes the biggest gap for agents and Compose, where Enter-to-replay is off.
8. **Follow-up 2:** a short server-side grace window before a delivery is marked `cli_offline`. It covers sleep, deploys, restarts, and the join race without an offline queue.
9. **Follow-up 3:** fix fan-out correctness. Today one listener whose app is down triggers retries to every listener, and whichever listener answers last sets the delivery status.
10. **Skip:** guest mode, a `listen` TUI, `--json`, an MCP server, trigger and fixtures, re-signing and verification helpers, an automatic offline queue, and per-developer routing. Section 4c says what would reopen each one.

## 1. Scope and method

**Question.** What do developers need from a webhook relay CLI across the whole local loop? Which of those needs should Hookspot cover now, later, or never?

**Tools compared**
- **Relays** (the vendor hosts the URL, stores the request, and pushes it to the CLI):
  - Stripe CLI, which is provider-specific and re-signs events [65, 132].
  - Hookdeck CLI [72, 133].
  - smee.io and smee-client (plus gosmee).
  - Svix CLI `listen` [81].
  - webhook.site with whcli [80].
- **Tunnels** (a generic proxy to a local port): ngrok [76, 77], cloudflared quick tunnels [79], localtunnel [62], and Tailscale Funnel.
- **MCP-era entrants** from 2026, mostly vendor pages with 0–5 GitHub stars: Hooklistener [122], webhooks.cc [123], webhook-toolkit [124], RequestBin's MCP [125], webhook-co, and OtterKit.
- **Compared at source level:** Stripe CLI (commit 160f231) and Hookdeck CLI (commit c601293). Hookspot's CLI and backend were read at their 2026-09-24 working trees [130, 131].

**Sources**

| Type | Coverage | Caveat |
|---|---|---|
| GitHub issues | stripe-cli, hookdeck-cli, smee-client and smee.io, svix, localtunnel, gosmee; ranked by upvotes and comment count | The strongest demand signal. Hookdeck's issues are mostly written by its maintainers, so they show product intent, not user demand. |
| Docs | Provider docs (Stripe, Slack, Meta, Zoom, Microsoft Graph, GitHub, Twilio), tool docs, and the Standard Webhooks spec | Mostly undated. |
| Source code | Stripe CLI, Hookdeck CLI, Svix CLI, Hookspot CLI and backend | Some findings are inferences from reading code, marked I. |
| Reddit | 62 threads fetched and 61 read with comments (876 comments) from 16 subreddits, 2020-06 to 2026-08 [105] | Found by title search only. Heavily seeded by vendors since 2025. |
| Hacker News | 8 threads plus 4 comment searches, 2017–2026 | |
| Stack Overflow | The top questions on signature errors, redirects, and handshakes | |

**Date range.** 2014 (ngrok #169) to 2026-09-23. Most evidence is from 2019–2026. Every source was accessed on 2026-09-24.

**Evidence labels**
- **E:** read directly in a source or in code. E-test means it was also run locally.
- **I:** an inference.
- **C:** the sources conflict.
- **U:** unknown.

**Evidence strength**
- **Strong:** several independent sources, a high-signal issue (at least 10 upvotes or 20 comments), or a reproduced code path.
- **Medium:** one or two primary sources, or code-verified behavior with no user reports.
- **Weak:** a single anecdote, a vendor claim, or an inference.

**Ranking and cost.** Needs are ranked by evidence strength × user impact ÷ cost.
- **Cost:** S is up to 1 day, M is a few days, and L is a week or more.
- **BE:** whether the change needs the backend.

**Fixed constraints.** The recommendations respect these:
- No new environment variables.
- `listen` never prompts.
- Commands behave the same in Compose and CI as in a terminal, and never hang.
- YAGNI.
- The owner's decisions:
  - `listen` auto-creates sources.
  - A `sources` TUI plus flag subcommands.
  - Server-side delivery filtering per source.
  - The disabled-source warning.
  - End-to-end browser sign-up through `hookspot login`, with no guest mode.

## 2. Ranked needs across the journey

- Needs from different notes are merged into one entry each, placed in the stage where the problem first appears, with cross-references between stages.
- Rows are ranked within each stage.
- **Rec** is one of: Add now (current plan), Follow-up, Skip, or Keep (already covered).

### 2.1 Setup and first event

| # | Need | Evidence | How peers solve it | Hookspot today / plan gap | Rec | Cost | BE |
|---|---|---|---|---|---|---|---|
| 1.1 | **The listener is live before the first event arrives** (a provider test or GitHub `ping` must not land while no CLI is listening) | **Strong (E).** GitHub sends `ping` as soon as a webhook is created [89]. Test harnesses regex a "Ready" line with 20–300 s timeouts [126, 127]; see also stripe #760 [18]. Hookspot prints "Waiting for requests..." *before* the WebSocket join (`listen.go:119` runs before `:130`). A delivery that arrives before the join, or during the 10×2 s connect retries, becomes `cli_offline` and is never retried (`deliver_job.ex:32,65-68`) [130, 131]. | Stripe prints `Ready!` only once its session exists. Hookdeck prints "Connected" after connecting [132, 133]. | Gap. | **Add now.** Print `Connecting…`, then `Ready. Waiting for requests (Ctrl-C to quit)` only after the join, and document that line as the readiness contract. `sources create` also says "start `listen` before sending a provider test." | S | N |
| 1.2 | **A permanent URL, and being told it's permanent** | **Strong (E).** URL churn is the top Reddit theme (12 threads) [105, 106, 107]. HN 21454645: re-entering the URL in the Stripe dashboard "was pretty tedious" [94]. localtunnel's `--subdomain` is unreliable (66 upvotes) [62]. Quick-tunnel URLs are random per run [79]. webhook.site free URLs expire after 7 days [80]. ngrok's free plan has one dev domain [76]. Stripe CLI users value "no changing URLs" [108]. | Hookdeck: permanent source URLs. Stripe: no URL at all. ngrok: one free static domain. | Strong: a permanent `src_…` URL per source, and the plan warns on rename and delete. The output never says the URL is permanent. | **Add now.** Change the creation line to `Webhook URL (permanent; paste it into Stripe once): …`. | S | N |
| 1.3 | **A brand-new user can sign up from `hookspot login` and land back in the CLI** | **Medium (I, from E-code; not run end to end).** Password sign-up requires email confirmation and doesn't log the user in. Onboarding redirects to the dashboard, not to `/cli/login/:token`. Approval requires a project, and the attempt expires after 10 minutes. The approval page's `no_project_hint` suggests `hookspot project use`, which can't help a user with no organization [131]. | Hookdeck skips sign-up with a guest account and upgrades it through the browser later. Stripe requires an existing account. | Owner decided: fix it, with no guest mode. | **Add now (decided).** 1. Walk the path manually. 2. Keep the CLI return path through sign-up, email confirmation, and onboarding. 3. Let the approval page create the first organization and project inline, then approve. 4. On expiry, say "run `hookspot login` again." | S–M | Y |
| 1.4 | **Name the project on every run** (no silently wrong target) | **Medium (E).** Hookdeck #334: `listen` silently fell back to a guest account [41]. stripe #1581: an env var silently overrode `--api-key` [31]. | Hookdeck announces profile changes on stderr. | The plan prints `Org \| Project` only when it creates a source, so a stale `.hookspot` config can listen in the wrong project without anyone noticing. | **Add now.** `Listening on 1 source • 1 connection in Acme \| Payments`. | S | N |
| 1.5 | **`--forward-to 3000` just works** | **Medium (E-code).** Today `3000` is accepted as a hostname, and it fails at the first delivery with a DNS error [130]. The plan's own guard redirects `listen 3000` users to `--forward-to`. Reddit prizes short `ngrok http 3000`-style commands [105]. | Stripe maps a bare number to `localhost:PORT` [132]. Hookdeck takes a port positional [133]. | The plan defers this on the grounds that "the first delivery already reports connection refused". It actually reports a DNS error. | **Add now.** Keep the guard on positional source names. | S | N |
| 1.6 | **Reach the app from a CLI running in Docker** | **Medium–Strong (E).** stripe #427, #714, #1159 [9, 17, 25]; hookdeck #18 [34]; Docker's `host-gateway` [82]. | Official images, plus README notes on `host.docker.internal`. | The plan's Compose example (`http://app:3000`) is right. There's no container-aware hint and no example for an app running on the host. | **Add now.** When `/.dockerenv` or `/run/.containerenv` exists and the target is localhost, the "connection refused" hint explains that `localhost` is the container itself. The README adds `--forward-to http://host.docker.internal:3000` with `extra_hosts: ["host.docker.internal:host-gateway"]`. | S | N |
| 1.7 | **The local hop bypasses a corporate proxy** | **Medium (E-code; impact I, no reports).** The forwarder uses `http.DefaultTransport`, which applies `HTTP(S)_PROXY` to everything except localhost and loopback. Docker injects the proxy variables into every container [83], so `http://app:3000` goes to the corporate proxy [130]. | Stripe and Hookdeck forward through their own Transport with no proxy [132, 133]. | Gap. | **Add now** (Task 11 already edits `proxy.go`). Give the forwarder its own Transport with `Proxy: nil`, and add a test with `HTTP_PROXY` set. | S | N |
| 1.8 | **Providers that verify the URL with a handshake can register it** (Slack `url_verification`, Meta `hub.challenge`, Graph `validationToken`, Zoom, Twitch, Dropbox) | **Strong for those providers (E).** Their docs require the endpoint to echo a challenge synchronously [84, 86, 87, 88]. Zoom disables a subscription after 6 failed revalidations [87]. SO 52872580 has 9,471 views [104]. It also comes up in a Reddit thread [117]. Hookspot ingest answers immediately with a static 200 or the static custom response (`respond_service.ex`) [131]. | Tunnels pass the handshake through synchronously. Hookdeck completes it server-side for supported providers [74]. | A hard blocker for these providers: the first event never arrives. | **Add now:** a README caveat. **Follow-up:** server-side echo rules that need no secret (Slack, Meta, Graph, Twitch, Dropbox), prioritized by which providers users target (U). Zoom (which needs the secret stored) and a synchronous mode: later or never. | S / M | N / Y |
| 1.9 | **Ingest accepts whatever the provider sends** | **Strong on the code path (E-test); prevalence U.** Because of `plug :accepts, ["json"]`, Phoenix answers 406 to `Accept: text/plain`, `application/xml`, or `text/html` without `*/*`, before anything is stored. Reproduced with `mix run` [131]. | Relays and tunnels ignore `Accept` (I). | Silent loss: the request never shows up in history, and the provider may disable the endpoint. | **Add now** as a standalone backend fix: drop `:accepts` from the ingest catch-all, keep it for `/up`, and add a test with `Accept: text/plain`. | S | Y |
| 1.10 | **Log in anywhere without hanging** | **Strong (E).** stripe #1168 [26]; hookdeck #400, #373, #334 [41, 44, 46]. | Pairing code plus URL, a paste-key fallback, and an env key. | Covered. Hookspot prints the code and URL before opening the browser, has `login -i` and `HOOKSPOT_CLI_KEY`, and gives up after a bounded wait (about 10 minutes) [130]. | **Keep.** Add WSL2 (no `xdg-open`) to the manual tests. | S | N |
| 1.11 | **Install through a familiar package manager, and get a binary the OS will run** | **Strong (E).** stripe #804 asked for npm (52 upvotes) [20]. pnpm v10 blocks Hookdeck's postinstall [37]. winget got 15 upvotes over six years [11]. Windows flags the Stripe CLI as malware (31 comments) [15]. | Stripe: apt, brew, scoop, Docker, and npm since 2026-05. Hookdeck: npm with a postinstall (which broke), brew, Docker. | Good. The npm package bundles all 6 binaries with no `postinstall`, the shim forwards SIGTERM, and there's a Homebrew formula [130]. There's no winget or scoop, no Windows signing, and no Docker image. | **Keep.** **Follow-up:** a SmartScreen note in INSTALL (S) and an official Docker image (M). **Skip** winget and code signing until users report blocks. | – | N |
| 1.12 | **Self-signed local HTTPS, an outbound proxy, and Windows/WSL2** | **Medium (E).** stripe #108 (16 upvotes; fixed the same day with `--skip-verify`) [3]. smee #84: the proxy was "a show stopper" [52]. stripe #970: a WSL2 hang (15 upvotes) [22]. | `--skip-verify` / `--insecure`; the proxy env vars are honored. | No way to skip TLS verification. The outbound proxy works but isn't documented. WSL2 is untested. | **Follow-up:** `--insecure` (S) and a docs line on `HTTPS_PROXY` and egress (S). **Add now:** a WSL2 row in Post-Completion. | S | N |
| 1.13 | **Try it without an account** | **Medium (E).** Svix, smee, cloudflared, and Hookdeck need no account [79, 81]. Hookdeck's guest accounts have "no delivery history, retries", and a 2026 bug trail (#334, #373) [41, 44]. | Guest sandboxes and anonymous URLs. | Login is required. | **Skip** (owner decision). Row 1.3 covers the new-user path. | L | Y |
| 1.14 | **`localhost` works whether the app listens on IPv4 or IPv6** | **Weak (C).** The concern comes from a 2014 ngrok issue [63]. T1's Go test reached both a `::1`-only and a `127.0.0.1`-only server through `localhost`. | – | Robust, unless the user types a literal loopback address that doesn't match what the app binds. | **Skip** (see §3.3). | – | N |

### 2.2 Iterating on a handler

| # | Need | Evidence | How peers solve it | Hookspot today / plan gap | Rec | Cost | BE |
|---|---|---|---|---|---|---|---|
| 2.1 | **Replay a request without redoing the provider flow** | **Strong (E).** "I still prefer to setup ngrok because it allows me to view and replay requests" (HN 21455379) [94]. HN threads from 2017 and 2022 praise replay [95, 96]. stripe #1206 has 10 upvotes [27]. Replay is Reddit theme T3 (11 threads): "flip a switch and retry it" [105, 109]. | ngrok: replay from the inspector, optionally modified [77]. Hookdeck: the TUI `r` key on any event (recorded server-side), plus dashboard retry. Stripe: `events resend`, but the Dashboard "Resend" button skips the CLI [27]. | Enter replays only the *last* request, locally, and only when stdin is a terminal. A dashboard retry does reach a listening CLI, so Hookspot doesn't have Stripe's #1206 problem [131]. But nothing in the terminal leads to it. | **Add now:** a dashboard link in the banner and on each request line (`/:org/:project/requests/:uid`). **Follow-up:** `requests retry` (row 5.1). Keep Enter as the fast local replay. | S | N |
| 2.2 | **See what the handler answered** (status, latency, error body) | **Medium (E).** stripe #1011 asks for the response content [24]; HN comments praise request and response logs [94]. | Stripe: `[200] POST url [evt]`. Hookdeck: status, latency, and a link. ngrok: the full request and response. | Covered, and better than Stripe: status, latency, and request id on every line, and both bodies on non-2xx [130]. | **Keep.** | – | N |
| 2.3 | **Pause at a breakpoint or run a slow handler** | **Medium (E).** "How is it useful to step through the code when you only get 30 seconds…" (stripe #1010) [23]; see also #710 [16]. | Stripe: a hidden `--timeout` flag and one goroutine per event [132]. Hookdeck: a server-set timeout, one goroutine per event, and up to 50 connections [133]. | A fixed 30 s timeout. Deliveries are forwarded one at a time inside the WebSocket read loop (`ws/client.go:383-393`), so one paused request holds back the rest (I, untested). After a timeout, the server's retry 10 s later can run the handler a second time (I). | **Follow-up:** a `--timeout DURATION` flag (a flag, not an env var) (S). Then run a breakpoint test and decide on concurrent dispatch (M). | S / M | N |
| 2.4 | **Pin the payload version** (Stripe API version) | **Strong (E).** stripe #1335 has 49 upvotes, the most of any open `listen` issue [28]. A normal Stripe endpoint takes an `api_version` [69]. | Stripe `listen` uses the account default or `--latest` only. | A Hookspot URL is a normal provider endpoint, so the version is pinned at the provider (I). | **Add now (docs line):** "Set the endpoint's API version in Stripe." A free positioning win. | S | N |
| 2.5 | **Tell events apart at a glance** | **Weak (I).** Every Stripe event prints as `POST /webhooks/stripe`, while Stripe's CLI puts the event type first [6]. No user issue asks for this. | Stripe: the event type. Hookdeck: none. | One line per event, with no type. | **Follow-up:** a label taken from the JSON `type`, `X-GitHub-Event` plus `action`, or `X-Shopify-Topic`. Schedule it after the prefixed-uids plan, which also edits `printer.go`. | S | N |
| 2.6 | **Generate or edit events** | **Strong for Stripe (E).** `trigger` with custom data has 45 upvotes [5]. Reddit complains about fake fixture data and confusing `trigger` flags (theme T10, 10 threads) [105]. smee "Copy as curl" has 7 upvotes [51]. | Stripe's `trigger --override/--edit` creates real API objects [66]. ngrok offers edit-and-replay. | None. A generic relay can't create provider objects, and an edited body fails signature verification. | **Skip.** Provider CLIs own triggers. The `curl` self-test (rows 1.1 and 1.2) covers synthetic requests, and replay covers the rest. | M | N |
| 2.7 | **Several sources in one session** | **Weak–Medium (E).** hookdeck #70 [35]; HN 21455510 [94]. | Hookdeck: `'*'` or a list of sources. | Covered: `listen a b` tags each line with its source, and the plan adds the server-side filter. | **Keep.** | – | N |
| 2.8 | **An interactive `listen` TUI** (select, retry, inspect) | **Weak (C).** No user issue asks for it. Hookdeck's TUI caused failures without a TTY (#333) and a "looks connected" bug (#399) [40, 45]. | Hookdeck: a full-screen TUI by default. | Not planned. The plan's TUI budget goes to `sources` (owner decision). | **Skip for now** (§3.3). | L | N |
| 2.9 | **Output modes** (quiet, compact) | **Weak (E).** Hookdeck's `--output` issues were all filed by maintainers, with no user upvotes [48]. | Hookdeck `--output`. | One line per 2xx response, detail on failure. | **Skip.** | – | N |

### 2.3 Debugging failures

| # | Need | Evidence | How peers solve it | Hookspot today / plan gap | Rec | Cost | BE |
|---|---|---|---|---|---|---|---|
| 3.1 | **Know the listener is still live, and learn what was missed during a drop** | **Strong (E).** stripe #600 (19 upvotes, 45 comments): CI users were "scanning the stripe-cli debug log for 'Disconnected'" [13]. Also #389 (15 upvotes) [7], #80 after sleep (9 upvotes) [2], and #435 [10]. hookdeck #323: "no obvious error in the CLI output" [39]. smee #172 [54]. On Reddit, users close the CLI without realizing it must keep running [114]. | Both peers fixed reconnects (Stripe 1.4.3, Hookdeck v2.3.2). Neither tells you what you missed. | A fixed 2 s reconnect loop, with `reconnecting in 2s...` on stderr and no "reconnected" line. Deliveries during the gap become `cli_offline` and are never retried [130, 131]. | **Add now.** On rejoin, print `Reconnected (offline 14s). Requests that arrived meanwhile were not delivered: <dashboard link>`. The CLI already knows the gap and the slugs. **Follow-up:** the grace window (row 5.2). | S | N |
| 3.2 | **Recover from a restarting dev server, and see it happen** | **Medium (E).** stripe #313 (9 upvotes, open since 2019): "failed deliveries are not retried" [6]. Reddit: retries flood the log [112]. | Stripe: none; you resend by hand. Hookdeck: server-side retry rules. | The server retries 5xx, 408, and 429 up to 5 times at 10/20/30/40 s, including the CLI's own 502 on "connection refused" (`finalize_service.ex:41-48`) [131]. The CLI never shows this. 4xx responses aren't retried, unlike Stripe in production. | **Add now (docs):** the provider sees Hookspot's response, not your app's, and only 5xx/408/429 are retried. **Follow-up:** show `attempt 2/5 · retrying in 20s` (the backend adds attempt fields to the payload). | S / S+S | N / Y |
| 3.3 | **Understand 3xx redirects** | **Medium (E).** SO 75062050: a trailing-slash redirect, "Took me 2 hours" [103]. T2 found three more questions like it. | Stripe doesn't follow redirects [132]. Hookdeck follows up to 10, which hides a misconfiguration that providers won't tolerate [133]. | Hookspot correctly doesn't follow redirects. It prints `→ 308` but not the `Location`. The plan's trailing-slash change makes `APPEND_SLASH` and `trailingSlash` redirects more likely (I). | **Add now.** Print `└─ redirect → <Location>` and the hint `webhook senders don't follow redirects; use the final URL (check the trailing slash or https)`. | S | N |
| 3.4 | **Did the webhook reach Hookspot at all?** | **Medium (E).** Reddit theme T18 (5 threads): "the requests are not reaching our callback at all" [112, 116]. Providers themselves block local testing, for example Paddle's account verification and Meta's subscriptions [111, 116]. | Inspectors (webhook.site, ngrok). | Every accepted request is stored before delivery and visible in the dashboard [131], except in the 406 case (row 1.9). | **Keep.** The links (row 2.1) and the 406 fix make it usable. | – | – |
| 3.5 | **Clear errors when the local server is down** | **Medium (E).** stripe #427 printed a raw dial error [9]. | Hookdeck: a startup health check. | Hints for refused connections, timeouts, DNS, and TLS [130]. | **Keep.** The Docker hint is row 1.6. Skip a health check: server retries already absorb "not started yet". | – | N |
| 3.6 | **Readable non-JSON bodies** | **Weak–Medium (E).** smee.io #11: form bodies were lost (5 upvotes) [56]. | ngrok pretty-prints JSON and XML. | The bytes are exact, but form bodies print as one truncated line. | **Follow-up:** print form bodies as `key = value` lines (Slack, Twilio). | S | N |
| 3.7 | **Large bodies** | **Weak (E).** smee.io returned 413 at 156 KB [58]. | smee: a hard limit. | No explicit ingest cap and 32 MiB WebSocket frames. A local response over 16 MiB ends `listen` (I) [130]. | **Skip** until someone hits it. | S | N |

### 2.4 Signature verification and payload fidelity

| # | Need | Evidence | How peers solve it | Hookspot today / plan gap | Rec | Cost | BE |
|---|---|---|---|---|---|---|---|
| 4.1 | **A byte-exact body and unchanged signature headers** | **Strong (E).** smee re-serialized JSON and broke signatures for years: #136 (9 upvotes) and #325 [53, 55]. Standard Webhooks requires that the payload sent is the payload signed [91]. | Hookdeck: "doesn't change the body or headers" [73]. Svix: a base64 body with single-value headers [81]. Stripe: re-signs. | Byte-exact end to end: `bytea` storage, base64 on the wire, a `bytes.Reader` locally, and every value of a repeated header kept. The most faithful of Hookspot, Hookdeck, and Svix [130, 131, 133]. Nowhere is this stated. | **Keep, and add a docs line now:** "Hookspot forwards the body byte for byte with the provider's signature headers; verify with the provider's secret, as in production." Optionally add a backend test for non-UTF-8 bodies. | S | N |
| 4.2 | **Know which secret to verify with** | **Strong (E).** Stripe: "The most common error is using the wrong endpoint secret" [68]. stripe #672 and #475 ask how to get the CLI's secret into a container [12, 14]. Reddit theme T5 (6 threads) [112, 113]. | The Stripe CLI prints a per-session `whsec` and has `--print-secret` [65]. Hookdeck signs with a project secret in its own header. | Because signatures pass through, the right secret is the one the provider shows for the endpoint registered at the Hookspot URL. It's stable because the URL is, and nothing has to be injected into containers. The CLI never says so. | **Add now:** one line in the next-steps block: "verify with the signing secret the provider shows for *this* endpoint (not production's)." | S | N |
| 4.3 | **A replay after about 5 minutes still verifies** | **Strong in the docs (E), and code (E).** Stripe's libraries default to a 5-minute tolerance, and Stripe re-signs its own retries [67]. Slack and Standard Webhooks check timestamps too [85, 91]. Hookdeck documents the same failure for its retries [73]. See also svix #93 [61]. Enter-replay and dashboard retries resend the original timestamped signature [130]. | Stripe's `events resend` re-signs, because Stripe is the provider. Hookdeck documents the limitation. | No hint. A stale-signature 400 looks like a bug in the handler. Automatic retries (10–40 s) are within the window. | **Add now (docs line).** **Follow-up:** when a *replayed* delivery gets 400, 401, or 403 and a known timestamp header (`Stripe-Signature` `t=`, `webhook-timestamp`, `svix-timestamp`, `X-Slack-Request-Timestamp`) is more than 5 minutes old, say so. It needs no secrets. | S | N |
| 4.4 | **Framework raw-body pitfalls** | **Strong (E).** SO 53899365: 144 votes, 89.8k views [102]. stripe-node #341: 86 comments [59]. The Next.js rawBody RFC: 68 reactions [60]. "agents almost always get this wrong" [99]. Reddit theme T4 (9 threads) [111]. | Provider docs and SDK error messages. No relay detects it. | Hookspot can't tell a raw-body 400 from any other 400. | **Follow-up (docs):** a short "Verifying signatures behind Hookspot" section that links to the provider pages. Skip detection. | S | N |
| 4.5 | **URL-signed providers** (Twilio) | **Medium (E).** Twilio signs the full public URL [90]. | ngrok keeps the public Host (I). | The path is replaced by the destination path and `Host` is dropped. | **Follow-up (docs):** validate against the Hookspot source URL. | S | N |
| 4.6 | **The relay re-signs events or verifies them for me** | **Weak (E).** Stripe can re-sign only because it is the provider [132], and stripe #797 shows re-signing bugs [19]. ngrok's `verify-webhook` needs the secret at the edge [78]. | Stripe's `whsec`, ngrok's edge verification, `svix signature verify`. | None. Hookspot holds no provider secrets. | **Never** (see §4c). | M–L | Y |

### 2.5 History and replay

| # | Need | Evidence | How peers solve it | Hookspot today / plan gap | Rec | Cost | BE |
|---|---|---|---|---|---|---|---|
| 5.1 | **Resend a specific past request without a terminal or a browser** | **Strong (E).** stripe #1206 (10 upvotes): the CLI resend was "a saver, after some hours of debug" [27]. See also [6, 94], and Portr's launch thread on the importance of "inspect and replay" [97]. Every agent-oriented peer offers it [122, 123, 133]. | Stripe `events resend`. Hookdeck `gateway request retry` and the TUI `r` key. MCP tools `replay_request` and `forward_request`. | The backend has `Ingest.API.Request.retry(uid)` behind the dashboard (`request_controller.ex:55-58`), but no `/cli` route. Enter needs a TTY and the dashboard needs a browser [130, 131]. | **Follow-up 1.** `hookspot requests retry REQ_UID` over a new `/cli/projects/:project_uid/requests/:uid/retry` route. It reuses Task 2's `put_cli_project` and Task 4's 403 and `reason` mapping. | S–M | Y |
| 5.2 | **Don't lose requests during a short outage** (sleep, deploy, restart, the join race) | **Medium (E).** [2, 39]. Reddit theme T8, "store the raw request first" (10 threads, many seeded by vendors) [105, 110, 115]. | Hookdeck holds events during a grace window after an abnormal disconnect and offers `pause`. Otherwise it discards events that arrive with no listener, and replay is manual [72]. | Requests are stored first and kept 14 days. A delivery with no listener becomes `cli_offline` and is never retried [131]. | **Add now:** the reconnect notice (row 3.1). **Follow-up 2:** a grace window, for example re-checking every 5 s for up to 60 s before marking `cli_offline`. **Skip** an automatic offline queue (§3.3). | S–M | Y |
| 5.3 | **Inspect or export a past request by ID** | **Medium (E).** smee "Copy as curl" (7 upvotes) [51]. smee.io "Download payloads?", to commit them to a test suite [57]. Hookdeck added `request list/get/raw-body` on 2026-02-18, along with its MCP server [133]. | API-backed list and get commands. | Dashboard only. The live output truncates by default. | **Follow-up:** `requests get REQ_UID` (full headers, the raw body to stdout, and the forward result) and `requests list SOURCE --limit N`. Human-readable text first. | M | Y |
| 5.4 | **History kept long enough** | **Strong (E).** ngrok: 24 h on the free plan, 72 h on paid plans [77]. Hookdeck: 3, 7, or 30 days [75]. webhook.site: 7 days [80]. smee: none. | Tiered retention. | 14 days for every organization [131]. | **Keep,** and mention it in the docs. | – | N |
| 5.5 | **Edit and replay, bulk replay, search by content** | **Weak (E).** ngrok's modified replay [77]; stripe #214, "backfill", got 1 upvote in 7 years [4]. | ngrok's web editor; Hookdeck's bulk retry. | None. | **Skip.** If it's ever needed, it belongs in the dashboard. | M–L | Y |

### 2.6 Team usage

| # | Need | Evidence | How peers solve it | Hookspot today / plan gap | Rec | Cost | BE |
|---|---|---|---|---|---|---|---|
| 6.1 | **Correct status and retries when several CLIs receive the same delivery** | **Medium–Weak (I, from E-code; untested).** Every CLI answers with the same `attempt_uid`, and the last answer sets the status. A teammate whose app is down returns 502, which schedules up to 4 retries that are broadcast to everyone (`finalize_service.ex:37-51`, `project_channel.ex:58-68`) [131]. Oban uniqueness caps it at about 4 extra broadcasts. Compose replicas duplicate every delivery the same way (I). | Hookdeck creates one event per session: "Sessions do not compete" [72]. | Plan row 18 says "last response wins" but misses both the retry amplification and the replica duplicates. | **Add now:** correct row 18 and the Compose docs ("each `listen` replica forwards every delivery; run one"). **Follow-up 3:** a system retry skips a delivery that's already `ok`, and the attempt records who responded. First confirm the behavior with a two-socket `Phoenix.ChannelTest`. | S | Y |
| 6.2 | **Isolation from teammates' test events** | **Medium (E).** Stripe recommends a sandbox per developer [71]. "the standard answer at team scale is a per-developer App" [64]. Reddit reports cross-talk between two `listen` sessions [113]. Homegrown team relays were built to fan out to everyone, not to isolate [100, 101, 129]. Stripe has documented since 2019 that it can't limit events to one user [33], yet T4's searches found no stripe-cli issue asking for it. | Isolation at the provider. Hookdeck splits traffic by content, not by user. | Per-developer sources already work with the plan: `hookspot listen stripe-alice` (I). | **Add now (docs):** a "Teams" note: one source per developer, each registered in that developer's sandbox or app. Per-developer routing stays deferred. | S | N |
| 6.3 | **Know who else is listening** | **Weak–Medium (E).** Hookdeck documents that every session gets a copy [72]. An HN user notes there's "no way to shut it down remotely and open it elsewhere" [96]. | Hookdeck shows CLI sessions in its dashboard. | Presence tracks each user's sessions, but the CLI shows nothing [131]. | **Follow-up:** `⚠ also listening to stripe: bob@acme (every request goes to all of them)`. | S+S | Y |
| 6.4 | **A dashboard retry goes to the person who clicked it** | **Weak (E).** Hookdeck routes retries to the same user [72]. Hookspot stores `triggered_by_uid` but broadcasts to everyone [131]. | Hookdeck. | Teammates receive each other's retries. | **Follow-up,** as the first piece of any routing plan. | M | Y |
| 6.5 | **Privacy of stored payloads** | **Weak–Medium (E).** smee #27, "Authenticated channels" (10 upvotes) [50]. ngrok warns that stored bodies are visible to the whole team [77]. | Authenticated channels; opt-in capture. | Channels require a CLI key, and the join access check is in the prefixed-uids plan. Viewers receive full bodies (I). | **Skip** until a customer asks. | M | Y |

### 2.7 CI and automated tests

| # | Need | Evidence | How peers solve it | Hookspot today / plan gap | Rec | Cost | BE |
|---|---|---|---|---|---|---|---|
| 7.1 | **Never block, and fail fast with the fix** | **Strong (E).** Stripe PR #1537: login "blocks indefinitely in any non-interactive context" [32]. stripe #1359 [30]. hookdeck #400 and #337–#339 [42, 46]. Hookdeck's agent evals [49]. | Stripe sniffs agent env vars. Hookdeck adds TTY guards and `ci --api-key`. | Covered. `listen` never prompts, the plan's `isInteractive` requires both stdin and stdout to be terminals, and a missing key prints `not logged in` with the fix [130]. | **Keep.** No agent detection. | – | N |
| 7.2 | **Readiness a machine can detect** | **Strong (E).** Real harnesses regex Stripe's human `Ready!` line, even when they pass `--format json` [126, 127]. See also stripe #760 [18]. | Stripe `Ready!`; Hookdeck "Connected". | Same gap as row 1.1. | **Add now** (row 1.1). No health port or ready file. | S | N |
| 7.3 | **Data on stdout, diagnostics on stderr** | **Strong (E).** Hookdeck needs a major version to move its diagnostics off stdout [43]; see also #433 [48]. stripe #1353: `jq` breaks on a spinner line [29]. Stripe's JSON mode still prints text response lines on stdout when forwarding (code-read) [132]. | Hookdeck v3, plus a test that diagnostics never reach stdout. | The plan adds `Created source…`, `Connected source…`, the route hints, the disabled warning, and the rename warning without saying which stream each goes to. | **Add now.** The banner, URLs, and request blocks go to stdout. Hints, warnings, and notices go to stderr. This keeps a later `--json` additive instead of breaking. | S | N |
| 7.4 | **Exit codes that tell the truth** | **Medium (E).** hookdeck #339: `listen` "exits 0 within milliseconds having forwarded nothing" [42]. stripe #813 [21]. | Hookdeck: `Renderer.Err()`. | Exits 1 on any error and 0 on SIGINT or SIGTERM. The first connection gives up after 10×2 s [130]. None of this is documented. | **Add now:** one line in help and the README, and a test that auth, project, and connection failures exit non-zero. | S | N |
| 7.5 | **Clean shutdown under a supervisor** | **Medium (E).** hookdeck #429: its npm shim didn't forward signals, and the process was still alive 12 s later [47]. | – | Hookspot's shim forwards SIGTERM [130]. Whether `npx -y hookspot@…` running as PID 1 in `node:22-alpine` passes SIGTERM through is U. | **Add now (Post-Completion):** time `docker compose stop` with the plan's exact snippet, and document `init: true` if it waits for the 10 s kill. | S | N |
| 7.6 | **The relay as an end-to-end smoke test in Compose or CI** | **Medium (E).** stripe #51 (20 comments) [1]; Compose recipes [128]. | Env-var keys and Docker images. | Covered: `listen NAME` is idempotent, never prompts, and is configured through env vars only. | **Keep.** The official image is a follow-up (M). | – | N |
| 7.7 | **Offline handler tests with signed fixtures** | **Medium (E).** Stripe's testing docs recommend mocks, and the SDKs can sign fixtures [70]. | Provider SDK helpers. | Not Hookspot's job. | **Skip** trigger, fixtures, and signing in the CLI. | – | N |

### 2.8 AI-agent usage

| # | Need | Evidence | How peers solve it | Hookspot today / plan gap | Rec | Cost | BE |
|---|---|---|---|---|---|---|---|
| 8.1 | **Replay the same request after a fix, without a terminal** | **Strong for agents (I, from E).** An agent has no TTY to press Enter and no browser for the dashboard. Re-triggering at the provider creates a new event ID, so the agent can't retest idempotency on a repeated ID [130]. Peers expose replay by ID [122, 123, 133]. | As in row 5.1. | Gap. | **Follow-up 1** (row 5.1). | S–M | Y |
| 8.2 | **Help an agent can learn from** | **Medium (E).** Stripe adds agent guidance to `listen --help` [132]. hookdeck #333: a weaker model never found the headless flag [40]. Hookdeck's evals [49]. | Agent-specific help sections; a README section on output without a terminal. | `listen --help` has no examples. Seeing full bodies needs `--max-body-lines 0 --max-value-chars 0` [130]. | **Add now:** `Example:` blocks on `listen` and `sources` (basic, Compose, background plus waiting for `Ready`, full bodies). Skip agent detection. | S | N |
| 8.3 | **Inspect a past request by ID** | **Medium (E).** As in row 5.3; the MCP entrants lead with it [122, 123]. | CLI list/get commands or MCP tools. | Dashboard only. | **Follow-up** (row 5.3). | M | Y |
| 8.4 | **Wait for the next webhook, then exit** | **Weak (E).** Only MCP and SDK vendors offer it (Hooklistener waits up to 60 s; webhooks.cc; webhook-toolkit) [122, 123, 124]. No issue asks for it on stripe-cli or hookdeck-cli. Claude Code can run a process in the background and watch its output [92]. | MCP blocking tools; SDK polling over stored history. | A naive `listen --once` started after the trigger would miss the event (`cli_offline`). | **Later, on demand:** a history-backed `requests wait --source stripe --timeout 60s` (exit 0 on a match, 1 on error, 2 on timeout), after rows 5.1–5.3. | M | Y |
| 8.5 | **Machine-readable `listen` output** | **Weak–Medium (C).** Stripe ships `--format json` and steers agents to it [132]. A code search found about five users, and they still regex the human `Ready` line [126, 127]. Hookdeck, the peer investing most in agents, has no JSON mode on `listen` [133]. Output format affects how well agents use a tool [93]. | Stripe: JSON per event. webhook-toolkit: NDJSON [124]. | The plan rules out `--json` under YAGNI. | **Skip now, but decide the streams now** (row 7.3). Trigger to revisit: a consumer that needs to assert on fields. The shape then: NDJSON records of type `ready`, `request`, `forward`, and `error`. | M | N |
| 8.6 | **An MCP server** | **Weak (E).** Hookdeck's MCP RFC got 2 comments and 0 reactions, and says "Skills + CLI for development. MCP for investigation" [38]. The entrants have 0–5 stars [123, 124]. RequestBin's comparison wrongly says Hookdeck has no MCP server [125]. | Hosted or stdio MCP servers. | None. | **Skip.** Revisit only if Hookspot adds production investigation. | L | Y |
| 8.7 | **Agents as webhook consumers** (for example, pushing PR comments into a running agent session) | **Medium (E).** On HN's Claude Code "channels" thread (400 points), users want GitHub webhooks delivered into a session [98]. | Relays with a stable URL. | Delivering to a local process through a stable public URL is what Hookspot already does (I). | **No CLI work.** A positioning note. | – | N |

## 3. Where the plan is weaker, and stronger, than its peers

### 3.1 Stronger

- **It never prompts in `listen`.** Hookdeck's check looks only at stdin, so it hangs under Compose `tty: true` [133].
- **It never silently rewrites a destination path.** Hookdeck's `--path` does [133]. Hookdeck users asked to change the path from the CLI (hookdeck #87, 21 comments) [36]. The plan's `sources update --path` does that, and returns a 409 instead of overwriting a shared destination.
- **Creation is safe when replicas or teammates run it at the same time** (row locks, and a single refetch after a 409).
- **Fidelity:** the body is byte-exact and every header value is kept (row 4.1). smee's re-serialization still breaks signatures [55].
- **Dashboard retries reach the CLI,** unlike Stripe [27]. **The server retries 5xx responses,** which Stripe users have asked for since 2019 [6].
- **It doesn't follow redirects,** unlike Hookdeck [133]. That matches how providers behave.
- **Every source has a permanent URL and 14 days of history.** ngrok's free plan keeps 24 hours and Hookdeck's Developer plan 3 days [75, 77].
- **The npm package installs with pnpm and forwards SIGTERM** [37, 47]. **There's no silent guest fallback** [41].
- **Server-side filtering** makes `listen stripe` mean Stripe deliveries only, which is a plain rule for Compose.

### 3.2 Weaker

| Gap | Peers that do better | Fix (§4) |
|---|---|---|
| "Waiting for requests" prints before the channel join | Stripe, Hookdeck | 4a-1 |
| Nothing says what was missed during a drop | Neither does it well; Hookdeck at least keeps history and shows links | 4a-1, 4b-2 |
| No path from a terminal line to a specific request, and no replay by ID | Hookdeck (links, the TUI `r` key, `request retry`), Stripe (`events resend`), ngrok (inspector) | 4a-3, 4b-1 |
| Replay needs a terminal on stdin | Hookdeck's retry command works without one | 4b-1 |
| No bare port, no Docker hint, no official image | Stripe, Hookdeck | 4a-9, 4a-10, 4b-12 |
| The project appears only when a source is created | Hookdeck | 4a-6 |
| The local hop goes through `HTTP_PROXY` | Stripe, Hookdeck | 4a-7 |
| Providers that need a handshake can't register | Hookdeck (server-side), every tunnel | 4a-13 (docs), 4b-8 |
| Serial forwarding with a fixed 30 s timeout | Stripe (hidden `--timeout`); both forward concurrently | 4b-6 |
| No `--insecure` and no `-H` | Stripe, Hookdeck | 4b-10 |
| Retry amplification with several listeners | Hookdeck (one event per session) | 4a-13 (docs), 4b-3 |
| No `--help` examples | Stripe | 4a-12 |
| No JSON output | Stripe | Skipped on purpose (§3.3) |

### 3.3 Conflicts in the notes, and how this report resolves them

| Topic | One side | Other side | Resolution |
|---|---|---|---|
| **Where the TUI goes** | 01: the plan spends its TUI effort on source management, which people do rarely. The loop they repeat, iterating on a handler, has only Enter-to-replay. Option: ship `sources` as a table plus subcommands and move the TUI to `listen` later. | T2: skip a `listen` TUI. Demand for it comes mostly from Hookdeck's maintainers, and it cost Hookdeck TTY failures (#333) and a "looks connected" bug (#399) [40, 45]. | **Keep the owner's `sources` TUI and add no `listen` TUI.** The iteration loop gets cheap per-line help now (Ready, the reconnect notice, links, the redirect target) and `requests retry`/`get` later. Those endpoints are exactly what a future `listen` TUI's retry and details keys would need, so nothing is ruled out. Note that the TUI (Tasks 13–14) is the plan's largest item with the least direct evidence of demand. If scope has to shrink, it's the one part whose function the subcommands and the non-TTY table already cover. |
| **`--json` / NDJSON** | 01: skip it. Stripe ships `--format json` and tells agents to use it [132]. | T5: skip it for now, but decide stdout versus stderr now. Real Stripe JSON users still regex the human `Ready` line [126, 127]. Stripe's JSON stream mixes in text lines when forwarding, and #1353 breaks `jq` [29]. Hookdeck has no JSON on `listen` and needs a major version to fix its streams [43]. | **No `--json` in this plan. Add the stream rule now** (4a-5), so JSON can be added later without breaking anyone. Trigger: a real consumer that needs to assert on fields. |
| **Offline catch-up and per-developer routing** | The plan defers both. T4: keep them deferred, but fix fan-out correctness and add a grace window. | T6 theme T8 (10 threads): "store the raw request first, and don't lose webhooks"; accept requests while the CLI is offline and deliver them on reconnect, now. | **The first half is already true:** ingest stores the raw bytes before delivering, keeps them 14 days, and the dashboard can retry them. **The second half stays deferred.** 1. The category leader discards events that arrive with no listener [72]. 2. Explicit demand is 1 upvote in 7 years [4], against 10 upvotes for "let me resend it" [27]. 3. T6 attributes much of the T8 signal to vendor seeding and finds that providers themselves are trusted to deliver (C) [120, 121]. 4. After a weekend offline, an unbounded replay would push stale, out-of-order events into a local database (I). 5. In a team, `cli_offline` only means *nobody* was listening, so catch-up per developer needs per-developer delivery identity, which is the routing design. **Instead:** the reconnect notice now; then the grace window, the fan-out fix, and `requests retry`. Design routing and catch-up together, starting with "a retry goes to whoever clicked it", when users report cross-talk that per-developer sources can't solve. |
| **IPv6 `localhost`** | Recon listed `localhost` resolving to `::1` as a blocker. | T1's test: Go reaches both an IPv4-only and an IPv6-only server through `localhost`. The public reports come from ngrok 1.x in 2014 [63], from Node tools, and from Docker, where the real cause is the container [9]. | **Drop it from the plan.** At most, a later hint for a literal loopback address that fails to connect. |
| **Replay by ID: now or later** | T6: add `replay <id>` now (theme T3, 11 threads). | T4 and T5: a follow-up, because it needs a new `/cli` write route. | **Follow-up 1.** The plan already spans two repos and 16 tasks, and the dashboard link closes the gap for people now, at S cost. If the owner wants one agent-facing item in this plan, this is the one: the backend retry already exists, and the route needs exactly what Tasks 2 and 4 build (the project scope plug and the 403 mapping). |
| **The ingest 406** | T1: a follow-up (found by reading code). | T3: add it now (reproduced). | **Add now** as a standalone backend fix. It's reproduced, it's one line, and the failure is silent loss. |
| **Bare port** | The plan defers it because "the first delivery already reports connection refused". | 01 and T6: add it. | **Add now.** The plan's reason is wrong (the error is DNS, not refused), and the plan's own guard sends `listen 3000` users to `--forward-to`. |
| **Serial or concurrent forwarding** | T2 leans toward keeping serial forwarding, which preserves arrival order. | The peers forward concurrently. Nothing shows users need ordering at the relay, and Stripe doesn't guarantee order even in production [8]. | **Follow-up.** Ship `--timeout` first, test a breakpoint, then decide. |
| **Following redirects** | Hookdeck follows them. | Stripe and Hookspot return the 3xx. | **Keep not following, and show `Location`.** |
| **Do agents have a TTY?** | Stripe PR #1537: agents like Claude Code have a real TTY [32]. | hookdeck #333: agents have none [40]. | **The plan's both-TTYs rule is safe either way.** A TTY on stdin only turns on Enter-to-replay, which is harmless. |
| **A verification helper** | T6: a diagnostic ("the body arrived unmodified; the mismatch is in your app") and possibly a local `verify`. | T3: never. | **Never build a verification helper.** It needs provider secrets and a scheme per provider. The byte-exact docs line and the replay-timestamp hint cover what a relay can actually diagnose. |
| **Guest mode** | Four peers need no account, which lowers friction. | Hookdeck's 2026 guest-mode bugs, and guests get no history. | **Owner decided: skip it,** and fix sign-up instead (4a-11). |
| **Is ngrok's static URL free?** | Reddit users say it's paid. | ngrok staff say one free dev domain has existed for years [106]. | **The complaints lag behind the facts.** Still, say that Hookspot's URL is permanent, because that belief is what drives switching. |

## 4. Prioritized recommendations

### 4a. Changes to the current plan, ordered by value ÷ cost

| # | Change | Where in the plan | Needs | Cost | BE |
|---|---|---|---|---|---|
| 1 | Print `Ready. Waiting for requests (Ctrl-C to quit)` only after the channel join. On rejoin, print `Reconnected (offline Ns). Requests that arrived meanwhile were not delivered: <link>` (to stderr). | Task 10 (`cmd/listen.go`) | 1.1, 3.1, 7.2 | S | N |
| 2 | Next-steps block after `Created source` or `Connected source`: the URL is permanent, paste it into the provider now while this runs, which secret to verify with, and `curl -X POST <url> -H 'content-type: application/json' -d '{}'`. `sources create` adds "start `listen` before sending a provider test." | Tasks 10 and 12 | 1.1, 1.2, 4.2 | S | N |
| 3 | A dashboard link in the banner and on each request line (`/:org/:project/requests/:uid`). | Task 10 (printer), after the prefixed-uids plan | 2.1, 5.1 | S | N |
| 4 | Remove `plug :accepts, ["json"]` from the ingest catch-all, and test with `Accept: text/plain`. | A new backend task before Task 1, or a hotfix | 1.9 | S | Y |
| 5 | A stream rule in Technical Details: records go to stdout; hints, warnings, and notices go to stderr. Apply it to every new line. | Technical Details; Tasks 10 and 12 | 7.3 | S | N |
| 6 | The project in every banner. | Task 10 | 1.4 | S | N |
| 7 | The forwarder gets its own `http.Transport` with `Proxy: nil` (still not following redirects), plus a test with `HTTP_PROXY` set. | Task 11 | 1.7 | S | N |
| 8 | For 3xx responses, print the `Location` and the "senders don't follow redirects" hint. | Task 11, or Task 10 (printer) | 3.3 | S | N |
| 9 | Accept `--forward-to 3000` as `localhost:3000`, make the `listen 3000` guard hint suggest it, and remove "bare port" from Deferred. | Task 11 (`endpoint.Parse`), Task 9 (hint) | 1.5 | S | N |
| 10 | A container-aware "connection refused" hint, and a README example for an app on the host that uses `extra_hosts`. | Tasks 10 and 16 | 1.6 | S | N |
| 11 | **(Decided)** End-to-end sign-up from `hookspot login`: 1. confirm the path by hand; 2. keep the CLI return path through sign-up, confirmation, and onboarding; 3. create the first organization and project on the approval page; 4. print a clear message on expiry. | A new backend task | 1.3 | S–M | Y |
| 12 | `Example:` blocks on `listen` and `sources`, a one-line exit-code contract, and a test that failures exit non-zero. | Task 16 (help), Task 10 (tests) | 7.4, 8.2 | S | N |
| 13 | Docs in Task 16. **Signatures:** the body is forwarded byte for byte; which secret to use; replays fail the ~5-minute tolerance. **Providers:** handshake providers aren't supported yet; pin Stripe's `api_version` on the endpoint. **Delivery:** only 5xx/408/429 are retried; history is kept 14 days. **Teams:** one source per developer; correct edge row 18 (retry amplification, and replicas duplicate deliveries). **Network:** `HTTPS_PROXY` is honored, and outbound WSS on port 443 is needed. | Task 16; edge row 18 | 1.8, 2.4, 3.2, 4.1–4.3, 6.1, 6.2 | S | N |
| 14 | Post-Completion checks. **WSL2:** `login` without `xdg-open`, and forwarding to an app in WSL2 and on the Windows host. **Compose:** time `docker compose stop` with the npx snippet (add `init: true` if needed), and confirm that replicas duplicate deliveries. | Post-Completion | 1.10, 1.12, 6.1, 7.5 | S | N |

Items 1–10 and 12–14 are all S and mostly CLI-only; together they're roughly 3–4 days of CLI work (I). Item 4 is a one-line backend change. Item 11 is the decided sign-up work, and its size depends on the manual walkthrough.

### 4b. Follow-up roadmap, in order

1. **`hookspot requests retry REQ_UID`** (S–M, BE). The strongest replay demand and the biggest agent gap (rows 5.1 and 8.1). It reuses the dashboard's `Ingest.API.Request.retry`.
2. **A grace window before `cli_offline`** (S–M, BE). It covers sleep, deploys, restarts, `restart: unless-stopped`, and the join race (row 5.2).
3. **Fan-out correctness** (S, BE). A system retry skips deliveries that are already `ok`, and the attempt records who responded. Confirm first with a two-socket channel test (row 6.1).
4. **Server retries visible in `listen`** (S+S, BE): `attempt 2/5 · retrying in 20s` (row 3.2).
5. **`requests get` and `requests list`** (M, BE). Human-readable text first; they also serve fixture export (rows 5.3 and 8.3).
6. **`listen --timeout DURATION`**, then a breakpoint test to decide on concurrent dispatch (S, then M; row 2.3).
7. **A hint for replayed signatures past the timestamp tolerance** (S; row 4.3).
8. **Handshake echo rules** for Slack, Meta, Graph, Twitch, and Dropbox (M, BE; row 1.8). Move this up if the providers Hookspot users target include any of them.
9. **Printer polish** (S): an event-type label and form bodies as `key = value` lines (rows 2.5 and 3.6).
10. **`--insecure` and `-H/--header`** (S; row 1.12). `-H` also covers the Host checks in Rails `config.hosts` and Django `ALLOWED_HOSTS` when Compose forwards to `http://app:3000` (I, untested).
11. **A warning about other listeners when joining** (S+S, BE; row 6.3).
12. **An official Docker image** (M; rows 1.11 and 7.6). Every fresh container then skips the npm download, and the PID 1 signal question goes away.
13. **Docs** (S):
    - A signatures section that links to each provider's raw-body guidance (row 4.4).
    - URL-signed providers like Twilio (row 4.5).
    - A Windows SmartScreen note.
    - Published free-tier and size limits, stated plainly enough for LLMs to quote correctly [106].
    - Only the destination path is reachable on the local app, never the whole server (I, from the path rewrite in `deliver_job.ex`). This answers Reddit's worry about exposing localhost (theme T12) [105].
14. **Only when a trigger appears:**
    - `--json` NDJSON (row 8.5).
    - `requests wait` (row 8.4).
    - Replay `--times N` / `--concurrency` for idempotency tests [118, 119].
    - A near-miss name hint (01 #12; no user evidence).
    - `--no-create` (already deferred in the plan).
    - A routing and catch-up plan, starting with row 6.4.

### 4c. Explicit skips

| Skip | Why | Revisit when |
|---|---|---|
| Guest mode | Owner decision. Without rate limiting, an unauthenticated guest endpoint lets anyone mint ingest URLs. Hookdeck #334 shows it silently misroutes CI traffic [41], and guests get no history. | Funnel data, after the sign-up fix, shows sign-up is where new users drop off. |
| Sign-up inside the terminal | Passwords typed into a terminal, an email round trip, no OAuth, and prompts that break Compose. | Never. |
| A `listen` TUI | §3.3. | Users still ask for in-terminal selection after links and `requests retry` ship. |
| `--json` in this plan | §3.3. | A concrete consumer appears. |
| An MCP server | Row 8.6. | Hookspot adds production investigation. |
| Trigger, fixtures, and signing | Provider-specific. Stripe's `trigger` creates real objects, and SDKs already sign fixtures [70]. | Never, in the CLI. |
| Relay re-signing | It needs provider secrets and formats, and it would force a verification path that exists only in development. | Only if Hookspot adds non-CLI HTTP destinations; the format would then be Standard Webhooks. |
| A verification helper, or verification at the edge | It needs secrets and a scheme per provider, and duplicates the provider SDKs [78]. | A product decision for production gateway use. |
| An automatic offline queue | §3.3. | Users ask for it directly. |
| Per-developer routing | §3.3. | Users report cross-talk that per-developer sources can't solve. |
| Edit-and-replay, bulk replay, content search | Weak demand; they belong in the dashboard. | Production use. |
| Event filters, quiet mode | Provider subscriptions and one source per concern are enough. | – |
| An API-version pinning feature | The version is pinned on the provider's endpoint. | – |
| A startup health check | Server retries absorb "not started yet", and it adds noise in Compose. | – |
| winget, scoop, code signing | npm covers Windows, and demand is modest [11]. | Users report SmartScreen or antivirus blocks [15]. |
| Agent detection through env vars | The both-TTYs rule is already safe. | – |
| IPv6 handling | Not a problem for Go. | – |
| Ordering guarantees | Providers don't guarantee order [8]. | – |
| Header case and order, HEAD becoming GET, very large bodies | Low impact. | Someone reports one. |
| Payload privacy controls | Nobody has asked. | A customer asks. |

### 4d. Bugs found during the research

| Bug | Evidence strength | Impact | Fix | When |
|---|---|---|---|---|
| **Ingest `plug :accepts, ["json"]` returns 406 before storing** | **Strong.** Reproduced with `mix run` on Phoenix 1.8.13: `text/plain`, `application/xml`, and `text/html` get 406; `*/*` and JSON are accepted [131]. How many real providers send such headers is U (the stored `accept` headers would tell). | Silent loss; the provider may disable the endpoint. | Drop `:accepts` from the ingest catch-all. | Now (4a-4) |
| **`HTTP_PROXY` applied to the local hop** (CLI) | **Medium.** Code plus Go stdlib behavior plus Docker's proxy injection [83]. No user reports. | In a proxied Docker setup, `http://app:3000` goes to the corporate proxy. | A dedicated Transport with `Proxy: nil`. | Now (4a-7) |
| **"Waiting" printed before the channel join** (CLI) | **Strong (code).** Presence starts at the join, and `cli_offline` is never retried [130, 131]. | The first event (for example GitHub's `ping`) is lost, and harnesses trust the line. | Print `Ready` after the join. | Now (4a-1) |
| **Providers that need a handshake can't register** | **Strong for those providers.** Provider docs plus the static `RespondService` [84, 86, 131]. | Slack, Meta, Graph, Zoom, Twitch, and Dropbox never start sending. | Docs now; echo rules later. | Now (docs); 4b-8 |
| **Retry amplification and last-writer-wins with several listeners** | **Medium–Weak.** Read in code, not tested. Oban uniqueness bounds it to about 4 extra broadcasts [131]. | Teammates' apps get duplicates, and the status depends on who answered last. | Skip system retries once `ok`; record who responded. | Docs now; 4b-3 |
| **Head-of-line blocking from serial forwarding** | **Weak.** Read in code (`ws/client.go:383-393`), not tested. How much Phoenix buffers is U. | A breakpoint stalls later deliveries for up to 30 s, and a timeout plus retry can run the handler twice. | `--timeout`, a test, then a decision. | 4b-6 |
| **Replayed timestamped signatures fail after about 5 minutes** | **Strong.** Provider docs [67, 85, 91], plus code: a replay reuses the original headers [130]. | A stale-signature 400 looks like a bug in the handler. | A docs line now; a hint later. | Now (docs); 4b-7 |
| **New users can't finish `hookspot login`** | **Medium.** Read in code, not run [131]. | The first run fails for brand-new users. | The decided sign-up fix. | Now (4a-11) |
| **Deliveries stuck in `sent` after a CLI drops mid-forward** | **U.** T2 found no sweeper. | Requests would sit as "sent" with no retry. | Investigate. | Follow-up |
| **A local response over 16 MiB ends `listen`; HEAD is stored and forwarded as GET; edge-proxy `X-Forwarded-*` headers reach the local app** | **Weak (I/U).** | Low. | – | If reported |

## 5. Walkthrough: building a Stripe webhook handler with Hookspot

*I'm an AI coding agent building a Stripe `checkout.session.completed` handler. I can run shell commands, but a foreground command times out after 2 minutes by default (10 at most), so long-running processes go in the background and I read their output later [92]. I can't click a dashboard, press Enter in someone else's terminal, or approve a browser login. Every output token costs me.*

The markers below say how well each step is served:
- **[Today]:** works now.
- **[Plan]:** the current plan provides it.
- **[4a]:** provided once the §4a additions land.
- **[Gap]:** not covered; a follow-up would provide it.

**0. Learn the tool.**
```
hookspot listen --help
```
- **[4a]** gives me examples for running in the background, waiting for `Ready`, and printing full bodies, plus the exit-code contract.
- Today there are no examples. I'd guess the truncation flags.

**1. Authenticate.**
- **[Today]:** without a key, every command fails fast with `not logged in … Run 'hookspot login' or set HOOKSPOT_CLI_KEY`.
- I run `hookspot login` in the background and show the user the URL and confirmation code.
- If the user is brand new, today (I) they sign up, confirm their email, onboard, end up in the dashboard, and the CLI attempt expires after 10 minutes.
- **[4a, decided]:** they sign up, create an organization and project on the approval page, and I see `Logged in as …`.

**2. Write the handler.**
- I mount a raw-body parser *before* `express.json()` on `/webhooks/stripe`. Agents often get this wrong [99], and the CLI can't detect it.
- **[4a]:** the docs say Hookspot forwards the body byte for byte, so if verification fails I look at my middleware, not the relay.

**3. Start the relay and wait until it's really ready.**
```
# run_in_background
hookspot listen stripe --forward-to localhost:3000/webhooks/stripe \
  --max-body-lines 0 --max-value-chars 0 >tmp/hookspot.log 2>tmp/hookspot.err
# with Monitor, or a bounded loop with a 30 s ceiling
until grep -q '^Ready' tmp/hookspot.log; do sleep 1; done
grep -o 'https://in\.hookspot\.io/src_[A-Za-z0-9]*' tmp/hookspot.log
```
- **[Plan]:** the `stripe` source is created with no prompt; the output says `Created source "stripe" in Acme | Payments`; later runs change nothing.
- **[4a]:**
  - Later runs still name the project.
  - `Ready` appears only after the join.
  - Hints go to stderr, so the log I parse stays clean.
- Today I'd see "Waiting for requests..." before the join, and an event I triggered right then would become `cli_offline` and never be retried.

**4. Register the URL with Stripe.**
```
stripe webhook_endpoints create --url https://in.hookspot.io/src_… \
  --enabled-events checkout.session.completed
```
I put the returned `whsec_…` into `.env` as `STRIPE_WEBHOOK_SECRET`.
- **[4a]:** the next-steps block tells me:
  - the URL is permanent, so this is a one-time step for the project, not for every session;
  - to verify with *this* endpoint's secret.
- **[4a docs]:** if I need a fixed payload shape, I set the endpoint's `api_version` in Stripe [69].
- **[Today]:** Stripe's signature reaches my handler unchanged.

**5. Send a test event.**
- **[4a]:** first, the printed `curl` self-test proves the route without involving Stripe.
- Then `stripe trigger checkout.session.completed` [66].

**6. Read what arrived and what my handler answered.**
```
tail -n 80 tmp/hookspot.log
```
- **[Today]:** the status, latency, and request id, with both bodies on failure.
- **[Plan]:** a 404 or 405 at the root prints a hint.
- **[4a]:**
  - A 308 shows its `Location` and a hint about the trailing slash.
  - Inside Docker, "connection refused" explains that `localhost` is the container.
- A raw-body 400 gets no specific hint; the docs cover it.

**7. Fix the code and resend the *same* event.**
```
hookspot requests retry req_…      # doesn't exist
```
- **[Gap, follow-up 1].** Enter-to-replay needs a terminal on stdin, and the dashboard needs a browser.
- So I run `stripe trigger` again, which creates new Stripe objects and a new event ID. I can't test idempotency on a repeated ID.
- **[4a]:** the per-request dashboard link at least lets a person retry it for me.

**8. Resend after lunch.**
- Stripe's `t=` timestamp is now more than 5 minutes old, so verification fails [67].
- **[4a docs]** explain why.
- **[Gap, follow-up 7]:** a hint in the output would say so directly.

**9. Inspect an earlier failure after the log has moved on.**
```
hookspot requests get req_…        # doesn't exist
```
**[Gap, follow-up 5].** Today the only way is the dashboard.

**10. Restart the listener to add a `github` source.**
- Requests that arrive while it's down become `cli_offline`.
- **[4a]:** the reconnect notice covers drops *within* a session, but not a process restart.
- **[Gap, follow-up 2]:** the grace window would cover short restarts.

**11. A teammate also runs `listen stripe`.**
- We both receive every request. If their app is down, my app also receives retries caused by their 502 (I).
- **[Plan]** documents the fan-out; **[4a]** corrects the wording.
- **[4a docs]** suggest `listen stripe-agent`, registered in my own sandbox.
- **[Gap, follow-up 3]** fixes the retries.

**12. Hand off to CI.**
- **[Today]:** handler unit tests use Stripe's signing helper and don't need a relay [70].
- **[Plan]:** the end-to-end smoke test uses the plan's Compose service.
- **[4a]:**
  - A check that `docker compose stop` exits promptly through `npx`.
  - A note to run only one `listen` replica.

**13. Stop.** Killing the background task sends SIGTERM, and `listen` exits 0 **[Today]**.

**For a GitHub handler instead:**
- GitHub sends `ping` the moment the webhook is created [89], so `Ready` after the join matters even more: start `listen github` first, then create the webhook.
- `X-Hub-Signature-256` carries no timestamp, so replays should keep verifying (I).
- Every event arrives as `POST /webhooks/github`, and the event name is only in `X-GitHub-Event`. That's what the event-type label (follow-up 9) is for.
- GitHub and Probot users often start with smee, which re-serializes JSON and breaks signatures [53, 55]. The byte-exact guarantee is the reason to switch.

**For a developer at a terminal:**
- Enter replays the last request, the dashboard link reaches any older one, and the `sources` TUI handles management.
- Steps 8–11 are the same gaps.
- Step 7 is solved for a person with a terminal, but not for their Compose setup.

## 6. Limitations and open questions

**Limitations**
- **Reddit:** threads were found by title search only; comment search failed. Passing mentions of the Hookdeck CLI, smee, or Svix are therefore under-sampled.
  - Since 2025, many webhook threads are vendor promotion.
  - Most threads score under 5.
  - Treat Reddit counts as direction, not magnitude.
- **GitHub trackers:** Hookdeck's issues are mostly written by its maintainers, so they show intent, not demand. Stripe's tracker is the best demand signal, but it's Stripe-specific.
- **Code reads, not runs:** several Hookspot findings weren't run: head-of-line blocking, retry amplification, the new-user sign-up path, SIGTERM through `npx` in Compose, and WSL2. Only the 406 reproduction and the `localhost` resolution tests (IPv6 and `*.localhost`) were run.
- **Peers:** ngrok's agent is closed source. The Svix `listen` evidence is docs-only. MCP tool counts and vendor comparisons are marketing claims.
- **No Hookspot data:** no usage, funnel, or support data was available, so user impact is inferred from peers.

**Open questions, and how to answer them**
1. **Which providers do Hookspot users target?** This sets the priority of the handshake echo rules and the URL-signing docs. Ask users, or run the same kind of query over stored request headers.
2. **Which real providers send a restrictive `Accept` header?** Query the stored `accept` headers in the ingest database.
3. **Does the new-user sign-up path fail the way the code suggests?** Walk it by hand before building the fix.
4. **Does serial forwarding stall behind a breakpoint, and how much does the Phoenix channel buffer?** A local test.
5. **Does retry amplification happen as read?** A two-socket `Phoenix.ChannelTest`.
6. **Are deliveries left in `sent` after a CLI drops mid-forward?** Look for a sweeper, or test it.
7. **Does `npx -y hookspot@…` as PID 1 pass SIGTERM through?** Time `docker compose stop`.
8. **Can a dashboard URL filter deliveries by `status=cli_offline`?** If so, the reconnect notice can link to exactly the missed requests.
9. **What are Hookspot's free-tier quotas and body-size limits?** Needed for the docs.
10. **How does WSL2 behave?** Specifically, whether `xdg-open` exists, and localhost forwarding under NAT versus mirrored networking.
11. **Would agents use a CLI `wait` command rather than backgrounding `listen` and watching its log?** No data yet.

## 7. Sources

All accessed 2026-09-24. Dates are creation or publication dates; "n.d." means none was shown. Reaction and comment counts are as of the access date.

### GitHub: stripe/stripe-cli

1. stripe/stripe-cli #51, running `stripe listen` non-interactively in Docker for automated tests (20 comments). 2019-07-21. https://github.com/stripe/stripe-cli/issues/51
2. stripe/stripe-cli #80, "Listen stops silently after computer goes to sleep" (9 upvotes). 2019-08-07. https://github.com/stripe/stripe-cli/issues/80
3. stripe/stripe-cli #108, "Allow forwarding to endpoints with a self signed certificate" (16 upvotes). 2019-08-15. https://github.com/stripe/stripe-cli/issues/108
4. stripe/stripe-cli #214, "Add backfill option" (1 upvote). 2019-10-03. https://github.com/stripe/stripe-cli/issues/214
5. stripe/stripe-cli #295, "`trigger` with customizable data" (45 upvotes). 2019-11-06. https://github.com/stripe/stripe-cli/issues/295
6. stripe/stripe-cli #313, "Support webhook retries with Stripe CLI" (9 upvotes, open). 2019-11-14. https://github.com/stripe/stripe-cli/issues/313
7. stripe/stripe-cli #389, "Listen command stops working with i/o timeout" (15 upvotes). 2020-02-25. https://github.com/stripe/stripe-cli/issues/389
8. stripe/stripe-cli #418, "Webhooks are being sent in incorrect order" (13 upvotes). 2020-04-06. https://github.com/stripe/stripe-cli/issues/418
9. stripe/stripe-cli #427, "Webhook call failed when run stripe-cli in docker". 2020-04-22. https://github.com/stripe/stripe-cli/issues/427
10. stripe/stripe-cli #435, "Sometimes Webhooks don't come through" (7 upvotes). 2020-04-28. https://github.com/stripe/stripe-cli/issues/435
11. stripe/stripe-cli #451, "Support official Windows Package manager - winget" (15 upvotes). 2020-05-19. https://github.com/stripe/stripe-cli/issues/451
12. stripe/stripe-cli #475, "Make it easier to obtain the webhook signing secret" (`--print-secret`). 2020-07-01. https://github.com/stripe/stripe-cli/issues/475
13. stripe/stripe-cli #600, "Stripe CLI is not receiving all events" (19 upvotes, 45 comments). 2021-03-02. https://github.com/stripe/stripe-cli/issues/600
14. stripe/stripe-cli #672, "Way to programmatically get webhook signing secret from Stripe listen?". 2021-05-18. https://github.com/stripe/stripe-cli/issues/672
15. stripe/stripe-cli #692, "Windows detects the Stripe CLI as a virus/trojan" (31 comments). 2021-06-16. https://github.com/stripe/stripe-cli/issues/692
16. stripe/stripe-cli #710, "Client.Timeout exceeded while awaiting headers". 2021-07-21. https://github.com/stripe/stripe-cli/issues/710
17. stripe/stripe-cli #714, "Stripe CLI running in docker container failing to reset connection". 2021-07-28. https://github.com/stripe/stripe-cli/issues/714
18. stripe/stripe-cli #760, using `listen` with start-server-and-test (readiness). 2021-09-30. https://github.com/stripe/stripe-cli/issues/760
19. stripe/stripe-cli #797, "Signatures of connect events are invalid when forwarded using Stripe CLI". 2021-12-06. https://github.com/stripe/stripe-cli/issues/797
20. stripe/stripe-cli #804, "NPM package for stripe-cli?" (52 upvotes). 2022-01-07. https://github.com/stripe/stripe-cli/issues/804
21. stripe/stripe-cli #813, a Compose container that "exited with code 0". 2022-01-24. https://github.com/stripe/stripe-cli/issues/813
22. stripe/stripe-cli #970, "After upgrading on wsl2, stripe command hangs" (15 upvotes, 25 comments). 2022-09-13. https://github.com/stripe/stripe-cli/issues/970
23. stripe/stripe-cli #1010, "Client.Timeout on CLI" (open). 2022-12-14. https://github.com/stripe/stripe-cli/issues/1010
24. stripe/stripe-cli #1011, "stripe listen --forward-to ... get the response content". 2022-12-15. https://github.com/stripe/stripe-cli/issues/1011
25. stripe/stripe-cli #1159, "Docker container crashes when listening for webhooks". 2024-03-27. https://github.com/stripe/stripe-cli/issues/1159
26. stripe/stripe-cli #1168, "`stripe login` producing pairing code, but no URL". 2024-04-10. https://github.com/stripe/stripe-cli/issues/1168
27. stripe/stripe-cli #1206, "'Resend' for webhook events doesn't get forwarded to local listener" (10 upvotes, open). 2024-06-19. https://github.com/stripe/stripe-cli/issues/1206
28. stripe/stripe-cli #1335, "Still no way to specify --stripe-version with listen?" (49 upvotes, open). 2025-05-01. https://github.com/stripe/stripe-cli/issues/1335
29. stripe/stripe-cli #1353, "Cannot use jq with `--format JSON`". 2025-07-19. https://github.com/stripe/stripe-cli/issues/1353
30. stripe/stripe-cli #1359, "Add no interaction mode". 2025-08-15. https://github.com/stripe/stripe-cli/issues/1359
31. stripe/stripe-cli #1581, `--api-key` silently overridden by an env var. 2026-05-10. https://github.com/stripe/stripe-cli/issues/1581
32. stripe/stripe-cli PR #1537, "fail fast on auth errors in non-interactive and agent contexts". 2026-04-07. https://github.com/stripe/stripe-cli/pull/1537
33. stripe/stripe-cli wiki, "Listen command". Last edited 2019-10-31. https://github.com/stripe/stripe-cli/wiki/listen-command

### GitHub: hookdeck

34. hookdeck/hookdeck-cli #18, "CLI won't connect to websocket when running in docker container on Windows". 2021-10-27. https://github.com/hookdeck/hookdeck-cli/issues/18
35. hookdeck/hookdeck-cli #70, "Add ability to listen to multiple sources at once" (3 upvotes). 2024-05-01. https://github.com/hookdeck/hookdeck-cli/issues/70
36. hookdeck/hookdeck-cli #87, "Set or change the Destination path from the CLI" (21 comments). 2024-07-12. https://github.com/hookdeck/hookdeck-cli/issues/87
37. hookdeck/hookdeck-cli #166, "postinstall scripts blocked by pnpm v10+ security policies". 2025-10-27. https://github.com/hookdeck/hookdeck-cli/issues/166
38. hookdeck/hookdeck-cli #228, MCP RFC (2 comments, 0 reactions). 2026-03-05. https://github.com/hookdeck/hookdeck-cli/issues/228
39. hookdeck/hookdeck-cli #323, "Known issue: hookdeck listen can stop delivering events (CLI_UNAVAILABLE)". 2026-08-04. https://github.com/hookdeck/hookdeck-cli/issues/323
40. hookdeck/hookdeck-cli #333, "listen: detect a non-interactive environment and fall back to compact output". 2026-08-12. https://github.com/hookdeck/hookdeck-cli/issues/333
41. hookdeck/hookdeck-cli #334, "listen ignores HOOKDECK_API_KEY and silently creates a guest account". 2026-08-12. https://github.com/hookdeck/hookdeck-cli/issues/334
42. hookdeck/hookdeck-cli #337–#339: an auth hang, destructive commands exiting 0, and "`hookdeck listen` exits 0 within milliseconds having forwarded nothing". 2026-08-13. https://github.com/hookdeck/hookdeck-cli/issues/339
43. hookdeck/hookdeck-cli #340, agent-safety epic ("Data to stdout, diagnostics to stderr"). 2026-08-13. https://github.com/hookdeck/hookdeck-cli/issues/340
44. hookdeck/hookdeck-cli #373, "guest-upgrade flow can hang". 2026-08-28. https://github.com/hookdeck/hookdeck-cli/issues/373
45. hookdeck/hookdeck-cli #399, "interactive mode looks connected before it is". 2026-09-14. https://github.com/hookdeck/hookdeck-cli/issues/399
46. hookdeck/hookdeck-cli #400, "hookdeck login opens a browser and hangs forever when there is no terminal". 2026-09-14. https://github.com/hookdeck/hookdeck-cli/issues/400
47. hookdeck/hookdeck-cli #429, "npm shim does not forward signals, so `listen` cannot shut down gracefully". 2026-09-23. https://github.com/hookdeck/hookdeck-cli/issues/429
48. hookdeck/hookdeck-cli #433, "`--output json` emits non-JSON on the failure path". 2026-09-23. https://github.com/hookdeck/hookdeck-cli/issues/433
49. hookdeck/evals: README and issues #12 (2026-08-14) and #27 (2026-08-19). Repository created 2026-08-07. https://github.com/hookdeck/evals

### GitHub: other projects

50. probot/smee-client #27, "Authenticated channels" (10 upvotes). 2017-12-20. https://github.com/probot/smee-client/issues/27
51. probot/smee-client #62, "Copy as curl" (7 upvotes). 2018-06-28. https://github.com/probot/smee-client/issues/62
52. probot/smee-client #84, "Connection timeout when connecting to smee under proxy". 2018-11-13. https://github.com/probot/smee-client/issues/84
53. probot/smee-client #136, "Smee seems to reformat JSON without updating Content-Length header" (9 upvotes). 2019-11-03. https://github.com/probot/smee-client/issues/136
54. probot/smee-client #172, "Client stops working" (6 upvotes). 2021-09-03. https://github.com/probot/smee-client/issues/172
55. probot/smee-client #325, "Signature verification failing for Stripe events" (open). 2024-10-11. https://github.com/probot/smee-client/issues/325
56. probot/smee.io #11, "Content body not as JSON does not get transferred" (5 upvotes). 2019-05-17. https://github.com/probot/smee.io/issues/11
57. probot/smee.io #43, "Download payloads?". 2020-04-22. https://github.com/probot/smee.io/issues/43
58. probot/smee.io #98, "big requests lead to 413". 2022-07-22. https://github.com/probot/smee.io/issues/98
59. stripe/stripe-node #341, "Webhook signature verification and bodyParser.json issue" (86 comments). 2017-05-26. https://github.com/stripe/stripe-node/issues/341
60. vercel/next.js discussion #13405, "RFC – add rawBody to NextApiRequest" (68 reactions). 2020-05-26. https://github.com/vercel/next.js/discussions/13405
61. svix/svix-webhooks #93, "Allow signature validation without timestamp tolerance enforcement". 2021-07-10. https://github.com/svix/svix-webhooks/issues/93
62. localtunnel/localtunnel issues sorted by reactions (#248, `--subdomain` unreliable, 66 upvotes; #344, backup host, 87 upvotes). n.d. https://github.com/localtunnel/localtunnel/issues?q=sort%3Areactions-%2B1-desc
63. inconshreveable/ngrok #169, "ngrok fails to tunnel to servers listening for only ipv6 traffic". 2014-10-10. https://github.com/inconshreveable/ngrok/issues/169
64. kuhlman-labs/fishhawk #3169, "Local dev webhook ingress: an opt-in smee.io relay". 2026-09-03. https://github.com/kuhlman-labs/fishhawk/issues/3169

### Official docs

65. Stripe Docs, "stripe listen". n.d. https://docs.stripe.com/cli/listen
66. Stripe Docs, "stripe trigger". n.d. https://docs.stripe.com/cli/trigger
67. Stripe Docs, "Receive Stripe events in your webhook endpoint" (5-minute tolerance; retries re-signed). n.d. https://docs.stripe.com/webhooks
68. Stripe Docs, "Resolve webhook signature verification errors". n.d. https://docs.stripe.com/webhooks/signature
69. Stripe API reference, "Create a webhook endpoint" (`api_version`). n.d. https://docs.stripe.com/api/webhook_endpoints/create
70. Stripe Docs, "Automated testing". n.d. https://docs.stripe.com/automated-testing
71. Stripe Docs, "Sandboxes". n.d. https://docs.stripe.com/sandboxes
72. Hookdeck Docs, "CLI" (events received while not listening; multiple CLI listeners). n.d. https://hookdeck.com/docs/cli
73. Hookdeck Docs, "Receive and Process Webhooks". n.d. https://hookdeck.com/docs/use-cases/receive-webhooks
74. Hookdeck, "How to test and replay Slack webhooks locally" (vendor guide; handshake handling). n.d. https://hookdeck.com/webhooks/platforms/how-to-test-and-replay-slack-webhooks-locally-with-hookdeck
75. Hookdeck, "Pricing". n.d. https://hookdeck.com/pricing
76. ngrok Docs, "Free plan limits". n.d. https://ngrok.com/docs/pricing-limits/free-plan-limits/
77. ngrok Docs, "Traffic Inspector" (retention; modified replay). n.d. https://ngrok.com/docs/obs/traffic-inspection
78. ngrok Docs, Traffic Policy `verify-webhook`. n.d. https://ngrok.com/docs/traffic-policy/actions/verify-webhook/
79. Cloudflare Docs, "Quick Tunnels" (TryCloudflare). n.d. https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/trycloudflare/
80. webhook.site Docs, FAQ (free URL limits and retention). n.d. https://docs.webhook.site/index.html
81. svix/svix-webhooks, `svix-cli` README and source (`listen`, `signature`). main branch, n.d. https://github.com/svix/svix-webhooks/tree/main/svix-cli
82. Docker Docs, `docker container run` (`--add-host host-gateway`). n.d. https://docs.docker.com/reference/cli/docker/container/run/
83. Docker Docs, "Use a proxy server with the Docker CLI". n.d. https://docs.docker.com/engine/cli/proxy/
84. Slack Docs, `url_verification` event. n.d. https://docs.slack.dev/reference/events/url_verification/
85. Slack Docs, "Verifying requests from Slack". n.d. https://docs.slack.dev/authentication/verifying-requests-from-slack
86. Meta Docs, "Webhooks: Getting Started" (verification requests). n.d. https://developers.facebook.com/docs/graph-api/webhooks/getting-started
87. Zoom Docs, "Webhooks" (URL validation, 72-hour revalidation). n.d. https://developers.zoom.us/docs/api/webhooks/
88. Microsoft Graph docs, notification URL validation (docs source include; seen via search summary). n.d. https://github.com/microsoftgraph/microsoft-graph-docs-contrib/blob/main/concepts/includes/change-notifications-delivery-notificationurl-validation.md
89. GitHub Docs, "Webhook events and payloads: ping". n.d. https://docs.github.com/en/webhooks/webhook-events-and-payloads#ping
90. Twilio Docs, "Webhooks security". Last modified 2026-08-20. https://www.twilio.com/docs/usage/security
91. Standard Webhooks specification v1.0.0. n.d. https://github.com/standard-webhooks/standard-webhooks/blob/main/spec/standard-webhooks.md
92. Claude Code Docs, "Interactive mode" (background Bash) and "Tools reference" (timeouts, Monitor). n.d. https://code.claude.com/docs/en/interactive-mode ; https://code.claude.com/docs/en/tools-reference
93. Anthropic Engineering, "Writing effective tools for AI agents". 2025-09-11. https://www.anthropic.com/engineering/writing-tools-for-agents

### Hacker News and Stack Overflow

94. Hacker News, "Stripe CLI" (706 points; comments 21454645, 21455379, 21455434, 21455510, 21455552). 2019-11-05. https://news.ycombinator.com/item?id=21454153
95. Hacker News, "Ngrok: Secure tunnels to localhost", comment 14280010 (replay without redoing the payment flow). 2017-05-06. https://news.ycombinator.com/item?id=14280010
96. Hacker News, "Ngrok Alternatives" (244 points; comments 30445774, 30445504). 2022-02-23. https://news.ycombinator.com/item?id=30443747
97. Hacker News, "Show HN: Portr – open-source ngrok alternative designed for teams" (172 points; comment 39916537). 2024-04-03. https://news.ycombinator.com/item?id=39913197
98. Hacker News, "Push events into a running session with channels" (Claude Code; 400 points; comments 47449274, 47450517). 2026-03-20. https://news.ycombinator.com/item?id=47448524
99. Hacker News comment 47053890 (agents get the raw-body parser order wrong). 2026-02-17. https://news.ycombinator.com/item?id=47053890
100. Hacker News, "Show HN: HookShare – Relay WebHooks to Multiple Dev Machines". 2023-04-24. https://news.ycombinator.com/item?id=35682400
101. Hacker News, "Show HN: … webhook forwarder to solve my multi-environment headaches" (Splithook). 2024-06-23. https://news.ycombinator.com/item?id=40768367
102. Stack Overflow 53899365, "Stripe Error: No signatures found matching the expected signature for payload" (score 144, 89,835 views). 2018-12-22. https://stackoverflow.com/questions/53899365
103. Stack Overflow 75062050, "Stripe webhook returning 308 error when calling Vercel serverless function". 2023-01-09. https://stackoverflow.com/questions/75062050
104. Stack Overflow 52872580, "How to respond with the correct challenge value…" (Slack; 9,471 views). 2018-10-18. https://stackoverflow.com/questions/52872580

### Reddit

105. Reddit corpus: 62 threads (61 read with comments, 876 comments) across 16 subreddits, listed with scores in research note T6 §6. 2020-06 to 2026-08. https://www.reddit.com (via Arctic Shift)
106. Reddit r/node, "any free alternatives for the ngrok??". 2026-02-11. https://www.reddit.com/r/node/comments/1r1y1hf/
107. Reddit r/webdev, "How to develop locally with webhooks?". 2023-01-31. https://www.reddit.com/r/webdev/comments/10pscck/
108. Reddit r/stripe, "Localhost webhooks testing using Anchor https". 2026-01-07. https://www.reddit.com/r/stripe/comments/1q6tp2a/
109. Reddit r/webdev, "Managing all the webhook endpoints is becoming a nightmare". 2026-02-17. https://www.reddit.com/r/webdev/comments/1r7cmot/
110. Reddit r/stripe, "Stripe showed 200 on a webhook but my server never received it…". 2026-06-12. https://www.reddit.com/r/stripe/comments/1u47mru/
111. Reddit r/webdev, "spent 3 days integrating paddle. the api worked in 20 minutes. the other 2.5 days was webhooks.". 2026-04-30. https://www.reddit.com/r/webdev/comments/1szlskk/
112. Reddit r/stripe, "[Help] Stripe Webhook not receiving events / failing signature verification…". 2026-05-31. https://www.reddit.com/r/stripe/comments/1tsritg/
113. Reddit r/stripe, "Running two stripe webhooks locally". 2025-01-23. https://www.reddit.com/r/stripe/comments/1i8d325/
114. Reddit r/stripe, "Stripe CLI not forwarding events to localhost". 2022-09-02. https://www.reddit.com/r/stripe/comments/x45kvn/
115. Reddit r/selfhosted, "Need free webhook receiver". 2026-01-18. https://www.reddit.com/r/selfhosted/comments/1qg64ow/
116. Reddit r/webdev, "Instagram webhook tests work, but real DMs and postbacks never arrive". 2026-08-10. https://www.reddit.com/r/webdev/comments/1vkii7b/
117. Reddit r/django, "You don't need to deploy to test a webhook integration. Tunnel your local server instead.". 2026-07-12. https://www.reddit.com/r/django/comments/1uuh1qb/
118. Reddit r/stripe, "What's the best way to test payment edge cases (limbo, duplicate webhooks, 3DS)…". 2026-05-05. https://www.reddit.com/r/stripe/comments/1t49bkl/
119. Reddit r/rails, "5 Stripe webhook gotchas that bit me in production Rails apps". 2026-04-29. https://www.reddit.com/r/rails/comments/1syp4hf/
120. Reddit r/stripe, "How we catch silent Stripe webhook failures before they cost us". 2026-05-11. https://www.reddit.com/r/stripe/comments/1t9xbw8/
121. Reddit r/stripe, "Ever had a Stripe webhook fail and miss a payment?". 2025-05-14. https://www.reddit.com/r/stripe/comments/1kmkz3b/

### Vendor pages, recipes, and test harnesses

122. Hooklistener, "MCP for AI coding assistants" (vendor). 2026-02-12, updated 2026-06-10. https://www.hooklistener.com/guides/mcp-ai-coding-assistants
123. webhooks.cc, MCP docs (vendor). Updated 2026-05. https://webhooks.cc/docs/mcp
124. THE-KIPDEV/webhook-toolkit README (0 stars). 2026-09-18. https://github.com/THE-KIPDEV/webhook-toolkit
125. RequestBin blog, "Best webhook testing tools for AI coding agents" (vendor). 2026-06-01. https://blog.requestbin.net/best-webhook-testing-tools-for-ai-coding-agents-2026/
126. AgentOps-AI/agentops, `app/scripts/run-api-with-stripe.sh` (greps Stripe's human "Ready" line). n.d. https://github.com/AgentOps-AI/agentops/blob/main/app/scripts/run-api-with-stripe.sh
127. omarmahmoud200210/movie-reservation-system, `docs/superpowers/plans/2026-07-15-stripe-live-testing.md` (an agent-written test harness plan). 2026-07-15. https://github.com/omarmahmoud200210/movie-reservation-system
128. Martin Bean, "Using the Stripe CLI with Docker Compose". 2025-08-15. https://martinbean.dev/blog/2025/08/15/using-the-stripe-cli-with-docker-compose/
129. Arnica, "How we converted a GitHub tool into a general-purpose webhook proxy". The page shows 2026-06-02; originally about 2023-04. https://www.arnica.io/blog/how-we-converted-a-github-tool-into-a-general-purpose-webhook-proxy

### Source code read

130. Hookspot CLI source (this repo), working tree: `cmd/listen.go`, `cmd/login_browser.go`, `cmd/errors.go`, `internal/proxy/proxy.go`, `internal/ws/client.go`, `internal/printer/printer.go`, `npm/`. 2026-09-24. Local.
131. Hookspot backend source (`~/projects/my/hookspot`), working tree: `lib/ingest/{router,endpoint}.ex`, `lib/ingest/jobs/deliveries/deliver_job.ex`, `lib/ingest/services/{requests/respond_service,delivery_attempts/finalize_service,deliveries/retry_service}.ex`, `lib/app_web/channels/{project_channel,cli_presence}.ex`, `lib/app_web/controllers/{request_controller,user/*}.ex`, `lib/app/iam/models/organization.ex`. 2026-09-24. Local.
132. stripe/stripe-cli source at commit 160f231 (`pkg/cmd/listen.go`, `pkg/proxy/*`, `pkg/cmd/login_helpers.go`). 2026-09-23. https://github.com/stripe/stripe-cli
133. hookdeck/hookdeck-cli source and README at commit c601293 (`pkg/listen/*`, `pkg/cmd/request_*.go`, `pkg/websocket/*`). 2026-09-24. https://github.com/hookdeck/hookdeck-cli

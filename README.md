# Hookspot CLI

Get webhooks on your laptop. [Hookspot](https://hookspot.dev) gives you a
webhook URL. While `hookspot listen` runs, each webhook reaches the app on your
machine, and you can see what arrived, what your app answered, and send it
again.

## Quickstart

1. Sign up at [hookspot.dev](https://hookspot.dev) and create a route. You get
   a webhook URL to paste into the service that sends webhooks.
2. Install the CLI, approve the login in your browser, then start forwarding to
   your app:

   ```sh
   npm install -g @hookspot/cli
   hookspot login
   hookspot listen --forward-to http://localhost:3000
   ```

3. Click **Send test request** in the app, or press `t` in the CLI. The webhook
   shows up there and in your terminal.

## Installation

npm (Node 18 or newer):

```sh
npm install -g @hookspot/cli
```

Homebrew on macOS or Linux:

```sh
brew install hookspot/hookspot/hookspot-cli
```

Docker, with a CLI key instead of `hookspot login`:

```sh
docker run --rm -it --add-host=host.docker.internal:host-gateway \
  -e HOOKSPOT_CLI_KEY -e HOOKSPOT_ORGANIZATION_SLUG -e HOOKSPOT_PROJECT_SLUG \
  hookspot/cli listen --forward-to http://host.docker.internal:3000
```

Without Node.js, Homebrew, or Docker, download the archive for your system
from the [latest release](https://github.com/hookspot/hookspot-cli/releases/latest)
and follow its [`INSTALL.md`](docs/releases/INSTALL.md).

`hookspot listen` and `hookspot version` tell you when a newer release is out,
and how to update through the channel you installed from:
`npm install -g @hookspot/cli@latest`, `brew upgrade hookspot-cli`, or
`docker pull hookspot/cli`.

## Usage

```sh
hookspot login                                      # approve the login in your browser
hookspot project use                                # switch the active project
hookspot listen --forward-to http://localhost:3000  # forward webhooks to your app
```

In CI, set `HOOKSPOT_CLI_KEY` instead of running `hookspot login`.

## Documentation

- [Install and log in](https://hookspot.dev/docs/cli/): login, projects, and
  CLI keys in CI
- [Listen and forward](https://hookspot.dev/docs/cli/listen/): the full-screen
  view, test events, replays, and the stream
- [CLI reference](https://hookspot.dev/docs/cli/reference/): every command,
  flag, environment variable, and the config file

## Development

[`AGENTS.md`](AGENTS.md) covers the toolchain, the `make` targets, golden files
and end-to-end builds.

## Releasing

Pushing a `v*` tag publishes the release to GitHub Releases, Homebrew, and npm:

```sh
git tag -a v1.2.3 -m "v1.2.3"
git push origin v1.2.3
```

The [release runbook](docs/releases/RUNBOOK.md) covers version choice, secrets,
re-running a failed job, and yanking a release.

## License

MIT. See [LICENSE](LICENSE).

# cflio

A Confluence Cloud CLI built for AI coding agents.

Asking an agent to edit a Confluence page through the Atlassian MCP server breaks in two structural
ways. Updating a page means passing the whole body as a tool argument, which the model has to
regenerate as output tokens — around 35 KB of body HTML is enough for the stream to stall. And the
MCP exposes only Markdown and its own HTML dialect, so every read and write crosses a format
conversion that macros do not survive.

`cflio` moves the round-trip onto the filesystem: `read` downloads a page's body to a local file,
the agent edits that file with its regular file-editing tools, and `update` writes it back. Only
the diff costs tokens.

- **The body never passes through the model.** Bodies go to and from files; stdout carries metadata
  and exploration results only.
- **Lossless round-trip.** The body is stored and sent as the server's own `storage` representation,
  byte for byte. There is no format converter, so macros and layout you did not touch come back
  unchanged.
- **Never silently overwrite someone else's edit.** Updates are locked against the version captured
  at read time, and there is no `--force`.

## Install

```sh
go install github.com/178inaba/cflio@latest
```

## Setup

`cflio` authenticates with an Atlassian API token (Basic auth with your account email).

1. Create a token at [id.atlassian.com/manage-profile/security/api-tokens](https://id.atlassian.com/manage-profile/security/api-tokens).
2. Register it:

   ```sh
   cflio auth login
   ```

   You will be asked for your site URL (anything copied from the browser works — only the host is
   kept), your Atlassian account email, and the token. `cflio` verifies the credentials before
   saving anything, then records the site as a named profile under `~/.config/cflio/`
   (`XDG_CONFIG_HOME` is respected; the directory is `0700` and the file `0600`). The first profile
   registered becomes the default.

## Usage

```sh
# Download a page's body and its metadata sidecar
cflio read https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Release+Notes -o page.xml

# ...edit page.xml with your editor, then write it back
cflio update -f page.xml
cflio update -f page.xml --message 'Clarify the rollback steps'

# Explore
cflio search 'type = page and space = "DEV" and text ~ "release notes"'
cflio children 123456
cflio comments https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Release+Notes
```

Commands that address a page accept both the URL as copied from the browser and a bare page ID.
Add `--format json` to any of them for structured output instead of Markdown. Run `cflio --help` or
`cflio <command> --help` for the full flag reference.

### The read/update cycle

`read` writes two files: the page body (`page.xml`) exactly as the API returned it, and a sidecar
(`page.xml.meta.json`) holding the page ID, version, title, status and URL.

`update` takes only the body file — the page it targets, the profile it authenticates with and the
version it locks against all come from the sidecar, so an update cannot be pointed at the wrong
page. If the page changed on the server since it was read, `update` exits non-zero without writing
and tells you to re-read; Confluence's own page history is the undo mechanism, so no local backup
is kept. After a successful update the sidecar's version is refreshed, so you can keep editing and
updating the same file without re-reading.

Every version `cflio` writes carries a message (`Updated via cflio` by default, `--message` to
override) so agent edits are identifiable in the page history.

### Multiple sites

A command given a page URL picks the matching profile automatically, and `update` does the same
from the URL its sidecar recorded. A URL from an unregistered site fails immediately rather than
falling back to the default profile. `search`, and commands given a bare page ID, use the default
profile; pass `--profile <name>` to choose another, or `cflio profile use <name>` to change the
default. An explicit `--profile` that disagrees with the URL's site is an error rather than a
silent choice.

### Timeouts

Every invocation runs under a deadline — 90 seconds by default, `--timeout` to change it (a Go
duration such as `30m`, or `0` for no deadline). On the deadline or on SIGINT/SIGTERM the command
exits cleanly with a clear error. The default is chosen so the CLI finishes or fails on its own
before a typical agent harness force-kills it (Claude Code's Bash tool sends SIGKILL after 120 s).

### Environment variables

- `CFLIO_PROFILE` selects a profile without passing `--profile` every time. It stands in for the
  default profile, so URL-based auto-selection still wins.
- `CFLIO_TOKEN` replaces the resolved profile's stored token for one invocation; the site and email
  still come from the profile.
- Shared names such as `ATLASSIAN_API_TOKEN` are intentionally **not** read, so another tool's
  credentials can never be picked up by accident.

## Agent Skill

This repo ships an [Agent Skill](./skills/cflio/SKILL.md) that tells an AI agent when and how to
reach for `cflio`. Install it by either:

- copying `skills/cflio/` into your agent's skills directory (e.g. `~/.claude/skills/cflio/` for
  Claude Code), or
- using a skill installer that consumes GitHub repos directly, e.g.
  [`npx skills`](https://www.npmjs.com/package/skills).

## Notes and limitations

- **Confluence Cloud only.** Data Center and Server are out of scope.
- **Comment display is best-effort.** The comment API offers no rendered representation, so comment
  bodies are converted from storage XHTML to plain text locally. This affects display only: page
  bodies are never converted. `comments` shows root comments and their direct replies; replies to
  replies are not fetched. Authors appear as Atlassian account IDs.
- **Short links** (`/wiki/x/…`) are not resolved — open one in a browser and pass the full URL.
- **No retries.** A rate-limited or failing request reports the error rather than backing off.
- Creating, deleting and moving pages, posting comments, attachments, and ADF (the representation
  behind live docs) are not supported. See
  [the tracking issue](https://github.com/178inaba/cflio/issues/1) for the full list.

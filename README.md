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
- **Lossless round-trip.** A body that will be written back is stored and sent as the server's own
  `storage` representation, byte for byte. Nothing converts it on the way through, so macros and
  layout you did not touch come back unchanged. (`read --markdown` converts a body for *reading*;
  that file is deliberately not updatable.)
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
   kept), your Atlassian account email, and the token. The token is not echoed when the prompt is
   attached to a terminal, so it stays out of your scrollback. `cflio` verifies the credentials
   before saving anything, then records the site as a named profile under `~/.config/cflio/`
   (`XDG_CONFIG_HOME` is respected; the directory is `0700` and the file `0600`). The first profile
   registered becomes the default.

## Usage

```sh
# Download a page's body and its metadata sidecar
cflio read https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Release+Notes -o page.xml

# ...edit page.xml with your editor, then write it back
cflio update -f page.xml
cflio update -f page.xml --message 'Clarify the rollback steps'

# Just reading it? Get Markdown instead of storage XHTML
cflio read https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Release+Notes --markdown

# Explore
cflio search 'type = page and space = "DEV" and text ~ "release notes"'
cflio children 123456
cflio comments https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Release+Notes

# See what the page shows: list its attachments, then pull the ones you want
cflio attachments list 123456
cflio attachments download 123456 --pattern '*.png' -o ./assets
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

### Reading a page without editing it

Storage XHTML is the right representation for editing and the wrong one for reading: `ac:` macros,
`ri:` references and `local-id` noise are most of the bytes. `read --markdown` converts the body to
Markdown — the request still asks the API for `storage`, which is the only representation that
carries macros and code bodies intact — and writes `<page-id>.md` by default.

Mentions and page links are resolved on the way: a mention becomes the person's display name, and a
link to another page becomes a Markdown link you can pass straight back to `cflio read`. That costs
a couple of extra requests, batched so the count does not grow with the number of links. A reference
that cannot be resolved — a deleted account, a page this token cannot see — falls back to the
account ID or the bare page title rather than failing the read. So does one whose lookup failed
outright, and the output says when that happened (below).

That file has **no sidecar** and cannot be written back: `update` refuses it, by design. So use
`--markdown` when a page is only going to be read, and the storage default when it might be edited.
The differing default filenames mean reading a page both ways leaves two files rather than one
overwriting the other.

The conversion is best-effort and says where it fell short. Unknown elements pass their text
through, tables keep every cell's content even when the structure cannot survive, and macros the
converter does not handle become a grep-able placeholder followed by whatever body text they wrap.
Anything that became a placeholder is counted in the command's output:

```
Degraded: 3 (adf-extension, jira)
```

If that line is absent, the conversion lost nothing. If it is present and you need the part that
degraded, read the page again without `--markdown`.

Reference resolution reports separately, because a lookup can fail in a way the fallback rendering
hides. A reference that was looked up and matched nothing is a settled answer — the account or the
page is gone — and passes silently. A reference whose lookup produced *no* answer does not: the
request failed, or it was never attempted because the API returned no web link and a same-space
link has no space key to resolve against. Those are counted:

```
Unchecked: 2 (not looked up; names and links may be missing)
```

When that line is present, the rendering may be missing names and links that do exist. Re-reading
without `--markdown` does not help — storage resolves nothing at all — so the useful response is to
run the same command again; in keeping with the no-retries posture below, `cflio` will not do it
for you.

### Reading a page's images and files

A page's images and files are attachments, and no representation of the body carries their contents:
`read --markdown` renders an image as its filename and nothing more. To actually see one, download
it and read the file.

`attachments list` shows every attachment's filename, media type and size, so you can tell a 10 KB
screenshot from a 4 MB PDF before fetching either. `attachments download` then writes the ones whose
filename matches a glob:

```sh
cflio attachments download 123456 --pattern '*.png' -o ./assets
```

The bytes are the response body written through unchanged, so a downloaded image opens as the image
it is.

`--pattern` is **required** — there is no bare "download everything" form, so pulling that 4 MB PDF
while reaching for one screenshot has to be asked for on purpose (`--pattern '*'` does it). It is
case-sensitive, and matching nothing is an error rather than a silent success.

An existing file is never replaced. If any matching attachment would overwrite one, the command
fails before downloading anything and names the file, so a run can never leave some files replaced
and others not — delete them, or point `-o` somewhere else. `-o` defaults to the working directory
and is created if it does not exist.

Uploading attachments is not supported.

### Multiple sites

A command given a page URL picks the matching profile automatically, and `update` does the same
from the URL its sidecar recorded. A URL from an unregistered site fails immediately rather than
falling back to the default profile. `search`, and commands given a bare page ID, use the default
profile; pass `--profile <name>` to choose another, or `cflio profile use <name>` to change the
default. An explicit `--profile` that disagrees with the URL's site is an error rather than a
silent choice.

### Timeouts and exit codes

Every invocation runs under a deadline — 90 seconds by default, `--timeout` to change it (a Go
duration such as `30m`, or `0` for no deadline). On the deadline the command fails with a clear
error that names the flag. The default is chosen so the CLI finishes or fails on its own before a
typical agent harness force-kills it (Claude Code's Bash tool sends SIGKILL after 120 s).

A run that ends normally reports which happened in its exit status, so a caller does not have to
read stderr to classify a failure: `0` for success, `124` when the deadline expired, and `1` for
every other failure. `124` is the code GNU `timeout` uses, and it marks the one failure worth
retrying — with a larger `--timeout`; every other one needs the `Error:` line read instead. A
deadline that expires while `read --markdown` is resolving references is the exception: it is
absorbed like any other lookup failure, so the reference is counted in `Unchecked: N` above and
the run still exits `0`.

Ctrl-C and `SIGTERM` are not reported as failures: cflio prints nothing and terminates by the
signal, so a shell reports `130` for Ctrl-C and `143` for `SIGTERM`, and a Ctrl-C inside a loop over
cflio invocations ends the loop too. At a `cflio auth login` prompt the terminal's echo is restored
first, and no profile is written or changed.

### Environment variables

- `CFLIO_PROFILE` selects a profile without passing `--profile` every time. It stands in for the
  default profile, so URL-based auto-selection still wins.
- `CFLIO_TOKEN` replaces the resolved profile's stored token for one invocation; the site and email
  still come from the profile.
- Shared names such as `ATLASSIAN_API_TOKEN` are intentionally **not** read, so another tool's
  credentials can never be picked up by accident.

## Agent Skill

This repo ships an [Agent Skill](./skills/cflio/SKILL.md) that tells an AI agent when and how to
reach for `cflio`. In Claude Code, install it as a plugin:

```sh
claude plugin marketplace add 178inaba/cflio
claude plugin install cflio@cflio
```

For other agents, use a skill installer that consumes GitHub repos directly, e.g.
[`npx skills`](https://www.npmjs.com/package/skills):

```sh
npx skills add 178inaba/cflio
```

## Development

```sh
go test -race ./...

# Lint runs in Docker so the version matches CI — see compose.yaml
docker compose run --rm lint

# Let golangci-lint apply the fixes it can make itself
docker compose run --rm lint --fix
```

## Notes and limitations

- **Confluence Cloud only.** Data Center and Server are out of scope.
- **Comment display is best-effort.** The comment API offers no rendered representation, so comment
  bodies are converted from storage XHTML to Markdown locally, by the same converter
  `read --markdown` uses. `comments` shows root comments and their direct replies; replies to
  replies are not fetched. Authors appear as Atlassian account IDs, and references inside a comment
  body are not resolved — only `read --markdown` resolves mentions and page links.
- **Converted output never feeds an update.** Both converted outputs — comment bodies and
  `read --markdown` — are for reading only. A body that will be written back is never converted.
- **Short links** (`/wiki/x/…`) are not resolved — open one in a browser and pass the full URL.
- **No retries.** A rate-limited or failing request reports the error rather than backing off.
- **Attachments are read-only.** They can be listed and downloaded; uploading one is not supported.
  A downloaded file is also not wired back into `read --markdown`, which still renders an image as
  its filename.
- Creating, deleting and moving pages, posting comments, uploading attachments, and ADF (the
  representation behind live docs) are not supported. See
  [the tracking issue](https://github.com/178inaba/cflio/issues/1) for the full list.

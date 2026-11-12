# atlio

An Atlassian Cloud CLI for AI coding agents: read Jira issues, and read and update Confluence page
bodies, through local files, without the body passing through the model.

Asking an agent to edit a Confluence page through the Atlassian MCP server breaks in two structural
ways. Updating a page means passing the whole body as a tool argument, which the model has to
regenerate as output tokens — around 35 KB of body HTML is enough for the stream to stall. And the
MCP exposes only Markdown and its own HTML dialect, so every read and write crosses a format
conversion that macros do not survive.

`atlio` moves the round-trip onto the filesystem: `confluence read` downloads a page's body to a
local file, the agent edits that file with its regular file-editing tools, and `confluence update`
writes it back. Only the diff costs tokens. Jira issues travel the same way in one direction:
`jira issue read` writes an issue's rich-text fields to a file and prints its metadata. Jira is
read-only.

- **The body never passes through the model.** Bodies go to and from files; stdout carries metadata
  and exploration results only.
- **Lossless round-trip.** A body that will be written back is stored and sent as the server's own
  `storage` representation, byte for byte. Nothing converts it on the way through, so macros and
  layout you did not touch come back unchanged. (`confluence read --markdown` converts a body for
  *reading*; that file is deliberately not updatable.)
- **Never silently overwrite someone else's edit.** Updates are locked against the version captured
  at read time, and there is no `--force`.

## Install

On macOS, with Homebrew:

```sh
brew install 178inaba/tap/atlio
```

On any platform, download the archive for your OS and architecture from the
[Releases page](https://github.com/178inaba/atlio/releases) and put `atlio` somewhere on your
`PATH`.

Or, with a Go toolchain:

```sh
go install github.com/178inaba/atlio@latest
```

`atlio --version` prints the version you ended up with.

### Verify a download

```sh
gh attestation verify <downloaded archive> --repo 178inaba/atlio
```

A pass means the archive is exactly what this repository's release workflow built from the tagged
commit — the checksum is checked as part of it.

## Setup

`atlio` authenticates with an Atlassian API token (Basic auth with your account email).

1. Create a token at [id.atlassian.com/manage-profile/security/api-tokens](https://id.atlassian.com/manage-profile/security/api-tokens).
2. Register it:

   ```sh
   atlio auth login
   ```

   You will be asked for your Atlassian site URL (anything copied from the browser works — only the
   host is kept), your Atlassian account email, and the token. The token is not echoed when the
   prompt is attached to a terminal, so it stays out of your scrollback. `atlio` checks the
   credentials against both Confluence and Jira before saving anything, and saves them when either
   product accepts them — a site with only one of the two can be registered — saying which products
   answered. It records the site as a named profile under `~/.config/atlio/` (`XDG_CONFIG_HOME` is
   respected; the directory is `0700` and the file `0600`); one profile serves both products. The
   first profile registered becomes the default.

## Usage

```sh
# Download a page's body and its metadata sidecar
atlio confluence read https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Release+Notes -o page.xml

# ...edit page.xml with your editor, then write it back
atlio confluence update -f page.xml
atlio confluence update -f page.xml --message 'Clarify the rollback steps'

# Rename the page while writing it back — or on its own, with the body untouched
atlio confluence update -f page.xml --title 'Release Notes (Q3)'

# Create a page under that one from a file, then keep editing the same file
atlio confluence create --parent 123456 --title 'Release Notes (Q4)' -f new.xml
atlio confluence update -f new.xml

# Just reading it? Get Markdown instead of storage XHTML
atlio confluence read https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Release+Notes --markdown

# Explore
atlio confluence search 'type = page and space = "DEV" and text ~ "release notes"'
atlio confluence children 123456
atlio confluence comments list https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Release+Notes

# Leave a note on the page without leaving the terminal
atlio confluence comments create 123456 -f note.xml

# See what the page shows: pull the images the body uses along with the Markdown
atlio confluence read 123456 --markdown --attachments ./assets

# ...or reach for an attachment the body does not reference
atlio confluence attachments list 123456
atlio confluence attachments download 123456 --pattern '*.png' -o ./assets

# Change a PlantUML diagram on the page, without hand-encoding its payload
atlio confluence plantuml list -f page.xml
atlio confluence plantuml get -f page.xml --id 7c1d4e8a -o diagram.puml
# ...edit diagram.puml, then put it back and update the page
atlio confluence plantuml set -f page.xml --id 7c1d4e8a --source diagram.puml
atlio confluence update -f page.xml

# ...or put a new diagram on the page
atlio confluence plantuml add -f page.xml --source diagram.puml --after 7c1d4e8a
atlio confluence update -f page.xml

# Read a Jira issue: its rich-text fields as Markdown, its metadata on stdout
atlio jira issue read https://example.atlassian.net/browse/KAN-1 --markdown
atlio jira issue read KAN-1
```

Commands that address a page accept the URL as copied from the browser, the short link the Share
dialog copies, and a bare page ID. Commands that address an issue accept its key or its URL (see
[Reading a Jira issue](#reading-a-jira-issue)).
Add `--format json` to any of them for structured output instead of Markdown. Run `atlio --help` or
`atlio <command> --help` for the full flag reference.

### The read/update cycle

`confluence read` writes two files: the page body (`page.xml`) exactly as the API returned it, and a
sidecar (`page.xml.meta.json`) holding the page ID, version, title, status, URL and subtype.

`confluence update` takes only the body file — the page it targets, the profile it authenticates
with and the version it locks against all come from the sidecar, so an update cannot be pointed at
the wrong page. If the page changed on the server since it was read, `confluence update` exits
non-zero without writing and tells you to re-read; Confluence's own page history is the undo
mechanism, so no local backup is kept. After a successful update the sidecar records what the server
returned — the new version, the title and the page's URL — so you can keep editing and updating the
same file without re-reading.

`--title` renames the page in the same request that writes the body back, since the update endpoint
takes the title as a required field: there is nothing else to call. The body still travels, so a
rename on an untouched file resends the same bytes and only the title changes. An empty `--title`
is refused before anything is sent.

Every version `atlio` writes carries a message (`Updated via atlio` by default, `--message` to
override) so agent edits are identifiable in the page history.

### Creating a page

`atlio confluence create --parent <page> --title <title> -f <file>` creates a page under `--parent`,
and all three flags are required. The file is sent as the `storage` representation, byte for byte —
the same representation `confluence read` downloads — so Markdown written into it posts as the
literal characters you typed. An empty file creates an empty page.

On success the file becomes a checkout of the new page: the sidecar `confluence read` would write is
written next to it, filled from what the server created, so the next step can be
`confluence update -f <file>` with no read in between. A file that already has a sidecar is refused
before anything is sent — it is already a checkout of a page, so write to that page with
`confluence update`, or pass a file with no sidecar to create a new one. `-f -` is refused too: the
sidecar has nowhere to go.

Only published classic pages are created, and only under a parent page: no drafts, no live docs, and
nothing at the root of a space. The space is the parent's.

### Reading a page without editing it

Storage XHTML is the right representation for editing and the wrong one for reading: `ac:` macros,
`ri:` references and `local-id` noise are most of the bytes. `confluence read --markdown` converts
the body to Markdown — the request still asks the API for `storage`, which is the only
representation that carries macros and code bodies intact — and writes `<page-id>.md` by default.

Mentions and page links are resolved on the way: a mention becomes the person's display name, and a
link to another page becomes a Markdown link you can pass straight back to `atlio confluence read`.
That costs a couple of extra requests, batched so the count does not grow with the number of links.
A reference that cannot be resolved — a deleted account, a page this token cannot see — falls back
to the account ID or the bare page title rather than failing the read. So does one whose lookup
failed outright, and the output says when that happened (below). Images resolve the same way if you
ask for it — see [Reading a page's images and files](#reading-a-pages-images-and-files).

That file has **no sidecar** and cannot be written back: `confluence update` refuses it, by design.
So use `--markdown` when a page is only going to be read, and the storage default when it might be
edited. The differing default filenames mean reading a page both ways leaves two files rather than
one overwriting the other.

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
run the same command again; in keeping with the no-retries posture below, `atlio` will not do it
for you. The same count covers the attachment fetches `--attachments` makes (below).

### Posting a comment

`atlio confluence comments create <page> -f <file>` posts a file's contents as a footer comment. The
file is sent as the `storage` representation, byte for byte — the same representation
`confluence read` downloads and `confluence update` writes back — so a comment can carry an info
panel, a code block or any other macro, and nothing is converted on the way. Markdown written into
that file therefore posts as the literal characters you typed. `-f -` reads the body from stdin
instead.

Top-level comments only: replies and inline comments are not posted. There is no `--body` string
flag either: storage XHTML in a single shell argument is a quoting trap, and passing a body through
one would put back the copy-paste this command exists to remove.

The comment cannot be edited or deleted with `atlio`; do that in the browser.

### Reading a page's images and files

A page's images and files are attachments, and no representation of the body carries their contents:
`confluence read --markdown` on its own renders an image as its filename and nothing more. To
actually see one, the file has to be downloaded and read.

For the images the body actually uses, `--attachments` does that as part of the read:

```sh
atlio confluence read 123456 --markdown --attachments ./assets
```

The referenced attachments land in `./assets`, and the Markdown links to them
(`![main.png](./assets/main.png)`) so an image can be opened straight from the body. It requires
`--markdown` — a storage body is written back verbatim, so there would be nothing to link the files
from — and it fetches only what the body references; the page's other attachments are the
`confluence attachments` commands' job. The link is the directory as you passed it joined with the
filename, so it resolves from the working directory, the same as `-o`.

An attachment that cannot be fetched leaves the image rendered as its filename rather than failing
the read, and is counted in `Unchecked:` above. A file already sitting at the destination is kept —
not replaced, and not downloaded again — so reading the same page twice is cheap and never
overwrites anything. (`confluence attachments download` refuses the whole run on such a collision; a
read cannot, or re-reading a page would stop working the second time.)

For an attachment the body does not reference — a PDF linked from the text, a spreadsheet — use the
`confluence attachments` commands directly. `confluence attachments list` shows every attachment's
filename, media type and size, so you can tell a 10 KB screenshot from a 4 MB PDF before fetching
either. `confluence attachments download` then writes the ones whose filename matches a glob:

```sh
atlio confluence attachments download 123456 --pattern '*.png' -o ./assets
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

### PlantUML diagrams

A page drawn on with the [PlantUML Diagrams for Confluence][puml-app] app stores each diagram as a
`plantumlcloud` macro, whose source is percent-encoded, deflated and base64'd into a `data`
parameter. `confluence read --markdown` already renders one as a fenced `plantuml` block;
`atlio confluence plantuml` is the way back:

```sh
atlio confluence read <url> -o page.xml
atlio confluence plantuml list -f page.xml
atlio confluence plantuml get -f page.xml --id 7c1d4e8a -o diagram.puml
# ...edit diagram.puml with your regular tools...
atlio confluence plantuml set -f page.xml --id 7c1d4e8a --source diagram.puml
atlio confluence plantuml add -f page.xml --source new.puml --after 7c1d4e8a   # a diagram that is not there yet
atlio confluence update -f page.xml
```

All four subcommands work on the file `confluence read` downloaded and never talk to Confluence; the
page changes when you run `confluence update`. A `--markdown` file is refused, with the guidance
`confluence update` gives.

Do not edit the `data` parameter by hand. The encoding is the app's own, not PlantUML's public one
(which uses a custom base64 alphabet and is rejected), and there is a second half to it: a diagram
drawn in the Confluence editor also exists as a rendered `.svg`/`.png` attachment, and the viewer
keeps showing that attachment for as long as the macro's `revision` matches it. `set` increments
`revision` for you, which is what makes the new diagram appear; a macro with no `revision`
parameter has no such attachment and is left without one. Nothing else in the file changes — only
those two text nodes are rewritten, so unrelated macros and layout survive byte for byte. The
stale attachment is left as it is, which only shows up in page exports.

Macros are selected by `ac:local-id`, which Confluence keeps across editor saves (it reissues
`ac:macro-id` instead). `--name <filename>` is there for convenience and must match exactly one
macro; a macro with no `ac:local-id` can only be reached that way, and passing its empty local-id
as `--id ''` is refused rather than quietly matched against something else.

One case is refused rather than half-done: a macro with no `ac:local-id` on a **live doc**. The
live editor matches the storage body back onto its own copy by local-id, so the edit never reaches
what the editor and viewer render, and the next autosave overwrites the storage body with the old
macro. Open that diagram in the Confluence editor and save it once — which gives the macro a
local-id — then read the page again. `confluence read` records the page's subtype in the sidecar so
this can be told offline; a sidecar written before it did says so and asks for a fresh read rather
than guessing.

`add` puts a diagram that is not on the page yet into the downloaded body, building the whole macro
element out of a source file so that none of it has to be written by hand. One part of it cannot be
left out and has no visible symptom: Confluence keeps the identifiers it is given and back-fills
`ac:macro-id` when it is missing, but it does **not** back-fill `ac:local-id`. A macro inserted
without one renders, and can then never be changed through the storage body again on a live doc —
the same dead end `set` refuses above. `add` generates both ids for that reason, and prints the new
`ac:local-id` so a follow-up `set` can address the diagram.

Without `--after` the macro is appended at the end of the body. `--after <local-id>` puts it
directly after the element carrying that identifier: Confluence stamps `local-id` on block elements
and `ac:local-id` on macros, and either is accepted, so a diagram can be placed beside an existing
one. An anchor that matches nothing — or more than one element — is refused rather than guessed at,
and every other byte of the file is left untouched.

The `filename` parameter defaults to the source file's name with its extension replaced by `.svg`
(`d.puml` → `d.svg`); `--filename` overrides it. The app names the attachments it creates after it
when someone later saves the diagram in the editor, and the viewer renders without it. A macro
added this way stays updatable on both live docs and classic pages, so `add` has no live-doc
refusal of its own.

[puml-app]: https://marketplace.atlassian.com/apps/1215115/plantuml-diagrams-for-confluence-i-uml-flowchart-git?hosting=cloud&tab=overview

### Reading a Jira issue

`jira issue read` writes an issue's rich-text fields to a file and prints the issue's metadata —
key, URL, summary, type, status, priority, assignee, reporter, labels, parent, subtasks, links,
dates and attachments — on stdout. A Jira description can run to tens of thousands of characters,
so the fields are never printed.

The rich-text fields are every field whose value is an Atlassian Document Format (ADF) document:
the description, the environment and multi-line custom fields, in that order, custom fields by
field ID. The command has the two modes `confluence read` has:

- **By default** it writes `<KEY>.adf.json`: one JSON object holding those fields keyed by field ID,
  each value exactly as the API returned it. It loses nothing.
- **`--markdown`** writes `<KEY>.md` instead: the description first, then one section per other
  field headed by the field's name (`## Environment`). That file is for reading only.

`-o` names the file in either mode and replaces whatever is there. An issue with no rich-text field
writes no file, and the output carries no `Body:` line. Neither mode writes a sidecar.

The Markdown conversion reports what it could not represent the way `confluence read --markdown`
does, summed over all the fields:

```
Degraded: 2 (extension, mediaSingle)
```

Images are among them: an image in a description references its file by an ID Jira does not map
to the issue's attachments, so it becomes a placeholder, and the `Attachment:` lines in the output
are how you learn which files exist. If you need what degraded, read `<KEY>.adf.json`.

An issue is named by its key (`KAN-1`, any case) or by a URL copied from the browser:

- `/browse/KAN-1`;
- a board, backlog or list view with the issue open in its panel (`?selectedIssue=KAN-1`);
- an issue view under `/jira/` (`/jira/software/projects/KAN/issues/KAN-1`, and the `core` and
  `servicedesk` equivalents).

A moved issue is answered under its new key, and the output and the default filename use that key.

### Multiple sites

A command given a page or issue URL picks the matching profile automatically, and
`confluence update` does the same from the URL its sidecar recorded. A URL from an unregistered site
fails immediately rather than falling back to the default profile. `confluence search`, and commands
given a bare page ID or issue key, use the default profile; pass `--profile <name>` to choose
another, or `atlio profile use <name>` to change the default. An explicit `--profile` that disagrees
with the URL's site is an error rather than a silent choice.

### Timeouts and exit codes

Every invocation runs under a deadline — 90 seconds by default, `--timeout` to change it (a Go
duration such as `30m`, or `0` for no deadline). On the deadline the command fails with a clear
error that names the flag. The default is chosen so the CLI finishes or fails on its own before a
typical agent harness force-kills it (Claude Code's Bash tool sends SIGKILL after 120 s).

A run that ends normally reports which happened in its exit status, so a caller does not have to
read stderr to classify a failure: `0` for success, `124` when the deadline expired, and `1` for
every other failure. `124` is the code GNU `timeout` uses, and it marks the one failure worth
retrying — with a larger `--timeout`, and for a command that creates something only after the check
below; every other one needs the `Error:` line read instead. A
deadline that expires while `confluence read --markdown` is resolving references or downloading
attachments is the exception: it is absorbed like any other lookup failure, so the reference is
counted in `Unchecked: N` above and the run still exits `0`.

A command that creates something must not be re-run blindly after a `124`: the deadline can expire
after the server has already created the thing. A re-run of `confluence comments create` then posts
a second comment. A re-run of `confluence create` makes no second page — Confluence refuses a title
the space already holds — but fails with the duplicate-title `400` and still leaves you without a
checkout. That `400` alone does not say the first create landed: the deadline may have expired
before anything was sent, and the title may already be taken elsewhere in the space. Check whether
the thing exists first, and re-run only if it does not — for `confluence create`, look for the page
with `atlio confluence children <parent>` (raising `--limit` if it says more child pages are
available, since it lists the oldest first) and, if it is there, run `confluence read` on it to get
a checkout; for `confluence comments create`, look with `atlio confluence comments list <page>`
(raising `--limit` the same way), and if the comment is there nothing is left to do. `confluence update` needs no such check: its
version lock turns a re-run after a write that did land into the "changed since it was read" refusal
rather than a second write.

Ctrl-C and `SIGTERM` are not reported as failures: atlio prints nothing and terminates by the
signal, so a shell reports `130` for Ctrl-C and `143` for `SIGTERM`, and a Ctrl-C inside a loop over
atlio invocations ends the loop too. At an `atlio auth login` prompt the terminal's echo is restored
first, and no profile is written or changed.

### Environment variables

- `ATLIO_PROFILE` selects a profile without passing `--profile` every time. It stands in for the
  default profile, so URL-based auto-selection still wins.
- `ATLIO_TOKEN` replaces the resolved profile's stored token for one invocation; the site and email
  still come from the profile.
- Shared names such as `ATLASSIAN_API_TOKEN` are intentionally **not** read, so another tool's
  credentials can never be picked up by accident.

## Agent Skill

This repo ships an [Agent Skill](./skills/atlio/SKILL.md) that tells an AI agent when and how to
reach for `atlio`. In Claude Code, install it as a plugin:

```sh
claude plugin marketplace add 178inaba/atlio
claude plugin install atlio@atlio
```

For other agents, use a skill installer that consumes GitHub repos directly, e.g.
[`npx skills`](https://www.npmjs.com/package/skills):

```sh
# The URL form is required: 178inaba/atlio@stable reads that suffix as a
# skill-name filter, so it silently installs from the default branch.
npx skills add https://github.com/178inaba/atlio/tree/stable
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

- **Atlassian Cloud only.** Confluence and Jira Data Center and Server are out of scope. Credentials
  are an API token without scopes; a scoped token is refused on the site URL.
- **Jira is read-only.** Nothing atlio sends to Jira changes anything in it: every request is a
  `GET`. Creating or editing issues, commenting and transitions are not supported.
- **Comment display is best-effort.** The comment API offers no rendered representation, so comment
  bodies are converted from storage XHTML to Markdown locally, by the same converter
  `confluence read --markdown` uses. `confluence comments list` shows root comments and their direct
  replies; replies to replies are not fetched. Authors appear as Atlassian account IDs, and
  references inside a comment body are not resolved — only `confluence read --markdown` resolves
  mentions and page links.
- **Converted output never feeds an update.** Every converted output — comment bodies,
  `confluence read --markdown` and `jira issue read --markdown` — is for reading only. A body that
  will be written back is never converted.
- **Short links** (`/wiki/x/…`) are resolved locally: the token encodes the page ID, so no request
  is made to expand one. A short link pointing at a blog post decodes just as well, but the page
  API then answers 404 — atlio addresses pages only. A token that does not decode reports so and
  asks for the full URL.
- **No retries.** A rate-limited or failing request reports the error rather than backing off.
- **Attachments are read-only.** They can be listed and downloaded; uploading one is not supported.
  `confluence read --markdown --attachments` links the images it downloads, but an image whose
  attachment lives on another page or blog post still renders as its filename: the file would have
  to be fetched from content other than the page being read.
- **Comments are post-only.** A footer comment can be posted; editing or deleting one, replying to
  one, and posting an inline comment are not supported.
- Deleting and moving pages, uploading attachments, and reading or writing a Confluence page as ADF
  (the representation behind live docs) are not supported. See
  [the tracking issue](https://github.com/178inaba/atlio/issues/1) for the full list.

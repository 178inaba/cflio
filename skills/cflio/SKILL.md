---
name: cflio
description: Read and edit Confluence Cloud pages from the CLI without the page body passing through your context. Use whenever the user shares a Confluence link, asks you to read, search or summarize a Confluence page, or asks you to edit, update or fix wording on one.
---

# cflio

`cflio` is a Confluence Cloud CLI built for AI agents. Page bodies travel to and from **files**, never through your context or output tokens, so editing a large page costs only the diff you make — not a regeneration of the whole document.

## When to reach for it

- The user shares a Confluence URL, or asks what a page says → `cflio read <url> --markdown -o <file>`, then read the file.
- The user asks you to change something on a page → `cflio read <url> -o <file>` (no `--markdown`), edit the downloaded file, then `cflio update -f <file>`.
- The user asks you to rename a page → `cflio read <url> -o <file>`, then `cflio update -f <file> --title '<new title>'`. No body edit is needed; the file is resent as it was downloaded.
- The user asks you to find something in Confluence → `cflio search '<CQL>'`.
- You need to see how a page fits in the tree, or what people said on it → `cflio children <page>` and `cflio comments list <page>`.
- The user asks you to leave a note or a question on a page → write it to a file as storage XHTML, then `cflio comments create <page> -f <file>`.
- The task needs the images a page shows → add `--attachments <dir>` to the `read --markdown` above; the body links the files it downloaded, so read those paths.
- The task needs a file the body does not show as an image → `cflio attachments list <page>`, then `cflio attachments download <page> --pattern '<glob>' -o <dir>` and read the downloaded file.
- The user asks you to change a PlantUML diagram on a page, or to put a new one on it → `cflio plantuml` (`list`, `get`, `set`, `add`) on the file `read` downloaded, then `cflio update`.

## Operating contract

- **Read → edit the file → update.** Never reconstruct a page body in your context and never paste one into a command argument; that is the exact failure mode this tool exists to avoid. Edit the downloaded file in place with your normal file-editing tools.
- **Pick the read mode from the task, not from a default.**
  - Only going to read, quote or summarize the page → `--markdown`. You get clean Markdown instead of storage XHTML, which costs far fewer tokens to read.
  - The page might be edited → plain `read`. An edit needs the XHTML in your context anyway, and the Markdown file cannot be written back.
  - Guessed wrong and now need to edit? Run `read` again without `--markdown` and redo the edit on that file. The two modes default to different filenames (`<page-id>.md` and `<page-id>.xml`), so nothing is overwritten.
- **The storage file is XHTML, and it round-trips byte-for-byte.** Edit it as XHTML. Do not reformat it, do not pretty-print it, and never rewrite the file you are going to `update` into Markdown — untouched regions must stay untouched so unrelated macros and layout survive. (`--markdown` is a separate read-only output, not a conversion of that file.)
- **Markdown mode is lossy, and it says so.** The output reports what the conversion could not represent (`Degraded: 3 (adf-extension, jira)`, or `unsupported_count` in JSON). If that count is non-zero and the task depends on the part that degraded, re-read the page without `--markdown` and read the storage instead.
- **A failed reference lookup is reported too, and is a different problem.** `Unchecked: 2 (not looked up; names and links may be missing)` — `unchecked_count` in JSON — means that many mentions, page links or `--attachments` fetches could not be checked, because the request failed or there was nothing to query. They render as a bare account ID, page title or image filename, exactly like a reference that genuinely resolves to nothing, so do not conclude from the rendering that the person, page or attachment does not exist. Re-reading without `--markdown` is **not** the fix here: storage resolves no references at all. Run the same `read --markdown` again instead — `cflio` never retries a failed request on its own.
- **To read or change a PlantUML diagram, use `cflio plantuml`; never hand-edit the `data` parameter.** Its encoding is the app's own, not PlantUML's public one, and a diagram drawn in the Confluence editor also needs the macro's `revision` bumped or the viewer goes on showing the old picture. `set` does both. It refuses one case outright — a macro with no `ac:local-id` on a live doc, where the edit would be silently reverted — and tells you what to do instead; do not work around it.
- **To put a new diagram on a page, use `cflio plantuml add`; never write the macro XML yourself.** A macro inserted without an `ac:local-id` renders and is then stuck: Confluence does not back-fill that attribute, and on a live doc no later edit through the storage body reaches the rendered page. `add` builds the whole element — both identifiers included — and prints the `ac:local-id` to hand to a later `set`.
- **Leave macros alone unless the task is about them.** `<ac:structured-macro>` and `<ri:…>` elements are live Confluence features, not decoration.
- **On a version conflict, re-read and re-apply.** If `update` reports the page changed since it was read, run `read` again and redo your edit on the fresh copy. There is no force flag, by design — someone else's edit is never overwritten silently.
- **Never print a page body to stdout** (no `cat` of the downloaded file into your reply). Summarize or quote the relevant lines instead.
- **Explore before editing.** `search`, `children` and `comments list` are read-only and cheap; use them to find the right page rather than guessing at URLs.
- **A comment body is storage XHTML, exactly like a page body.** `comments create` sends the file's bytes unchanged, so Markdown written into it posts as the literal `##` and `**` you typed. Write the macros you want (`<p>`, `<ac:structured-macro ac:name="info">`, …) the same way you would in a page body. `-f -` reads the body from stdin. Only top-level footer comments are posted: for a reply, or for a comment anchored to a span of the body, draft the text and let the user post it.
- **Structured output**: add `--format json` when you want to parse a result rather than read it.
- **Multiple sites**: a page URL selects the right profile automatically. For `search`, which has no URL, pass `--profile <name>` if the user has more than one site registered — check with `cflio profile list`.
- **First-time setup**: if a command fails because no profile is registered, tell the user to run `cflio auth login` (they will need an Atlassian API token; see the repo README).
- **Exit codes say what to do next.** `0` is success. `124` means the `--timeout` deadline expired — raise `--timeout` and run the command again. `1` is every other failure; read the `Error: …` line on stderr and report it to the user rather than retrying.
- **An interrupt is not a failure.** On Ctrl-C or `SIGTERM` cflio prints nothing and terminates by the signal, which a shell reports as `130` or `143` — neither of those is one of the codes above, since the process never exits normally. An interrupted `read` leaves no body file unless the signal lands during the write itself, so do not treat a missing file as a failed request.
- **An image on a page is only readable as a file.** `read --markdown` on its own renders `<ac:image>` as the filename alone. `--attachments <dir>` downloads the ones the body references and links them, so the body tells you which path to read; it requires `--markdown`, and an image it could not fetch stays filename text and is counted in `Unchecked:`. A file already at the destination is kept rather than replaced or re-fetched, so re-reading a page is safe to repeat.
- **`attachments download` is for the files the body does not reference.** `--pattern` is required and case-sensitive; scope it to what the task needs rather than reaching for `--pattern '*'`, since every file you pull you then have to read. A pattern matching nothing is an error, not an empty success, and — unlike the read path above — an existing file is refused rather than kept, failing the whole run: pick a different `-o` instead of deleting the user's files.
- **Not supported**: creating, deleting or moving pages, editing or deleting a comment, replying to one, posting an inline comment, and uploading attachments. Draft those for the user to post themselves.

Run `cflio --help` or `cflio <command> --help` for the full flag reference; it is the source of truth for exact flags and defaults, so this document does not duplicate it.

---
name: atlio
description: Read Jira issues, and read, edit and create Confluence Cloud pages, from the CLI without the body passing through your context. Use whenever the user shares a Jira link or an issue key (e.g. KAN-1), asks what an issue says, asks you to find Jira issues, shares a Confluence link, asks you to read, search or summarize a Confluence page, asks you to edit, update or fix wording on one, asks you to create a new Confluence page, or asks you to put a screenshot, image or file on one.
---

# atlio

`atlio` is an Atlassian Cloud CLI built for AI agents. Jira issues and Confluence page bodies travel to and from **files**, never through your context or output tokens, so reading a long issue or editing a large page costs only what you choose to read and the diff you make — not a regeneration of the whole document.

## When to reach for it

- **Jira** — a Jira link or an issue key, or reading an issue, finding issues, or an issue's comments or attachments → read [references/jira.md](references/jira.md) before the first `atlio jira` command.
- **Confluence** — a Confluence link, or reading, searching, editing, renaming, creating or commenting on a page, or putting an image, a file or a PlantUML diagram on one → read [references/confluence.md](references/confluence.md) before the first `atlio confluence` command.

## Operating contract

- **Each product's `read` writes to a file, in one of two modes.** Without `--markdown`, the server's own representation unconverted; with `--markdown`, a Markdown conversion for reading only, which costs far fewer tokens to read and cannot be written back. The two modes write different default files, so running one after the other overwrites nothing.
- **Markdown mode is lossy, and it says so.** The output reports what the conversion could not represent (a `Degraded:` line with the count and the kinds of content, or `unsupported_count` in JSON). If that count is non-zero and the task depends on the part that degraded, run the read again without `--markdown` and read that file instead.
- **`attachments download` takes the files you name.** `--pattern` is required and case-sensitive; scope it to what the task needs rather than reaching for `--pattern '*'`, since every file you pull you then have to read. A pattern matching nothing is an error, not an empty success, and an existing file is refused, failing the whole run: pick a different `-o` instead of deleting the user's files.
- **Never print a downloaded body to stdout** (no `cat` of a downloaded file into your reply). Summarize or quote the relevant lines instead.
- **Structured output**: add `--format json` when you want to parse a result rather than read it.
- **A URL copied from the browser is a reference like any other** — pass one straight through rather than asking the user for an ID or another form.
- **Multiple sites**: a URL selects the right profile automatically. For a command given no URL, which carries no site, pass `--profile <name>` if the user has more than one site registered — check with `atlio profile list`.
- **First-time setup**: if a command fails because no profile is registered, tell the user to run `atlio auth login` (they will need an Atlassian API token; see the repo README).
- **Exit codes say what to do next.** `0` is success. `124` means the `--timeout` deadline expired — raise `--timeout` and run the command again, unless the error says the request may have been carried out (see the next point). `1` is every other failure; read the `Error: …` line on stderr and report it to the user rather than retrying, unless the error says the request may have been carried out.
- **An error that says the request may have been carried out is worth retrying, but not blindly for a command that creates something.** It is the one sent in full and then answered by no response before the deadline (`124`), a `5xx`, or a `2xx` that could not be parsed (`1`); a `3xx` or `4xx`, and a failure before the whole request was sent, never say it. For a command that creates something, run the check the error names first, and run it again once only if the thing is not there.
- **`atlio` never retries a failed request on its own.**
- **An interrupt is not a failure.** On Ctrl-C or `SIGTERM` atlio prints nothing and terminates by the signal, which a shell reports as `130` or `143` — neither of those is one of the codes above, since the process never exits normally. An interrupted `read` leaves no body file unless the signal lands during the write itself, so do not treat a missing file as a failed request.
- **Draft what atlio does not support.** For anything atlio does not support, draft the text for the user to post themselves.

Run `atlio --help` or `atlio <command> --help` for the full flag reference; it is the source of truth for exact flags and defaults, so this document does not duplicate it.

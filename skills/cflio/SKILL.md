---
name: cflio
description: Read and edit Confluence Cloud pages from the CLI without the page body passing through your context. Use whenever the user shares a Confluence link, asks you to read, search or summarize a Confluence page, or asks you to edit, update or fix wording on one.
---

# cflio

`cflio` is a Confluence Cloud CLI built for AI agents. Page bodies travel to and from **files**, never through your context or output tokens, so editing a large page costs only the diff you make — not a regeneration of the whole document.

## When to reach for it

- The user shares a Confluence URL, or asks what a page says → `cflio read <url> -o <file>`, then read the file.
- The user asks you to change something on a page → read it, edit the downloaded file, then `cflio update -f <file>`.
- The user asks you to find something in Confluence → `cflio search '<CQL>'`.
- You need to see how a page fits in the tree, or what people said on it → `cflio children <page>` and `cflio comments <page>`.

## Operating contract

- **Read → edit the file → update.** Never reconstruct a page body in your context and never paste one into a command argument; that is the exact failure mode this tool exists to avoid. Edit the downloaded file in place with your normal file-editing tools.
- **The file is storage-format XHTML, and it round-trips byte-for-byte.** Edit it as XHTML. Do not reformat it, do not pretty-print it, and do not convert it to or from Markdown — untouched regions must stay untouched so unrelated macros and layout survive.
- **Leave macros alone unless the task is about them.** `<ac:structured-macro>` and `<ri:…>` elements are live Confluence features, not decoration.
- **On a version conflict, re-read and re-apply.** If `update` reports the page changed since it was read, run `read` again and redo your edit on the fresh copy. There is no force flag, by design — someone else's edit is never overwritten silently.
- **Never print a page body to stdout** (no `cat` of the downloaded file into your reply). Summarize or quote the relevant lines instead.
- **Explore before editing.** `search`, `children` and `comments` are read-only and cheap; use them to find the right page rather than guessing at URLs.
- **Structured output**: add `--format json` when you want to parse a result rather than read it.
- **Multiple sites**: a page URL selects the right profile automatically. For `search`, which has no URL, pass `--profile <name>` if the user has more than one site registered — check with `cflio profile list`.
- **First-time setup**: if a command fails because no profile is registered, tell the user to run `cflio auth login` (they will need an Atlassian API token; see the repo README).
- **Not supported**: creating, deleting or moving pages, posting comments, and attachments. Draft replies for the user to post themselves.

Run `cflio --help` or `cflio <command> --help` for the full flag reference; it is the source of truth for exact flags and defaults, so this document does not duplicate it.

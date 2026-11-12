# Jira

## When to reach for it

- The user shares a Jira issue link or an issue key, or asks what an issue says → `atlio jira issue read <url|key> --markdown -o <file>`, then read the file. The issue's metadata (status, assignee, links, attachments, …) is printed directly.
- The user asks you to find issues in Jira → `atlio jira search '<JQL>'`. The query goes to Jira as typed and needs at least one search restriction (`project = KAN`, `assignee = currentUser()`, …): Jira rejects one that only orders, such as `order by created desc` alone.
- You need what people said on an issue → `atlio jira comments list <url|key>`.
- The task needs an image or file attached to an issue → `atlio jira attachments list <url|key>`, then `atlio jira attachments download <url|key> --pattern '<glob>' -o <dir>` and read the downloaded file.

## Operating contract

- **An issue's two files.** `jira issue read --markdown` writes the description and every other rich-text field as Markdown (`<KEY>.md`) for reading; without the flag it writes those fields unconverted as ADF JSON (`<KEY>.adf.json`). An image in a description always counts in `Degraded:`, since it cannot be linked to its attachment. The `Attachment:` lines tell you which files the issue has. `jira comments list` converts comment bodies the same way and reports one `Degraded:` line for the whole listing; comments have no unconverted form to fall back on, since `<KEY>.adf.json` holds the issue's own fields only.
- **Jira is read-only.** atlio changes nothing in Jira: commenting on, editing and transitioning an issue are not supported.
- **An issue URL is an issue reference like any other.** A `/browse/KAN-1` link, a board, backlog or list URL with the issue open in its panel, or an issue URL under `/jira/` all work as copied from the browser. A board URL with no issue open names no issue and is refused.
- **`jira attachments download` is the only way to see an issue's attachments.** Jira allows two attachments with the same filename; when matched attachments' filenames are equal ignoring case, `jira attachments download` writes each with its attachment ID before the extension (`screen-10002.png`), so read the file at the output's `path`, not at its `filename`.

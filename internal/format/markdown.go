package format

import (
	"encoding/xml"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// PageRef names a page the way storage links to one: by space key and title,
// with no page ID anywhere in the body.
type PageRef struct {
	SpaceKey string
	Title    string
}

// Options carries the reference resolution ToMarkdown cannot do itself. The
// converter is a pure function, so looking a name or a URL up — which needs
// the API — happens in the command layer and arrives here as data. A zero
// Options is valid: every reference falls back to what the body already
// carries.
type Options struct {
	// UserNames maps an Atlassian account ID to a display name.
	UserNames map[string]string
	// PageURLs maps a link target to the page's URL.
	PageURLs map[PageRef]string
}

// Result is a conversion and what it could not represent.
type Result struct {
	Markdown string
	// Unsupported names the macros and extensions that became placeholders,
	// deduplicated and sorted; UnsupportedCount counts every occurrence. A
	// reader decides from these whether the rendering can be trusted, without
	// having to read the whole file first.
	Unsupported      []string
	UnsupportedCount int
}

// ToMarkdown converts a storage-format body to Markdown for reading. It never
// fails: unknown input degrades in structure, never in content, so an element
// this converter has never seen still yields the text inside it.
func ToMarkdown(storage string, opts Options) Result {
	r := &renderer{opts: opts}

	// Assembled in one pass: appending the trailing newline afterwards would
	// copy the whole body a second time, and this tool exists for the pages
	// where that is worth avoiding.
	var out strings.Builder
	for i, b := range r.blocks(parseStorage(storage).children) {
		if i > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(b.text)
	}
	if out.Len() > 0 {
		// Unlike the storage path, which writes the API's bytes back
		// verbatim, nothing here has to survive a byte comparison.
		out.WriteByte('\n')
	}

	result := Result{Markdown: out.String()}
	for name, count := range r.unsupported {
		result.Unsupported = append(result.Unsupported, name)
		result.UnsupportedCount += count
	}
	sort.Strings(result.Unsupported)
	return result
}

// node is a parsed storage element, or a run of text when name is empty.
// Attributes are keyed by local name: storage prefixes them (ac:, ri:) but
// never reuses one local name for two meanings on the same element.
type node struct {
	space    string
	local    string
	attr     map[string]string
	text     string
	children []*node
}

func (n *node) isText() bool { return n.local == "" }

// child returns the first child with the given prefix and name, or nil.
func (n *node) child(space, local string) *node {
	if n == nil {
		return nil
	}
	for _, c := range n.children {
		if c.space == space && c.local == local {
			return c
		}
	}
	return nil
}

// parseStorage builds the element tree. Rendering Markdown needs look-ahead
// that a token stream cannot give: a table's header separator depends on its
// first row, and a list item's indentation on what nests under it.
func parseStorage(storage string) *node {
	decoder := newStorageDecoder(storage)

	root := &node{}
	stack := []*node{root}
	for {
		token, err := decoder.Token()
		if err != nil {
			// EOF, or a document too broken to recover from. Either way,
			// render what was parsed rather than losing the whole page.
			break
		}

		parent := stack[len(stack)-1]
		switch t := token.(type) {
		case xml.CharData:
			parent.children = append(parent.children, &node{text: string(t)})
		case xml.StartElement:
			element := &node{
				space: strings.ToLower(t.Name.Space),
				local: strings.ToLower(t.Name.Local),
			}
			if len(t.Attr) > 0 {
				element.attr = make(map[string]string, len(t.Attr))
				for _, a := range t.Attr {
					element.attr[a.Name.Local] = a.Value
				}
			}
			parent.children = append(parent.children, element)
			stack = append(stack, element)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return root
}

// block is one Markdown block. isList marks a list so a nested one can hang
// directly under its parent item instead of being pushed a blank line away.
type block struct {
	text   string
	isList bool
}

func joinBlocks(blocks []block) string {
	texts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		texts = append(texts, b.text)
	}
	return strings.Join(texts, "\n\n")
}

type renderer struct {
	opts        Options
	unsupported map[string]int
}

// unrepresentable records an element that became a placeholder.
func (r *renderer) unrepresentable(name string) {
	if r.unsupported == nil {
		r.unsupported = make(map[string]int)
	}
	r.unsupported[name]++
}

// blocks renders a node's children as a sequence of blocks. Inline children
// accumulate into a paragraph that is flushed when a block child arrives, so
// text sitting directly inside a container survives alongside its siblings.
func (r *renderer) blocks(nodes []*node) []block {
	var out []block
	var paragraph strings.Builder

	flush := func() {
		if text := strings.TrimSpace(paragraph.String()); text != "" {
			out = append(out, block{text: escapeBlockStarts(text)})
		}
		paragraph.Reset()
	}

	var walk func(nodes []*node)
	walk = func(nodes []*node) {
		for _, n := range nodes {
			switch rendered, isBlock := r.block(n); {
			case isBlock:
				flush()
				out = append(out, rendered...)
			case n.isText() || isInlineElement(n):
				paragraph.WriteString(r.inlineNode(n))
			default:
				// An element this converter does not know: drop the tag and
				// keep the children. Losing body text is the worst outcome
				// available, and storage is full of non-macro ac: elements
				// (layouts, inline comment markers) whose children are
				// ordinary content.
				walk(n.children)
			}
		}
	}
	walk(nodes)
	flush()

	return out
}

func isInlineElement(n *node) bool {
	switch n.space {
	case "":
		switch n.local {
		case "strong", "b", "em", "i", "code", "s", "del", "strike",
			"u", "sub", "sup", "span", "time", "br", "a", "img":
			return true
		}
	case "ac":
		switch n.local {
		case "link", "image":
			return true
		}
	}
	return false
}

// block renders a block-level element, reporting whether it recognised one.
// Recognition and rendering are the same switch on purpose: a second list of
// block tag names would silently disagree with this one, and an element
// missing from it degrades into the unknown-element path without a word.
func (r *renderer) block(n *node) ([]block, bool) {
	if n.space == "ac" {
		switch n.local {
		case "structured-macro":
			return r.macro(n), true
		case "adf-extension":
			return r.adfExtension(n), true
		case "task-list":
			return r.taskList(n), true
		}
		return nil, false
	}
	if n.space != "" {
		return nil, false
	}

	switch n.local {
	case "p":
		if text := strings.TrimSpace(r.inlineChildren(n)); text != "" {
			return []block{{text: escapeBlockStarts(text)}}, true
		}
		return nil, true
	case "h1", "h2", "h3", "h4", "h5", "h6":
		if text := strings.TrimSpace(r.inlineChildren(n)); text != "" {
			level, _ := strconv.Atoi(n.local[1:])
			return []block{{text: strings.Repeat("#", level) + " " + text}}, true
		}
		return nil, true
	case "hr":
		return []block{{text: "---"}}, true
	case "ul", "ol":
		return r.list(n), true
	case "table":
		return r.table(n), true
	case "blockquote":
		return quote(r.blocks(n.children)), true
	case "pre":
		return []block{{text: fence("", rawText(n))}}, true
	}
	return nil, false
}

func (r *renderer) list(n *node) []block {
	var items []string
	for _, child := range n.children {
		if child.space != "" || child.local != "li" {
			continue
		}

		marker := "- "
		if n.local == "ol" {
			marker = strconv.Itoa(len(items)+1) + ". "
		}
		items = append(items, r.listItem(child, marker))
	}
	return listBlock(items)
}

func listBlock(items []string) []block {
	if len(items) == 0 {
		return nil
	}
	return []block{{text: strings.Join(items, "\n"), isList: true}}
}

func (r *renderer) listItem(li *node, marker string) string {
	var body strings.Builder
	for i, b := range r.blocks(li.children) {
		if i > 0 {
			if b.isList {
				body.WriteString("\n")
			} else {
				body.WriteString("\n\n")
			}
		}
		body.WriteString(b.text)
	}

	lines := strings.Split(body.String(), "\n")
	indent := strings.Repeat(" ", len(marker))
	for i, line := range lines {
		switch {
		case i == 0:
			lines[i] = strings.TrimRight(marker+line, " ")
		case line != "":
			lines[i] = indent + line
		}
	}
	return strings.Join(lines, "\n")
}

func (r *renderer) taskList(n *node) []block {
	var items []string
	for _, task := range n.children {
		if task.space != "ac" || task.local != "task" {
			continue
		}

		marker := "- [ ] "
		if strings.TrimSpace(rawText(task.child("ac", "task-status"))) == "complete" {
			marker = "- [x] "
		}
		items = append(items, r.listItem(task.child("ac", "task-body"), marker))
	}
	return listBlock(items)
}

// table renders a Markdown table. Structure degrades where Markdown cannot
// follow — colspan and rowspan are dropped, block content inside a cell is
// flattened — but every cell's text is emitted, and a table is never replaced
// by a placeholder.
func (r *renderer) table(n *node) []block {
	rows := tableRows(n)
	if len(rows) == 0 {
		return nil
	}

	columns := len(tableCells(rows[0]))
	header := make([]string, columns)
	body := rows
	if isHeaderRow(rows[0]) {
		header = r.rowCells(rows[0])
		body = rows[1:]
	}

	lines := []string{tableLine(pad(header, columns)), tableLine(repeat("---", columns))}
	for _, row := range body {
		lines = append(lines, tableLine(pad(r.rowCells(row), columns)))
	}
	return []block{{text: strings.Join(lines, "\n")}}
}

// tableRows collects the rows of a table, seeing through thead/tbody/tfoot.
func tableRows(n *node) []*node {
	var rows []*node
	for _, child := range n.children {
		switch {
		case child.space != "":
		case child.local == "tr":
			rows = append(rows, child)
		case child.local == "thead" || child.local == "tbody" || child.local == "tfoot":
			rows = append(rows, tableRows(child)...)
		}
	}
	return rows
}

func tableCells(row *node) []*node {
	var cells []*node
	for _, child := range row.children {
		if child.space == "" && (child.local == "td" || child.local == "th") {
			cells = append(cells, child)
		}
	}
	return cells
}

func isHeaderRow(row *node) bool {
	cells := tableCells(row)
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		if cell.local != "th" {
			return false
		}
	}
	return true
}

func (r *renderer) rowCells(row *node) []string {
	cells := tableCells(row)
	texts := make([]string, 0, len(cells))
	for _, cell := range cells {
		// A cell is one Markdown table cell however much structure it holds,
		// so blocks collapse to spaces and pipes are escaped rather than
		// ending the cell early.
		text := strings.TrimSpace(collapseSpace(joinBlocks(r.blocks(cell.children))))
		texts = append(texts, strings.ReplaceAll(text, "|", `\|`))
	}
	return texts
}

// pad lengthens a row to the table's column count. Rows are never truncated:
// an extra cell renders outside the table but keeps its text.
func pad(cells []string, columns int) []string {
	for len(cells) < columns {
		cells = append(cells, "")
	}
	return cells
}

func repeat(cell string, columns int) []string {
	cells := make([]string, columns)
	for i := range cells {
		cells[i] = cell
	}
	return cells
}

func tableLine(cells []string) string {
	return "| " + strings.Join(cells, " | ") + " |"
}

// macro renders the macros this converter understands and degrades the rest.
func (r *renderer) macro(n *node) []block {
	name := n.attr["name"]
	switch name {
	case "code":
		return []block{{text: fence(macroParameter(n, "language"), rawText(n.child("ac", "plain-text-body")))}}
	case "info", "note", "warning", "tip", "panel":
		return r.panel(n, name)
	}

	if name == "" {
		name = "unnamed"
	}
	r.unrepresentable(name)

	// The placeholder marks the macro, but ac:rich-text-body is storage
	// declaring "this part is body text" — dropping it would silently delete
	// everything an expand or a section wraps. Parameters and plain-text
	// bodies are macro configuration, so they stay out.
	return r.placeholder("[unsupported macro: "+name+"]", n.child("ac", "rich-text-body"))
}

// placeholder marks an element this converter cannot represent and keeps
// whatever body text it wraps.
func (r *renderer) placeholder(text string, body *node) []block {
	return append([]block{{text: text}}, r.blocks(body.childNodes())...)
}

func (r *renderer) panel(n *node, kind string) []block {
	heading := strings.ToUpper(kind[:1]) + kind[1:]
	if title := macroParameter(n, "title"); title != "" {
		heading += ": " + escapeText(title)
	}

	blocks := []block{{text: "**" + heading + "**"}}
	blocks = append(blocks, r.blocks(n.child("ac", "rich-text-body").childNodes())...)
	return quote(blocks)
}

// adfExtension degrades a new-editor blob. Its ac:adf-node subtree is a
// parameter tree rather than markup, so it is not rendered; ac:adf-fallback
// is the editor's own rendering of the same content and is.
func (r *renderer) adfExtension(n *node) []block {
	r.unrepresentable("adf-extension")

	return r.placeholder("[unsupported adf-extension]", n.child("ac", "adf-fallback"))
}

// childNodes is nil-safe so callers can reach into a body element that may
// not be there.
func (n *node) childNodes() []*node {
	if n == nil {
		return nil
	}
	return n.children
}

func macroParameter(n *node, name string) string {
	for _, child := range n.children {
		if child.space == "ac" && child.local == "parameter" && child.attr["name"] == name {
			return strings.TrimSpace(rawText(child))
		}
	}
	return ""
}

func quote(blocks []block) []block {
	text := joinBlocks(blocks)
	if text == "" {
		return nil
	}

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = ">"
			continue
		}
		lines[i] = "> " + line
	}
	return []block{{text: strings.Join(lines, "\n")}}
}

func (r *renderer) inlineChildren(n *node) string {
	var out strings.Builder
	for _, child := range n.childNodes() {
		out.WriteString(r.inlineNode(child))
	}
	return out.String()
}

func (r *renderer) inlineNode(n *node) string {
	if n.isText() {
		return escapeText(collapseSpace(n.text))
	}

	if n.space == "ac" {
		switch n.local {
		case "link":
			return r.link(n)
		case "image":
			return r.image(n)
		}
	}
	if n.space == "" {
		switch n.local {
		case "strong", "b":
			return emphasize("**", r.inlineChildren(n))
		case "em", "i":
			return emphasize("*", r.inlineChildren(n))
		case "s", "del", "strike":
			return emphasize("~~", r.inlineChildren(n))
		case "code":
			return codeSpan(collapseSpace(rawText(n)))
		case "br":
			return "\n"
		case "a":
			return r.anchor(n)
		case "img":
			return "![" + escapeText(n.attr["alt"]) + "](" + n.attr["src"] + ")"
		}
	}

	// Underline, sub/superscript, colour spans and anything unrecognised:
	// CommonMark has no equivalent, so the markup goes and the text stays.
	return r.inlineChildren(n)
}

func (r *renderer) anchor(n *node) string {
	text := strings.TrimSpace(r.inlineChildren(n))
	href := n.attr["href"]
	switch {
	case href == "":
		return text
	case text == "":
		return "[" + escapeText(href) + "](" + href + ")"
	default:
		return "[" + text + "](" + href + ")"
	}
}

// link renders <ac:link>. Storage names its targets rather than addressing
// them, so an unresolved target renders as text: a URL built here from a
// space key and a title is not a form pageref.Parse accepts, which would make
// it a link cflio itself cannot follow.
func (r *renderer) link(n *node) string {
	body := r.linkBody(n)

	target := riChild(n)
	if target == nil {
		// An anchor-only link, which names no resource of its own.
		return body
	}

	switch target.local {
	case "user":
		id := target.attr["account-id"]
		if name := r.opts.UserNames[id]; name != "" {
			return "@" + escapeText(name)
		}
		return "@" + escapeText(id)
	case "page":
		title := target.attr["content-title"]
		text := body
		if text == "" {
			text = escapeText(title)
		}
		if url := r.opts.PageURLs[PageRef{SpaceKey: target.attr["space-key"], Title: title}]; url != "" {
			return "[" + text + "](" + url + ")"
		}
		return text
	case "attachment":
		if body != "" {
			return body
		}
		return escapeText(target.attr["filename"])
	}
	return body
}

// riChild returns the ri: element naming what a link or an image points at.
func riChild(n *node) *node {
	for _, child := range n.children {
		if child.space == "ri" {
			return child
		}
	}
	return nil
}

func (r *renderer) linkBody(n *node) string {
	if plain := n.child("ac", "plain-text-link-body"); plain != nil {
		return escapeText(collapseSpace(rawText(plain)))
	}
	return strings.TrimSpace(r.inlineChildren(n.child("ac", "link-body")))
}

// image renders <ac:image>. An attachment carries no URL that would resolve
// offline, and cflio does not support attachments, so the filename is the
// useful thing to keep.
func (r *renderer) image(n *node) string {
	if source := riChild(n); source != nil {
		switch source.local {
		case "url":
			return "![" + escapeText(n.attr["alt"]) + "](" + source.attr["value"] + ")"
		case "attachment":
			return escapeText(source.attr["filename"])
		}
	}
	return escapeText(n.attr["alt"])
}

// rawText concatenates the text under a node without touching it, for CDATA
// bodies that have to come out byte-identical.
func rawText(n *node) string {
	if n == nil {
		return ""
	}
	if n.isText() {
		return n.text
	}
	// A code macro's body is one CDATA run, so the decoder hands it over as a
	// single text child. Returning it directly saves copying what is often
	// the largest string on the page.
	if len(n.children) == 1 && n.children[0].isText() {
		return n.children[0].text
	}

	var out strings.Builder
	for _, child := range n.children {
		out.WriteString(rawText(child))
	}
	return out.String()
}

// collapseSpace squeezes whitespace runs to a single space, the way an HTML
// renderer would. A leading or trailing space is kept: it separates this text
// from the inline markup next to it. Non-breaking spaces collapse too, so the
// empty <p>&nbsp;</p> paragraphs Confluence leaves behind disappear.
func collapseSpace(s string) string {
	var out strings.Builder
	pending := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			pending = true
			continue
		}
		if pending {
			out.WriteByte(' ')
			pending = false
		}
		out.WriteRune(r)
	}
	if pending {
		out.WriteByte(' ')
	}
	return out.String()
}

// textEscaper covers the characters that would otherwise be read as Markdown
// syntax. Underscore is deliberately absent: identifiers like read_page_body
// are everywhere in the pages this mode exists to make readable, CommonMark
// does not emphasise intra-word underscores, and escaping every one costs
// more legibility than it buys correctness.
var textEscaper = strings.NewReplacer(
	`\`, `\\`,
	"`", "\\`",
	"*", `\*`,
	"[", `\[`,
	"]", `\]`,
	"<", `\<`,
)

func escapeText(s string) string { return textEscaper.Replace(s) }

// escapeBlockStarts protects a paragraph whose text would otherwise open a
// list, a heading or a quote. It applies to paragraphs only — the markers this
// converter emits itself are syntax, not text.
func escapeBlockStarts(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = escapeLineStart(line)
	}
	return strings.Join(lines, "\n")
}

func escapeLineStart(line string) string {
	if line == "" {
		return line
	}
	switch line[0] {
	case '#', '>', '-', '+':
		return `\` + line
	}

	digits := 0
	for digits < len(line) && line[digits] >= '0' && line[digits] <= '9' {
		digits++
	}
	if digits > 0 && digits < len(line) && (line[digits] == '.' || line[digits] == ')') {
		return line[:digits] + `\` + line[digits:]
	}
	return line
}

func emphasize(marker, text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	return marker + text + marker
}

func codeSpan(text string) string {
	ticks := strings.Repeat("`", longestBacktickRun(text)+1)
	if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") {
		return ticks + " " + text + " " + ticks
	}
	return ticks + text + ticks
}

// fence wraps a code body, widening the fence past any backtick run inside so
// the body cannot end it early.
func fence(language, body string) string {
	marker := strings.Repeat("`", max(longestBacktickRun(body)+1, 3))
	return marker + language + "\n" + body + "\n" + marker
}

func longestBacktickRun(s string) int {
	longest, run := 0, 0
	for _, r := range s {
		if r != '`' {
			run = 0
			continue
		}
		run++
		if run > longest {
			longest = run
		}
	}
	return longest
}

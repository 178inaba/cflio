package format

import (
	"cmp"
	"encoding/json/v2"
	"math"
	"strconv"
	"strings"
	"time"
)

// adfNode is one node of an Atlassian Document Format tree. ADF is plain JSON
// in one shape for every node, so a single recursive type covers the whole
// spec; attrs stay a generic map because each node type reads only the one or
// two it needs.
type adfNode struct {
	Type    string         `json:"type"`
	Attrs   map[string]any `json:"attrs"`
	Content []adfNode      `json:"content"`
	Marks   []adfMark      `json:"marks"`
	Text    string         `json:"text"`
}

type adfMark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs"`
}

// ADFToMarkdown converts an Atlassian Document Format document to Markdown
// for reading. Like ToMarkdown it never fails: a node it does not represent
// becomes a counted placeholder followed by whatever content it holds, so
// structure degrades and text does not.
//
// A document that does not decode is rendered as far as it decoded: v2 leaves
// the members it filled before the error in place, which for a cut-off
// document is everything up to the cut.
func ADFToMarkdown(doc []byte) Result {
	return ADFSectionsToMarkdown([]ADFSection{{Doc: doc}})
}

// ADFSection is one ADF document to render as part of a larger Markdown file.
type ADFSection struct {
	// Heading, when set, is rendered as a level-2 heading ahead of the
	// document's content.
	Heading string
	Doc     []byte
}

// ADFSectionsToMarkdown renders several ADF documents as one Markdown file,
// each under its heading, and reports what none of them could represent as
// one Result, the way a single document's losses are reported.
func ADFSectionsToMarkdown(sections []ADFSection) Result {
	r := &adfRenderer{}
	var blocks []block
	for _, section := range sections {
		if section.Heading != "" {
			blocks = append(blocks, block{text: "## " + escapeText(section.Heading)})
		}
		blocks = append(blocks, r.document(section.Doc)...)
	}
	return r.result(blocks)
}

// DocumentsResult is several ADF documents converted one by one. Markdown
// holds one entry per document, in input order, each ending in a newline as
// Result.Markdown does unless the document rendered to nothing; Unsupported
// and UnsupportedCount report what none of them could represent, in Result's
// terms.
type DocumentsResult struct {
	Markdown         []string
	Unsupported      []string
	UnsupportedCount int
}

// ADFDocumentsToMarkdown converts each document to its own Markdown and
// reports the losses of all of them together, for a listing whose entries are
// rendered apart but read as one output.
func ADFDocumentsToMarkdown(docs [][]byte) DocumentsResult {
	r := &adfRenderer{}
	result := DocumentsResult{Markdown: make([]string, len(docs))}
	for i, doc := range docs {
		result.Markdown[i] = assemble(r.document(doc))
	}
	result.Unsupported, result.UnsupportedCount = r.report()
	return result
}

type adfRenderer struct {
	losses
}

func (r *adfRenderer) document(doc []byte) []block {
	// An empty or null value is no document at all — a comment or field with
	// nothing in it — so it renders as nothing rather than as a document that
	// failed to decode.
	if len(doc) == 0 || string(doc) == "null" {
		return nil
	}

	var root adfNode
	err := json.Unmarshal(doc, &root)

	if root.Type == "doc" {
		blocks := r.blocks(root.Content)
		if err != nil {
			// Decoding stops at the first value it cannot place and drops
			// everything after it, so a document that rendered at all may
			// still be missing its tail. Counting that is what keeps a zero
			// in Degraded meaning the rendering is complete.
			blocks = append(blocks, block{text: r.placeholder("invalid-adf", "")})
		}
		return blocks
	}

	// Anything else at the root is not a document, but it may still hold
	// one's content, so it is kept behind a placeholder like any other node
	// this converter cannot place.
	blocks := []block{{text: r.placeholder("invalid-adf", "")}}
	if root.Type == "" {
		return append(blocks, r.blocks(root.Content)...)
	}
	return append(blocks, r.blocks([]adfNode{root})...)
}

// placeholder records a node this converter cannot represent and returns the
// marker that stands in for it. The two go together so that no placeholder
// can reach the output without being counted.
func (r *adfRenderer) placeholder(name, key string) string {
	r.unrepresentable(name)
	return placeholderText(name, key)
}

// adfInlineTypes are the inline node types the spec defines. Every other type
// is treated as a block when it appears among blocks, which is what keeps the
// text of an unknown block from being run into the paragraph beside it.
var adfInlineTypes = map[string]bool{
	"text":            true,
	"hardBreak":       true,
	"mention":         true,
	"emoji":           true,
	"date":            true,
	"status":          true,
	"inlineCard":      true,
	"mediaInline":     true,
	"inlineExtension": true,
	"placeholder":     true,
}

// blocks renders a sequence of nodes as blocks. Inline nodes accumulate into a
// paragraph that is flushed when a block node arrives, so text sitting
// directly inside a container survives alongside its siblings.
func (r *adfRenderer) blocks(nodes []adfNode) []block {
	var out []block
	var paragraph strings.Builder

	flush := func() {
		if text := strings.TrimSpace(paragraph.String()); text != "" {
			out = append(out, block{text: escapeBlockStarts(text)})
		}
		paragraph.Reset()
	}

	for _, n := range nodes {
		if adfInlineTypes[n.Type] {
			paragraph.WriteString(r.inline(n))
			continue
		}
		flush()
		out = append(out, r.block(n)...)
	}
	flush()

	return out
}

func (r *adfRenderer) block(n adfNode) []block {
	switch n.Type {
	case "paragraph", "caption":
		return r.paragraph(n.Content)
	case "heading":
		text := strings.TrimSpace(r.inlines(n.Content))
		if text == "" {
			return nil
		}
		level, ok := attrInt(n.Attrs, "level")
		if !ok || level < 1 || level > 6 {
			level = 1
		}
		return []block{{text: strings.Repeat("#", level) + " " + text}}
	case "bulletList", "orderedList":
		return r.list(n)
	case "listItem":
		return listBlock([]string{itemText(r.blocks(n.Content), "- ")})
	case "taskList":
		return r.taskList(n)
	case "taskItem":
		return listBlock([]string{taskItemText(r.blocks(n.Content), taskBox(n))})
	case "codeBlock":
		return []block{{text: fence(attrString(n.Attrs, "language"), adfRawText(n.Content))}}
	case "blockquote":
		return quote(r.blocks(n.Content))
	case "rule":
		return []block{{text: "---"}}
	case "table":
		return r.table(n)
	case "panel":
		heading := "Panel"
		if kind := attrString(n.Attrs, "panelType"); kind != "" {
			heading = strings.ToUpper(kind[:1]) + kind[1:]
		}
		return panelBlocks(heading, r.blocks(n.Content))
	case "expand", "nestedExpand":
		return detailsBlocks(attrString(n.Attrs, "title"), r.blocks(n.Content))
	case "blockCard", "embedCard":
		if link := cardLink(n); link != "" {
			return []block{{text: link}}
		}
	case "mediaSingle", "mediaGroup":
		return r.mediaContainer(n)
	}
	return r.unknownBlock(n)
}

func (r *adfRenderer) paragraph(nodes []adfNode) []block {
	if text := strings.TrimSpace(r.inlines(nodes)); text != "" {
		return []block{{text: escapeBlockStarts(text)}}
	}
	return nil
}

func (r *adfRenderer) list(n adfNode) []block {
	start := 1
	if order, ok := attrInt(n.Attrs, "order"); ok && order >= 0 {
		start = order
	}

	var items []string
	for _, child := range n.Content {
		marker := "- "
		if n.Type == "orderedList" {
			marker = strconv.Itoa(start+len(items)) + ". "
		}
		// A child that is not a listItem is out of spec, but its content is
		// still content, so it becomes an item of its own rather than vanishing.
		content := []adfNode{child}
		if child.Type == "listItem" {
			content = child.Content
		}
		items = append(items, itemText(r.blocks(content), marker))
	}
	return listBlock(items)
}

// taskList renders a task list. A nested list arrives as a sibling of the
// items rather than inside one, so it is hung under the item before it, which
// is where the editor shows it.
func (r *adfRenderer) taskList(n adfNode) []block {
	var items []string
	var box string
	var body []block
	started := false
	for _, child := range n.Content {
		if child.Type != "taskItem" {
			body = append(body, r.blocks([]adfNode{child})...)
			started = true
			continue
		}
		if started {
			items = append(items, taskItemText(body, box))
		}
		box, body, started = taskBox(child), r.blocks(child.Content), true
	}
	if started {
		items = append(items, taskItemText(body, box))
	}
	return listBlock(items)
}

// taskItemText lays a task out as a list item whose content opens with its
// checkbox. The box is content, not marker: what is nested under the task
// aligns with the box, two columns in, where Markdown reads it as nested —
// indenting it past the box would turn it into continuation text instead.
func taskItemText(blocks []block, box string) string {
	return "- " + box + strings.TrimPrefix(itemText(blocks, "- "), "-")
}

func taskBox(n adfNode) string {
	if attrString(n.Attrs, "state") == "DONE" {
		return "[x]"
	}
	return "[ ]"
}

// table renders a Markdown table on the terms the storage converter does:
// structure degrades where Markdown cannot follow, every cell's text is kept.
func (r *adfRenderer) table(n adfNode) []block {
	var rows []adfNode
	var stray []adfNode
	for _, child := range n.Content {
		if child.Type == "tableRow" {
			rows = append(rows, child)
		} else {
			stray = append(stray, child)
		}
	}
	if len(rows) == 0 {
		return r.blocks(stray)
	}

	cells := make([][]string, 0, len(rows))
	for _, row := range rows {
		cells = append(cells, r.rowCells(row))
	}
	table := tableBlock(cells, len(rows[0].Content), isADFHeaderRow(rows[0]))
	return append([]block{table}, r.blocks(stray)...)
}

func isADFHeaderRow(row adfNode) bool {
	if len(row.Content) == 0 {
		return false
	}
	for _, cell := range row.Content {
		if cell.Type != "tableHeader" {
			return false
		}
	}
	return true
}

func (r *adfRenderer) rowCells(row adfNode) []string {
	texts := make([]string, 0, len(row.Content))
	for _, cell := range row.Content {
		content := []adfNode{cell}
		if cell.Type == "tableCell" || cell.Type == "tableHeader" {
			content = cell.Content
		}
		texts = append(texts, cellText(r.blocks(content)))
	}
	return texts
}

// mediaContainer degrades an image or file block. Its media children carry a
// Media Services ID, which is not a Jira attachment ID and has no documented
// mapping to one, so there is nothing to link; the container is the one thing
// counted, since counting its children too would report one picture as two
// losses. A caption is text the author wrote and is kept.
func (r *adfRenderer) mediaContainer(n adfNode) []block {
	out := []block{{text: r.placeholder(n.Type, "")}}
	for _, child := range n.Content {
		if child.Type == "media" {
			continue
		}
		out = append(out, r.blocks([]adfNode{child})...)
	}
	return out
}

// unknownBlock marks a node this converter does not represent and keeps the
// content it wraps: an extension's body or an unknown node's children are
// still the author's text.
func (r *adfRenderer) unknownBlock(n adfNode) []block {
	text := r.placeholder(cmp.Or(n.Type, "unnamed"), attrString(n.Attrs, "extensionKey"))
	return append([]block{{text: text}}, r.blocks(n.Content)...)
}

func (r *adfRenderer) inlines(nodes []adfNode) string {
	var out strings.Builder
	for _, n := range nodes {
		out.WriteString(r.inline(n))
	}
	return out.String()
}

func (r *adfRenderer) inline(n adfNode) string {
	switch n.Type {
	case "text":
		return adfText(n)
	case "hardBreak":
		return "\n"
	case "mention":
		// The attribute carries the leading @ already.
		if text := attrString(n.Attrs, "text"); text != "" {
			return escapeText(text)
		}
		return "@" + escapeText(attrString(n.Attrs, "id"))
	case "emoji":
		return escapeText(cmp.Or(attrString(n.Attrs, "text"), attrString(n.Attrs, "shortName")))
	case "date":
		return adfDate(attrString(n.Attrs, "timestamp"))
	case "status":
		return escapeText(attrString(n.Attrs, "text"))
	case "inlineCard":
		if link := cardLink(n); link != "" {
			return link
		}
	}

	return r.placeholder(cmp.Or(n.Type, "unnamed"), attrString(n.Attrs, "extensionKey")) + r.inlines(n.Content)
}

// adfText renders a text node with the marks Markdown has a form for. code
// goes innermost and unescaped, since a code span shows its content
// literally; link goes outermost, since a link can wrap emphasis but not the
// other way round. Underline, colour, sub/superscript and border have no
// Markdown form and are dropped, keeping the text — that is a change of
// presentation, not a loss of content, so it is not counted.
func adfText(n adfNode) string {
	var code, strong, em, strike bool
	var href string
	for _, m := range n.Marks {
		switch m.Type {
		case "code":
			code = true
		case "strong":
			strong = true
		case "em":
			em = true
		case "strike":
			strike = true
		case "link":
			href = attrString(m.Attrs, "href")
		}
	}

	text := escapeText(n.Text)
	if code {
		text = codeSpan(n.Text)
	}
	if em {
		text = emphasize("*", text)
	}
	if strong {
		text = emphasize("**", text)
	}
	if strike {
		text = emphasize("~~", text)
	}
	if href != "" {
		text = "[" + text + "](" + href + ")"
	}
	return text
}

// cardLink renders a smart link as the URL it points at, or "" for a card
// that carries JSON-LD data instead of a URL, which has no address to link.
func cardLink(n adfNode) string {
	url := attrString(n.Attrs, "url")
	if url == "" {
		return ""
	}
	return "[" + escapeText(url) + "](" + url + ")"
}

// adfDate renders a date node's timestamp as a UTC calendar date. The spec
// types it as a string and states no unit; a value of 1e11 or more is read as
// milliseconds, since as seconds it would be past the year 5000. One that is
// not an integer at all is shown as it is rather than dropped.
func adfDate(timestamp string) string {
	v, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return escapeText(timestamp)
	}
	t := time.Unix(v, 0)
	if v >= 1e11 {
		t = time.UnixMilli(v)
	}
	return t.UTC().Format(time.DateOnly)
}

// adfRawText concatenates the text under nodes untouched, for code blocks
// whose content has to come out byte-identical.
func adfRawText(nodes []adfNode) string {
	var out strings.Builder
	for _, n := range nodes {
		out.WriteString(n.Text)
		out.WriteString(adfRawText(n.Content))
	}
	return out.String()
}

func attrString(attrs map[string]any, key string) string {
	s, _ := attrs[key].(string)
	return s
}

// attrInt reads a numeric attribute, which JSON decoding into any yields as a
// float64; ok is false for anything that is not a whole number.
func attrInt(attrs map[string]any, key string) (int, bool) {
	f, ok := attrs[key].(float64)
	if !ok || f != math.Trunc(f) || math.Abs(f) > math.MaxInt32 {
		return 0, false
	}
	return int(f), true
}

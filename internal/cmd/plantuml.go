package cmd

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/178inaba/cflio/internal/format"
	"github.com/178inaba/cflio/internal/plantuml"
	"github.com/178inaba/cflio/internal/sidecar"
	"github.com/spf13/cobra"
)

// plantUMLMacroName is the macro the app "PlantUML Diagrams for Confluence"
// writes. It is the only macro this command group touches.
const plantUMLMacroName = "plantumlcloud"

// plantUMLPreviewLimit caps `list`'s one-line source preview. The full source
// is what `get` is for.
const plantUMLPreviewLimit = 60

func newPlantUMLCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plantuml",
		Short: "Read and rewrite the PlantUML diagrams in a downloaded page",
		Long: `Work with the PlantUML diagrams on a page you have downloaded with
` + "`cflio read`" + `, without hand-editing the macro's encoded payload.

A ` + "`plantumlcloud`" + ` macro stores its diagram source percent-encoded, deflated
and base64'd in a data parameter. Producing that by hand is both error-prone
-- the encoding is the app's own, not PlantUML's public one -- and exactly
the kind of blob this tool exists to keep out of an agent's context. These
subcommands read and write it for you:

  cflio read <url> -o page.xml
  cflio plantuml list -f page.xml
  cflio plantuml get -f page.xml --id <local-id> -o diagram.puml
  ...edit diagram.puml with your regular file editing tools...
  cflio plantuml set -f page.xml --id <local-id> --source diagram.puml
  cflio update -f page.xml

All three work on the local file and never talk to Confluence; the page is
only written when you run ` + "`cflio update`" + `. Inserting a new diagram is not
supported: draw it in the Confluence editor and edit it from here.`,
	}
	cmd.AddCommand(newPlantUMLListCmd(), newPlantUMLGetCmd(), newPlantUMLSetCmd())
	return cmd
}

func newPlantUMLListCmd() *cobra.Command {
	var (
		file      string
		outFormat format.Format
	)

	cmd := &cobra.Command{
		Use:   "list -f <file>",
		Short: "List the PlantUML diagrams in a downloaded page body",
		Long: `List every plantumlcloud macro in a downloaded page body, in the order it
appears, with the identifiers ` + "`get`" + ` and ` + "`set`" + ` select one by.

A macro whose payload does not decode is still listed, marked as
undecodable, so a page can be inspected without the listing hiding what it
could not read.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPlantUMLList(cmd, file, outFormat)
		},
	}

	addPlantUMLFileFlag(cmd, &file)
	addFormatFlag(cmd, &outFormat)

	return cmd
}

func newPlantUMLGetCmd() *cobra.Command {
	var (
		file      string
		id        string
		name      string
		outPath   string
		outFormat format.Format
	)

	cmd := &cobra.Command{
		Use:   "get -f <file> --id <local-id>",
		Short: "Write one diagram's source to a file",
		Long: `Decode one diagram's source out of a downloaded page body and write it to
a file, then print where it went.

The source is never printed: edit the file with your regular file editing
tools and write it back with ` + "`cflio plantuml set`" + `, the same way a page body
is edited. It is written exactly as it was decoded, with no trailing newline
added, so what you edit is the diagram as the page holds it. (` + "`set`" + ` re-encodes
whatever it is given, so running it on an unedited file still rewrites the
macro's payload and bumps its revision -- the diagram is the same, the bytes
are not.)

An existing file at the destination is overwritten, as ` + "`cflio read -o`" + `
overwrites one -- so do not run this again over a source you have already
edited.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPlantUMLGet(cmd, file, id, name, outPath, outFormat)
		},
	}

	addPlantUMLFileFlag(cmd, &file)
	addPlantUMLSelectFlags(cmd, &id, &name)
	cmd.Flags().StringVarP(&outPath, "output", "o", "",
		"file to write the diagram source to (default ./<filename without extension>.puml)")
	addFormatFlag(cmd, &outFormat)

	return cmd
}

func newPlantUMLSetCmd() *cobra.Command {
	var (
		file      string
		id        string
		name      string
		source    string
		outFormat format.Format
	)

	cmd := &cobra.Command{
		Use:   "set -f <file> --id <local-id> --source <path>",
		Short: "Replace one diagram's source in a downloaded page body",
		Long: `Encode a diagram source file into one macro's data parameter, leaving every
other byte of the page body untouched. Write the page back with
` + "`cflio update`" + `.

The macro's revision parameter is incremented at the same time, when it has
one. A diagram drawn in the Confluence editor also exists as a rendered
attachment on the page, and the viewer keeps showing that attachment for as
long as the revision matches it -- so without the bump the page would go on
displaying the old diagram. A macro with no revision parameter has no such
attachment and is left without one.

The source file is encoded exactly as it is, trailing newline included.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPlantUMLSet(cmd, file, id, name, source, outFormat)
		},
	}

	addPlantUMLFileFlag(cmd, &file)
	addPlantUMLSelectFlags(cmd, &id, &name)
	cmd.Flags().StringVar(&source, "source", "",
		"file holding the new diagram source")
	if err := cmd.MarkFlagRequired("source"); err != nil {
		panic(err)
	}
	addFormatFlag(cmd, &outFormat)

	return cmd
}

// addPlantUMLFileFlag registers the -f every subcommand takes. It names the
// downloaded page body, exactly as `update` takes it.
func addPlantUMLFileFlag(cmd *cobra.Command, file *string) {
	cmd.Flags().StringVarP(file, "file", "f", "",
		"page body downloaded by cflio read, without --markdown")
	if err := cmd.MarkFlagRequired("file"); err != nil {
		panic(err)
	}
}

// addPlantUMLSelectFlags registers the two ways to name a macro. Exactly one
// has to be given: with several diagrams on a page there is no sane default,
// and picking the first would silently rewrite the wrong one.
func addPlantUMLSelectFlags(cmd *cobra.Command, id, name *string) {
	cmd.Flags().StringVar(id, "id", "",
		"ac:local-id of the macro to act on, as cflio plantuml list prints it")
	cmd.Flags().StringVar(name, "name", "",
		"filename parameter of the macro to act on; must match exactly one")
	cmd.MarkFlagsMutuallyExclusive("id", "name")
	cmd.MarkFlagsOneRequired("id", "name")
}

// validatePlantUMLSelector rejects an explicitly empty --id or --name.
//
// An empty value would otherwise read as "the flag was not given": selectMacro
// branches on which of the two is empty, so `--id ""` falls through to
// matching on --name, and an empty --name matches every macro that carries no
// filename. That is a path an agent actually takes -- `list --format json`
// reports "local_id": "" for a macro that has none -- and following it would
// act on a different diagram without saying so. `update --title` refuses an
// empty value for the same reason.
func validatePlantUMLSelector(cmd *cobra.Command, id, name string) error {
	if cmd.Flags().Changed("id") && id == "" {
		return errors.New("--id cannot be empty: a macro that `cflio plantuml list` reports " +
			"with no local-id carries no ac:local-id to select it by, so name it with " +
			"--name <filename> instead")
	}
	if cmd.Flags().Changed("name") && name == "" {
		return errors.New("--name cannot be empty: pass the macro's filename parameter, " +
			"or select it with --id <local-id>")
	}
	return nil
}

// plantUMLFile is a downloaded page body, its sidecar and the PlantUML macros
// it holds. The three travel together because every subcommand needs all of
// them: the body to read offsets into, the sidecar to reject a file that was
// not produced by `read` and to tell a live doc apart, and the macros to
// select from.
type plantUMLFile struct {
	path    string
	storage string
	meta    sidecar.Meta
	macros  []format.Macro
}

// loadPlantUMLFile reads the body file and its sidecar.
//
// The sidecar is loaded even by `list`, which has no use for its contents: it
// is what refuses a --markdown file, whose Markdown holds no macro to find and
// could not be written back if it did. sidecar.Load already says that in the
// words `update` says it in.
func loadPlantUMLFile(path string) (plantUMLFile, error) {
	meta, err := sidecar.Load(path)
	if err != nil {
		return plantUMLFile{}, err
	}

	body, err := os.ReadFile(path)
	if err != nil {
		return plantUMLFile{}, fmt.Errorf("read %s: %w", path, err)
	}

	storage := string(body)
	var macros []format.Macro
	for _, m := range format.Macros(storage) {
		if m.Name == plantUMLMacroName {
			macros = append(macros, m)
		}
	}
	return plantUMLFile{path: path, storage: storage, meta: meta, macros: macros}, nil
}

// selectMacro resolves --id or --name to the one macro it names, and fails
// with the candidates listed rather than acting on a guess.
func (f plantUMLFile) selectMacro(id, name string) (format.Macro, error) {
	if len(f.macros) == 0 {
		return format.Macro{}, fmt.Errorf("%s holds no %s macro", f.path, plantUMLMacroName)
	}

	flag, want := "--id", id
	match := func(m format.Macro) bool { return m.LocalID == id }
	if id == "" {
		flag, want = "--name", name
		match = func(m format.Macro) bool { return plantUMLFilename(m) == name }
	}

	var matched []format.Macro
	for _, m := range f.macros {
		if match(m) {
			matched = append(matched, m)
		}
	}

	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		return format.Macro{}, fmt.Errorf("no %s macro in %s has %s %q; the file holds %s",
			plantUMLMacroName, f.path, flag, want, plantUMLCandidates(f.macros))
	default:
		return format.Macro{}, fmt.Errorf("%s %q matches %d macros in %s (%s); name one of them with --id",
			flag, want, len(matched), f.path, plantUMLCandidates(matched))
	}
}

// plantUMLCandidates renders the macros an ambiguous or unmatched selection
// could have meant, so the next command can be written from the error alone.
func plantUMLCandidates(macros []format.Macro) string {
	described := make([]string, 0, len(macros))
	for _, m := range macros {
		described = append(described, fmt.Sprintf("%s (%s)", plantUMLLocalID(m.LocalID), plantUMLName(plantUMLFilename(m))))
	}
	return strings.Join(described, ", ")
}

// plantUMLFilename is the macro's filename parameter, empty when it has none.
func plantUMLFilename(m format.Macro) string {
	p, _ := m.Param("filename")
	return p.Value
}

// plantUMLLocalID and plantUMLName render a macro's two identifiers for a
// human reader. They take the strings rather than the macro because the same
// labels are printed from the result types, which carry no macro -- and going
// through one pair of functions is what keeps `list`, `get`, `set` and the
// error messages naming a missing identifier the same way.
func plantUMLLocalID(localID string) string {
	if localID == "" {
		return "(no local-id)"
	}
	return "local-id " + localID
}

func plantUMLName(filename string) string {
	if filename == "" {
		return "(no filename)"
	}
	return filename
}

// plantUMLRevisionLabel is the third of the same kind, for the parameter a
// macro can be without.
func plantUMLRevisionLabel(revision string) string {
	if revision == "" {
		return "no revision"
	}
	return "revision " + revision
}

// plantUMLSource decodes a macro's payload, naming the macro in whatever the
// codec refuses. Which payloads are readable is internal/plantuml's rule, so
// that the Markdown conversion and this draw the same line.
func plantUMLSource(m format.Macro) (string, error) {
	compressed, _ := m.Param("compressed")
	data, _ := m.Param("data")

	source, err := plantuml.Source(compressed.Value, data.Value)
	if err != nil {
		return "", fmt.Errorf("read the diagram in macro %s: %w", plantUMLLocalID(m.LocalID), err)
	}
	return source, nil
}

// plantUMLMacroItem is one macro as `list` reports it.
type plantUMLMacroItem struct {
	// LocalID and Filename are empty when the macro carries neither; the
	// Markdown rendering substitutes a placeholder, the JSON does not, so a
	// consumer can tell an absent identifier from a literal one.
	LocalID  string `json:"local_id"`
	Filename string `json:"filename"`
	// Revision is the parameter's text rather than a number: it is absent on
	// a macro that has no rendered attachment, and `set` reports what it did
	// with it separately.
	Revision string `json:"revision"`
	// Decodable says whether the payload could be read at all. Preview is
	// empty when it could not.
	Decodable bool   `json:"decodable"`
	Preview   string `json:"source_preview"`
}

func (p plantUMLMacroItem) markdown() string {
	preview := p.Preview
	if !p.Decodable {
		preview = "(source did not decode)"
	}
	return fmt.Sprintf("- **%s** %s (%s) — %s",
		plantUMLLocalID(p.LocalID), plantUMLName(p.Filename),
		plantUMLRevisionLabel(p.Revision), preview)
}

func runPlantUMLList(cmd *cobra.Command, file string, outFormat format.Format) error {
	f, err := loadPlantUMLFile(file)
	if err != nil {
		return err
	}

	items := make([]plantUMLMacroItem, 0, len(f.macros))
	for _, m := range f.macros {
		revision, _ := m.Param("revision")
		item := plantUMLMacroItem{
			LocalID:  m.LocalID,
			Filename: plantUMLFilename(m),
			Revision: revision.Value,
		}
		if source, err := plantUMLSource(m); err == nil {
			item.Decodable = true
			item.Preview = plantUMLPreview(source)
		}
		items = append(items, item)
	}

	return writeList(cmd, outFormat, "diagrams", items, "")
}

// plantUMLPreview reduces a diagram source to the one line `list` shows.
//
// The first line that is not a @start directive is the useful one — every
// diagram opens with @startuml, so showing that would tell the reader nothing
// — but a source that is nothing but directives still gets its first line
// rather than an empty preview.
func plantUMLPreview(source string) string {
	var first string
	for line := range strings.SplitSeq(source, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "@start") {
			return truncatePreview(line)
		}
		if first == "" {
			first = line
		}
	}
	return truncatePreview(first)
}

// truncatePreview cuts a preview to length, counting characters rather than
// bytes so a multi-byte one is never cut in half.
func truncatePreview(line string) string {
	// A line can hold no more characters than bytes, so this settles most
	// lines without decoding them.
	if len(line) <= plantUMLPreviewLimit {
		return line
	}
	runes := []rune(line)
	if len(runes) <= plantUMLPreviewLimit {
		return line
	}
	return string(runes[:plantUMLPreviewLimit]) + "…"
}

// plantUMLGetResult is what get prints on success: where the source went,
// never the source itself.
type plantUMLGetResult struct {
	LocalID  string `json:"local_id"`
	Filename string `json:"filename"`
	Path     string `json:"path"`
	Bytes    int    `json:"bytes"`
}

func runPlantUMLGet(cmd *cobra.Command, file, id, name, outPath string, outFormat format.Format) error {
	// Validated before the file is read, the way `attachments download`
	// hoists it: this command writes a file, and reaching the writer with an
	// unvalidated format would leave one behind on the way to the error.
	if err := outFormat.Validate(); err != nil {
		return err
	}
	if err := validatePlantUMLSelector(cmd, id, name); err != nil {
		return err
	}

	f, err := loadPlantUMLFile(file)
	if err != nil {
		return err
	}
	m, err := f.selectMacro(id, name)
	if err != nil {
		return err
	}
	source, err := plantUMLSource(m)
	if err != nil {
		return err
	}

	dest := outPath
	if dest == "" {
		if dest, err = plantUMLDest(m); err != nil {
			return err
		}
	}
	// Written with the page body's writer, so the source lands byte for byte:
	// a newline added here would travel back into the macro on the next `set`.
	if err := writeBody(dest, source); err != nil {
		return err
	}

	return writePlantUMLGetResult(cmd, outFormat, plantUMLGetResult{
		LocalID:  m.LocalID,
		Filename: plantUMLFilename(m),
		Path:     dest,
		Bytes:    len(source),
	})
}

// plantUMLDest names the file `get` writes when -o is left off: the filename
// parameter with its extension swapped for .puml, or the local-id when the
// macro has no filename.
//
// Both come from the page, which is what makes the check at the end the same
// one attachmentDest makes: a value carrying a path would put the file
// somewhere the caller never named.
func plantUMLDest(m format.Macro) (string, error) {
	stem := m.LocalID
	if filename := plantUMLFilename(m); filename != "" {
		// Only when something is left: a filename that is nothing but an
		// extension falls back to the local-id rather than to an empty name.
		if trimmed := strings.TrimSuffix(filename, filepath.Ext(filename)); trimmed != "" {
			stem = trimmed
		}
	}
	if stem == "" {
		return "", errors.New("this macro has neither a filename nor an ac:local-id " +
			"to name the output file after; pass -o")
	}
	if !isPlainFilename(stem) {
		return "", fmt.Errorf("%q is not a plain filename, so it cannot name the output file; pass -o", stem)
	}
	return stem + ".puml", nil
}

// plantUMLSetResult is what set prints on success. Revision is nil when the
// macro carries no revision parameter, which is also what says the page has no
// rendered attachment of this diagram to out-rank.
type plantUMLSetResult struct {
	LocalID  string            `json:"local_id"`
	Filename string            `json:"filename"`
	Path     string            `json:"path"`
	Bytes    int               `json:"bytes"`
	Revision *plantUMLRevision `json:"revision,omitempty"`
}

type plantUMLRevision struct {
	From int `json:"from"`
	To   int `json:"to"`
}

func runPlantUMLSet(cmd *cobra.Command, file, id, name, source string, outFormat format.Format) error {
	if err := outFormat.Validate(); err != nil {
		return err
	}
	if err := validatePlantUMLSelector(cmd, id, name); err != nil {
		return err
	}

	f, err := loadPlantUMLFile(file)
	if err != nil {
		return err
	}
	m, err := f.selectMacro(id, name)
	if err != nil {
		return err
	}
	// Checked before anything is written, and before the source is even read:
	// the payload format has to be one cflio can read back, or the next `get`
	// would fail on a diagram cflio itself wrote.
	if _, err := plantUMLSource(m); err != nil {
		return err
	}
	if err := f.checkReachable(m); err != nil {
		return err
	}

	diagram, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read %s: %w", source, err)
	}
	data, err := plantuml.Encode(string(diagram))
	if err != nil {
		return err
	}

	edits, revision, err := plantUMLEdits(m, data)
	if err != nil {
		return err
	}
	if err := writeBody(f.path, applyStorageEdits(f.storage, edits)); err != nil {
		return err
	}

	return writePlantUMLSetResult(cmd, outFormat, plantUMLSetResult{
		LocalID:  m.LocalID,
		Filename: plantUMLFilename(m),
		Path:     f.path,
		Bytes:    len(diagram),
		Revision: revision,
	})
}

// checkReachable refuses the one macro a storage edit cannot reach: one with
// no ac:local-id on a live doc.
//
// The live doc's editor keeps its own copy of the document and matches the
// storage body back onto it by local-id. A macro without one is not matched:
// the change never reaches what the editor and the viewer render, and the next
// autosave writes the old macro back over the storage body. Refusing is the
// only honest answer — an update that reports success and then silently
// reverts is worse than one that never ran.
func (f plantUMLFile) checkReachable(m format.Macro) error {
	if m.LocalID != "" {
		return nil
	}

	live, known := f.meta.LiveDoc()
	if !known {
		return fmt.Errorf("macro %s in %s has no ac:local-id, and the sidecar does not say whether "+
			"the page is a live doc -- it was written before cflio recorded that. "+
			"Run `cflio read` on the page again and redo the edit on the fresh file",
			plantUMLName(plantUMLFilename(m)), f.path)
	}
	if live {
		return fmt.Errorf("macro %s has no ac:local-id and %s is a live doc, "+
			"so a change to it through the storage body never reaches the rendered document "+
			"and is overwritten by the next edit in Confluence. "+
			"Open the diagram in the Confluence editor and save it once, which gives the macro an "+
			"ac:local-id, then read the page again and retry",
			plantUMLName(plantUMLFilename(m)), f.meta.PageURL)
	}
	return nil
}

// storageEdit is one byte range of a storage body and what replaces it.
type storageEdit struct {
	start, end int
	value      string
}

// plantUMLEdits works out what set has to rewrite, and reports the revision
// change for the caller to print.
func plantUMLEdits(m format.Macro, data string) ([]storageEdit, *plantUMLRevision, error) {
	dataParam, ok := m.Param("data")
	if !ok || dataParam.Empty {
		// Only reachable on a macro cflio would refuse to read, since a
		// decodable one has a data parameter by definition. Reported rather
		// than assumed away, because writing one is a different feature:
		// cflio does not insert diagrams.
		return nil, nil, fmt.Errorf("macro %s has no data parameter to replace", plantUMLLocalID(m.LocalID))
	}
	edits := []storageEdit{{start: dataParam.Start, end: dataParam.End, value: data}}

	revisionParam, ok := m.Param("revision")
	if !ok {
		return edits, nil, nil
	}
	// A parameter that is there but unreadable is not treated as absent:
	// leaving it alone would leave the viewer showing the old diagram, which
	// is the failure this command exists to prevent.
	current, err := strconv.Atoi(revisionParam.Value)
	if revisionParam.Empty || err != nil || current < 0 {
		return nil, nil, fmt.Errorf("macro %s has revision=%q, which is not a number to increment; "+
			"fix it in the page body first", plantUMLLocalID(m.LocalID), revisionParam.Value)
	}
	edits = append(edits, storageEdit{
		start: revisionParam.Start,
		end:   revisionParam.End,
		value: strconv.Itoa(current + 1),
	})
	return edits, &plantUMLRevision{From: current, To: current + 1}, nil
}

// applyStorageEdits rebuilds the body with every edit spliced in, in one pass
// over it: replacing them one at a time would copy the whole page once per
// edit, and a page body is the largest thing this tool handles.
//
// Splicing rather than re-serializing the parsed body is what keeps the rest
// of the page byte-identical: a page holds macros, layouts and markup this
// tool has no model of, and rewriting it from a model would quietly normalise
// all of them.
func applyStorageEdits(storage string, edits []storageEdit) string {
	slices.SortFunc(edits, func(a, b storageEdit) int { return cmp.Compare(a.start, b.start) })

	var out strings.Builder
	out.Grow(len(storage))
	written := 0
	for _, e := range edits {
		out.WriteString(storage[written:e.start])
		out.WriteString(e.value)
		written = e.end
	}
	out.WriteString(storage[written:])
	return out.String()
}

// writePlantUMLGetResult and writePlantUMLSetResult render one result per
// --format, each holding both of its forms, the way writeReadResult and
// writeUpdateResult do. Neither validates the format: both callers hoist that
// check ahead of writing their file.
func writePlantUMLGetResult(cmd *cobra.Command, outFormat format.Format, result plantUMLGetResult) error {
	if outFormat == format.JSON {
		return writeJSON(cmd, result)
	}

	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Diagram:  %s (%s)\nSource:   %s (%d bytes)\n",
		plantUMLName(result.Filename), plantUMLLocalID(result.LocalID), result.Path, result.Bytes)
	return err
}

func writePlantUMLSetResult(cmd *cobra.Command, outFormat format.Format, result plantUMLSetResult) error {
	if outFormat == format.JSON {
		return writeJSON(cmd, result)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Diagram:  %s (%s)\nUpdated:  %s (%d bytes of source)\n",
		plantUMLName(result.Filename), plantUMLLocalID(result.LocalID), result.Path, result.Bytes)
	if result.Revision != nil {
		fmt.Fprintf(&out, "Revision: %d -> %d\n", result.Revision.From, result.Revision.To)
	} else {
		out.WriteString("Revision: none (this macro has no revision parameter)\n")
	}
	fmt.Fprintf(&out, "Write the page back with `cflio update -f %s`.\n", result.Path)

	_, err := fmt.Fprint(cmd.OutOrStdout(), out.String())
	return err
}

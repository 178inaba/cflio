package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/178inaba/cflio/internal/format"
	"github.com/178inaba/cflio/internal/plantuml"
	"github.com/178inaba/cflio/internal/sidecar"
)

// diagramMacro renders one plantumlcloud macro, with the source encoded the
// way the app encodes it. attrs goes into the element tag verbatim, and an
// empty filename or revision leaves that parameter out, so a case can build
// the shapes a real page carries.
func diagramMacro(t *testing.T, attrs, filename, source, revision string) string {
	t.Helper()

	macro := `<ac:structured-macro ac:name="plantumlcloud"` + attrs + `>`
	if filename != "" {
		macro += `<ac:parameter ac:name="filename">` + filename + `</ac:parameter>`
	}
	macro += `<ac:parameter ac:name="data">` + mustEncode(t, source) + `</ac:parameter>` +
		`<ac:parameter ac:name="compressed">true</ac:parameter>`
	if revision != "" {
		macro += `<ac:parameter ac:name="revision">` + revision + `</ac:parameter>`
	}
	return macro + `</ac:structured-macro>`
}

// classicMeta is a sidecar for a page that is not a live doc, which is what
// most cases need: it is the state in which every macro can be updated.
func classicMeta() sidecar.Meta {
	classic := ""
	meta := currentMeta()
	meta.Subtype = &classic
	return meta
}

// liveMeta is classicMeta's counterpart: the sidecar of a page whose editor
// rewrites the storage body behind cflio's back.
func liveMeta() sidecar.Meta {
	live := liveSubtypeValue
	meta := currentMeta()
	meta.Subtype = &live
	return meta
}

// liveSubtypeValue is what the API reports for a live doc. Spelt out here
// rather than imported so the tests pin the literal the sidecar has to carry.
const liveSubtypeValue = "live"

// seedDiagramPage writes a body holding the given macros, plus a sidecar.
func seedDiagramPage(t *testing.T, meta sidecar.Meta, macros ...string) string {
	t.Helper()
	return seedReadPage(t, "<p>before</p>"+strings.Join(macros, "<p>between</p>")+"<p>after</p>", meta)
}

func TestPlantUMLListReportsEveryDiagramInDocumentOrder(t *testing.T) {
	file := seedDiagramPage(t, classicMeta(),
		diagramMacro(t, ` ac:local-id="aaa"`, "first.svg", "@startuml\nAlice -> Bob\n@enduml", "1"),
		// No local-id, no filename, no revision: the shape `list` has to
		// render entirely out of placeholders.
		diagramMacro(t, "", "", "@startuml\nactor User\n@enduml", ""),
		`<ac:structured-macro ac:name="plantumlcloud" ac:local-id="ccc">`+
			`<ac:parameter ac:name="filename">broken.svg</ac:parameter>`+
			`<ac:parameter ac:name="data">not base64!!</ac:parameter>`+
			`<ac:parameter ac:name="compressed">true</ac:parameter>`+
			`</ac:structured-macro>`,
	)

	run, err := runCflio(t, "plantuml", "list", "-f", file)
	if err != nil {
		t.Fatalf("plantuml list error = %v", err)
	}

	lines := strings.Split(strings.TrimRight(run.stdout, "\n"), "\n")
	want := []string{
		"- **local-id aaa** first.svg (revision 1) — Alice -> Bob",
		"- **(no local-id)** (no filename) (no revision) — actor User",
		"- **local-id ccc** broken.svg (no revision) — (source did not decode)",
	}
	if len(lines) != len(want) {
		t.Fatalf("plantuml list printed %d lines, want %d:\n%s", len(lines), len(want), run.stdout)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i+1, lines[i], want[i])
		}
	}
}

func TestPlantUMLListJSONCarriesTheSameDiagrams(t *testing.T) {
	file := seedDiagramPage(t, classicMeta(),
		diagramMacro(t, ` ac:local-id="aaa"`, "first.svg", "@startuml\nAlice -> Bob\n@enduml", "3"),
	)

	run, err := runCflio(t, "plantuml", "list", "-f", file, "--format", "json")
	if err != nil {
		t.Fatalf("plantuml list error = %v", err)
	}

	var payload struct {
		Diagrams []struct {
			LocalID   string `json:"local_id"`
			Filename  string `json:"filename"`
			Revision  string `json:"revision"`
			Decodable bool   `json:"decodable"`
			Preview   string `json:"source_preview"`
		} `json:"diagrams"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v; output was %s", err, run.stdout)
	}
	if len(payload.Diagrams) != 1 {
		t.Fatalf("payload holds %d diagrams, want 1: %s", len(payload.Diagrams), run.stdout)
	}
	got := payload.Diagrams[0]
	if got.LocalID != "aaa" || got.Filename != "first.svg" || got.Revision != "3" {
		t.Errorf("diagram = %+v, want local-id aaa / first.svg / revision 3", got)
	}
	if !got.Decodable || got.Preview != "Alice -> Bob" {
		t.Errorf("decodable = %v, preview = %q, want true / %q", got.Decodable, got.Preview, "Alice -> Bob")
	}
}

func TestPlantUMLListReportsAFileWithNoDiagram(t *testing.T) {
	file := seedReadPage(t, "<p>no macros here</p>", classicMeta())

	run, err := runCflio(t, "plantuml", "list", "-f", file)
	if err != nil {
		t.Fatalf("plantuml list error = %v", err)
	}
	if got, want := run.stdout, "No diagrams.\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestPlantUMLGetWritesTheSourceTheMarkdownReadRenders(t *testing.T) {
	// Taken from the fixture the Markdown converter is pinned against, so
	// what `get` writes is compared with what a `read --markdown` fence holds
	// for the same payload rather than with this test's own idea of it.
	raw, err := os.ReadFile("../format/testdata/plantumlcloud_macro.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	fenced, err := os.ReadFile("../format/testdata/plantumlcloud_macro.md")
	if err != nil {
		t.Fatalf("read rendered fixture: %v", err)
	}
	want := strings.TrimSuffix(strings.TrimPrefix(string(fenced), "```plantuml\n"), "```\n")
	want = strings.TrimSuffix(want, "\n")

	file := seedReadPage(t, string(raw), classicMeta())
	out := filepath.Join(t.TempDir(), "diagram.puml")

	run, err := runCflio(t, "plantuml", "get", "-f", file, "--id", "7c1d4e8a", "-o", out)
	if err != nil {
		t.Fatalf("plantuml get error = %v", err)
	}
	if !strings.Contains(run.stdout, out) {
		t.Errorf("stdout = %q, want it to name %s", run.stdout, out)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != want {
		t.Errorf("written source = %q, want %q", got, want)
	}
}

func TestPlantUMLGetDefaultsItsOutputName(t *testing.T) {
	for _, tt := range []struct {
		name     string
		attrs    string
		filename string
		selector []string
		want     string
	}{
		{
			name:     "from the filename",
			attrs:    ` ac:local-id="aaa"`,
			filename: "diagram.svg",
			selector: []string{"--id", "aaa"},
			want:     "diagram.puml",
		},
		{
			name:     "from the local-id when there is no filename",
			attrs:    ` ac:local-id="aaa"`,
			selector: []string{"--id", "aaa"},
			want:     "aaa.puml",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			file := seedDiagramPage(t, classicMeta(),
				diagramMacro(t, tt.attrs, tt.filename, "@startuml\nactor User\n@enduml", "1"))

			// -o is left off, so the file lands in the working directory.
			dir := t.TempDir()
			t.Chdir(dir)

			args := append([]string{"plantuml", "get", "-f", file}, tt.selector...)
			if _, err := runCflio(t, args...); err != nil {
				t.Fatalf("plantuml get error = %v", err)
			}

			got, err := os.ReadFile(filepath.Join(dir, tt.want))
			if err != nil {
				t.Fatalf("ReadFile(%s) error = %v", tt.want, err)
			}
			if want := "@startuml\nactor User\n@enduml"; string(got) != want {
				t.Errorf("written source = %q, want %q", got, want)
			}
		})
	}
}

func TestPlantUMLSetChangesOnlyTheDataAndRevisionNodes(t *testing.T) {
	before := "<p>before</p>" +
		diagramMacro(t, ` ac:local-id="aaa"`, "first.svg", "@startuml\nAlice -> Bob\n@enduml", "1") +
		"<p>between</p>" +
		diagramMacro(t, ` ac:local-id="bbb"`, "second.svg", "@startuml\nactor User\n@enduml", "4") +
		"<p>after</p>"
	file := seedReadPage(t, before, classicMeta())

	const edited = "@startuml\nAlice -> Carol\n@enduml"
	source := filepath.Join(t.TempDir(), "diagram.puml")
	if err := os.WriteFile(source, []byte(edited), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	run, err := runCflio(t, "plantuml", "set", "-f", file, "--id", "aaa", "--source", source)
	if err != nil {
		t.Fatalf("plantuml set error = %v", err)
	}
	if !strings.Contains(run.stdout, "Revision: 1 -> 2") {
		t.Errorf("stdout = %q, want it to report the revision bump", run.stdout)
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	after := string(raw)

	// Exactly two text nodes changed: the edited macro's data and revision.
	// Rebuilding the expected body from the parts that must not have moved is
	// what pins that -- anything else the command touched shows up here.
	want := "<p>before</p>" +
		diagramMacro(t, ` ac:local-id="aaa"`, "first.svg", edited, "2") +
		"<p>between</p>" +
		diagramMacro(t, ` ac:local-id="bbb"`, "second.svg", "@startuml\nactor User\n@enduml", "4") +
		"<p>after</p>"
	if after != want {
		t.Errorf("body after set =\n%s\nwant\n%s", after, want)
	}
}

// TestPlantUMLSetRoundTripsBackThroughBothReaders closes the loop the
// commands exist for: what `set` writes has to come back out of `get` and to
// render as the new diagram in `read --markdown`, whose converter decodes the
// payload by a path of its own.
func TestPlantUMLSetRoundTripsBackThroughBothReaders(t *testing.T) {
	file := seedDiagramPage(t, classicMeta(),
		diagramMacro(t, ` ac:local-id="aaa"`, "first.svg", "@startuml\nAlice -> Bob\n@enduml", "1"))

	const edited = "@startuml\nactor 利用者\nnote right: 100% done\n@enduml"
	source := filepath.Join(t.TempDir(), "diagram.puml")
	if err := os.WriteFile(source, []byte(edited), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := runCflio(t, "plantuml", "set", "-f", file, "--id", "aaa", "--source", source); err != nil {
		t.Fatalf("plantuml set error = %v", err)
	}

	out := filepath.Join(t.TempDir(), "back.puml")
	if _, err := runCflio(t, "plantuml", "get", "-f", file, "--id", "aaa", "-o", out); err != nil {
		t.Fatalf("plantuml get error = %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != edited {
		t.Errorf("source after a set/get round trip = %q, want %q", got, edited)
	}

	// The Markdown converter reads the payload through its own decode path,
	// so it is what says a page updated this way renders the new diagram.
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	rendered := format.ToMarkdown(string(body), format.Options{})
	if !strings.Contains(rendered.Markdown, "```plantuml\n"+edited+"\n```") {
		t.Errorf("read --markdown would render\n%s\nwant it to fence %q", rendered.Markdown, edited)
	}
	if rendered.UnsupportedCount != 0 {
		t.Errorf("the updated macro degraded to a placeholder (%v)", rendered.Unsupported)
	}
}

func TestPlantUMLSetLeavesAMacroWithNoRevisionParameterWithoutOne(t *testing.T) {
	before := diagramMacro(t, ` ac:local-id="aaa"`, "first.svg", "@startuml\nAlice -> Bob\n@enduml", "")
	file := seedReadPage(t, before, classicMeta())

	const edited = "@startuml\nAlice -> Carol\n@enduml"
	source := filepath.Join(t.TempDir(), "diagram.puml")
	if err := os.WriteFile(source, []byte(edited), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	run, err := runCflio(t, "plantuml", "set", "-f", file, "--id", "aaa", "--source", source)
	if err != nil {
		t.Fatalf("plantuml set error = %v", err)
	}
	if !strings.Contains(run.stdout, "Revision: none") {
		t.Errorf("stdout = %q, want it to report that there is no revision", run.stdout)
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	want := diagramMacro(t, ` ac:local-id="aaa"`, "first.svg", edited, "")
	if string(raw) != want {
		t.Errorf("body after set =\n%s\nwant\n%s", raw, want)
	}
	if strings.Contains(string(raw), "revision") {
		t.Error("set added a revision parameter to a macro that had none")
	}
}

func TestPlantUMLSelectionFailsWithTheCandidatesListed(t *testing.T) {
	file := seedDiagramPage(t, classicMeta(),
		diagramMacro(t, ` ac:local-id="aaa"`, "same.svg", "@startuml\nAlice -> Bob\n@enduml", "1"),
		diagramMacro(t, ` ac:local-id="bbb"`, "same.svg", "@startuml\nactor User\n@enduml", "1"),
	)

	for _, tt := range []struct {
		name     string
		selector []string
		wants    []string
	}{
		{
			name:     "no such local-id",
			selector: []string{"--id", "zzz"},
			wants:    []string{`--id "zzz"`, "local-id aaa (same.svg)", "local-id bbb (same.svg)"},
		},
		{
			name:     "no such filename",
			selector: []string{"--name", "other.svg"},
			wants:    []string{`--name "other.svg"`, "local-id aaa (same.svg)"},
		},
		{
			name:     "ambiguous filename",
			selector: []string{"--name", "same.svg"},
			wants:    []string{`matches 2 macros`, "local-id aaa (same.svg)", "--id"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"plantuml", "get", "-f", file}, tt.selector...)
			_, err := runCflio(t, args...)
			if err == nil {
				t.Fatal("plantuml get error = nil, want a failure")
			}
			for _, want := range tt.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to mention %q", err, want)
				}
			}
		})
	}
}

func TestPlantUMLSelectsAMacroWithNoLocalIDByName(t *testing.T) {
	file := seedDiagramPage(t, classicMeta(),
		diagramMacro(t, "", "only.svg", "@startuml\nactor User\n@enduml", "1"))

	out := filepath.Join(t.TempDir(), "diagram.puml")
	if _, err := runCflio(t, "plantuml", "get", "-f", file, "--name", "only.svg", "-o", out); err != nil {
		t.Fatalf("plantuml get error = %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("Stat(%s) error = %v", out, err)
	}
}

func TestPlantUMLRequiresExactlyOneSelector(t *testing.T) {
	file := seedDiagramPage(t, classicMeta(),
		diagramMacro(t, ` ac:local-id="aaa"`, "only.svg", "@startuml\nactor User\n@enduml", "1"))

	for _, tt := range []struct {
		name     string
		selector []string
	}{
		{name: "neither", selector: nil},
		{name: "both", selector: []string{"--id", "aaa", "--name", "only.svg"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"plantuml", "get", "-f", file}, tt.selector...)
			if _, err := runCflio(t, args...); err == nil {
				t.Error("plantuml get error = nil, want a failure")
			}
		})
	}
}

func TestPlantUMLRefusesAPayloadItCannotRead(t *testing.T) {
	uncompressed := `<ac:structured-macro ac:name="plantumlcloud" ac:local-id="aaa">` +
		`<ac:parameter ac:name="data">whatever</ac:parameter>` +
		`<ac:parameter ac:name="compressed">false</ac:parameter>` +
		`</ac:structured-macro>`
	file := seedReadPage(t, uncompressed, classicMeta())

	source := filepath.Join(t.TempDir(), "diagram.puml")
	if err := os.WriteFile(source, []byte("@startuml\n@enduml"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	for _, tt := range []struct {
		name string
		args []string
	}{
		{name: "get", args: []string{"plantuml", "get", "-f", file, "--id", "aaa",
			"-o", filepath.Join(t.TempDir(), "out.puml")}},
		{name: "set", args: []string{"plantuml", "set", "-f", file, "--id", "aaa", "--source", source}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runCflio(t, tt.args...)
			if err == nil {
				t.Fatalf("plantuml %s error = nil, want a failure", tt.name)
			}
			if !strings.Contains(err.Error(), `compressed="false"`) {
				t.Errorf("error = %q, want it to name the payload format", err)
			}
		})
	}

	// The body is left exactly as it was: a refusal must not half-write.
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(raw) != uncompressed {
		t.Errorf("body after the refusals =\n%s\nwant it unchanged", raw)
	}
}

func TestPlantUMLSetRefusesARevisionItCannotIncrement(t *testing.T) {
	file := seedReadPage(t,
		diagramMacro(t, ` ac:local-id="aaa"`, "first.svg", "@startuml\nAlice -> Bob\n@enduml", "v2"),
		classicMeta())

	source := filepath.Join(t.TempDir(), "diagram.puml")
	if err := os.WriteFile(source, []byte("@startuml\n@enduml"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := runCflio(t, "plantuml", "set", "-f", file, "--id", "aaa", "--source", source)
	if err == nil {
		t.Fatal("plantuml set error = nil, want a failure")
	}
	if !strings.Contains(err.Error(), `revision="v2"`) {
		t.Errorf("error = %q, want it to name the unusable revision", err)
	}
}

// TestPlantUMLSetGuardsAMacroAStorageEditCannotReach covers the live-doc
// trap: without a local-id the change never reaches the rendered document,
// and the next editor save throws it away.
func TestPlantUMLSetGuardsAMacroAStorageEditCannotReach(t *testing.T) {
	unrecorded := currentMeta()

	for _, tt := range []struct {
		name    string
		meta    sidecar.Meta
		wantErr string
	}{
		{name: "live doc", meta: liveMeta(), wantErr: "live doc"},
		{name: "subtype not recorded", meta: unrecorded, wantErr: "cflio read"},
		{name: "classic page", meta: classicMeta()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			file := seedDiagramPage(t, tt.meta,
				diagramMacro(t, "", "only.svg", "@startuml\nactor User\n@enduml", "1"))

			const edited = "@startuml\nactor Admin\n@enduml"
			source := filepath.Join(t.TempDir(), "diagram.puml")
			if err := os.WriteFile(source, []byte(edited), 0o644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			_, err := runCflio(t, "plantuml", "set", "-f", file, "--name", "only.svg", "--source", source)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("plantuml set error = %v, want it to succeed", err)
				}
				return
			}
			if err == nil {
				t.Fatal("plantuml set error = nil, want a failure")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
			}

			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			if strings.Contains(string(raw), mustEncode(t, edited)) {
				t.Error("set wrote the new payload despite refusing")
			}
		})
	}
}

// TestPlantUMLSetUpdatesAMacroWithALocalIDOnALiveDoc pins the other half of
// the guard: the local-id is what the live doc's editor matches on, so a
// macro that has one is edited like any other.
func TestPlantUMLSetUpdatesAMacroWithALocalIDOnALiveDoc(t *testing.T) {
	file := seedDiagramPage(t, liveMeta(),
		diagramMacro(t, ` ac:local-id="aaa"`, "only.svg", "@startuml\nactor User\n@enduml", "1"))

	const edited = "@startuml\nactor Admin\n@enduml"
	source := filepath.Join(t.TempDir(), "diagram.puml")
	if err := os.WriteFile(source, []byte(edited), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := runCflio(t, "plantuml", "set", "-f", file, "--id", "aaa", "--source", source); err != nil {
		t.Fatalf("plantuml set error = %v", err)
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(raw), mustEncode(t, edited)) {
		t.Error("set did not write the new payload")
	}
}

func mustEncode(t *testing.T, source string) string {
	t.Helper()

	data, err := plantuml.Encode(source)
	if err != nil {
		t.Fatalf("plantuml.Encode() error = %v", err)
	}
	return data
}

// TestPlantUMLRefusesAFileWithNoSidecar pins that every subcommand sends a
// --markdown file back the way `update` does, rather than each inventing its
// own answer.
func TestPlantUMLRefusesAFileWithNoSidecar(t *testing.T) {
	file := filepath.Join(t.TempDir(), "page.md")
	if err := os.WriteFile(file, []byte("# a page\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	source := filepath.Join(t.TempDir(), "diagram.puml")
	if err := os.WriteFile(source, []byte("@startuml\n@enduml"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	for _, args := range [][]string{
		{"plantuml", "list", "-f", file},
		{"plantuml", "get", "-f", file, "--id", "aaa"},
		{"plantuml", "set", "-f", file, "--id", "aaa", "--source", source},
		{"plantuml", "add", "-f", file, "--source", source},
	} {
		t.Run(args[1], func(t *testing.T) {
			_, err := runCflio(t, args...)
			if err == nil {
				t.Fatal("error = nil, want a failure")
			}
			if !strings.Contains(err.Error(), "--markdown") {
				t.Errorf("error = %q, want the guidance `update` gives", err)
			}
		})
	}
}

func TestPlantUMLReportsAFileWithNoDiagramToSelectFrom(t *testing.T) {
	file := seedReadPage(t, "<p>no macros here</p>", classicMeta())

	_, err := runCflio(t, "plantuml", "get", "-f", file, "--id", "aaa")
	if err == nil {
		t.Fatal("plantuml get error = nil, want a failure")
	}
	if want := fmt.Sprintf("%s holds no plantumlcloud macro", file); !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to say %q", err, want)
	}
}

// TestPlantUMLRefusesAnEmptySelector covers the value an agent can reach for
// by accident: `list --format json` reports an empty local_id for a macro that
// has none, and feeding that straight back to --id used to fall through to
// matching on an empty --name -- which matches every macro with no filename.
func TestPlantUMLRefusesAnEmptySelector(t *testing.T) {
	file := seedDiagramPage(t, classicMeta(),
		// No filename, so an empty --name would match it, and a local-id, so
		// selecting it is a different answer from selecting by name.
		diagramMacro(t, ` ac:local-id="aaa"`, "", "@startuml\nactor User\n@enduml", "1"))

	source := filepath.Join(t.TempDir(), "diagram.puml")
	if err := os.WriteFile(source, []byte("@startuml\n@enduml"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	for _, tt := range []struct {
		name     string
		selector []string
		want     string
	}{
		{name: "empty id", selector: []string{"--id", ""}, want: "--id cannot be empty"},
		{name: "empty name", selector: []string{"--name", ""}, want: "--name cannot be empty"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, verb := range []string{"get", "set"} {
				args := append([]string{"plantuml", verb, "-f", file}, tt.selector...)
				if verb == "set" {
					args = append(args, "--source", source)
				} else {
					args = append(args, "-o", filepath.Join(t.TempDir(), "out.puml"))
				}

				_, err := runCflio(t, args...)
				if err == nil {
					t.Errorf("plantuml %s error = nil, want a failure", verb)
					continue
				}
				if !strings.Contains(err.Error(), tt.want) {
					t.Errorf("plantuml %s error = %q, want it to mention %q", verb, err, tt.want)
				}
			}
		})
	}
}

// TestPlantUMLGetRefusesAFilenameThatIsNotAPlainName covers the default output
// name's guard: the filename comes from the page, so it is what could carry a
// path out of the working directory.
func TestPlantUMLGetRefusesAFilenameThatIsNotAPlainName(t *testing.T) {
	file := seedDiagramPage(t, classicMeta(),
		diagramMacro(t, ` ac:local-id="aaa"`, "../evil.svg", "@startuml\nactor User\n@enduml", "1"))

	dir := t.TempDir()
	t.Chdir(dir)

	// -o is left off, so the filename parameter is what would name the file.
	_, err := runCflio(t, "plantuml", "get", "-f", file, "--id", "aaa")
	if err == nil {
		t.Fatal("plantuml get error = nil, want a failure")
	}
	if !strings.Contains(err.Error(), "-o") {
		t.Errorf("error = %q, want it to point at -o", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "..", "evil.puml")); statErr == nil {
		t.Error("plantuml get wrote a file outside the working directory")
	}
}

// uuidShape is what a generated identifier has to look like: lower-case,
// hyphenated, with version 4 and the RFC variant nibble set. Confluence keeps
// the ids it is given, so a malformed one would live on the page.
var uuidShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// macroIDPattern reads the attribute format.Macros does not report. It is not
// a selector -- Confluence reissues it -- so nothing but these tests needs it.
var macroIDPattern = regexp.MustCompile(`ac:macro-id="([^"]*)"`)

// seedDiagramSource writes a diagram source file under name and returns its
// path. The name matters: it is what the default filename is derived from.
func seedDiagramSource(t *testing.T, name, source string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// splicedAt reports what `add` inserted at off, and fails the test if the file
// changed anywhere else -- which is the acceptance criterion that a byte diff
// shows nothing but the new element.
func splicedAt(t *testing.T, before, after string, off int) string {
	t.Helper()

	head, tail := before[:off], before[off:]
	if !strings.HasPrefix(after, head) || !strings.HasSuffix(after, tail) {
		t.Fatalf("the body around offset %d did not survive:\n before %q\n  after %q", off, before, after)
	}
	return after[off : len(after)-len(tail)]
}

// addedMacro runs `add` and returns the body's macro along with the element as
// it was written, having checked that the rest of the file is untouched.
func addedMacro(t *testing.T, file, before string, off int, args ...string) (format.Macro, string) {
	t.Helper()

	if _, err := runCflio(t, append([]string{"plantuml", "add", "-f", file}, args...)...); err != nil {
		t.Fatalf("plantuml add error = %v", err)
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	after := string(raw)
	element := splicedAt(t, before, after, off)

	macros := format.Macros(after)
	if len(macros) != 1 {
		t.Fatalf("the body holds %d macros, want 1: %s", len(macros), after)
	}
	return macros[0], element
}

func TestPlantUMLAddAppendsAMacroTheOtherSubcommandsCanRead(t *testing.T) {
	const source = "@startuml\nAlice -> Bob: hi\n@enduml\n"
	const before = "<p>before</p>"
	file := seedReadPage(t, before, classicMeta())

	macro, element := addedMacro(t, file, before, len(before),
		"--source", seedDiagramSource(t, "d.puml", source))

	if macro.Name != "plantumlcloud" {
		t.Errorf("macro name = %q, want %q", macro.Name, "plantumlcloud")
	}
	for _, tt := range []struct{ name, want string }{
		{name: "toolbar", want: "bottom"},
		{name: "filename", want: "d.svg"},
		{name: "compressed", want: "true"},
		{name: "revision", want: "1"},
	} {
		p, ok := macro.Param(tt.name)
		if !ok {
			t.Errorf("the macro has no %s parameter: %s", tt.name, element)
			continue
		}
		if p.Value != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, p.Value, tt.want)
		}
	}

	// The payload is read back with the decoder the rest of cflio reads a
	// page with, so what `add` writes is a diagram this tool can edit.
	data, _ := macro.Param("data")
	got, err := plantuml.Decode(data.Value)
	if err != nil {
		t.Fatalf("plantuml.Decode() error = %v", err)
	}
	if got != source {
		t.Errorf("decoded source = %q, want %q", got, source)
	}

	macroID := macroIDPattern.FindStringSubmatch(element)
	if macroID == nil {
		t.Fatalf("the element carries no ac:macro-id: %s", element)
	}
	if !uuidShape.MatchString(macro.LocalID) {
		t.Errorf("ac:local-id = %q, want a version-4 UUID", macro.LocalID)
	}
	if !uuidShape.MatchString(macroID[1]) {
		t.Errorf("ac:macro-id = %q, want a version-4 UUID", macroID[1])
	}
	if macro.LocalID == macroID[1] {
		t.Errorf("ac:local-id and ac:macro-id are both %q, want two distinct ids", macro.LocalID)
	}
	// The editor writes the element without whitespace between its children,
	// and `update` sends the file back byte for byte.
	if strings.ContainsAny(element, "\n\t") {
		t.Errorf("the element carries whitespace the editor does not write: %s", element)
	}
}

func TestPlantUMLAddPutsTheMacroAfterTheAnchor(t *testing.T) {
	for _, tt := range []struct {
		name   string
		anchor string
		// before is the body, and cut the text the macro has to land past.
		before, cut string
	}{
		{
			// Confluence stamps local-id on block elements...
			name:   "a block element",
			anchor: "para",
			before: `<p local-id="para">one</p><p>two</p>`,
			cut:    `<p local-id="para">one</p>`,
		},
		{
			// ...and ac:local-id on macros. Either is a usable anchor.
			name:   "a macro",
			anchor: "info",
			before: `<ac:structured-macro ac:name="info" ac:local-id="info"/><p>two</p>`,
			cut:    `<ac:structured-macro ac:name="info" ac:local-id="info"/>`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			file := seedReadPage(t, tt.before, classicMeta())

			if _, err := runCflio(t, "plantuml", "add", "-f", file,
				"--source", seedDiagramSource(t, "d.puml", "@startuml\n@enduml"),
				"--after", tt.anchor); err != nil {
				t.Fatalf("plantuml add error = %v", err)
			}

			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			element := splicedAt(t, tt.before, string(raw), len(tt.cut))
			if !strings.HasPrefix(element, `<ac:structured-macro ac:name="plantumlcloud"`) {
				t.Errorf("what landed after the anchor is not the diagram: %s", element)
			}
		})
	}
}

func TestPlantUMLAddNamesTheDiagramFile(t *testing.T) {
	for _, tt := range []struct {
		name     string
		source   string
		filename string
		want     string
	}{
		{name: "from the source's name", source: "d.puml", want: "d.svg"},
		{name: "from a source in a directory", source: "sub/diagram.puml", want: "diagram.svg"},
		{name: "from a source with no extension", source: "sketch", want: "sketch.svg"},
		{name: "from the flag", source: "d.puml", filename: "custom.svg", want: "custom.svg"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const before = "<p>before</p>"
			file := seedReadPage(t, before, classicMeta())

			args := []string{"--source", seedDiagramSource(t, tt.source, "@startuml\n@enduml")}
			if tt.filename != "" {
				args = append(args, "--filename", tt.filename)
			}
			macro, _ := addedMacro(t, file, before, len(before), args...)

			if got, _ := macro.Param("filename"); got.Value != tt.want {
				t.Errorf("filename = %q, want %q", got.Value, tt.want)
			}
		})
	}
}

// TestPlantUMLAddRefusesWhatItCannotResolve covers every way `add` can fail
// after the file is open: in each the page has to be left exactly as it was,
// since a half-applied insertion is not something a re-run can reason about.
func TestPlantUMLAddRefusesWhatItCannotResolve(t *testing.T) {
	source := seedDiagramSource(t, "d.puml", "@startuml\n@enduml")

	for _, tt := range []struct {
		name   string
		before string
		args   []string
		want   string
	}{
		{
			name:   "an anchor no element carries",
			before: `<p local-id="para">one</p>`,
			args:   []string{"--source", source, "--after", "nowhere"},
			want:   "--after",
		},
		{
			// Two elements with one id is not a page Confluence writes, but
			// inserting after the first of them would put the diagram
			// somewhere the caller never named.
			name:   "an anchor two elements carry",
			before: `<p local-id="dup">one</p><p local-id="dup">two</p>`,
			args:   []string{"--source", source, "--after", "dup"},
			want:   "matches 2 elements",
		},
		{
			// An empty value reads as "the flag was not given", which for
			// --after is a different insertion point entirely.
			name:   "an empty anchor",
			before: `<p local-id="para">one</p>`,
			args:   []string{"--source", source, "--after", ""},
			want:   "--after cannot be empty",
		},
		{
			name:   "an empty filename",
			before: `<p>one</p>`,
			args:   []string{"--source", source, "--filename", ""},
			want:   "--filename cannot be empty",
		},
		{
			// `plantuml get` derives its default output path from this
			// parameter, so a value carrying a path would be written here and
			// refused there.
			name:   "a filename that is not a plain name",
			before: `<p>one</p>`,
			args:   []string{"--source", source, "--filename", "../evil.svg"},
			want:   "--filename",
		},
		{
			// A source that is nothing but an extension leaves no stem to
			// build a filename out of.
			name:   "a source with no name to derive from",
			before: `<p>one</p>`,
			args:   []string{"--source", seedDiagramSource(t, ".puml", "@startuml\n@enduml")},
			want:   "--filename",
		},
		{
			name:   "a source that is not there",
			before: `<p>one</p>`,
			args:   []string{"--source", filepath.Join(t.TempDir(), "missing.puml")},
			want:   "missing.puml",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			file := seedReadPage(t, tt.before, classicMeta())

			_, err := runCflio(t, append([]string{"plantuml", "add", "-f", file}, tt.args...)...)
			if err == nil {
				t.Fatal("plantuml add error = nil, want a failure")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}

			raw, readErr := os.ReadFile(file)
			if readErr != nil {
				t.Fatalf("ReadFile() error = %v", readErr)
			}
			if string(raw) != tt.before {
				t.Errorf("the body was rewritten by a failed add: %q", raw)
			}
		})
	}
}

// TestPlantUMLAddReportsAnIDTheOtherSubcommandsAccept closes the loop the
// command exists for: what `add` prints has to be what `set` is then called
// with, without reading the page body.
func TestPlantUMLAddReportsAnIDTheOtherSubcommandsAccept(t *testing.T) {
	file := seedReadPage(t, "<p>before</p>", classicMeta())

	run, err := runCflio(t, "plantuml", "add", "-f", file, "--format", "json",
		"--source", seedDiagramSource(t, "d.puml", "@startuml\nactor User\n@enduml"))
	if err != nil {
		t.Fatalf("plantuml add error = %v", err)
	}

	var added struct {
		LocalID  string `json:"local_id"`
		Filename string `json:"filename"`
		Path     string `json:"path"`
		Bytes    int    `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &added); err != nil {
		t.Fatalf("Unmarshal() error = %v; output was %s", err, run.stdout)
	}
	if added.Filename != "d.svg" || added.Path != file || added.Bytes != len("@startuml\nactor User\n@enduml") {
		t.Errorf("result = %+v, want d.svg / %s / 27 bytes", added, file)
	}

	// The reported id has to select the new macro, not merely resemble one.
	replacement := seedDiagramSource(t, "next.puml", "@startuml\nactor Admin\n@enduml")
	if _, err := runCflio(t, "plantuml", "set", "-f", file,
		"--id", added.LocalID, "--source", replacement); err != nil {
		t.Fatalf("plantuml set error = %v", err)
	}

	out := filepath.Join(t.TempDir(), "roundtrip.puml")
	if _, err := runCflio(t, "plantuml", "get", "-f", file, "--id", added.LocalID, "-o", out); err != nil {
		t.Fatalf("plantuml get error = %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if want := "@startuml\nactor Admin\n@enduml"; string(got) != want {
		t.Errorf("round-tripped source = %q, want %q", got, want)
	}
}

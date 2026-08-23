package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// TestPlantUMLRefusesAFileWithNoSidecar pins that all three subcommands send a
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

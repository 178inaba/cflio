package cmd

import (
	"bytes"
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/178inaba/atlio/internal/jira"
	"github.com/178inaba/atlio/internal/sidecar"
)

// The ADF fields of the recorded issue, spelled with irregular whitespace so
// that a file holding a re-encoding of them, rather than these bytes, fails.
const (
	testDescriptionADF = `{"type": "doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Steps to reproduce"}]}]}`
	testEnvironmentADF = `{"type":"doc", "version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"macOS 15"}]}]}`
	testCustomADF      = `{"type":"doc","version":1,"content":[{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"Acceptance one"}]}]}]}]}`
)

// testIssueFields is a recorded issue's fields: the three ADF fields above, a
// non-ADF custom field, and every metadata field the command prints.
const testIssueFields = `{
	"summary":"Login fails",
	"issuetype":{"name":"Bug"},
	"status":{"name":"In Progress"},
	"priority":{"name":"High"},
	"assignee":{"displayName":"Ada Lovelace"},
	"reporter":{"displayName":"Grace Hopper"},
	"labels":["auth","web"],
	"parent":{"key":"KAN-0","fields":{"summary":"Auth epic"}},
	"subtasks":[{"key":"KAN-2","fields":{"summary":"Write test","status":{"name":"To Do"}}}],
	"issuelinks":[
		{"type":{"inward":"is blocked by","outward":"blocks"},"outwardIssue":{"key":"KAN-3","fields":{"summary":"Release","status":{"name":"Open"}}}},
		{"type":{"inward":"is blocked by","outward":"blocks"},"inwardIssue":{"key":"KAN-4","fields":{"summary":"Infra","status":{"name":"Done"}}}}
	],
	"created":"2025-01-02T03:04:05.000+0000",
	"updated":"2025-01-03T03:04:05.000+0000",
	"attachment":[{"filename":"screen.png","mimeType":"image/png","size":1234}],
	"customfield_10020":{"value":"Not rich text"},
	"customfield_10030":` + testCustomADF + `,
	"environment":` + testEnvironmentADF + `,
	"description":` + testDescriptionADF + `
}`

const testIssueNames = `{"description":"Description","environment":"Environment","customfield_10030":"Acceptance criteria","customfield_10020":"Team"}`

// minimalIssueFields is an issue with no rich-text field and nothing optional
// set.
const minimalIssueFields = `{
	"summary":"Bare","issuetype":{"name":"Task"},"status":{"name":"To Do"},"priority":null,
	"assignee":null,"reporter":null,"labels":[],"subtasks":[],"issuelinks":[],"attachment":[],
	"description":null,"environment":null,
	"created":"2025-01-02T03:04:05.000+0000","updated":"2025-01-02T03:04:05.000+0000"
}`

func issueResponse(key, fields, names string) string {
	return `{"id":"10001","key":"` + key + `","fields":` + fields + `,"names":` + names + `}`
}

// serveIssue answers every request with body, failing the test on anything
// but a GET: nothing in this release changes anything in Jira.
func serveIssue(t *testing.T, body string) *[]string {
	t.Helper()

	var paths []string
	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("request method = %s, want GET", r.Method)
		}
		paths = append(paths, r.URL.Path)
		_, _ = io.WriteString(w, body)
	})
	return &paths
}

func TestJiraIssueReadAcceptsEveryIssueReference(t *testing.T) {
	const otherSite = "https://other.atlassian.net"

	tests := []struct {
		name     string
		arg      string
		wantSite string
		wantKey  string
	}{
		{name: "key", arg: "KAN-1", wantSite: testSite},
		{name: "lowercase key passed through", arg: "kan-1", wantSite: testSite, wantKey: "kan-1"},
		{name: "browse url", arg: otherSite + "/browse/KAN-1", wantSite: otherSite},
		{name: "board with a selected issue", arg: otherSite + "/jira/software/projects/KAN/boards/1?selectedIssue=KAN-1", wantSite: otherSite},
		{name: "software issue view", arg: otherSite + "/jira/software/projects/KAN/issues/KAN-1", wantSite: otherSite},
		{name: "core issue view", arg: otherSite + "/jira/core/projects/KAN/issues/KAN-1", wantSite: otherSite},
		{name: "service desk queue", arg: otherSite + "/jira/servicedesk/projects/KAN/queues/custom/1/KAN-1", wantSite: otherSite},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)
			seedProfile(t, "other", otherSite)
			paths := serveIssue(t, issueResponse("KAN-1", minimalIssueFields, "{}"))

			var gotSite string
			served := jiraFactory
			jiraFactory = func(site, email, token string) (*jira.Client, error) {
				gotSite = site
				return served(site, email, token)
			}
			t.Cleanup(func() { jiraFactory = served })

			if _, err := runCLI(t, "jira", "issue", "read", tt.arg); err != nil {
				t.Fatalf("jira issue read error = %v", err)
			}

			wantPath := "/rest/api/3/issue/" + cmp.Or(tt.wantKey, "KAN-1")
			if !slices.Equal(*paths, []string{wantPath}) {
				t.Errorf("requests = %v, want one GET %s", *paths, wantPath)
			}
			if gotSite != tt.wantSite {
				t.Errorf("profile site = %q, want %q", gotSite, tt.wantSite)
			}
		})
	}
}

func TestJiraIssueReadRejectsWhatNamesNoIssue(t *testing.T) {
	tests := []struct {
		arg     string
		wantErr string
	}{
		{arg: "https://example.atlassian.net/jira/software/projects/KAN/boards/1", wantErr: "is a Jira URL that names no issue"},
		{arg: "https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Title", wantErr: "is not an issue URL or issue key"},
	}

	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)
			startAPI(t, neverCalled(t, "for an argument that names no issue"))

			_, err := runCLI(t, "jira", "issue", "read", tt.arg)
			if err == nil {
				t.Fatalf("jira issue read %q error = nil, want an error", tt.arg)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestJiraIssueReadWritesTheADFFieldsVerbatim(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	serveIssue(t, issueResponse("KAN-1", testIssueFields, testIssueNames))
	t.Chdir(t.TempDir())

	run, err := runCLI(t, "jira", "issue", "read", "KAN-1")
	if err != nil {
		t.Fatalf("jira issue read error = %v", err)
	}

	written, err := os.ReadFile("KAN-1.adf.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	ids, values := readObjectMembers(t, written)
	if want := []string{"description", "environment", "customfield_10030"}; !slices.Equal(ids, want) {
		t.Errorf("file keys = %v, want %v", ids, want)
	}
	for i, want := range []string{testDescriptionADF, testEnvironmentADF, testCustomADF} {
		if i < len(values) && values[i] != want {
			t.Errorf("value of %s =\n%s\nwant the API's bytes\n%s", ids[i], values[i], want)
		}
	}
	if !bytes.HasSuffix(written, []byte("}\n")) {
		t.Errorf("file = %q, want it to end with a newline", written)
	}

	for _, text := range []string{"Steps to reproduce", "macOS 15", "Acceptance one"} {
		if strings.Contains(run.stdout, text) {
			t.Errorf("output = %q, want the fields' text kept off stdout", run.stdout)
		}
	}
	if !strings.Contains(run.stdout, "Body:     KAN-1.adf.json (") {
		t.Errorf("output = %q, want it to name the file written", run.stdout)
	}
	if strings.Contains(run.stdout, "Degraded") {
		t.Errorf("output = %q, want no Degraded line outside --markdown", run.stdout)
	}
	if _, err := os.Stat("KAN-1.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(KAN-1.md) error = %v, want no Markdown file in the default mode", err)
	}
}

// readObjectMembers reads a JSON object's member names and raw values in the
// order the file has them, which a decode into a map would lose.
func readObjectMembers(t *testing.T, data []byte) ([]string, []string) {
	t.Helper()

	dec := jsontext.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.ReadToken(); err != nil || tok.Kind() != '{' {
		t.Fatalf("file does not open with an object: %v", err)
	}
	var ids, values []string
	for dec.PeekKind() != '}' {
		name, err := dec.ReadToken()
		if err != nil {
			t.Fatalf("ReadToken() error = %v", err)
		}
		// A Token is only valid until the next decoder call.
		ids = append(ids, name.String())
		value, err := dec.ReadValue()
		if err != nil {
			t.Fatalf("ReadValue() error = %v", err)
		}
		values = append(values, string(value))
	}
	return ids, values
}

func TestJiraIssueReadMarkdownWritesOneSectionPerField(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	serveIssue(t, issueResponse("KAN-1", testIssueFields, testIssueNames))
	t.Chdir(t.TempDir())

	if _, err := runCLI(t, "jira", "issue", "read", "--markdown", "KAN-1"); err != nil {
		t.Fatalf("jira issue read --markdown error = %v", err)
	}

	written, err := os.ReadFile("KAN-1.md")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	want := "Steps to reproduce\n\n## Environment\n\nmacOS 15\n\n## Acceptance criteria\n\n- Acceptance one\n"
	if string(written) != want {
		t.Errorf("file =\n%s\nwant\n%s", written, want)
	}
	if _, err := os.Stat("KAN-1.adf.json"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(KAN-1.adf.json) error = %v, want no ADF file with --markdown", err)
	}
}

func TestJiraIssueReadMarkdownReportsWhatDegraded(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	description := `{"type":"doc","version":1,"content":[
		{"type":"mediaSingle","content":[{"type":"media","attrs":{"id":"m","type":"file","collection":"c"}}]}]}`
	environment := `{"type":"doc","version":1,"content":[{"type":"extension","attrs":{"extensionKey":"k"}},
		{"type":"mediaGroup","content":[{"type":"media","attrs":{"id":"n","type":"file","collection":"c"}}]}]}`
	serveIssue(t, issueResponse("KAN-1",
		`{"summary":"S","description":`+description+`,"environment":`+environment+`}`, "{}"))
	dir := t.TempDir()

	run, err := runCLI(t, "jira", "issue", "read", "--markdown", "-o", filepath.Join(dir, "a.md"), "KAN-1")
	if err != nil {
		t.Fatalf("jira issue read --markdown error = %v", err)
	}
	if want := "Degraded: 3 (extension, mediaGroup, mediaSingle)\n"; !strings.Contains(run.stdout, want) {
		t.Errorf("output = %q, want it to contain %q", run.stdout, want)
	}

	run, err = runCLI(t, "jira", "issue", "read", "--markdown", "--format", "json", "-o", filepath.Join(dir, "b.md"), "KAN-1")
	if err != nil {
		t.Fatalf("jira issue read --markdown --format json error = %v", err)
	}
	var got struct {
		Unsupported      []string `json:"unsupported"`
		UnsupportedCount int      `json:"unsupported_count"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &got); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", run.stdout, err)
	}
	if got.UnsupportedCount != 3 || !slices.Equal(got.Unsupported, []string{"extension", "mediaGroup", "mediaSingle"}) {
		t.Errorf("unsupported = %v (count %d), want the three summed over both fields", got.Unsupported, got.UnsupportedCount)
	}
}

func TestJiraIssueReadPrintsTheMetadata(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	serveIssue(t, issueResponse("KAN-1", testIssueFields, testIssueNames))
	path := filepath.Join(t.TempDir(), "issue.json")

	run, err := runCLI(t, "jira", "issue", "read", "-o", path, "KAN-1")
	if err != nil {
		t.Fatalf("jira issue read error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}

	want := "Key:      KAN-1\n" +
		"Summary:  Login fails\n" +
		"Type:     Bug\n" +
		"Status:   In Progress\n" +
		"Priority: High\n" +
		"Assignee: Ada Lovelace\n" +
		"Reporter: Grace Hopper\n" +
		"Labels:   auth, web\n" +
		"Parent:   KAN-0 (Auth epic)\n" +
		"Subtask:  KAN-2 [To Do] Write test\n" +
		"Link:     blocks KAN-3 [Open] Release\n" +
		"Link:     is blocked by KAN-4 [Done] Infra\n" +
		"Created:  2025-01-02T03:04:05.000+0000\n" +
		"Updated:  2025-01-03T03:04:05.000+0000\n" +
		"Attachment: screen.png (image/png, 1234 bytes)\n" +
		"URL:      https://example.atlassian.net/browse/KAN-1\n" +
		"Body:     " + path + " (" + strconv.FormatInt(info.Size(), 10) + " bytes)\n"
	if run.stdout != want {
		t.Errorf("output =\n%s\nwant\n%s", run.stdout, want)
	}
}

func TestJiraIssueReadJSONCarriesEveryKey(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	serveIssue(t, issueResponse("KAN-1", testIssueFields, testIssueNames))
	path := filepath.Join(t.TempDir(), "issue.json")

	run, err := runCLI(t, "jira", "issue", "read", "--format", "json", "-o", path, "KAN-1")
	if err != nil {
		t.Fatalf("jira issue read error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(run.stdout), &got); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", run.stdout, err)
	}
	wantKeys := []string{
		"assignee", "attachments", "body_path", "bytes", "created_at", "key", "labels", "links",
		"parent", "priority", "reporter", "status", "subtasks", "summary", "type", "updated_at", "url",
	}
	if keys := slices.Sorted(maps.Keys(got)); !slices.Equal(keys, wantKeys) {
		t.Errorf("JSON keys = %v, want %v", keys, wantKeys)
	}

	// The nested values only: jiraIssueResult itself does not decode, since
	// v2 cannot allocate its embedded pointer to an unexported type.
	var typed struct {
		Parent      *issueSummary    `json:"parent"`
		Subtasks    []issueStatus    `json:"subtasks"`
		Links       []issueLink      `json:"links"`
		Attachments []attachmentItem `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &typed); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if want := []issueLink{
		{Relation: "blocks", Key: "KAN-3", Summary: "Release", Status: "Open"},
		{Relation: "is blocked by", Key: "KAN-4", Summary: "Infra", Status: "Done"},
	}; !slices.Equal(typed.Links, want) {
		t.Errorf("links = %+v, want %+v", typed.Links, want)
	}
	if want := []attachmentItem{{Filename: "screen.png", MediaType: "image/png", FileSize: 1234}}; !slices.Equal(typed.Attachments, want) {
		t.Errorf("attachments = %+v, want %+v", typed.Attachments, want)
	}
	if want := []issueStatus{{Key: "KAN-2", Summary: "Write test", Status: "To Do"}}; !slices.Equal(typed.Subtasks, want) {
		t.Errorf("subtasks = %+v, want %+v", typed.Subtasks, want)
	}
	if typed.Parent == nil || *typed.Parent != (issueSummary{Key: "KAN-0", Summary: "Auth epic"}) {
		t.Errorf("parent = %+v", typed.Parent)
	}
}

// TestJiraIssueReadWritesNothingWithoutRichText covers an issue whose every
// optional field is unset: no file in either mode, and the keys that describe
// one omitted along with every other key requirement 14 omits when empty.
func TestJiraIssueReadWritesNothingWithoutRichText(t *testing.T) {
	for _, mode := range [][]string{nil, {"--markdown"}} {
		t.Run(strings.Join(append([]string{"default"}, mode...), " "), func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)
			serveIssue(t, issueResponse("KAN-1", minimalIssueFields, "{}"))
			dir := t.TempDir()
			t.Chdir(dir)

			args := append([]string{"jira", "issue", "read", "--format", "json"}, mode...)
			run, err := runCLI(t, append(args, "KAN-1")...)
			if err != nil {
				t.Fatalf("jira issue read error = %v", err)
			}

			var got map[string]any
			if err := json.Unmarshal([]byte(run.stdout), &got); err != nil {
				t.Fatalf("Unmarshal(%q) error = %v", run.stdout, err)
			}
			wantKeys := []string{"created_at", "key", "priority", "status", "summary", "type", "updated_at", "url"}
			if keys := slices.Sorted(maps.Keys(got)); !slices.Equal(keys, wantKeys) {
				t.Errorf("JSON keys = %v, want %v", keys, wantKeys)
			}
			if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
				t.Errorf("directory holds %v (error %v), want no file written", entries, err)
			}

			run, err = runCLI(t, append(append([]string{"jira", "issue", "read"}, mode...), "KAN-1")...)
			if err != nil {
				t.Fatalf("jira issue read error = %v", err)
			}
			if strings.Contains(run.stdout, "Body:") {
				t.Errorf("output = %q, want no Body line", run.stdout)
			}
		})
	}
}

func TestJiraIssueReadReplacesTheOutputFile(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	serveIssue(t, issueResponse("KAN-1", testIssueFields, testIssueNames))
	path := filepath.Join(t.TempDir(), "out.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("stale ", 100)), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := runCLI(t, "jira", "issue", "read", "-o", path, "--markdown", "KAN-1"); err != nil {
		t.Fatalf("jira issue read error = %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(written), "stale") || !strings.HasPrefix(string(written), "Steps to reproduce") {
		t.Errorf("file = %q, want only the issue's Markdown", written)
	}
}

// TestJiraIssueReadRemovesAStaleSidecar covers a destination a Confluence read
// used before: the sidecar it left would pair that page with the issue, and
// `confluence update` would then send the issue to the page.
func TestJiraIssueReadRemovesAStaleSidecar(t *testing.T) {
	for _, mode := range [][]string{nil, {"--markdown"}} {
		t.Run(strings.Join(append([]string{"default"}, mode...), " "), func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)
			serveIssue(t, issueResponse("KAN-1", testIssueFields, testIssueNames))
			path := filepath.Join(t.TempDir(), "123456.xml")
			if err := sidecar.Write(path, sidecar.Meta{PageID: "123456", Version: 1}); err != nil {
				t.Fatalf("sidecar.Write() error = %v", err)
			}

			args := append([]string{"jira", "issue", "read", "-o", path}, mode...)
			if _, err := runCLI(t, append(args, "KAN-1")...); err != nil {
				t.Fatalf("jira issue read error = %v", err)
			}
			if _, err := os.Stat(sidecar.Path(path)); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Stat(sidecar) error = %v, want the stale sidecar removed", err)
			}
		})
	}
}

func TestJiraIssueReadUsesTheKeyJiraReturned(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	paths := serveIssue(t, issueResponse("NEW-7", testIssueFields, testIssueNames))
	t.Chdir(t.TempDir())

	run, err := runCLI(t, "jira", "issue", "read", "OLD-1")
	if err != nil {
		t.Fatalf("jira issue read error = %v", err)
	}
	if want := []string{"/rest/api/3/issue/OLD-1"}; !slices.Equal(*paths, want) {
		t.Errorf("requests = %v, want %v", *paths, want)
	}
	if _, err := os.Stat("NEW-7.adf.json"); err != nil {
		t.Errorf("Stat(NEW-7.adf.json) error = %v, want the file named after the returned key", err)
	}
	for _, want := range []string{"Key:      NEW-7\n", "URL:      https://example.atlassian.net/browse/NEW-7\n"} {
		if !strings.Contains(run.stdout, want) {
			t.Errorf("output = %q, want it to contain %q", run.stdout, want)
		}
	}
}

func TestJiraIssueReadReportsJiraErrors(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	startAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"errorMessages":["Issue does not exist or you do not have permission to see it."],"errors":{}}`)
	})
	t.Chdir(t.TempDir())

	_, err := runCLI(t, "jira", "issue", "read", "KAN-404")
	if err == nil {
		t.Fatal("jira issue read error = nil, want an error")
	}
	if want := "Issue does not exist or you do not have permission to see it."; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
	if code, _ := describeFailure(err, defaultTimeout); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if _, err := os.Stat("KAN-404.adf.json"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat() error = %v, want no file for a failed read", err)
	}
}

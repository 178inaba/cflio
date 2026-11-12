package issueref

import (
	"strings"
	"testing"
)

func TestParseAcceptsKeysAndIssueURLs(t *testing.T) {
	const host = "example.atlassian.net"

	tests := []struct {
		name string
		arg  string
		want Ref
	}{
		{name: "bare key", arg: "KAN-1", want: Ref{Key: "KAN-1"}},
		{name: "key with digits and underscores in the project", arg: "AB_2C-1234", want: Ref{Key: "AB_2C-1234"}},
		{name: "lowercase key passed through as typed", arg: "kan-1", want: Ref{Key: "kan-1"}},
		{name: "surrounding whitespace", arg: "  KAN-1\n", want: Ref{Key: "KAN-1"}},
		{name: "browse url", arg: "https://example.atlassian.net/browse/KAN-1", want: Ref{Key: "KAN-1", Host: host}},
		{name: "browse url with trailing slash", arg: "https://example.atlassian.net/browse/KAN-1/", want: Ref{Key: "KAN-1", Host: host}},
		{
			name: "browse url with other query parameters",
			arg:  "https://example.atlassian.net/browse/KAN-1?focusedCommentId=10000&atlOrigin=abc",
			want: Ref{Key: "KAN-1", Host: host},
		},
		{
			name: "board with a selected issue",
			arg:  "https://example.atlassian.net/jira/software/projects/KAN/boards/1?selectedIssue=KAN-7",
			want: Ref{Key: "KAN-7", Host: host},
		},
		{
			name: "backlog with a selected issue and other parameters",
			arg:  "https://example.atlassian.net/jira/software/projects/KAN/boards/1/backlog?epics=visible&selectedIssue=KAN-8",
			want: Ref{Key: "KAN-8", Host: host},
		},
		{
			name: "list view with a selected issue",
			arg:  "https://example.atlassian.net/jira/core/projects/KAN/list?selectedIssue=KAN-9",
			want: Ref{Key: "KAN-9", Host: host},
		},
		{
			name: "software issue view",
			arg:  "https://example.atlassian.net/jira/software/projects/KAN/issues/KAN-1",
			want: Ref{Key: "KAN-1", Host: host},
		},
		{
			name: "core issue view with a jql query",
			arg:  "https://example.atlassian.net/jira/core/projects/KAN/issues/KAN-2?jql=project%20%3D%20KAN",
			want: Ref{Key: "KAN-2", Host: host},
		},
		{
			name: "service desk queue",
			arg:  "https://example.atlassian.net/jira/servicedesk/projects/KAN/queues/custom/1/KAN-3",
			want: Ref{Key: "KAN-3", Host: host},
		},
		{
			name: "selected issue wins over the key in the path",
			arg:  "https://example.atlassian.net/jira/software/projects/KAN/issues/KAN-1?selectedIssue=KAN-4",
			want: Ref{Key: "KAN-4", Host: host},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.arg)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tt.arg, err)
			}
			if got != tt.want {
				t.Errorf("Parse(%q) = %+v, want %+v", tt.arg, got, tt.want)
			}
		})
	}
}

func TestParseRejectsWhatNamesNoIssue(t *testing.T) {
	tests := []struct {
		name    string
		arg     string
		wantErr string
	}{
		{
			name:    "board without a selected issue",
			arg:     "https://example.atlassian.net/jira/software/projects/KAN/boards/1",
			wantErr: "is a Jira URL that names no issue",
		},
		{
			name:    "browse url without a key",
			arg:     "https://example.atlassian.net/browse/KAN",
			wantErr: "is a Jira URL that names no issue",
		},
		{
			name:    "selected issue that is not a key",
			arg:     "https://example.atlassian.net/jira/software/projects/KAN/boards/1?selectedIssue=nope",
			wantErr: "is a Jira URL that names no issue",
		},
		{
			name:    "confluence page url",
			arg:     "https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Title",
			wantErr: "is not an issue URL or issue key",
		},
		{name: "page id", arg: "123456", wantErr: "is not an issue URL or issue key"},
		{name: "project key alone", arg: "KAN", wantErr: "is not an issue URL or issue key"},
		{name: "key starting with a digit", arg: "1KAN-1", wantErr: "is not an issue URL or issue key"},
		{name: "empty", arg: "", wantErr: "is not an issue URL or issue key"},
		{name: "relative path", arg: "/browse/KAN-1", wantErr: "is not an issue URL or issue key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.arg)
			if err == nil {
				t.Fatalf("Parse(%q) = %+v, want an error", tt.arg, got)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Parse(%q) error = %q, want it to contain %q", tt.arg, err, tt.wantErr)
			}
			// Both errors say what would have been accepted, so the caller
			// can correct the argument without opening the help.
			if !strings.Contains(err.Error(), "KAN-1") {
				t.Errorf("Parse(%q) error = %q, want it to show an accepted form", tt.arg, err)
			}
		})
	}
}

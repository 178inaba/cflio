package pageref

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want Ref
	}{
		{
			name: "bare page id",
			arg:  "123456",
			want: Ref{PageID: "123456"},
		},
		{
			name: "browser url with the title slug",
			arg:  "https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Some+Page+Title",
			want: Ref{PageID: "123456", Host: "example.atlassian.net"},
		},
		{
			name: "url without the title slug",
			arg:  "https://example.atlassian.net/wiki/spaces/DEV/pages/123456",
			want: Ref{PageID: "123456", Host: "example.atlassian.net"},
		},
		{
			name: "url with a trailing slash",
			arg:  "https://example.atlassian.net/wiki/spaces/DEV/pages/123456/",
			want: Ref{PageID: "123456", Host: "example.atlassian.net"},
		},
		{
			name: "url with a query string and fragment",
			arg:  "https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Title?focusedCommentId=9#heading",
			want: Ref{PageID: "123456", Host: "example.atlassian.net"},
		},
		{
			name: "personal space url",
			arg:  "https://example.atlassian.net/wiki/spaces/~1234abcd/pages/999/Notes",
			want: Ref{PageID: "999", Host: "example.atlassian.net"},
		},
		{
			name: "surrounding whitespace is tolerated",
			arg:  "  https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Title \n",
			want: Ref{PageID: "123456", Host: "example.atlassian.net"},
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

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name         string
		arg          string
		wantContains string
	}{
		{
			name:         "tiny link says so explicitly",
			arg:          "https://example.atlassian.net/wiki/x/AbCdEf",
			wantContains: "short link",
		},
		{
			name:         "empty argument",
			arg:          "",
			wantContains: "page URL or page ID",
		},
		{
			name:         "not a url and not an id",
			arg:          "Some Page Title",
			wantContains: "page URL or page ID",
		},
		{
			name:         "url without a page segment",
			arg:          "https://example.atlassian.net/wiki/spaces/DEV/overview",
			wantContains: "page URL or page ID",
		},
		{
			name:         "url whose page id is not numeric",
			arg:          "https://example.atlassian.net/wiki/spaces/DEV/pages/abc/Title",
			wantContains: "page URL or page ID",
		},
		{
			name:         "blog post url",
			arg:          "https://example.atlassian.net/wiki/spaces/DEV/blog/2026/01/01/123456/Post",
			wantContains: "page URL or page ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.arg)
			if err == nil {
				t.Fatalf("Parse(%q) error = nil, want an error", tt.arg)
			}
			if !strings.Contains(err.Error(), tt.wantContains) {
				t.Errorf("Parse(%q) error = %q, want it to contain %q", tt.arg, err, tt.wantContains)
			}
		})
	}
}

func TestPageURL(t *testing.T) {
	const site = "https://example.atlassian.net/wiki"

	tests := []struct {
		name   string
		site   string
		webui  string
		pageID string
		want   string
	}{
		{
			name:   "joins the site with the webui link",
			site:   site,
			webui:  "/spaces/DEV/pages/123456/Some+Page",
			pageID: "123456",
			want:   "https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Some+Page",
		},
		{
			name:   "tolerates a trailing slash on the site",
			site:   site + "/",
			webui:  "/spaces/DEV/pages/123456/Some+Page",
			pageID: "123456",
			want:   "https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Some+Page",
		},
		{
			name:   "falls back to the page-id form when webui is absent",
			site:   site,
			pageID: "123456",
			want:   "https://example.atlassian.net/wiki/pages/viewpage.action?pageId=123456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PageURL(tt.site, tt.webui, tt.pageID); got != tt.want {
				t.Errorf("PageURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSpacePageURL(t *testing.T) {
	got := SpacePageURL("https://example.atlassian.net/wiki", "DEV", "123456")
	want := "https://example.atlassian.net/wiki/spaces/DEV/pages/123456"
	if got != want {
		t.Errorf("SpacePageURL() = %q, want %q", got, want)
	}
}

func TestSpacePageURLRoundTripsThroughParse(t *testing.T) {
	// children prints these URLs, and an agent is expected to feed them
	// straight back into `cflio read`.
	url := SpacePageURL("https://example.atlassian.net/wiki", "DEV", "123456")

	ref, err := Parse(url)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", url, err)
	}
	if ref.PageID != "123456" || ref.Host != "example.atlassian.net" {
		t.Errorf("Parse(%q) = %+v, want page 123456 on example.atlassian.net", url, ref)
	}
}

func TestHostOf(t *testing.T) {
	tests := []struct {
		name    string
		pageURL string
		want    string
	}{
		{name: "page url", pageURL: "https://example.atlassian.net/wiki/spaces/DEV/pages/1/T", want: "example.atlassian.net"},
		{name: "empty", pageURL: "", want: ""},
		{name: "unparseable", pageURL: "://nope", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HostOf(tt.pageURL); got != tt.want {
				t.Errorf("HostOf(%q) = %q, want %q", tt.pageURL, got, tt.want)
			}
		})
	}
}

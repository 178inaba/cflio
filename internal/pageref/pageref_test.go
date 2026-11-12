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
			want: Ref{Target: PageID("123456")},
		},
		{
			name: "browser url with the title slug",
			arg:  "https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Some+Page+Title",
			want: Ref{Host: "example.atlassian.net", Target: PageID("123456")},
		},
		{
			name: "url without the title slug",
			arg:  "https://example.atlassian.net/wiki/spaces/DEV/pages/123456",
			want: Ref{Host: "example.atlassian.net", Target: PageID("123456")},
		},
		{
			name: "url with a trailing slash",
			arg:  "https://example.atlassian.net/wiki/spaces/DEV/pages/123456/",
			want: Ref{Host: "example.atlassian.net", Target: PageID("123456")},
		},
		{
			name: "url with a query string and fragment",
			arg:  "https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Title?focusedCommentId=9#heading",
			want: Ref{Host: "example.atlassian.net", Target: PageID("123456")},
		},
		{
			name: "personal space url",
			arg:  "https://example.atlassian.net/wiki/spaces/~1234abcd/pages/999/Notes",
			want: Ref{Host: "example.atlassian.net", Target: PageID("999")},
		},
		{
			// The form PageURL and ChildPageURL fall back to, so
			// `confluence children` output stays usable as input even without a
			// space key.
			name: "page-id query form",
			arg:  "https://example.atlassian.net/wiki/pages/viewpage.action?pageId=123456",
			want: Ref{Host: "example.atlassian.net", Target: PageID("123456")},
		},
		{
			name: "surrounding whitespace is tolerated",
			arg:  "  https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Title \n",
			want: Ref{Host: "example.atlassian.net", Target: PageID("123456")},
		},
		// The tokens below were encoded independently of the decoder under
		// test — a round-trip would only confirm it agrees with itself — and
		// none of them names a real page.
		{
			name: "short link",
			arg:  "https://example.atlassian.net/wiki/x/OT",
			want: Ref{Host: "example.atlassian.net", Target: PageID("12345")},
		},
		{
			name: "short link whose page id does not fit in 32 bits",
			arg:  "https://example.atlassian.net/wiki/x/ywT7cR8B",
			want: Ref{Host: "example.atlassian.net", Target: PageID("1234567890123")},
		},
		{
			name: "short link using both substituted characters",
			arg:  "https://example.atlassian.net/wiki/x/-_d2SBc",
			want: Ref{Host: "example.atlassian.net", Target: PageID("99999999999")},
		},
		{
			// An ID whose top byte is set leaves the trim nothing to take.
			name: "short link whose token is as long as they get",
			arg:  "https://example.atlassian.net/wiki/x/FYHpffQQIhE",
			want: Ref{Host: "example.atlassian.net", Target: PageID("1234567890123456789")},
		},
		{
			name: "short link with a trailing slash",
			arg:  "https://example.atlassian.net/wiki/x/QOIB/",
			want: Ref{Host: "example.atlassian.net", Target: PageID("123456")},
		},
		// A space's overview and the space URL both open its homepage.
		{
			name: "space overview url",
			arg:  "https://example.atlassian.net/wiki/spaces/DEV/overview",
			want: Ref{Host: "example.atlassian.net", Target: SpaceHomepage{Key: "DEV"}},
		},
		{
			name: "space overview url with a trailing slash",
			arg:  "https://example.atlassian.net/wiki/spaces/DEV/overview/",
			want: Ref{Host: "example.atlassian.net", Target: SpaceHomepage{Key: "DEV"}},
		},
		{
			name: "space url",
			arg:  "https://example.atlassian.net/wiki/spaces/DEV",
			want: Ref{Host: "example.atlassian.net", Target: SpaceHomepage{Key: "DEV"}},
		},
		{
			name: "space url with a trailing slash",
			arg:  "https://example.atlassian.net/wiki/spaces/DEV/",
			want: Ref{Host: "example.atlassian.net", Target: SpaceHomepage{Key: "DEV"}},
		},
		{
			name: "personal space overview url, percent-encoded",
			arg:  "https://example.atlassian.net/wiki/spaces/%7E557058abc/overview",
			want: Ref{Host: "example.atlassian.net", Target: SpaceHomepage{Key: "~557058abc"}},
		},
		{
			// The sidebar's space link carries the homepage's ID, which
			// saves the lookup.
			name: "space overview url carrying the homepage id",
			arg:  "https://example.atlassian.net/wiki/spaces/DEV/overview?homepageId=163938",
			want: Ref{Host: "example.atlassian.net", Target: PageID("163938")},
		},
		{
			name: "space url carrying the homepage id",
			arg:  "https://example.atlassian.net/wiki/spaces/DEV/?homepageId=163938",
			want: Ref{Host: "example.atlassian.net", Target: PageID("163938")},
		},
		{
			name: "space overview url with a non-numeric homepage id",
			arg:  "https://example.atlassian.net/wiki/spaces/DEV/overview?homepageId=abc",
			want: Ref{Host: "example.atlassian.net", Target: SpaceHomepage{Key: "DEV"}},
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
			// One character past the longest token TestParse decodes.
			name:         "short link whose token is too long",
			arg:          "https://example.atlassian.net/wiki/x/FYHpffQQIhEA",
			wantContains: "short link",
		},
		{
			name:         "short link whose token is outside the alphabet",
			arg:          "https://example.atlassian.net/wiki/x/Ab!Cd",
			wantContains: "short link",
		},
		{
			// base64 skips a newline rather than rejecting it, so this one
			// decodes short of a page ID's width.
			name:         "short link whose token carries an escaped newline",
			arg:          "https://example.atlassian.net/wiki/x/A%0AB",
			wantContains: "short link",
		},
		{
			name:         "short link whose token decodes to zero",
			arg:          "https://example.atlassian.net/wiki/x/AAAA",
			wantContains: "short link",
		},
		{
			name:         "short link with no token at all",
			arg:          "https://example.atlassian.net/wiki/x/",
			wantContains: "page URL or page ID",
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
			name:         "space pages list with no id",
			arg:          "https://example.atlassian.net/wiki/spaces/DEV/pages",
			wantContains: "page URL or page ID",
		},
		{
			name:         "spaces path with no key",
			arg:          "https://example.atlassian.net/wiki/spaces/",
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

// The refusal lists the forms Parse accepts, so it has to name the space
// homepage URL for a caller to learn that one works.
func TestParseErrorListsTheSpaceHomepageURL(t *testing.T) {
	_, err := Parse("https://example.atlassian.net/wiki/spaces/DEV/blog/2026/01/01/123456/Post")
	if err == nil {
		t.Fatal("Parse() error = nil, want an error")
	}
	if want := "/wiki/spaces/DEV/overview"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
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

func TestChildPageURL(t *testing.T) {
	const site = "https://example.atlassian.net/wiki"

	tests := []struct {
		name        string
		parentWebUI string
		want        string
	}{
		{
			name:        "parent link with the title slug",
			parentWebUI: "/spaces/DEV/pages/123456/Some+Page",
			want:        site + "/spaces/DEV/pages/11",
		},
		{
			name:        "parent link without the title slug",
			parentWebUI: "/spaces/DEV/pages/123456",
			want:        site + "/spaces/DEV/pages/11",
		},
		{
			name:        "personal space",
			parentWebUI: "/spaces/~557058:abc-def/pages/123456/Notes",
			want:        site + "/spaces/~557058:abc-def/pages/11",
		},
		// Every case below has to reach the page-id form rather than build
		// a URL around a missing key: /spaces//pages/<id> is a 404.
		{
			name: "parent has no link",
			want: site + "/pages/viewpage.action?pageId=11",
		},
		{
			name:        "parent link of some other shape",
			parentWebUI: "/pages/viewpage.action",
			want:        site + "/pages/viewpage.action?pageId=11",
		},
		{
			name:        "parent link carrying a query string",
			parentWebUI: "/spaces/DEV/pages/123456?x=1",
			want:        site + "/pages/viewpage.action?pageId=11",
		},
		{
			name:        "parent link with an empty key segment",
			parentWebUI: "/spaces//pages/123456",
			want:        site + "/pages/viewpage.action?pageId=11",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ChildPageURL(site, tt.parentWebUI, "11")
			if got != tt.want {
				t.Errorf("ChildPageURL(_, %q, _) = %q, want %q", tt.parentWebUI, got, tt.want)
			}

			// children prints these URLs, and an agent is expected to feed
			// them straight back into `atlio confluence read`.
			ref, err := Parse(got)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", got, err)
			}
			if want := (Ref{Host: "example.atlassian.net", Target: PageID("11")}); ref != want {
				t.Errorf("Parse(%q) = %+v, want page 11 on example.atlassian.net", got, ref)
			}
		})
	}
}

func TestChildPageURLToleratesATrailingSlashOnTheSite(t *testing.T) {
	got := ChildPageURL("https://example.atlassian.net/wiki/", "/spaces/DEV/pages/123456", "11")
	want := "https://example.atlassian.net/wiki/spaces/DEV/pages/11"
	if got != want {
		t.Errorf("ChildPageURL() = %q, want %q", got, want)
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

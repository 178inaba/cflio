// Package jira is a small client for the Jira Cloud platform REST API v3,
// covering exactly the calls atlio needs. Every call is a GET: atlio reads
// Jira and changes nothing in it.
package jira

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/178inaba/atlio/internal/atlassian"
)

// Client talks to one Jira Cloud site with one set of credentials.
type Client struct {
	api *atlassian.Client
}

// New creates a Client for the Atlassian site at siteURL, its scheme and
// host (e.g. "https://example.atlassian.net"). Jira serves its API and its
// issue pages from the site root, so the base path is empty.
func New(siteURL, email, token string, opts ...atlassian.Option) (*Client, error) {
	api, err := atlassian.New(siteURL, "", email, token, apiErrorMessages, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{api: api}, nil
}

// BrowseURL builds the browser URL of the issue with the given key, the
// /browse/<KEY> form issueref.Parse accepts back.
func (c *Client) BrowseURL(key string) string {
	return strings.TrimSuffix(c.api.BaseURL(), "/") + "/browse/" + url.PathEscape(key)
}

// User is the subset of the current-user response atlio needs.
type User struct {
	AccountID   string `json:"accountId"`
	Email       string `json:"emailAddress"`
	DisplayName string `json:"displayName"`
}

// CurrentUser identifies the authenticated account. `auth login` uses it to
// tell whether the site serves Jira to these credentials.
func (c *Client) CurrentUser(ctx context.Context) (User, error) {
	var user User
	if err := c.api.Get(ctx, "/rest/api/3/myself", nil, &user); err != nil {
		return User{}, err
	}
	return user, nil
}

// Issue is one issue as GetIssue read it: its key, the metadata fields atlio
// prints, and the display name of every field.
type Issue struct {
	// Key is the key Jira returned, which differs from the one asked for when
	// the issue has moved to another project: Jira answers the old key with
	// the issue under its new one, without a redirect.
	Key    string
	Fields IssueFields
	// Names maps a field ID (customfield_10010) to the name Jira displays
	// for it.
	Names map[string]string

	raw map[string]jsontext.Value
}

// IssueFields is the subset of an issue's fields atlio prints as metadata.
// Fields that may be unset are pointers, so unset and empty stay distinct.
type IssueFields struct {
	Summary     string        `json:"summary"`
	IssueType   Named         `json:"issuetype"`
	Status      Named         `json:"status"`
	Priority    *Named        `json:"priority"`
	Assignee    *Person       `json:"assignee"`
	Reporter    *Person       `json:"reporter"`
	Labels      []string      `json:"labels"`
	Parent      *LinkedIssue  `json:"parent"`
	Subtasks    []LinkedIssue `json:"subtasks"`
	IssueLinks  []IssueLink   `json:"issuelinks"`
	Created     string        `json:"created"`
	Updated     string        `json:"updated"`
	Attachments []Attachment  `json:"attachment"`
}

// Named is a field whose value Jira displays by its name: a type, a status, a
// priority.
type Named struct {
	Name string `json:"name"`
}

// Person is a user field.
type Person struct {
	DisplayName string `json:"displayName"`
}

// LinkedIssue is another issue as Jira embeds it in this one's parent,
// subtasks and links: its key and a few of its own fields.
type LinkedIssue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary string `json:"summary"`
		Status  Named  `json:"status"`
	} `json:"fields"`
}

// IssueLink is one link from this issue. Exactly one of InwardIssue and
// OutwardIssue is set: an outward issue is one this issue acts on (it
// "blocks" it), an inward one acts on this issue (it "is blocked by" it).
type IssueLink struct {
	Type struct {
		Inward  string `json:"inward"`
		Outward string `json:"outward"`
	} `json:"type"`
	InwardIssue  *LinkedIssue `json:"inwardIssue"`
	OutwardIssue *LinkedIssue `json:"outwardIssue"`
}

// Attachment is one file attached to the issue.
type Attachment struct {
	Filename string `json:"filename"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
}

// GetIssue reads one issue with every field and the display name of each.
// The comment thread and the work log are left out: neither is read, and a
// long discussion can outweigh the rest of the issue many times over. Neither
// value is an ADF document at its top level, so no rich-text field goes with
// them.
func (c *Client) GetIssue(ctx context.Context, key string) (*Issue, error) {
	query := url.Values{"fields": {"*all,-comment,-worklog"}, "expand": {"names"}}

	// fields is kept raw and decoded twice: once into the metadata, and once
	// into one raw value per field. The raw values are what a rich-text field
	// is written out as, so they must be the API's bytes rather than a
	// re-encoding — v2 copies a jsontext.Value verbatim.
	var resp struct {
		Key    string            `json:"key"`
		Fields jsontext.Value    `json:"fields"`
		Names  map[string]string `json:"names"`
	}
	path := "/rest/api/3/issue/" + url.PathEscape(key)
	if err := c.api.Get(ctx, path, query, &resp); err != nil {
		return nil, err
	}

	issue := &Issue{Key: resp.Key, Names: resp.Names}
	if len(resp.Fields) > 0 {
		if err := json.Unmarshal(resp.Fields, &issue.raw); err != nil {
			return nil, fmt.Errorf("parse fields of %s: %w", key, err)
		}
		if err := json.Unmarshal(resp.Fields, &issue.Fields); err != nil {
			return nil, fmt.Errorf("parse fields of %s: %w", key, err)
		}
	}
	return issue, nil
}

// searchFields names the fields Search asks for: what `jira search` prints.
// Without them the endpoint returns issue IDs only.
const searchFields = "summary,issuetype,status,assignee,updated"

// maxSearchPageSize is the most issues Search asks for in one request. Jira
// may still return fewer, so paging is driven by the token, not by this.
const maxSearchPageSize = 100

// SearchIssue is one issue as Search found it: its key and the fields
// searchFields asks for.
type SearchIssue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary   string  `json:"summary"`
		IssueType Named   `json:"issuetype"`
		Status    Named   `json:"status"`
		Assignee  *Person `json:"assignee"`
		Updated   string  `json:"updated"`
	} `json:"fields"`
}

// Search returns up to limit issues the JQL query matches, in the order Jira
// returns them, and whether more matched beyond limit. The response carries
// no total, so one issue past limit is asked for to tell a listing cut short
// from one that ended exactly there.
func (c *Client) Search(ctx context.Context, jql string, limit int) ([]SearchIssue, bool, error) {
	var all []SearchIssue
	token := ""
	for {
		query := url.Values{
			"jql":        {jql},
			"fields":     {searchFields},
			"maxResults": {strconv.Itoa(min(limit-len(all)+1, maxSearchPageSize))},
		}
		if token != "" {
			query.Set("nextPageToken", token)
		}

		var page struct {
			Issues        []SearchIssue `json:"issues"`
			NextPageToken string        `json:"nextPageToken"`
		}
		if err := c.api.Get(ctx, "/rest/api/3/search/jql", query, &page); err != nil {
			return nil, false, err
		}

		for _, issue := range page.Issues {
			if len(all) == limit {
				return all, true, nil
			}
			all = append(all, issue)
		}

		// The last page omits the token or sends it as null; v2 decodes both
		// as the empty string.
		token = page.NextPageToken
		if token == "" {
			return all, false, nil
		}
	}
}

// Field is one field of an issue with its value as the API returned it.
type Field struct {
	ID    string
	Value jsontext.Value
}

// ADFFields returns the fields whose value is an Atlassian Document Format
// document — description, environment and multi-line custom fields alike —
// recognised by the value's shape rather than by the field's schema, which
// the request does not fetch. They come in reading order: description,
// environment, then the rest by field ID, with a custom field's number
// compared as a number.
func (i *Issue) ADFFields() []Field {
	var fields []Field
	for _, id := range slices.SortedFunc(maps.Keys(i.raw), compareFieldIDs) {
		if isADFDocument(i.raw[id]) {
			fields = append(fields, Field{ID: id, Value: i.raw[id]})
		}
	}
	return fields
}

func isADFDocument(v jsontext.Value) bool {
	if v.Kind() != '{' {
		return false
	}
	var head struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(v, &head) == nil && head.Type == "doc"
}

// compareFieldIDs orders description and environment first, then custom
// fields by their number, then anything else by its ID.
func compareFieldIDs(a, b string) int {
	rankA, numA := fieldKey(a)
	rankB, numB := fieldKey(b)
	return cmp.Or(cmp.Compare(rankA, rankB), cmp.Compare(numA, numB), strings.Compare(a, b))
}

// fieldKey ranks a field ID for compareFieldIDs, with a custom field's number
// alongside; num is zero for every other field.
func fieldKey(id string) (rank int, num int64) {
	switch id {
	case "description":
		return 0, 0
	case "environment":
		return 1, 0
	}
	if digits, ok := strings.CutPrefix(id, "customfield_"); ok {
		if n, err := strconv.ParseInt(digits, 10, 64); err == nil {
			return 2, n
		}
	}
	return 3, 0
}

// apiErrorMessages parses Jira's error envelope,
// {"errorMessages":[...],"errors":{"<field>":"<message>"}}. The field errors
// are sorted by field so the same failure always reads the same way. Anything
// else yields nil, leaving the raw body to speak.
func apiErrorMessages(body []byte) []string {
	var envelope struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil
	}

	messages := slices.Clone(envelope.ErrorMessages)
	for _, field := range slices.Sorted(maps.Keys(envelope.Errors)) {
		messages = append(messages, field+": "+envelope.Errors[field])
	}
	return messages
}

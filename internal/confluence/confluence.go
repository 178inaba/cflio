// Package confluence is a small client for the Confluence Cloud REST API,
// covering exactly the calls cflio needs. It is hand-rolled rather than
// built on a third-party library because the page body has to survive as an
// untouched string end to end — a model layer that re-encodes it would break
// the lossless round-trip that is the whole point of this tool.
//
// Capabilities are split across API versions as Atlassian publishes them:
// pages, children and comments come from v2, while CQL search and the
// credential check only exist in v1.
package confluence

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	// maxPageSize is the per-request `limit` ceiling the v2 list endpoints
	// document. Callers may ask for more in total; the pagination helpers
	// chunk requests to stay within it.
	maxPageSize = 250

	// maxSearchPageSize is v1 CQL search's own, lower ceiling.
	maxSearchPageSize = 100
)

// Client talks to one Confluence Cloud site with one set of credentials.
type Client struct {
	site       *url.URL
	email      string
	token      string
	httpClient *http.Client
}

// Option configures a Client constructed by New.
type Option func(*Client)

// WithHTTPClient overrides the HTTP client. Tests use this to point at an
// httptest server; production callers should leave it unset.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// New creates a Client for the given site base URL (including the /wiki
// path, e.g. "https://example.atlassian.net/wiki"), authenticating with
// Atlassian Basic auth (account email + API token).
func New(siteURL, email, token string, opts ...Option) (*Client, error) {
	site, err := url.Parse(strings.TrimSuffix(siteURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse site url %q: %w", siteURL, err)
	}
	if site.Scheme == "" || site.Host == "" {
		return nil, fmt.Errorf("site url %q must be absolute, e.g. https://example.atlassian.net/wiki", siteURL)
	}

	c := &Client{site: site, email: email, token: token, httpClient: http.DefaultClient}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// SiteURL returns the site base URL the client was built for, for callers
// that need to turn a relative link into an absolute one.
func (c *Client) SiteURL() string {
	return c.site.String()
}

// User is the subset of the current-user response cflio needs.
type User struct {
	AccountID   string `json:"accountId"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
}

// CurrentUser identifies the authenticated account. It is the cheapest
// authenticated call available, so `auth login` uses it to validate
// credentials before writing anything to disk.
func (c *Client) CurrentUser(ctx context.Context) (User, error) {
	var user User
	if err := c.get(ctx, "/rest/api/user/current", nil, &user); err != nil {
		return User{}, err
	}
	return user, nil
}

// Version mirrors the v2 version object shared by pages and comments.
type Version struct {
	Number    int    `json:"number"`
	Message   string `json:"message"`
	CreatedAt string `json:"createdAt"`
	AuthorID  string `json:"authorId"`
}

// Page is the subset of v2's PageSingle cflio needs. Body holds the raw
// storage representation exactly as the API returned it.
type Page struct {
	ID      string  `json:"id"`
	Status  string  `json:"status"`
	Title   string  `json:"title"`
	SpaceID string  `json:"spaceId"`
	Version Version `json:"version"`
	Body    struct {
		Storage struct {
			Representation string `json:"representation"`
			Value          string `json:"value"`
		} `json:"storage"`
	} `json:"body"`
	Links links `json:"_links"`
}

// links is the single-entity `_links` envelope. Only the web link is
// modelled: the API also returns edit and tiny-link variants, which cflio
// has no use for.
type links struct {
	WebUI string `json:"webui"`
}

// GetPage fetches a page. withBody requests the storage representation;
// callers that only need the version (the optimistic-lock pre-flight in
// `update`) pass false to avoid pulling the whole body back.
func (c *Client) GetPage(ctx context.Context, id string, withBody bool) (*Page, error) {
	query := url.Values{}
	if withBody {
		query.Set("body-format", "storage")
	}

	var page Page
	if err := c.get(ctx, "/api/v2/pages/"+url.PathEscape(id), query, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// UpdatePageRequest is the v2 page-update payload. Every field is required
// by the API; cflio fills them from the sidecar written at read time.
type UpdatePageRequest struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Body   struct {
		Representation string `json:"representation"`
		Value          string `json:"value"`
	} `json:"body"`
	Version struct {
		Number  int    `json:"number"`
		Message string `json:"message"`
	} `json:"version"`
}

// NewUpdatePageRequest builds an update payload for a storage-format body.
func NewUpdatePageRequest(id, status, title, body string, version int, message string) UpdatePageRequest {
	var req UpdatePageRequest
	req.ID = id
	req.Status = status
	req.Title = title
	req.Body.Representation = "storage"
	req.Body.Value = body
	req.Version.Number = version
	req.Version.Message = message
	return req
}

// UpdatePage writes a new version of a page and returns the updated page,
// whose version number is the authoritative new value to record locally.
func (c *Client) UpdatePage(ctx context.Context, req UpdatePageRequest) (*Page, error) {
	var page Page
	if err := c.put(ctx, "/api/v2/pages/"+url.PathEscape(req.ID), req, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// SpaceKey returns a space's key (e.g. "DEV") given its numeric ID. Child
// listings only carry the ID, but readable page URLs need the key.
func (c *Client) SpaceKey(ctx context.Context, spaceID string) (string, error) {
	var space struct {
		Key string `json:"key"`
	}
	if err := c.get(ctx, "/api/v2/spaces/"+url.PathEscape(spaceID), nil, &space); err != nil {
		return "", err
	}
	return space.Key, nil
}

// Child is one entry of a page's direct children. Type distinguishes pages
// from whiteboards, folders, databases and embeds.
type Child struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Title   string `json:"title"`
	Type    string `json:"type"`
	SpaceID string `json:"spaceId"`
}

// childTypePage is the Child.Type value for regular pages.
const childTypePage = "page"

// ChildPages lists a page's direct child pages, oldest sibling first.
//
// It reads /pages/{id}/direct-children — which returns every hierarchical
// content type — and filters to pages, rather than the /pages/{id}/children
// endpoint that would do that server-side: the latter is the only
// deprecated GET in the v2 API. Filtering inside the pagination loop (as
// opposed to in the caller) is what keeps limit counting child *pages*
// rather than children of any type.
func (c *Client) ChildPages(ctx context.Context, id string, limit int) ([]Child, bool, error) {
	path := "/api/v2/pages/" + url.PathEscape(id) + "/direct-children"
	return paginate(ctx, c, path, nil, limit, func(child Child) bool {
		return child.Type == childTypePage
	})
}

// CommentKind selects between the two comment families a page can carry.
type CommentKind string

const (
	// FooterComments are the page-level discussion at the bottom of a page.
	FooterComments CommentKind = "footer-comments"
	// InlineComments are anchored to a highlighted span of the body.
	InlineComments CommentKind = "inline-comments"
)

// Comment is one footer or inline comment. ResolutionStatus and
// Properties are only populated for inline comments.
type Comment struct {
	ID               string  `json:"id"`
	Status           string  `json:"status"`
	Title            string  `json:"title"`
	ParentCommentID  string  `json:"parentCommentId"`
	Version          Version `json:"version"`
	ResolutionStatus string  `json:"resolutionStatus"`
	Body             struct {
		Storage struct {
			Value string `json:"value"`
		} `json:"storage"`
	} `json:"body"`
	Properties struct {
		InlineOriginalSelection string `json:"inlineOriginalSelection"`
	} `json:"properties"`
	Links links `json:"_links"`
}

// PageComments lists a page's root comments of the given kind, oldest
// first, with bodies in the storage representation. The comment endpoints
// do not accept body-format=view, so rendering for humans happens locally.
func (c *Client) PageComments(ctx context.Context, kind CommentKind, pageID string, limit int) ([]Comment, bool, error) {
	query := url.Values{}
	query.Set("body-format", "storage")
	query.Set("sort", "created-date")

	path := "/api/v2/pages/" + url.PathEscape(pageID) + "/" + string(kind)
	return paginate[Comment](ctx, c, path, query, limit, nil)
}

// CommentReplies lists the direct replies to one comment.
func (c *Client) CommentReplies(ctx context.Context, kind CommentKind, commentID string, limit int) ([]Comment, bool, error) {
	query := url.Values{}
	query.Set("body-format", "storage")
	query.Set("sort", "created-date")

	path := "/api/v2/" + string(kind) + "/" + url.PathEscape(commentID) + "/children"
	return paginate[Comment](ctx, c, path, query, limit, nil)
}

// SearchResult is one CQL hit. Content is absent for results that are not
// content (spaces, users), so callers fall back to the top-level fields.
type SearchResult struct {
	Content *struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Status string `json:"status"`
		Title  string `json:"title"`
	} `json:"content"`
	Title        string `json:"title"`
	Excerpt      string `json:"excerpt"`
	URL          string `json:"url"`
	EntityType   string `json:"entityType"`
	LastModified string `json:"lastModified"`
}

// Search runs a CQL query, returning at most limit results plus the total
// number of matches the server reports, which is what lets the caller say
// how many results were left out.
//
// CQL search only exists in v1, and it pages by offset rather than cursor.
func (c *Client) Search(ctx context.Context, cql string, limit int) ([]SearchResult, int, error) {
	var all []SearchResult
	total := 0

	for len(all) < limit {
		query := url.Values{}
		query.Set("cql", cql)
		query.Set("start", strconv.Itoa(len(all)))
		query.Set("limit", strconv.Itoa(min(limit-len(all), maxSearchPageSize)))

		var page struct {
			Results   []SearchResult `json:"results"`
			TotalSize int            `json:"totalSize"`
		}
		if err := c.get(ctx, "/rest/api/search", query, &page); err != nil {
			return nil, 0, err
		}

		total = page.TotalSize
		all = append(all, page.Results...)
		if len(page.Results) == 0 {
			break
		}
	}

	if len(all) > limit {
		all = all[:limit]
	}
	return all, total, nil
}

// multiEntityResult is the shape every paginated v2 list endpoint returns.
type multiEntityResult[T any] struct {
	Results []T `json:"results"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}

// paginate walks a v2 cursor-paginated endpoint until limit items pass the
// keep predicate (nil keeps everything) or the server runs out of pages.
// The bool reports whether more items remain beyond what was returned;
// v2 gives no total count, so that is all a caller can say.
func paginate[T any](ctx context.Context, c *Client, path string, query url.Values, limit int, keep func(T) bool) ([]T, bool, error) {
	var all []T
	cursor := ""
	for {
		// One past the caller's limit, so a full page proves more exist.
		page := cloneValues(query)
		page.Set("limit", strconv.Itoa(min(limit-len(all)+1, maxPageSize)))
		if cursor != "" {
			page.Set("cursor", cursor)
		}

		var result multiEntityResult[T]
		if err := c.get(ctx, path, page, &result); err != nil {
			return nil, false, err
		}

		for _, item := range result.Results {
			if keep != nil && !keep(item) {
				continue
			}
			if len(all) == limit {
				// One kept item past the limit is enough to report that
				// more exist, without a count the API cannot give.
				return all, true, nil
			}
			all = append(all, item)
		}

		cursor = nextCursor(result.Links.Next)
		if cursor == "" {
			return all, false, nil
		}
	}
}

func cloneValues(v url.Values) url.Values {
	clone := make(url.Values, len(v)+2)
	maps.Copy(clone, v)
	return clone
}

// nextCursor extracts the opaque cursor from the relative "next" link v2
// returns. An unparseable or cursor-less link ends pagination rather than
// risking a loop on the same page.
func nextCursor(next string) string {
	if next == "" {
		return ""
	}
	u, err := url.Parse(next)
	if err != nil {
		return ""
	}
	return u.Query().Get("cursor")
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	endpoint := c.site.JoinPath(path)
	if len(query) > 0 {
		endpoint.RawQuery = query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", path, err)
	}
	return c.do(req, path, out)
}

func (c *Client) put(ctx context.Context, path string, payload, out any) error {
	body, err := encodeJSON(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.site.JoinPath(path).String(), body)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, path, out)
}

// encodeJSON disables Go's default HTML escaping. Storage XHTML is mostly
// angle brackets and ampersands, and escaping each one to \uXXXX triples
// its size in the request — exactly the wrong trade for the large pages
// this tool exists to handle.
func encodeJSON(payload any) (io.Reader, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return nil, fmt.Errorf("encode request body: %w", err)
	}
	return strings.NewReader(buf.String()), nil
}

func (c *Client) do(req *http.Request, path string, out any) error {
	req.SetBasicAuth(c.email, c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", req.Method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Error envelopes are small, so buffering one to parse it is fine.
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read response from %s %s: %w", req.Method, path, err)
		}
		return newAPIError(req.Method, path, resp.StatusCode, body)
	}

	if out == nil {
		return nil
	}
	// Decoded straight from the stream rather than buffered first: a page
	// response carries the whole body, and reading it into a []byte before
	// unmarshalling would hold a second full copy of a multi-megabyte page.
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("parse response from %s %s: %w", req.Method, path, err)
	}
	return nil
}

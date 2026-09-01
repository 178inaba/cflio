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
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
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

	// maxBulkUsers is the number of account IDs the bulk user endpoint
	// documents per request. It answers a longer list by silently returning
	// the first hundred, so exceeding it loses people rather than failing.
	maxBulkUsers = 100

	// maxTitlesPerQuery bounds how many titles are OR-ed into one CQL query,
	// keeping the generated query string to a few hundred bytes for the page
	// titles actually seen in the wild. It is not a guarantee: fifty titles at
	// Confluence's 255-character ceiling would still outgrow what some proxies
	// carry, which costs the batch its resolution and nothing more.
	maxTitlesPerQuery = 50

	// maxErrorBody bounds how much of a failed response is read to build the
	// error from it. See responseError for why it is this far above the width
	// the message is finally truncated to.
	maxErrorBody = 64 << 10
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

// Users looks up the accounts named by accountIDs, in as few requests as the
// bulk endpoint's ceiling allows. Accounts that no longer exist, or that the
// token cannot see, are simply absent from the result.
//
// Like the credential check, this is v1: the v2 API models no users.
func (c *Client) Users(ctx context.Context, accountIDs []string) ([]User, error) {
	var all []User
	for batch := range slices.Chunk(accountIDs, maxBulkUsers) {
		query := url.Values{"accountId": batch}

		var page struct {
			Results []User `json:"results"`
		}
		if err := c.get(ctx, "/rest/api/user/bulk", query, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
	}
	return all, nil
}

// PageMatch is one page a title lookup resolved to. Title is what the search
// API returned, which is HTML-escaped and may carry highlight markers, so
// comparing it to a title read out of a page body means normalising it first
// — format.StripHighlightMarkers does both halves.
type PageMatch struct {
	ID    string
	Title string
}

// PagesByTitle resolves exact page titles within one space. Titles are unique
// per space, so one query answers the whole batch; a title that matched
// nothing is simply missing from the result.
//
// One query per space rather than one OR-ing (space, title) pairs across
// spaces: every hit then belongs to the space that was asked for, so the
// caller can attribute it by title alone without the response having to carry
// the space back.
func (c *Client) PagesByTitle(ctx context.Context, spaceKey string, titles []string) ([]PageMatch, error) {
	var all []PageMatch
	for batch := range slices.Chunk(titles, maxTitlesPerQuery) {
		clauses := make([]string, 0, len(batch))
		for _, title := range batch {
			clauses = append(clauses, "title = "+quoteCQL(title))
		}
		cql := "type = page and space = " + quoteCQL(spaceKey) +
			" and (" + strings.Join(clauses, " or ") + ")"

		// One result per title: the uniqueness that makes this lookup
		// possible is also what makes asking for more a wasted round trip.
		results, _, err := c.Search(ctx, cql, len(batch))
		if err != nil {
			return nil, err
		}

		for _, result := range results {
			if result.Content == nil {
				continue
			}
			all = append(all, PageMatch{ID: result.Content.ID, Title: result.Content.Title})
		}
	}
	return all, nil
}

var cqlEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

// quoteCQL renders s as a CQL string literal, quotes included. Titles and
// space keys are page data, not input this package controls, so a quote or a
// backslash in one has to be escaped rather than end the literal early and
// leave the rest of the value to be read as query syntax.
func quoteCQL(s string) string {
	return `"` + cqlEscaper.Replace(s) + `"`
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
	ID     string `json:"id"`
	Status string `json:"status"`
	Title  string `json:"title"`
	// Subtype is "live" for a live doc and absent for a classic page. It is
	// what tells the two apart offline, once `read` has recorded it in the
	// sidecar: a live doc's editor rewrites the storage body behind cflio's
	// back, which some edits have to refuse rather than silently lose.
	Subtype string  `json:"subtype"`
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

// Child is one entry of a page's direct children. Type distinguishes pages
// from whiteboards, folders, databases and embeds. The API returns no space
// for a child, so callers that need one read it off the parent page.
type Child struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Type   string `json:"type"`
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

// createCommentRequest is the payload that posts a top-level comment.
//
// parentCommentId, which would make it a reply, is deliberately absent: the
// API takes it as an alternative to pageId rather than in addition to one, so
// a reply is a different request shape and not an extra field to leave empty.
type createCommentRequest struct {
	PageID string      `json:"pageId"`
	Body   commentBody `json:"body"`
}

// commentBody is the representation-tagged body a comment is written with.
type commentBody struct {
	Representation string `json:"representation"`
	Value          string `json:"value"`
}

// CreateFooterComment posts body as a new footer comment on a page and
// returns the comment the server created.
//
// The body travels as the storage representation, unchanged. The API also
// accepts "wiki" and "atlas_doc_format", neither of which cflio sends: they
// are conversions, and a body that is converted on the way in is no longer
// the one the caller wrote.
func (c *Client) CreateFooterComment(ctx context.Context, pageID, body string) (*Comment, error) {
	req := createCommentRequest{
		PageID: pageID,
		Body:   commentBody{Representation: "storage", Value: body},
	}

	var comment Comment
	if err := c.post(ctx, "/api/v2/"+string(FooterComments), req, &comment); err != nil {
		return nil, err
	}
	return &comment, nil
}

// Attachment is one file attached to a page. Title is the filename, which is
// what `attachments download` matches its glob against. DownloadLink is
// relative to the site base URL, in the form
// /rest/api/content/{pageID}/child/attachment/{attachmentID}/download.
//
// The API's own attachment id is deliberately not modelled: DownloadLink
// already carries it, and it is nothing a caller could use — downloads select
// by filename, and Confluence keeps same-named attachments as versions of one
// attachment rather than as separate ones, so no two listing entries share a
// name for an id to tell apart.
type Attachment struct {
	Title        string `json:"title"`
	MediaType    string `json:"mediaType"`
	FileSize     int64  `json:"fileSize"`
	DownloadLink string `json:"downloadLink"`
}

// PageAttachments lists the files attached to a page.
func (c *Client) PageAttachments(ctx context.Context, pageID string, limit int) ([]Attachment, bool, error) {
	path := "/api/v2/pages/" + url.PathEscape(pageID) + "/attachments"
	return paginate[Attachment](ctx, c, path, nil, limit, nil)
}

// DownloadAttachment streams the bytes behind downloadLink to w and returns
// how many it wrote. The body is copied through untouched: an attachment is
// the only response cflio takes that is not JSON, and re-encoding it would
// leave an image that no longer opens.
//
// It cannot go through do, which asks for JSON and decodes the response as
// JSON. Nothing else about the request differs, so the auth and the error
// handling are the same.
//
// The download redirects once, to Atlassian's media service on another host.
// Go's http.Client follows it and drops the Authorization header on the way,
// because the target is neither the initial host nor a subdomain of it (see
// shouldCopyHeaderOnRedirect in net/http). That is what the media service
// needs: the redirect target is a signed URL, and it rejects a request that
// carries the site's credentials as well.
func (c *Client) DownloadAttachment(ctx context.Context, downloadLink string, w io.Writer) (int64, error) {
	endpoint, err := c.attachmentURL(downloadLink)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, fmt.Errorf("build request for %s: %w", downloadLink, err)
	}
	// No Accept header: the response is the file itself, and asking for
	// application/json is a claim the media service is entitled to act on.
	//
	// The credentials go to the site and nowhere else. attachmentURL passes an
	// absolute link through, and one pointing at another host is a signed URL
	// like the redirect target — it needs no auth header, and sending the
	// site's to a host that never asked for it both leaks it and is what the
	// media service rejects. Same reasoning as the redirect below, applied
	// where http.Client cannot: it only strips headers it did not set.
	if req.URL.Host == c.site.Host {
		req.SetBasicAuth(c.email, c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s %s: %w", req.Method, downloadLink, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, responseError(req.Method, downloadLink, resp)
	}

	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return n, fmt.Errorf("download %s: %w", downloadLink, err)
	}
	return n, nil
}

// attachmentURL turns the relative download link an attachment carries into an
// absolute URL.
//
// The link is relative to the listing's `_links.base`, which is the site base
// URL this client was built for — the same string. So the two are
// concatenated: url.URL.ResolveReference would read the leading slash as
// site-root-relative and drop the /wiki path the base ends with, addressing a
// host that serves nothing there.
//
// A link that is neither absolute nor rooted at / is an error rather than a
// guess: every shape the API has been observed to return is one of those two,
// and a third would mean the assumption above no longer holds.
func (c *Client) attachmentURL(downloadLink string) (string, error) {
	u, err := url.Parse(downloadLink)
	if err != nil {
		return "", fmt.Errorf("parse download link %q: %w", downloadLink, err)
	}
	if u.IsAbs() {
		return u.String(), nil
	}
	if !strings.HasPrefix(downloadLink, "/") {
		return "", fmt.Errorf("download link %q is neither absolute nor rooted at /", downloadLink)
	}
	return c.site.String() + downloadLink, nil
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
		// The server already said how many matches exist, so a query that
		// matched fewer than the caller asked for needs no further page to
		// prove it. Without this, every under-filled search — the normal case
		// when looking a set of titles up, since a missing one is expected —
		// costs an extra round trip that comes back empty.
		if total > 0 && len(all) >= total {
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
	return c.send(ctx, http.MethodPut, path, payload, out)
}

func (c *Client) post(ctx context.Context, path string, payload, out any) error {
	return c.send(ctx, http.MethodPost, path, payload, out)
}

// send issues a request carrying a JSON body. The two methods that do share
// everything but the verb, and encodeJSON's escaping trade is the reason to
// keep them on one path: a comment body is storage XHTML like a page body,
// and a second encoder would be the place for that to drift.
func (c *Client) send(ctx context.Context, method, path string, payload, out any) error {
	body, err := encodeJSON(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, method, c.site.JoinPath(path).String(), body)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, path, out)
}

// encodeJSON renders a request body. It relies on v2 leaving HTML alone:
// storage XHTML is mostly angle brackets and ampersands, and escaping each
// one to \uXXXX triples its size in the request — exactly the wrong trade for
// the large pages this tool exists to handle. v1 needed SetEscapeHTML(false)
// to reach the same place.
//
// The bytes.Buffer is what keeps a multi-megabyte body down to one copy:
// MarshalWrite appends straight into a bytes.Buffer instead of filling an
// intermediate buffer and copying out of it. net/http reads Content-Length
// off the body's concrete type, and *bytes.Buffer is one of the types it
// knows, so that type has to survive the io.Reader this returns.
//
// This is the one marshal site that does not ask for Deterministic. Nothing
// diffs a request body: the payload types carry no maps, and the server reads
// members by name.
func encodeJSON(payload any) (io.Reader, error) {
	var buf bytes.Buffer
	if err := json.MarshalWrite(&buf, payload); err != nil {
		return nil, fmt.Errorf("encode request body: %w", err)
	}
	return &buf, nil
}

// responseError turns a non-2xx response into an *APIError.
//
// The body is read through a LimitReader because it is not always the small
// JSON envelope the API documents: a gateway answers with an HTML page, and the
// download path can be handed anything at all. maxErrorBody sits far above
// maxRawBodyInError, which is where the message actually gets cut — the limit
// here only stops an unbounded read, and cutting near the display width would
// split a multi-byte rune where truncate's rune-boundary rewind can no longer
// see it.
func responseError(method, path string, resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if err != nil {
		return fmt.Errorf("read response from %s %s: %w", method, path, err)
	}
	return newAPIError(method, path, resp.StatusCode, body)
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
		return responseError(req.Method, path, resp)
	}

	if out == nil {
		return nil
	}
	// Decoded straight from the stream rather than buffered first: a page
	// response carries the whole body, and reading it into a []byte before
	// unmarshalling would hold a second full copy of a multi-megabyte page.
	// UnmarshalRead reads to EOF and rejects anything after the value, which
	// is what every endpoint here returns: exactly one JSON document.
	if err := json.UnmarshalRead(resp.Body, out); err != nil {
		return fmt.Errorf("parse response from %s %s: %w", req.Method, path, err)
	}
	return nil
}

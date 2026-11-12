// Package atlassian is the HTTP transport shared by atlio's product clients:
// it builds requests against one product's base URL on a site, authenticates them with
// Atlassian Basic auth (account email + API token), encodes JSON and
// multipart/form-data bodies, decodes JSON, and turns a non-2xx response into
// an *APIError.
//
// What differs between products — endpoint paths, payload types, and the
// shape of an error body — stays in the product package, which supplies the
// last of these as a MessageFunc.
package atlassian

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
)

// MessageFunc extracts human-readable messages from the body of a failed
// response. It returns nil when the body is not in a shape it recognises, in
// which case the error falls back to the raw body.
type MessageFunc func(body []byte) []string

// Client sends requests to one product's API on one site with one set of
// credentials.
type Client struct {
	base       *url.URL
	email      string
	token      string
	messages   MessageFunc
	httpClient *http.Client
}

// Option configures a Client constructed by New.
type Option func(*Client)

// WithHTTPClient overrides the HTTP client. Tests use this to answer requests
// from an httptest server or a RoundTripper of their own; production callers
// should leave it unset.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// New creates a Client for the site at siteURL, its scheme and host (e.g.
// "https://example.atlassian.net"). Request paths are joined onto basePath,
// the product's root on that site: "/wiki" for a product served under it, ""
// for one served at the site root. messages may be nil, in which case every
// error reports its raw body.
func New(siteURL, basePath, email, token string, messages MessageFunc, opts ...Option) (*Client, error) {
	site, err := url.Parse(siteURL)
	if err != nil {
		return nil, fmt.Errorf("parse site url %q: %w", siteURL, err)
	}
	if site.Scheme == "" || site.Host == "" {
		return nil, fmt.Errorf("site url %q must be absolute, e.g. https://example.atlassian.net", siteURL)
	}

	c := &Client{
		base:       site.JoinPath(basePath),
		email:      email,
		token:      token,
		messages:   messages,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// BaseURL returns the URL request paths are joined onto.
func (c *Client) BaseURL() string {
	return c.base.String()
}

// Get issues a GET for path under the base URL and decodes the JSON response
// into out, which may be nil to discard it.
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	endpoint := c.base.JoinPath(path)
	if len(query) > 0 {
		endpoint.RawQuery = query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", path, err)
	}
	return c.doJSON(req, path, out)
}

// Send issues a request carrying payload as a JSON body. Every method that
// sends a body shares this path, and encodeJSON's escaping trade is the
// reason to keep it one: request bodies routinely carry markup, and a second
// encoder would be the place for that to drift.
func (c *Client) Send(ctx context.Context, method, path string, payload, out any) error {
	body, err := encodeJSON(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base.JoinPath(path).String(), body)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doJSON(req, path, out)
}

// SendMultipart issues a request whose body is the multipart/form-data write
// produces, with header added to it, and decodes the JSON response into out.
//
// The body is streamed through a pipe rather than built first: what it carries
// is files, and a buffered body would hold a second full copy of each. With no
// length known up front the request goes out chunked. The Content-Type is set
// after header is applied, since only it carries the boundary the parts are
// actually written with.
//
// The writer's goroutine is waited for before returning, so nothing it reads
// is touched once this returns. Closing the pipe first is what makes that wait
// finite when the server answered without reading the whole body: the writer
// is then blocked on a pipe nobody drains.
//
// When the request fails, the writer's own error is reported in its place
// only when it is the cause — the transport echoes a failed body read
// without promising to wrap it. A writer that failed because the pipe was
// closed under it, or because the context ended, is a consequence instead:
// the request's error is the one that says what happened, and with an
// expired deadline it is also the one that carries context.DeadlineExceeded.
func (c *Client) SendMultipart(ctx context.Context, method, path string, header http.Header, write func(*multipart.Writer) error, out any) error {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	req, err := http.NewRequestWithContext(ctx, method, c.base.JoinPath(path).String(), pr)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", path, err)
	}
	for key, values := range header {
		for _, v := range values {
			req.Header.Add(key, v)
		}
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	writeErr := make(chan error, 1)
	go func() {
		err := write(mw)
		if err == nil {
			err = mw.Close()
		}
		// A nil err closes the pipe with io.EOF, ending the body.
		pw.CloseWithError(err)
		writeErr <- err
	}()

	err = c.doJSON(req, path, out)
	_ = pr.Close()
	werr := <-writeErr
	if err != nil && werr != nil && ctx.Err() == nil && !errors.Is(werr, io.ErrClosedPipe) {
		return fmt.Errorf("%s %s: write request body: %w", method, path, werr)
	}
	// The writer only returns nil once the transport has read the body to its
	// end, so after that only a non-2xx response settles the outcome. Anything
	// else — no response, or a 2xx whose body could not be read or parsed —
	// leaves it unknown.
	if err != nil && werr == nil {
		if _, refused := errors.AsType[*APIError](err); !refused {
			return fmt.Errorf("%w: %w", err, ErrSentInFull)
		}
	}
	return err
}

// Download streams the body behind rawURL, an absolute URL, to w and returns
// how many bytes it wrote. path names the resource in errors, so that a
// failure reads the way the caller's link did rather than as the URL it was
// resolved to. The body is copied through untouched: it is a file, and
// re-encoding it would leave one that no longer opens.
//
// No Accept header is sent: the response is the file itself, and asking for
// application/json is a claim the server is entitled to act on.
//
// The credentials go to the site and nowhere else. A rawURL on another host
// is one the site handed out for a file it stores elsewhere, typically a
// pre-signed URL: it needs no auth header, and sending the site's to a host
// that never asked for it both leaks it and is what such a host rejects.
// http.Client cannot make this check itself, since it only strips headers it
// did not set; it does strip the header when a redirect leaves the host,
// because the target is neither the initial host nor a subdomain of it (see
// shouldCopyHeaderOnRedirect in net/http), and that is relied upon here.
func (c *Client) Download(ctx context.Context, rawURL, path string, w io.Writer) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, fmt.Errorf("build request for %s: %w", path, err)
	}
	if req.URL.Host == c.base.Host {
		req.SetBasicAuth(c.email, c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s %s: %w", req.Method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, c.responseError(req.Method, path, resp)
	}

	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return n, fmt.Errorf("download %s: %w", path, err)
	}
	return n, nil
}

// encodeJSON renders a request body. It relies on v2 leaving HTML alone:
// a body in a markup format is mostly angle brackets and ampersands, and
// escaping each one to \uXXXX triples its size in the request — exactly the
// wrong trade for the multi-megabyte bodies a document can have. v1 needed
// SetEscapeHTML(false) to reach the same place.
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

func (c *Client) doJSON(req *http.Request, path string, out any) error {
	req.SetBasicAuth(c.email, c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", req.Method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.responseError(req.Method, path, resp)
	}

	if out == nil {
		return nil
	}
	// Decoded straight from the stream rather than buffered first: a response
	// can carry a whole document, and reading it into a []byte before
	// unmarshalling would hold a second full copy of a multi-megabyte one.
	// UnmarshalRead reads to EOF and rejects anything after the value, which
	// is what every endpoint here returns: exactly one JSON document.
	if err := json.UnmarshalRead(resp.Body, out); err != nil {
		return fmt.Errorf("parse response from %s %s: %w", req.Method, path, err)
	}
	return nil
}

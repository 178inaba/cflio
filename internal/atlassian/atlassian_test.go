package atlassian

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// wantAuth is the Authorization header every test client's credentials
// produce.
var wantAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte("a@example.com:api-token"))

// newTestClient starts an httptest server with the given handler and returns
// a Client for it with /api as the base path, so a test can tell the base
// path was kept.
func newTestClient(t *testing.T, messages MessageFunc, handler http.HandlerFunc) *Client {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := New(srv.URL, "/api", "a@example.com", "api-token", messages)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func TestNewRejectsARelativeSiteURL(t *testing.T) {
	for _, site := range []string{"example.atlassian.net", "/", ""} {
		if _, err := New(site, "/wiki", "a@example.com", "t", nil); err == nil {
			t.Errorf("New(%q) error = nil, want an error for a non-absolute site url", site)
		}
	}
}

func TestBaseURLJoinsTheBasePathOntoTheSite(t *testing.T) {
	tests := []struct {
		site, basePath, want string
	}{
		{site: "https://example.atlassian.net", basePath: "/wiki", want: "https://example.atlassian.net/wiki"},
		{site: "https://example.atlassian.net/", basePath: "/wiki", want: "https://example.atlassian.net/wiki"},
		{site: "https://example.atlassian.net", basePath: "", want: "https://example.atlassian.net"},
	}

	for _, tt := range tests {
		client, err := New(tt.site, tt.basePath, "a@example.com", "t", nil)
		if err != nil {
			t.Fatalf("New(%q, %q) error = %v", tt.site, tt.basePath, err)
		}
		if got := client.BaseURL(); got != tt.want {
			t.Errorf("New(%q, %q).BaseURL() = %q, want %q", tt.site, tt.basePath, got, tt.want)
		}
	}
}

func TestGetSendsCredentialsAndAcceptsJSON(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotAccept string
	client := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		gotAuth, gotAccept = r.Header.Get("Authorization"), r.Header.Get("Accept")
		_, _ = fmt.Fprint(w, `{"name":"x"}`)
	})

	var out struct {
		Name string `json:"name"`
	}
	if err := client.Get(t.Context(), "/rest/thing", map[string][]string{"q": {"a b"}}, &out); err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if gotPath != "/api/rest/thing" {
		t.Errorf("path = %q, want the path joined onto the base", gotPath)
	}
	if gotQuery != "q=a+b" {
		t.Errorf("query = %q, want q=a+b", gotQuery)
	}
	if gotAuth != wantAuth {
		t.Errorf("Authorization = %q, want %q", gotAuth, wantAuth)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want application/json", gotAccept)
	}
	if out.Name != "x" {
		t.Errorf("decoded name = %q, want x", out.Name)
	}
}

func TestSendEncodesTheBodyAsJSONWithoutEscapingHTML(t *testing.T) {
	var gotMethod, gotAuth, gotContentType, gotBody string
	client := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotAuth = r.Method, r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		_, _ = fmt.Fprint(w, `{}`)
	})

	payload := struct {
		Value string `json:"value"`
	}{Value: "<p>a &amp; b</p>"}
	if err := client.Send(t.Context(), http.MethodPut, "/rest/thing", payload, nil); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotAuth != wantAuth {
		t.Errorf("Authorization = %q, want %q", gotAuth, wantAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if want := `{"value":"<p>a &amp; b</p>"}`; gotBody != want {
		t.Errorf("body = %s, want %s", gotBody, want)
	}
}

func TestAFailedResponseBecomesAnAPIError(t *testing.T) {
	tests := []struct {
		name         string
		messages     MessageFunc
		body         string
		wantMessages []string
		wantError    string
	}{
		{
			name:         "the product's messages are used when it recognises the body",
			messages:     func([]byte) []string { return []string{"one", "two"} },
			body:         `{"whatever":true}`,
			wantMessages: []string{"one", "two"},
			wantError:    "GET /rest/thing: HTTP 409: one; two",
		},
		{
			name:      "the raw body is used when it does not",
			messages:  func([]byte) []string { return nil },
			body:      "<html>gateway said no</html>",
			wantError: "GET /rest/thing: HTTP 409: <html>gateway said no</html>",
		},
		{
			name:      "the raw body is used when no message function was supplied",
			body:      "gateway said no",
			wantError: "GET /rest/thing: HTTP 409: gateway said no",
		},
		{
			name:      "an empty body leaves just the status",
			wantError: "GET /rest/thing: HTTP 409",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, tt.messages, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusConflict)
				_, _ = fmt.Fprint(w, tt.body)
			})

			err := client.Get(t.Context(), "/rest/thing", nil, nil)
			apiErr, ok := errors.AsType[*APIError](err)
			if !ok {
				t.Fatalf("Get() error = %v, want an *APIError", err)
			}
			// The path is the one the caller named, not the base-joined URL.
			if apiErr.Method != http.MethodGet || apiErr.Path != "/rest/thing" || apiErr.Status != http.StatusConflict {
				t.Errorf("APIError = %s %s %d, want GET /rest/thing 409", apiErr.Method, apiErr.Path, apiErr.Status)
			}
			if !slices.Equal(apiErr.Messages, tt.wantMessages) {
				t.Errorf("Messages = %q, want %q", apiErr.Messages, tt.wantMessages)
			}
			if err.Error() != tt.wantError {
				t.Errorf("Error() = %q, want %q", err.Error(), tt.wantError)
			}
		})
	}
}

func TestAFailedResponseBodyIsBounded(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "ascii", body: strings.Repeat("x", maxErrorBody+5000)},
		// Multi-byte runes must not be cut in half: an invalid UTF-8 tail
		// would render as a replacement character in the user's terminal.
		{name: "multibyte", body: strings.Repeat("あ", maxErrorBody)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, nil, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = fmt.Fprint(w, tt.body)
			})

			err := client.Get(t.Context(), "/rest/thing", nil, nil)
			apiErr, ok := errors.AsType[*APIError](err)
			if !ok {
				t.Fatalf("Get() error = %v, want an *APIError", err)
			}
			if len(apiErr.Body) != maxErrorBody {
				t.Errorf("len(Body) = %d, want the read stopped at %d", len(apiErr.Body), maxErrorBody)
			}
			if len(err.Error()) > maxRawBodyInError+200 {
				t.Errorf("error length = %d, want the raw body truncated", len(err.Error()))
			}
			if !utf8.ValidString(err.Error()) {
				t.Errorf("error = %q, want valid UTF-8 after truncation", err.Error())
			}
		})
	}
}

func TestRequestsHonorContextCancellation(t *testing.T) {
	client := newTestClient(t, nil, func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := client.Get(ctx, "/rest/thing", nil, nil); err == nil {
		t.Fatal("Get() error = nil, want the context error")
	} else if !errors.Is(err, context.Canceled) {
		t.Errorf("Get() error = %v, want it to wrap context.Canceled", err)
	}
}

// fileBytes is a real PNG signature plus a byte that is not valid UTF-8 on its
// own, so a download that decoded or re-encoded the body would corrupt it
// visibly rather than passing the assertion by luck.
var fileBytes = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0xff, 0x00, 0x42}

func TestDownloadStreamsTheBodyUnchangedWithoutAskingForJSON(t *testing.T) {
	var gotAccept string
	client := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		_, _ = w.Write(fileBytes)
	})

	var buf bytes.Buffer
	n, err := client.Download(t.Context(), client.BaseURL()+"/file", "/file", &buf)
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	if gotAccept != "" {
		t.Errorf("Accept = %q, want none: the response is the file, not JSON", gotAccept)
	}
	if !bytes.Equal(buf.Bytes(), fileBytes) || n != int64(len(fileBytes)) {
		t.Errorf("body = %v (n %d), want the served bytes verbatim %v", buf.Bytes(), n, fileBytes)
	}
}

func TestDownloadReportsAFailureUnderTheCallersPath(t *testing.T) {
	client := newTestClient(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	var buf bytes.Buffer
	_, err := client.Download(t.Context(), client.BaseURL()+"/file", "/file", &buf)
	apiErr, ok := errors.AsType[*APIError](err)
	if !ok {
		t.Fatalf("Download() error = %v, want an *APIError", err)
	}
	if apiErr.Path != "/file" || apiErr.Status != http.StatusNotFound {
		t.Errorf("APIError path %q status %d, want /file 404", apiErr.Path, apiErr.Status)
	}
	if buf.Len() != 0 {
		t.Errorf("body = %q, want nothing written for a failed download", buf.Bytes())
	}
}

// recordingTransport answers every request from responses, keyed by host, and
// records the request that reached each one.
//
// A RoundTripper rather than two httptest servers: these tests are about which
// host receives the Authorization header, and httptest servers all listen on
// 127.0.0.1 — Go compares hostnames with the port stripped, so two of them
// count as the same host and the header would be copied either way.
type recordingTransport struct {
	responses map[string]*http.Response
	seen      []*http.Request
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.seen = append(t.seen, req)
	resp, ok := t.responses[req.URL.Host]
	if !ok {
		return nil, fmt.Errorf("no canned response for host %q", req.URL.Host)
	}
	resp.Request = req
	return resp, nil
}

const (
	siteHost  = "example.atlassian.net"
	mediaHost = "media-cdn.example.com"
)

func newRecordingClient(t *testing.T, transport *recordingTransport) *Client {
	t.Helper()

	client, err := New("https://"+siteHost, "/wiki", "a@example.com", "api-token", nil,
		WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func okResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(fileBytes))}
}

func TestDownloadSendsCredentialsOnlyToTheSite(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantAuth bool
	}{
		{
			name:     "a URL on the site's host",
			url:      "https://" + siteHost + "/wiki/rest/api/content/10/child/attachment/att1/download",
			wantAuth: true,
		},
		{
			// A signed URL like a redirect target: it needs no auth header,
			// and the media host rejects a request that carries the site's.
			// http.Client only strips what it did not set, so a first request
			// straight to another host is on Download to get right.
			name:     "a URL on another host",
			url:      "https://" + mediaHost + "/signed/blob?token=abc",
			wantAuth: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := &recordingTransport{responses: map[string]*http.Response{
				siteHost:  okResponse(),
				mediaHost: okResponse(),
			}}
			client := newRecordingClient(t, transport)

			var buf bytes.Buffer
			if _, err := client.Download(t.Context(), tt.url, "/download", &buf); err != nil {
				t.Fatalf("Download() error = %v", err)
			}

			if len(transport.seen) != 1 {
				t.Fatalf("requests = %d, want 1", len(transport.seen))
			}
			want := ""
			if tt.wantAuth {
				want = wantAuth
			}
			if got := transport.seen[0].Header.Get("Authorization"); got != want {
				t.Errorf("Authorization = %q, want %q (host %s)", got, want, transport.seen[0].URL.Host)
			}
		})
	}
}

func TestDownloadDropsTheAuthHeaderOnARedirectToAnotherHost(t *testing.T) {
	transport := &recordingTransport{responses: map[string]*http.Response{
		siteHost: {
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": {"https://" + mediaHost + "/signed/blob?token=abc"}},
			Body:       io.NopCloser(strings.NewReader("")),
		},
		mediaHost: okResponse(),
	}}
	client := newRecordingClient(t, transport)

	var buf bytes.Buffer
	if _, err := client.Download(t.Context(),
		"https://"+siteHost+"/wiki/rest/api/content/10/child/attachment/att1/download", "/download", &buf); err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	if len(transport.seen) != 2 {
		t.Fatalf("requests = %d, want 2 (the site and the redirect target)", len(transport.seen))
	}
	if got := transport.seen[0].Header.Get("Authorization"); transport.seen[0].URL.Host != siteHost || got != wantAuth {
		t.Errorf("first request = %s (auth %q), want the site with credentials", transport.seen[0].URL, got)
	}
	// The redirect target is a signed URL. Forwarding the site's credentials
	// to a host that did not ask for them is what the media service rejects.
	if got := transport.seen[1].Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization on the redirect = %q, want it dropped across hosts", got)
	}
	if !bytes.Equal(buf.Bytes(), fileBytes) {
		t.Errorf("body = %v, want the redirect target's bytes %v", buf.Bytes(), fileBytes)
	}
}

// writeFilePart is a SendMultipart writer that sends content as one file part
// named name, the shape every upload this transport carries has.
func writeFilePart(name string, content []byte) func(*multipart.Writer) error {
	return func(mw *multipart.Writer) error {
		part, err := mw.CreateFormFile("file", name)
		if err != nil {
			return err
		}
		_, err = part.Write(content)
		return err
	}
}

func TestSendMultipartStreamsThePartsWithTheGivenHeader(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotAccept, gotToken, gotName string
	var gotContent []byte
	client := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAuth, gotAccept = r.Header.Get("Authorization"), r.Header.Get("Accept")
		gotToken = r.Header.Get("X-Test-Token")
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("FormFile() error = %v", err)
			return
		}
		defer func() { _ = file.Close() }()
		gotName = header.Filename
		gotContent, _ = io.ReadAll(file)
		_, _ = fmt.Fprint(w, `{"name":"x"}`)
	})

	var out struct {
		Name string `json:"name"`
	}
	header := http.Header{"X-Test-Token": {"nocheck"}}
	if err := client.SendMultipart(t.Context(), http.MethodPost, "/rest/upload", header,
		writeFilePart("a.png", fileBytes), &out); err != nil {
		t.Fatalf("SendMultipart() error = %v", err)
	}

	if gotMethod != http.MethodPost || gotPath != "/api/rest/upload" {
		t.Errorf("request = %s %s, want POST /api/rest/upload", gotMethod, gotPath)
	}
	if gotAuth != wantAuth {
		t.Errorf("Authorization = %q, want %q", gotAuth, wantAuth)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want application/json", gotAccept)
	}
	if gotToken != "nocheck" {
		t.Errorf("X-Test-Token = %q, want the caller's header passed through", gotToken)
	}
	if gotName != "a.png" || !bytes.Equal(gotContent, fileBytes) {
		t.Errorf("file part = %q %v, want a.png %v", gotName, gotContent, fileBytes)
	}
	if out.Name != "x" {
		t.Errorf("decoded name = %q, want x", out.Name)
	}
}

func TestSendMultipartReportsTheWritersError(t *testing.T) {
	client := newTestClient(t, nil, func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
	})

	errRead := errors.New("read local file")
	write := func(mw *multipart.Writer) error {
		if err := writeFilePart("a.png", fileBytes)(mw); err != nil {
			return err
		}
		return errRead
	}

	err := client.SendMultipart(t.Context(), http.MethodPost, "/rest/upload", nil, write, nil)
	if !errors.Is(err, errRead) {
		t.Errorf("SendMultipart() error = %v, want it to wrap the writer's error", err)
	}
}

func TestSendMultipartReturnsTheAPIErrorWhenTheServerAnswersWithoutReading(t *testing.T) {
	client := newTestClient(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	// Small enough to sit in the socket buffers, so the write never blocks and
	// the response is what the transport reports.
	err := client.SendMultipart(t.Context(), http.MethodPost, "/rest/upload", nil,
		writeFilePart("a.png", fileBytes), nil)

	if apiErr, ok := errors.AsType[*APIError](err); !ok || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("SendMultipart() error = %v, want an *APIError with status 400", err)
	}
	// A 4xx refuses the request, so nothing about the outcome is unknown.
	if errors.Is(err, ErrSentInFull) {
		t.Errorf("SendMultipart() error = %v, want no ErrSentInFull once the server refused it", err)
	}
}

func TestSendMultipartReturnsOnceTheServerStopsReadingALargeBody(t *testing.T) {
	client := newTestClient(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	// Far past what the socket buffers and the server's discard of an unread
	// body absorb, so the writer is left blocked once the server stops
	// reading. Which error comes back is net/http's race between the response
	// and the failed write; what matters is that one does, and that the writer
	// is done by then.
	var writerDone atomic.Bool
	write := func(mw *multipart.Writer) error {
		defer writerDone.Store(true)
		return writeFilePart("big.bin", make([]byte, 16<<20))(mw)
	}

	if err := client.SendMultipart(t.Context(), http.MethodPost, "/rest/upload", nil, write, nil); err == nil {
		t.Fatal("SendMultipart() error = nil, want the failure")
	}
	if !writerDone.Load() {
		t.Error("the writer was still running after SendMultipart returned")
	}
}

func TestSendMultipartKeepsTheDeadlineWhenTheServerStopsReading(t *testing.T) {
	// Released by the test rather than by r.Context(): a server that has not
	// read the body to its end does not watch the connection, so the client
	// going away never cancels that context, and closing the server would wait
	// on this handler for good.
	release := make(chan struct{})
	client := newTestClient(t, nil, func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	})
	defer close(release)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	// The writer wraps whatever the pipe answers, as a real one does with an
	// io.Copy error, so a pipe closed under the deadline reaches here as an
	// error of its own that must not displace the deadline.
	write := func(mw *multipart.Writer) error {
		if err := writeFilePart("big.bin", make([]byte, 16<<20))(mw); err != nil {
			return fmt.Errorf("copy big.bin: %w", err)
		}
		return nil
	}

	err := client.SendMultipart(ctx, http.MethodPost, "/rest/upload", nil, write, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("SendMultipart() error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	// The server never had the whole body, so it cannot have acted on it.
	if errors.Is(err, ErrSentInFull) {
		t.Errorf("SendMultipart() error = %v, want no ErrSentInFull for a body cut short", err)
	}
}

// answerWithATruncatedBody answers status with a body shorter than the
// Content-Length it declares, after reading the request body to its end, so
// the client has the whole status line but fails reading what follows it.
func answerWithATruncatedBody(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, "short")
	}
}

// smallPayload is a Send body small enough to sit in the socket buffers, so a
// server that reads it to its end is what the transport's record sees.
var smallPayload = map[string]string{"title": "x"}

// bodySenders are the two ways a request with a body is sent. Both go through
// the one place that decides whether a failure is marked, so every marking
// case runs against each.
var bodySenders = []struct {
	name string
	send func(ctx context.Context, c *Client, out any) error
	// replayable is whether the body can be sent again, which a 307 or 308
	// needs: a streamed body cannot, so the client hands that 3xx back.
	replayable bool
}{
	{"Send", func(ctx context.Context, c *Client, out any) error {
		return c.Send(ctx, http.MethodPost, "/rest/create", smallPayload, out)
	}, true},
	{"SendMultipart", func(ctx context.Context, c *Client, out any) error {
		return c.SendMultipart(ctx, http.MethodPost, "/rest/upload", nil, writeFilePart("a.png", fileBytes), out)
	}, false},
}

// TestOnlyAClientErrorSettlesAFullySentRequest covers the
// responses to a body the server read to its end: a 4xx refuses the request,
// while no 5xx definition says whether the request was carried out.
func TestOnlyAClientErrorSettlesAFullySentRequest(t *testing.T) {
	for _, s := range bodySenders {
		for _, tc := range []struct {
			status  int
			unknown bool
		}{
			{http.StatusBadRequest, false},
			{http.StatusNotFound, false},
			{http.StatusInternalServerError, true},
			{http.StatusBadGateway, true},
			{http.StatusServiceUnavailable, true},
			{http.StatusGatewayTimeout, true},
		} {
			t.Run(s.name+"/"+strconv.Itoa(tc.status), func(t *testing.T) {
				client := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					w.WriteHeader(tc.status)
				})

				err := s.send(t.Context(), client, nil)

				if apiErr, ok := errors.AsType[*APIError](err); !ok || apiErr.Status != tc.status {
					t.Fatalf("%s() error = %v, want an *APIError with status %d", s.name, err, tc.status)
				}
				if got := errors.Is(err, ErrSentInFull); got != tc.unknown {
					t.Errorf("errors.Is(err, ErrSentInFull) = %v, want %v for %v", got, tc.unknown, err)
				}
			})
		}
	}
}

// TestTheStatusAloneDecidesWhenTheErrorBodyIsLost covers a response whose
// status arrived but whose body could not be read: a 4xx still refuses the
// request, and a 5xx still leaves it unknown.
func TestTheStatusAloneDecidesWhenTheErrorBodyIsLost(t *testing.T) {
	for _, s := range bodySenders {
		for _, tc := range []struct {
			status  int
			unknown bool
		}{
			{http.StatusBadRequest, false},
			{http.StatusBadGateway, true},
		} {
			t.Run(s.name+"/"+strconv.Itoa(tc.status), func(t *testing.T) {
				client := newTestClient(t, nil, answerWithATruncatedBody(tc.status))

				err := s.send(t.Context(), client, nil)

				if err == nil {
					t.Fatalf("%s() error = nil, want the failure", s.name)
				}
				// An *APIError would mean the body was read after all, and the
				// case would be the plain status one above.
				if _, ok := errors.AsType[*APIError](err); ok {
					t.Fatalf("%s() error = %v, want the failed read of the error body", s.name, err)
				}
				if got := errors.Is(err, ErrSentInFull); got != tc.unknown {
					t.Errorf("errors.Is(err, ErrSentInFull) = %v, want %v for %v", got, tc.unknown, err)
				}
			})
		}
	}
}

// TestARedirectIsJudgedByTheRequestItLeadsTo covers a 3xx the client follows.
// A 301, 302 or 303 turns the request into a GET without its body: the 3xx
// settled the original, so what the GET meets marks nothing. A 307 or 308
// sends the body again, so the outcome of that resend decides; one whose body
// cannot be sent again gets the 307 or 308 back unfollowed, which settles it.
func TestARedirectIsJudgedByTheRequestItLeadsTo(t *testing.T) {
	for _, s := range bodySenders {
		for _, tc := range []struct {
			redirect int
			resends  bool
		}{
			{http.StatusMovedPermanently, false},
			{http.StatusFound, false},
			{http.StatusSeeOther, false},
			{http.StatusTemporaryRedirect, true},
			{http.StatusPermanentRedirect, true},
		} {
			t.Run(s.name+"/"+strconv.Itoa(tc.redirect), func(t *testing.T) {
				wantStatus, unknown := http.StatusBadGateway, tc.resends
				if tc.resends && !s.replayable {
					wantStatus, unknown = tc.redirect, false
				}

				client := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					if !strings.HasSuffix(r.URL.Path, "/moved") {
						w.Header().Set("Location", "/api/moved")
						w.WriteHeader(tc.redirect)
						return
					}
					w.WriteHeader(http.StatusBadGateway)
				})

				err := s.send(t.Context(), client, nil)

				if apiErr, ok := errors.AsType[*APIError](err); !ok || apiErr.Status != wantStatus {
					t.Fatalf("%s() error = %v, want an *APIError with status %d", s.name, err, wantStatus)
				}
				if got := errors.Is(err, ErrSentInFull); got != unknown {
					t.Errorf("errors.Is(err, ErrSentInFull) = %v, want %v for %v", got, unknown, err)
				}
			})
		}
	}
}

// TestARedirectLoopIsNotMarked covers a client that gives up following
// redirects: the last answer it had was a 3xx, which settled the request.
func TestARedirectLoopIsNotMarked(t *testing.T) {
	for _, s := range bodySenders {
		t.Run(s.name, func(t *testing.T) {
			client := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Location", r.URL.Path)
				w.WriteHeader(http.StatusFound)
			})

			err := s.send(t.Context(), client, nil)

			if err == nil || errors.Is(err, ErrSentInFull) {
				t.Errorf("%s() error = %v, want the redirect failure without ErrSentInFull", s.name, err)
			}
		})
	}
}

func TestAnUnreadableSuccessIsMarkedAsSentInFull(t *testing.T) {
	for _, s := range bodySenders {
		t.Run(s.name, func(t *testing.T) {
			client := newTestClient(t, nil, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				_, _ = fmt.Fprint(w, "<html>not json</html>")
			})

			var out struct{}
			err := s.send(t.Context(), client, &out)

			// A 2xx means the server accepted the request; only its answer is lost.
			if !errors.Is(err, ErrSentInFull) {
				t.Errorf("%s() error = %v, want ErrSentInFull for a 2xx that could not be parsed", s.name, err)
			}
		})
	}
}

func TestADeadlineAfterTheWholeBodyWasSentIsMarked(t *testing.T) {
	for _, s := range bodySenders {
		t.Run(s.name, func(t *testing.T) {
			release := make(chan struct{})
			client := newTestClient(t, nil, func(_ http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				<-release
			})
			defer close(release)

			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()

			err := s.send(ctx, client, nil)
			if !errors.Is(err, ErrSentInFull) {
				t.Errorf("%s() error = %v, want ErrSentInFull: the server had the whole body", s.name, err)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("%s() error = %v, want it to still wrap context.DeadlineExceeded", s.name, err)
			}
		})
	}
}

func TestSendDoesNotMarkADeadlineBeforeTheBodyWasSent(t *testing.T) {
	// Released by the test: a server that has not read the body to its end
	// never sees the client go away.
	release := make(chan struct{})
	client := newTestClient(t, nil, func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	})
	defer close(release)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	// Far past what the socket buffers absorb, so the body cannot all be
	// handed over while the server stops reading.
	payload := map[string]string{"body": strings.Repeat("x", 16<<20)}

	err := client.Send(ctx, http.MethodPost, "/rest/create", payload, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Send() error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if errors.Is(err, ErrSentInFull) {
		t.Errorf("Send() error = %v, want no ErrSentInFull for a body cut short", err)
	}
}

// TestGetIsNeverMarked covers the one method that changes nothing: whatever
// happened to it, running it again cannot repeat an effect.
func TestGetIsNeverMarked(t *testing.T) {
	t.Run("5xx", func(t *testing.T) {
		client := newTestClient(t, nil, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		})

		err := client.Get(t.Context(), "/rest/page", nil, nil)
		if err == nil || errors.Is(err, ErrSentInFull) {
			t.Errorf("Get() error = %v, want a failure without ErrSentInFull", err)
		}
	})

	t.Run("deadline", func(t *testing.T) {
		release := make(chan struct{})
		client := newTestClient(t, nil, func(_ http.ResponseWriter, _ *http.Request) {
			<-release
		})
		defer close(release)

		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()

		err := client.Get(ctx, "/rest/page", nil, nil)
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrSentInFull) {
			t.Errorf("Get() error = %v, want the deadline without ErrSentInFull", err)
		}
	})
}

// Package pageref parses the ways a caller can name a Confluence page —
// the URL as copied from the browser, the short link from the Share dialog,
// a bare page ID, or a space's URL for its homepage — and builds page URLs
// from the pieces the API returns.
package pageref

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Ref is a parsed page reference. Host is empty when the caller passed a
// bare page ID, in which case there is nothing to auto-select a profile
// from and the default profile applies.
type Ref struct {
	Host   string
	Target Target
}

// Target is what a reference names: a PageID, or a SpaceHomepage whose ID
// is not known yet. The unexported method keeps the set closed, so a type
// switch over the two is exhaustive.
type Target interface {
	isTarget()
}

// PageID names a page by its ID.
type PageID string

// SpaceHomepage names the homepage of the space with Key. It deliberately
// carries no page ID: the only way to one is looking the key up, so a
// reference that knows just the key cannot be sent as a page request.
type SpaceHomepage struct {
	Key string
}

func (PageID) isTarget()        {}
func (SpaceHomepage) isTarget() {}

var (
	pageIDPattern = regexp.MustCompile(`^[0-9]+$`)

	// A page's path, capturing the space key and then the id. The key may
	// be a personal space (~accountid) and the title slug is optional.
	// The browser URL prefixes this with /wiki; the relative web link the
	// API returns for a page does not, so both are built from one grammar
	// to keep them from drifting apart.
	pagePath = `/spaces/([^/]+)/pages/([0-9]+)(?:/.*)?$`

	pageURLPattern = regexp.MustCompile(`^/wiki` + pagePath)
	webUIPattern   = regexp.MustCompile(`^` + pagePath)

	// A space's overview, or the space URL, both of which open its
	// homepage. Matched against the decoded path, so a personal space's
	// key is captured as ~accountid whether or not the `~` was escaped.
	spaceHomepagePattern = regexp.MustCompile(`^/wiki/spaces/([^/]+)(?:/overview)?$`)

	// Confluence writes `/` as `-` and `+` as `_`, the reverse of the RFC
	// 4648 URL-safe pairing — so base64.URLEncoding decodes a token to the
	// wrong page rather than rejecting it.
	tinyEncoding = base64.NewEncoding(
		"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-").WithPadding(base64.NoPadding)
)

const (
	// A short link, as the Share dialog copies it.
	tinyURLPrefix = "/wiki/x/"

	// The `A` run decodeTinyToken puts back, as long as a whole token.
	tinyTokenPad = "AAAAAAAAAAA"
)

// Parse accepts a page URL, a short link, a bare page ID, or a space's
// overview or space URL, which name the space's homepage. It sends no
// request: a space URL without a homepageId yields a SpaceHomepage, and
// turning that into a page ID is left to the caller.
func Parse(arg string) (Ref, error) {
	arg = strings.TrimSpace(arg)

	if pageIDPattern.MatchString(arg) {
		return Ref{Target: PageID(arg)}, nil
	}

	u, err := url.Parse(arg)
	if err != nil || u.Host == "" {
		return Ref{}, unrecognizedError(arg)
	}

	path := strings.TrimSuffix(u.Path, "/")
	// An empty token is left to fall through to unrecognizedError, which
	// lists the forms Parse does accept.
	if token, ok := strings.CutPrefix(path, tinyURLPrefix); ok && token != "" {
		pageID, ok := decodeTinyToken(token)
		if !ok {
			return Ref{}, fmt.Errorf("%q is a Confluence short link whose token does not decode to a page ID; "+
				"open it in a browser and pass the full page URL instead", arg)
		}
		return Ref{Host: u.Host, Target: PageID(pageID)}, nil
	}
	if match := pageURLPattern.FindStringSubmatch(path); match != nil {
		return Ref{Host: u.Host, Target: PageID(match[2])}, nil
	}
	// The page-ID query form, which is what `atlio confluence read` records in
	// a sidecar when the API returns no web link.
	if strings.HasSuffix(path, "/pages/viewpage.action") {
		if id := u.Query().Get("pageId"); pageIDPattern.MatchString(id) {
			return Ref{Host: u.Host, Target: PageID(id)}, nil
		}
	}
	if match := spaceHomepagePattern.FindStringSubmatch(path); match != nil {
		// The sidebar's space link carries the homepage's ID; anything but
		// a number there is ignored in favour of the key.
		if id := u.Query().Get("homepageId"); pageIDPattern.MatchString(id) {
			return Ref{Host: u.Host, Target: PageID(id)}, nil
		}
		return Ref{Host: u.Host, Target: SpaceHomepage{Key: match[1]}}, nil
	}

	return Ref{}, unrecognizedError(arg)
}

// decodeTinyToken turns the token of a short link back into its page ID.
//
// Confluence packs the ID little-endian, encodes it in tinyEncoding's
// alphabet, then trims the padding and the trailing run of `A` the ID's
// leading zero bytes produce; putting that run back leaves eight bytes.
// Atlassian's own snippet packs 32 bits, but Cloud page IDs are not promised
// to fit in them, so the full width is read back.
func decodeTinyToken(token string) (string, bool) {
	if len(token) > len(tinyTokenPad) {
		return "", false
	}

	// base64 skips `\r` and `\n` rather than rejecting them, which is what
	// the width check catches.
	packed, err := tinyEncoding.DecodeString(token + tinyTokenPad[len(token):])
	if err != nil || len(packed) < 8 {
		return "", false
	}

	// No page is 0, and the short link's advice beats a guaranteed 404.
	pageID := binary.LittleEndian.Uint64(packed)
	if pageID == 0 {
		return "", false
	}
	return strconv.FormatUint(pageID, 10), true
}

func unrecognizedError(arg string) error {
	return fmt.Errorf("%q is not a page URL or page ID; pass a page ID (e.g. 123456), "+
		"a page URL (e.g. https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Title), "+
		"or a space URL for its homepage (e.g. https://example.atlassian.net/wiki/spaces/DEV/overview)", arg)
}

// PageURL builds a page's absolute URL from the Confluence base URL
// (<site>/wiki) and the relative web link the API returned. When the API
// returned no link, it falls back to the page-ID query form, which needs no
// space key.
func PageURL(base, webui, pageID string) string {
	base = strings.TrimSuffix(base, "/")
	if webui != "" {
		return base + webui
	}
	return base + "/pages/viewpage.action?pageId=" + url.QueryEscape(pageID)
}

// ChildPageURL builds the URL of a page sitting in the same space as the page
// parentWebUI links to, which every child of that page does. Child listings
// carry no web link of their own, so the parent's is what they are assembled
// from.
//
// When parentWebUI yields no space key it falls back to the page-ID query
// form, which needs none: a URL built from an empty key is a 404, so the key
// never reaches spacePageURL unless it is real.
func ChildPageURL(base, parentWebUI, childID string) string {
	spaceKey := SpaceKeyOf(parentWebUI)
	if spaceKey == "" {
		return PageURL(base, "", childID)
	}
	return SpacePageURL(base, spaceKey, childID)
}

// SpaceKeyOf returns the space key embedded in the relative web link the API
// returns for a page, or "" when the link is empty or not in that shape.
// Every key the API has been observed to emit — including the ~accountid
// form of a personal space — is made of characters that SpacePageURL's path
// escaping leaves alone, so the captured segment is returned undecoded.
func SpaceKeyOf(webui string) string {
	match := webUIPattern.FindStringSubmatch(webui)
	if match == nil {
		return ""
	}
	return match[1]
}

// SpacePageURL builds a page's URL from the Confluence base URL
// (<site>/wiki) and the page's space key. The result is in the form Parse
// accepts, so a link built with it can be fed straight back to
// `atlio confluence read`.
func SpacePageURL(base, spaceKey, pageID string) string {
	return strings.TrimSuffix(base, "/") + "/spaces/" + url.PathEscape(spaceKey) + "/pages/" + url.PathEscape(pageID)
}

// HostOf returns the host of a stored page URL, or "" if it has none. It is
// how `confluence update` picks a profile from the URL its sidecar recorded.
func HostOf(pageURL string) string {
	u, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	return u.Host
}

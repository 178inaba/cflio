// Package pageref parses the ways a caller can name a Confluence page —
// the URL as copied from the browser, the short link from the Share dialog,
// or a bare page ID — and builds page URLs from the pieces the API returns.
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
	PageID string
	Host   string
}

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

	// A short link, as the Share dialog copies it. The token encodes the
	// page ID locally, so decodeTinyToken resolves it without a request;
	// one that will not decode keeps its own message rather than falling
	// through to "unrecognized".
	tinyURLPattern = regexp.MustCompile(`^/wiki/x/(.+)$`)

	// The alphabet a token can be written in once Confluence's substitution
	// is undone, and the length a decodable one cannot exceed: eleven
	// characters carry the eight bytes of a page ID.
	tinyTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,` + strconv.Itoa(tinyTokenLen) + `}$`)
)

const tinyTokenLen = 11

// Parse accepts a page URL or a bare page ID.
func Parse(arg string) (Ref, error) {
	arg = strings.TrimSpace(arg)

	if pageIDPattern.MatchString(arg) {
		return Ref{PageID: arg}, nil
	}

	u, err := url.Parse(arg)
	if err != nil || u.Host == "" {
		return Ref{}, unrecognizedError(arg)
	}

	path := strings.TrimSuffix(u.Path, "/")
	if match := tinyURLPattern.FindStringSubmatch(path); match != nil {
		pageID, ok := decodeTinyToken(match[1])
		if !ok {
			return Ref{}, fmt.Errorf("%q is a Confluence short link whose token does not decode to a page ID; "+
				"open it in a browser and pass the full page URL instead", arg)
		}
		return Ref{PageID: pageID, Host: u.Host}, nil
	}
	if match := pageURLPattern.FindStringSubmatch(path); match != nil {
		return Ref{PageID: match[2], Host: u.Host}, nil
	}
	// The page-ID query form, which is what `cflio read` records in a
	// sidecar when the API returns no web link.
	if strings.HasSuffix(path, "/pages/viewpage.action") {
		if id := u.Query().Get("pageId"); pageIDPattern.MatchString(id) {
			return Ref{PageID: id, Host: u.Host}, nil
		}
	}

	return Ref{}, unrecognizedError(arg)
}

// decodeTinyToken turns the token of a short link back into the page ID it
// was made from, reporting whether it decoded at all.
//
// Confluence builds the token by packing the page ID little-endian, encoding
// it as standard base64 and then substituting `/` and `+` — which is not the
// RFC 4648 URL-safe alphabet, so base64.URLEncoding decodes it wrong — before
// trimming the padding and the trailing run of `A` the leading zero bytes of
// the ID produce. Undoing that leaves eight bytes to read as a uint64.
// Atlassian's own snippet packs 32 bits, but Cloud page IDs are not promised
// to fit in them, so the full width is read back.
func decodeTinyToken(token string) (string, bool) {
	if !tinyTokenPattern.MatchString(token) {
		return "", false
	}

	std := strings.NewReplacer("-", "/", "_", "+").Replace(token)
	packed, err := base64.StdEncoding.DecodeString(std + strings.Repeat("A", tinyTokenLen-len(std)) + "=")
	if err != nil {
		return "", false
	}

	// No page is 0, so a token that decodes to it is a token that did not
	// decode: the short link's advice beats the 404 the ID would earn.
	pageID := binary.LittleEndian.Uint64(packed)
	if pageID == 0 {
		return "", false
	}
	return strconv.FormatUint(pageID, 10), true
}

func unrecognizedError(arg string) error {
	return fmt.Errorf("%q is not a page URL or page ID; pass either a page ID (e.g. 123456) "+
		"or a page URL (e.g. https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Title)", arg)
}

// PageURL builds a page's absolute URL from the site base and the relative
// web link the API returned. When the API returned no link, it falls back
// to the page-ID query form, which needs no space key.
func PageURL(site, webui, pageID string) string {
	site = strings.TrimSuffix(site, "/")
	if webui != "" {
		return site + webui
	}
	return site + "/pages/viewpage.action?pageId=" + url.QueryEscape(pageID)
}

// ChildPageURL builds the URL of a page sitting in the same space as the page
// parentWebUI links to, which every child of that page does. Child listings
// carry no web link of their own, so the parent's is what they are assembled
// from.
//
// When parentWebUI yields no space key it falls back to the page-ID query
// form, which needs none: a URL built from an empty key is a 404, so the key
// never reaches spacePageURL unless it is real.
func ChildPageURL(site, parentWebUI, childID string) string {
	spaceKey := SpaceKeyOf(parentWebUI)
	if spaceKey == "" {
		return PageURL(site, "", childID)
	}
	return SpacePageURL(site, spaceKey, childID)
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

// SpacePageURL builds a page's URL from its space key. The result is in the
// form Parse accepts, so a link built with it can be fed straight back to
// `cflio read`.
func SpacePageURL(site, spaceKey, pageID string) string {
	return strings.TrimSuffix(site, "/") + "/spaces/" + url.PathEscape(spaceKey) + "/pages/" + url.PathEscape(pageID)
}

// HostOf returns the host of a stored page URL, or "" if it has none. It is
// how `update` picks a profile from the URL its sidecar recorded.
func HostOf(pageURL string) string {
	u, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	return u.Host
}

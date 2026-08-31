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

// Parse accepts a page URL, a short link, or a bare page ID.
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
	// An empty token is left to fall through to unrecognizedError, which
	// lists the forms Parse does accept.
	if token, ok := strings.CutPrefix(path, tinyURLPrefix); ok && token != "" {
		pageID, ok := decodeTinyToken(token)
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

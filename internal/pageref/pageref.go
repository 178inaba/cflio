// Package pageref parses the ways a caller can name a Confluence page —
// the URL as copied from the browser, or a bare page ID — and builds page
// URLs from the pieces the API returns.
package pageref

import (
	"fmt"
	"net/url"
	"regexp"
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

	// The browser URL for a page: /wiki/spaces/<KEY>/pages/<id>[/<title>].
	// The space key may be a personal space (~accountid), and the title
	// slug is optional.
	pageURLPattern = regexp.MustCompile(`^/wiki/spaces/[^/]+/pages/([0-9]+)(?:/.*)?$`)

	// Short links resolve server-side to a page; resolving them is out of
	// scope, so they get their own message rather than "unrecognized".
	tinyURLPattern = regexp.MustCompile(`^/wiki/x/`)
)

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
	if tinyURLPattern.MatchString(path) {
		return Ref{}, fmt.Errorf(
			"%q is a Confluence short link; open it in a browser and pass the full page URL instead", arg)
	}
	if match := pageURLPattern.FindStringSubmatch(path); match != nil {
		return Ref{PageID: match[1], Host: u.Host}, nil
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

// SpacePageURL builds a page's URL from its space key. Child listings carry
// no web link of their own, so their URLs are assembled from the parent's
// space key, which every child shares.
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

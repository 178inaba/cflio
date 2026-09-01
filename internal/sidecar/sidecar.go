// Package sidecar reads and writes the metadata file that `cflio read`
// leaves next to a downloaded page body. The sidecar is what makes `update`
// self-describing: the page it targets, the profile it authenticates with
// and the version it locks against all come from here, so there is no way
// to write to a page that was never read.
package sidecar

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/url"
	"os"
)

// Suffix is appended to the body file's path to get the sidecar's path.
const Suffix = ".meta.json"

// Meta is the sidecar's contents. Every field but Subtype is required: page
// ID and version drive the optimistic lock, title and status are mandatory in
// the update payload, and the page URL is what selects the profile.
type Meta struct {
	PageID  string `json:"page_id"`
	Version int    `json:"version"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	PageURL string `json:"page_url"`
	// Subtype is the page's v2 subtype — "live" for a live doc, empty for a
	// classic page. It is a pointer, and the one optional field, because
	// three states have to be told apart: a sidecar written before cflio
	// recorded it (nil, the key absent), a page the API reported no subtype
	// for (empty, the key present), and a live doc. A plain string would
	// collapse the first two, and they call for opposite answers — one is a
	// reason to ask for a fresh read, the other is a settled "not a live
	// doc". Nothing requires it: a sidecar already on disk has to keep
	// working with `update`.
	//
	// omitzero, not omitempty: v2 reads omitempty as "omit what encodes as an
	// empty JSON value", which covers a pointer to "" and would collapse the
	// very two states the pointer is here to keep apart.
	Subtype *string `json:"subtype,omitzero"`
}

// liveSubtype is the subtype the v2 API reports for a live doc. Every other
// page reports an empty one.
const liveSubtype = "live"

// LiveDoc reports whether the page is a live doc, and whether the sidecar
// knows: a sidecar written before cflio recorded the subtype answers known =
// false, which is a different thing from "not a live doc" and calls for a
// different answer — ask for a fresh read rather than act on a guess.
func (m Meta) LiveDoc() (live, known bool) {
	if m.Subtype == nil {
		return false, false
	}
	return *m.Subtype == liveSubtype, true
}

// Path returns the sidecar path for a body file.
func Path(bodyPath string) string {
	return bodyPath + Suffix
}

// Load reads and validates the sidecar next to bodyPath.
func Load(bodyPath string) (Meta, error) {
	path := Path(bodyPath)

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// --markdown is named because it is the reachable way to end up
		// here: that mode writes no sidecar by design, and an instruction to
		// just "run `cflio read`" is satisfied by re-running the same
		// Markdown read, which lands the caller back in this error.
		return Meta{}, fmt.Errorf("no sidecar at %s: the file was not produced by `cflio read`, "+
			"or it was read with --markdown, whose output carries no sidecar and cannot be written "+
			"back; run `cflio read` on the page without --markdown, edit that file, and update it", path)
	}
	if err != nil {
		return Meta{}, fmt.Errorf("read sidecar %s: %w", path, err)
	}

	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return Meta{}, fmt.Errorf("parse sidecar %s: %w", path, err)
	}
	if err := meta.validate(path); err != nil {
		return Meta{}, err
	}
	return meta, nil
}

// validate rejects a sidecar that would produce a malformed update request
// or a lock against nothing, rather than letting it fail later as an opaque
// API error.
func (m Meta) validate(path string) error {
	missing := make([]string, 0, 5)
	if m.PageID == "" {
		missing = append(missing, "page_id")
	}
	if m.Version <= 0 {
		missing = append(missing, "version")
	}
	if m.Title == "" {
		missing = append(missing, "title")
	}
	if m.Status == "" {
		missing = append(missing, "status")
	}
	// The host, not just the string, has to be there: it is what selects the
	// profile, and a host-less URL would silently fall through to the default
	// profile — the very fallback that is meant not to exist.
	if u, err := url.Parse(m.PageURL); err != nil || u.Host == "" {
		missing = append(missing, "page_url")
	}
	if len(missing) > 0 {
		return fmt.Errorf("sidecar %s is missing or has an unusable %v; re-run `cflio read` to regenerate it",
			path, missing)
	}
	return nil
}

// Remove deletes the sidecar next to bodyPath if one exists. A missing
// sidecar is not an error, so callers can use this to guarantee that no
// stale metadata survives alongside a body they are about to replace.
func Remove(bodyPath string) error {
	path := Path(bodyPath)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale sidecar %s: %w", path, err)
	}
	return nil
}

// Write creates or replaces the sidecar next to bodyPath.
func Write(bodyPath string, meta Meta) error {
	path := Path(bodyPath)

	// Deterministic does nothing for a struct, and is here so that the day
	// Meta grows a map field nobody has to notice: v2 leaves map members in
	// runtime order, and a reshuffling sidecar would fail no test.
	data, err := json.Marshal(meta, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		return fmt.Errorf("encode sidecar: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write sidecar %s: %w", path, err)
	}
	return nil
}

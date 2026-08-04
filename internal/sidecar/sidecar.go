// Package sidecar reads and writes the metadata file that `cflio read`
// leaves next to a downloaded page body. The sidecar is what makes `update`
// self-describing: the page it targets, the profile it authenticates with
// and the version it locks against all come from here, so there is no way
// to write to a page that was never read.
package sidecar

import (
	"encoding/json"
	"fmt"
	"os"
)

// Suffix is appended to the body file's path to get the sidecar's path.
const Suffix = ".meta.json"

// Meta is the sidecar's contents. Every field is required: page ID and
// version drive the optimistic lock, title and status are mandatory in the
// update payload, and the page URL is what selects the profile.
type Meta struct {
	PageID  string `json:"page_id"`
	Version int    `json:"version"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	PageURL string `json:"page_url"`
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
		return Meta{}, fmt.Errorf("no sidecar at %s: run `cflio read` on the page first, "+
			"then edit the downloaded file and update it", path)
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
	if m.PageURL == "" {
		missing = append(missing, "page_url")
	}
	if len(missing) > 0 {
		return fmt.Errorf("sidecar %s is missing %v; re-run `cflio read` to regenerate it", path, missing)
	}
	return nil
}

// Write creates or replaces the sidecar next to bodyPath.
func Write(bodyPath string, meta Meta) error {
	path := Path(bodyPath)

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sidecar: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write sidecar %s: %w", path, err)
	}
	return nil
}

package stclient

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// FileEntry is the gomobile-safe description of one file or directory.
type FileEntry struct {
	Name     string // basename
	Path     string // full relative path within the folder
	Size     int64
	Modified int64 // Unix seconds
	IsDir    bool
}

// ListFolder returns a JSON array of FileEntry for all non-deleted items in
// folderID. Call WaitForIndex first. An empty folder returns "[]", not an
// error (only "folder not in index" produces an error).
func (c *Client) ListFolder(folderID string) ([]byte, error) {
	_, model := c.snapshot()
	if model == nil {
		return nil, fmt.Errorf("not connected")
	}
	if !model.folderKnown(folderID) {
		return nil, fmt.Errorf("index for folder %q not yet received", folderID)
	}
	files := model.files(folderID)

	entries := make([]FileEntry, 0, len(files))
	for _, f := range files {
		if f.IsDeleted() || f.IsInvalid() {
			continue
		}
		entries = append(entries, FileEntry{
			Name:     filepath.Base(f.Name),
			Path:     f.Name,
			Size:     f.Size,
			Modified: f.ModifiedS,
			IsDir:    f.IsDirectory(),
		})
	}
	return json.Marshal(entries)
}

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
// folderID. Call WaitForIndex first.
func (c *Client) ListFolder(folderID string) ([]byte, error) {
	if c.model == nil {
		return nil, fmt.Errorf("not connected")
	}
	files := c.model.files(folderID)
	if files == nil {
		return nil, fmt.Errorf("index for folder %q not yet received", folderID)
	}

	entries := make([]FileEntry, 0, len(files))
	for _, f := range files {
		if f.Deleted || f.Invalid {
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

package stclient

import (
	"context"
	"fmt"
	"os"
)

// FetchProgress receives callbacks during a file download.
// All methods are called from a background goroutine.
// gomobile will generate a Java interface from this.
type FetchProgress interface {
	OnProgress(downloaded, total int64)
	OnDone(localPath string)
	OnError(msg string)
}

// FetchFile downloads filePath from folderID and writes it to destPath.
// Blocks are requested one at a time in order; destPath is removed on error.
// Pass nil for progress if you don't need callbacks.
func (c *Client) FetchFile(folderID, filePath, destPath string, progress FetchProgress) error {
	if c.conn == nil {
		return fmt.Errorf("not connected")
	}

	files := c.model.files(folderID)
	idx := -1
	for i, f := range files {
		if f.Name == filePath {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("file %q not in index for folder %q", filePath, folderID)
	}
	target := files[idx]

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", destPath, err)
	}
	defer f.Close()

	ctx := context.Background()
	var downloaded int64

	for blockNo, b := range target.Blocks {
		data, err := c.conn.Request(ctx, folderID, filePath, blockNo, b.Offset, int(b.Size), b.Hash, b.WeakHash, false)
		if err != nil {
			os.Remove(destPath)
			if progress != nil {
				progress.OnError(fmt.Sprintf("block %d: %v", blockNo, err))
			}
			return fmt.Errorf("request block %d: %w", blockNo, err)
		}
		if _, err := f.Write(data); err != nil {
			os.Remove(destPath)
			return fmt.Errorf("write block %d: %w", blockNo, err)
		}
		downloaded += int64(b.Size)
		if progress != nil {
			progress.OnProgress(downloaded, target.Size)
		}
	}

	if progress != nil {
		progress.OnDone(destPath)
	}
	return nil
}

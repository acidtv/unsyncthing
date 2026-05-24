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
	conn, model := c.snapshot()
	if conn == nil || model == nil {
		return fmt.Errorf("not connected")
	}

	files := model.files(folderID)
	if files == nil {
		return fmt.Errorf("no index for folder %q", folderID)
	}
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

	// Refuse non-file entries up front — for symlinks and directories, Blocks
	// is empty, so the original code would create a zero-byte file and call
	// OnDone, silently misrepresenting what got downloaded.
	if target.IsDirectory() {
		return fmt.Errorf("%q is a directory, not a file", filePath)
	}
	if target.IsSymlink() {
		return fmt.Errorf("%q is a symlink (not supported)", filePath)
	}
	if target.Size > 0 && len(target.Blocks) == 0 {
		return fmt.Errorf("%q has no blocks in index", filePath)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", destPath, err)
	}
	closed := false
	cleanup := func() {
		if !closed {
			_ = f.Close()
			closed = true
		}
		_ = os.Remove(destPath)
	}

	ctx := context.Background()
	var downloaded int64

	for blockNo, b := range target.Blocks {
		data, err := conn.Request(ctx, folderID, filePath, blockNo, b.Offset, int(b.Size), b.Hash, b.WeakHash, false)
		if err != nil {
			cleanup()
			if progress != nil {
				progress.OnError(fmt.Sprintf("block %d: %v", blockNo, err))
			}
			return fmt.Errorf("request block %d: %w", blockNo, err)
		}
		if _, err := f.Write(data); err != nil {
			cleanup()
			return fmt.Errorf("write block %d: %w", blockNo, err)
		}
		downloaded += int64(len(data))
		if progress != nil {
			progress.OnProgress(downloaded, target.Size)
		}
	}

	// Flush before reporting success so a process death after OnDone doesn't
	// leave a truncated file masquerading as complete.
	if err := f.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync %s: %w", destPath, err)
	}
	if err := f.Close(); err != nil {
		closed = true
		_ = os.Remove(destPath)
		return fmt.Errorf("close %s: %w", destPath, err)
	}
	closed = true

	if progress != nil {
		progress.OnDone(destPath)
	}
	return nil
}

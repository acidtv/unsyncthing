package stclient

import (
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
	var target *struct {
		size   int64
		blocks []blockRef
	}
	for _, f := range files {
		if f.Name == filePath {
			blocks := make([]blockRef, len(f.Blocks))
			for i, b := range f.Blocks {
				blocks[i] = blockRef{
					offset:   b.Offset,
					size:     int(b.Size),
					hash:     b.Hash,
					weakHash: b.WeakHash,
				}
			}
			target = &struct {
				size   int64
				blocks []blockRef
			}{size: f.Size, blocks: blocks}
			break
		}
	}
	if target == nil {
		return fmt.Errorf("file %q not in index for folder %q", filePath, folderID)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", destPath, err)
	}
	defer f.Close()

	var downloaded int64
	for i, b := range target.blocks {
		// NOTE: Connection.Request signature matches syncthing ≥ v1.20.
		// Returns ([]byte, error); adjust if your version differs.
		data, err := c.conn.Request(folderID, filePath, b.offset, b.size, b.hash, b.weakHash, false)
		if err != nil {
			os.Remove(destPath)
			if progress != nil {
				progress.OnError(fmt.Sprintf("block %d: %v", i, err))
			}
			return fmt.Errorf("request block %d: %w", i, err)
		}
		if _, err := f.Write(data); err != nil {
			os.Remove(destPath)
			return fmt.Errorf("write block %d: %w", i, err)
		}
		downloaded += int64(b.size)
		if progress != nil {
			progress.OnProgress(downloaded, target.size)
		}
	}

	if progress != nil {
		progress.OnDone(destPath)
	}
	return nil
}

type blockRef struct {
	offset   int64
	size     int
	hash     []byte
	weakHash uint32
}

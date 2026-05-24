package stclient

import (
	"fmt"
	"sync"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"
)

// peerModel implements protocol.Model. It stores the file index received from
// the peer and stubs out methods we don't need as a read-only client.
type peerModel struct {
	mu         sync.RWMutex
	folders    map[string][]protocol.FileInfo
	lastUpdate map[string]time.Time // when Index/IndexUpdate last touched the folder
}

func newPeerModel() *peerModel {
	return &peerModel{
		folders:    make(map[string][]protocol.FileInfo),
		lastUpdate: make(map[string]time.Time),
	}
}

func (m *peerModel) Index(_ protocol.Connection, idx *protocol.Index) error {
	m.mu.Lock()
	// Take a defensive copy — the protocol package may reuse buffers.
	files := make([]protocol.FileInfo, len(idx.Files))
	copy(files, idx.Files)
	m.folders[idx.Folder] = files
	m.lastUpdate[idx.Folder] = time.Now()
	m.mu.Unlock()
	return nil
}

func (m *peerModel) IndexUpdate(_ protocol.Connection, idxUp *protocol.IndexUpdate) error {
	m.mu.Lock()
	existing := m.folders[idxUp.Folder]
	byName := make(map[string]int, len(existing))
	for i, f := range existing {
		byName[f.Name] = i
	}
	for _, f := range idxUp.Files {
		if i, ok := byName[f.Name]; ok {
			existing[i] = f
		} else {
			existing = append(existing, f)
		}
	}
	m.folders[idxUp.Folder] = existing
	m.lastUpdate[idxUp.Folder] = time.Now()
	m.mu.Unlock()
	return nil
}

// Request is called when the peer asks us for data. We don't serve files.
func (m *peerModel) Request(_ protocol.Connection, _ *protocol.Request) (protocol.RequestResponse, error) {
	return nil, protocol.ErrNoSuchFile
}

func (m *peerModel) ClusterConfig(_ protocol.Connection, _ *protocol.ClusterConfig) error {
	return nil
}

func (m *peerModel) Closed(_ protocol.Connection, _ error) {}

func (m *peerModel) DownloadProgress(_ protocol.Connection, _ *protocol.DownloadProgress) error {
	return nil
}

// waitForIndex blocks until the index for folderID has settled (no
// Index/IndexUpdate for a short quiet period) or until timeout elapses.
// Returns nil with partial data if timeout fires after at least one update.
// Syncthing sends large initial syncs as one Index followed by many
// IndexUpdate batches, so we cannot return on the first message alone.
func (m *peerModel) waitForIndex(folderID string, timeout time.Duration) error {
	const (
		quiet      = 1 * time.Second
		pollPeriod = 100 * time.Millisecond
	)
	deadline := time.Now().Add(timeout)

	for {
		m.mu.RLock()
		last, haveAny := m.lastUpdate[folderID]
		m.mu.RUnlock()

		if haveAny && time.Since(last) >= quiet {
			return nil
		}
		if time.Now().After(deadline) {
			if haveAny {
				return nil // partial is better than nothing
			}
			return fmt.Errorf("timeout waiting for index of folder %q — "+
					"check that (1) this device has been added and accepted in the peer's Syncthing web UI, "+
					"(2) the folder is shared with this device on the peer, and "+
					"(3) the folder ID matches exactly", folderID)
		}
		time.Sleep(pollPeriod)
	}
}

// files returns a defensive copy of the file list for folderID. The copy
// prevents callers from observing mutations made by concurrent IndexUpdate
// calls (which mutate the backing array in place via existing[i] = f).
func (m *peerModel) files(folderID string) []protocol.FileInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	src := m.folders[folderID]
	if src == nil {
		return nil
	}
	out := make([]protocol.FileInfo, len(src))
	copy(out, src)
	return out
}

// folderKnown returns whether we have received any index data for folderID
// (even an empty Index counts as known).
func (m *peerModel) folderKnown(folderID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.folders[folderID]
	return ok
}

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
	mu      sync.RWMutex
	folders map[string][]protocol.FileInfo
	waiters map[string][]chan struct{}
	wanted  map[string]bool
}

func newPeerModel(folderIDs []string) *peerModel {
	wanted := make(map[string]bool, len(folderIDs))
	for _, id := range folderIDs {
		wanted[id] = true
	}
	return &peerModel{
		folders: make(map[string][]protocol.FileInfo),
		waiters: make(map[string][]chan struct{}),
		wanted:  wanted,
	}
}

func (m *peerModel) Index(conn protocol.Connection, folder string, files []protocol.FileInfo) error {
	m.mu.Lock()
	m.folders[folder] = files
	waiters := m.waiters[folder]
	delete(m.waiters, folder)
	m.mu.Unlock()
	for _, ch := range waiters {
		close(ch)
	}
	return nil
}

func (m *peerModel) IndexUpdate(conn protocol.Connection, folder string, files []protocol.FileInfo) error {
	m.mu.Lock()
	existing := m.folders[folder]
	byName := make(map[string]int, len(existing))
	for i, f := range existing {
		byName[f.Name] = i
	}
	for _, f := range files {
		if i, ok := byName[f.Name]; ok {
			existing[i] = f
		} else {
			existing = append(existing, f)
		}
	}
	m.folders[folder] = existing
	m.mu.Unlock()
	return nil
}

// Request is called when the peer asks us for data. We don't serve files.
func (m *peerModel) Request(
	conn protocol.Connection,
	folder, name string,
	blockNo int,
	hash []byte,
	weakHash uint32,
	fromTemporary bool,
	offset int64,
	size int32,
	buf []byte,
) (protocol.RequestResponse, error) {
	// NOTE: the exact signature here must match the protocol.Model interface
	// for the syncthing version in go.mod. Adjust if compilation fails.
	return nil, protocol.ErrNoSuchFile
}

func (m *peerModel) ClusterConfig(conn protocol.Connection, config protocol.ClusterConfig) error {
	return nil
}

func (m *peerModel) Closed(conn protocol.Connection, err error) {}

func (m *peerModel) DownloadProgress(
	conn protocol.Connection,
	folder string,
	updates []protocol.FileDownloadProgressUpdate,
) error {
	return nil
}

// waitForIndex blocks until the index for folderID arrives or timeout elapses.
func (m *peerModel) waitForIndex(folderID string, timeout time.Duration) error {
	m.mu.Lock()
	if _, ok := m.folders[folderID]; ok {
		m.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	m.waiters[folderID] = append(m.waiters[folderID], ch)
	m.mu.Unlock()

	select {
	case <-ch:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("timeout waiting for index of folder %q", folderID)
	}
}

func (m *peerModel) files(folderID string) []protocol.FileInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.folders[folderID]
}

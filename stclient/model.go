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
}

func newPeerModel(_ []string) *peerModel {
	return &peerModel{
		folders: make(map[string][]protocol.FileInfo),
		waiters: make(map[string][]chan struct{}),
	}
}

func (m *peerModel) Index(_ protocol.Connection, idx *protocol.Index) error {
	m.mu.Lock()
	m.folders[idx.Folder] = idx.Files
	waiters := m.waiters[idx.Folder]
	delete(m.waiters, idx.Folder)
	m.mu.Unlock()
	for _, ch := range waiters {
		close(ch)
	}
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
	m.mu.Unlock()
	return nil
}

// Request is called when the peer asks us for data. We don't serve files.
func (m *peerModel) Request(_ protocol.Connection, req *protocol.Request) (protocol.RequestResponse, error) {
	_ = req
	return nil, protocol.ErrNoSuchFile
}

func (m *peerModel) ClusterConfig(_ protocol.Connection, _ *protocol.ClusterConfig) error {
	return nil
}

func (m *peerModel) Closed(_ protocol.Connection, _ error) {}

func (m *peerModel) DownloadProgress(_ protocol.Connection, _ *protocol.DownloadProgress) error {
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

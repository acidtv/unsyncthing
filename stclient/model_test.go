package stclient

import (
	"sync"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"
)

func makeFile(name string, size int64) protocol.FileInfo {
	return protocol.FileInfo{Name: name, Size: size}
}

func makeDir(name string) protocol.FileInfo {
	return protocol.FileInfo{Name: name, Type: protocol.FileInfoTypeDirectory}
}

func makeDeleted(name string) protocol.FileInfo {
	return protocol.FileInfo{Name: name, Deleted: true}
}

func makeInvalid(name string) protocol.FileInfo {
	return protocol.FileInfo{Name: name, RawInvalid: true}
}

func TestNewPeerModel(t *testing.T) {
	m := newPeerModel()
	if m.folders == nil {
		t.Error("folders map is nil")
	}
	if m.lastUpdate == nil {
		t.Error("lastUpdate map is nil")
	}
}

func TestPeerModel_Index_StoresFiles(t *testing.T) {
	m := newPeerModel()
	err := m.Index(nil, &protocol.Index{
		Folder: "f",
		Files:  []protocol.FileInfo{makeFile("a.txt", 100), makeFile("b.txt", 200)},
	})
	if err != nil {
		t.Fatalf("Index() error: %v", err)
	}
	files := m.files("f")
	if len(files) != 2 {
		t.Errorf("got %d files, want 2", len(files))
	}
}

func TestPeerModel_Index_Replaces(t *testing.T) {
	m := newPeerModel()
	m.Index(nil, &protocol.Index{Folder: "f", Files: []protocol.FileInfo{makeFile("a.txt", 1)}})
	m.Index(nil, &protocol.Index{Folder: "f", Files: []protocol.FileInfo{makeFile("b.txt", 2), makeFile("c.txt", 3)}})

	files := m.files("f")
	if len(files) != 2 {
		t.Errorf("second Index() should replace; got %d files, want 2", len(files))
	}
}

func TestPeerModel_IndexUpdate_AddsNew(t *testing.T) {
	m := newPeerModel()
	m.Index(nil, &protocol.Index{Folder: "f", Files: []protocol.FileInfo{makeFile("a.txt", 1)}})
	m.IndexUpdate(nil, &protocol.IndexUpdate{Folder: "f", Files: []protocol.FileInfo{makeFile("b.txt", 2)}})

	files := m.files("f")
	if len(files) != 2 {
		t.Errorf("got %d files after IndexUpdate, want 2", len(files))
	}
}

func TestPeerModel_IndexUpdate_UpdatesExisting(t *testing.T) {
	m := newPeerModel()
	m.Index(nil, &protocol.Index{Folder: "f", Files: []protocol.FileInfo{makeFile("a.txt", 100)}})
	m.IndexUpdate(nil, &protocol.IndexUpdate{Folder: "f", Files: []protocol.FileInfo{makeFile("a.txt", 999)}})

	files := m.files("f")
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	if files[0].Size != 999 {
		t.Errorf("file size = %d, want 999", files[0].Size)
	}
}

func TestPeerModel_Files_UnknownFolder(t *testing.T) {
	m := newPeerModel()
	if m.files("nonexistent") != nil {
		t.Error("files() for unknown folder should return nil")
	}
}

func TestPeerModel_Files_DefensiveCopy(t *testing.T) {
	m := newPeerModel()
	m.Index(nil, &protocol.Index{Folder: "f", Files: []protocol.FileInfo{makeFile("a.txt", 1)}})

	copy1 := m.files("f")
	copy1[0].Name = "mutated"

	copy2 := m.files("f")
	if copy2[0].Name == "mutated" {
		t.Error("files() returned a slice alias instead of a defensive copy")
	}
}

func TestPeerModel_FolderKnown_Unknown(t *testing.T) {
	m := newPeerModel()
	if m.folderKnown("unknown") {
		t.Error("folderKnown() should return false for unknown folder")
	}
}

func TestPeerModel_FolderKnown_AfterEmptyIndex(t *testing.T) {
	m := newPeerModel()
	m.Index(nil, &protocol.Index{Folder: "f"})
	if !m.folderKnown("f") {
		t.Error("folderKnown() should return true after receiving an empty Index")
	}
}

func TestPeerModel_Request_ReturnsError(t *testing.T) {
	m := newPeerModel()
	resp, err := m.Request(nil, &protocol.Request{})
	if err == nil {
		t.Error("Request() should return an error (we don't serve files)")
	}
	if resp != nil {
		t.Error("Request() should return nil response")
	}
}

func TestPeerModel_Closed_FiresCallback(t *testing.T) {
	m := newPeerModel()
	var called bool
	m.setOnClosed(func(error) { called = true })
	m.Closed(nil, nil)
	if !called {
		t.Error("Closed() should fire the onClosed callback")
	}
}

func TestPeerModel_Closed_NoCallback(t *testing.T) {
	m := newPeerModel()
	m.Closed(nil, nil) // must not panic
}

func TestPeerModel_WaitForIndex_NeverSeen(t *testing.T) {
	m := newPeerModel()
	err := m.waitForIndex("missing", 50*time.Millisecond)
	if err == nil {
		t.Error("waitForIndex() should error when folder never received data and timeout elapses")
	}
}

func TestPeerModel_WaitForIndex_AlreadySettled(t *testing.T) {
	m := newPeerModel()
	// Pre-load data with a timestamp older than the quiet period (3s).
	m.mu.Lock()
	m.folders["f"] = []protocol.FileInfo{makeFile("a.txt", 1)}
	m.lastUpdate["f"] = time.Now().Add(-4 * time.Second)
	m.mu.Unlock()

	if err := m.waitForIndex("f", 100*time.Millisecond); err != nil {
		t.Errorf("waitForIndex() with settled data should return nil, got: %v", err)
	}
}

func TestPeerModel_WaitForIndex_ReturnsPartialOnTimeout(t *testing.T) {
	m := newPeerModel()
	// Data received recently — quiet period hasn't elapsed — but we have some data.
	m.mu.Lock()
	m.folders["f"] = []protocol.FileInfo{makeFile("a.txt", 1)}
	m.lastUpdate["f"] = time.Now()
	m.mu.Unlock()

	// Timeout fires while data is present → returns nil (partial is OK).
	if err := m.waitForIndex("f", 150*time.Millisecond); err != nil {
		t.Errorf("waitForIndex() with partial data should return nil on timeout, got: %v", err)
	}
}

func TestPeerModel_WaitForIndex_SettlesAfterDelay(t *testing.T) {
	m := newPeerModel()
	go func() {
		time.Sleep(20 * time.Millisecond)
		m.mu.Lock()
		m.folders["f"] = []protocol.FileInfo{makeFile("a.txt", 1)}
		m.lastUpdate["f"] = time.Now().Add(-4 * time.Second) // already past quiet
		m.mu.Unlock()
	}()
	if err := m.waitForIndex("f", 500*time.Millisecond); err != nil {
		t.Errorf("waitForIndex() should return nil once data settles, got: %v", err)
	}
}

func TestPeerModel_ConcurrentReadWrite(t *testing.T) {
	m := newPeerModel()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			m.Index(nil, &protocol.Index{Folder: "f", Files: []protocol.FileInfo{makeFile("a.txt", 1)}})
		}()
		go func() {
			defer wg.Done()
			m.files("f")
		}()
	}
	wg.Wait()
}

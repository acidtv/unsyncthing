package stclient

import (
	"encoding/json"
	"testing"

	"github.com/syncthing/syncthing/lib/protocol"
)

// testCert generates a fresh cert/key pair for use in tests.
func testCert(t *testing.T) CertResult {
	t.Helper()
	data, err := GenerateCert()
	if err != nil {
		t.Fatalf("GenerateCert: %v", err)
	}
	var r CertResult
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("unmarshal CertResult: %v", err)
	}
	return r
}

// testClient returns a connected-less Client backed by a freshly generated cert.
func testClient(t *testing.T) *Client {
	t.Helper()
	r := testCert(t)
	c, err := NewClient(r.CertPEM, r.KeyPEM)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// --- splitFolderIDs ---

func TestSplitFolderIDs(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"abc", []string{"abc"}},
		{"abc,def", []string{"abc", "def"}},
		{" abc , def ", []string{"abc", "def"}},
		{",abc,", []string{"abc"}},
		{"abc,,def", []string{"abc", "def"}},
		{"  ", nil},
		{"a,b,c", []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		got := splitFolderIDs(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitFolderIDs(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitFolderIDs(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

// --- buildClusterConfig ---

func TestBuildClusterConfig_FolderCount(t *testing.T) {
	cc := buildClusterConfig(protocol.LocalDeviceID, protocol.GlobalDeviceID, []string{"f1", "f2", "f3"})
	if len(cc.Folders) != 3 {
		t.Errorf("got %d folders, want 3", len(cc.Folders))
	}
}

func TestBuildClusterConfig_FolderID(t *testing.T) {
	cc := buildClusterConfig(protocol.LocalDeviceID, protocol.GlobalDeviceID, []string{"myfolder"})
	if cc.Folders[0].ID != "myfolder" {
		t.Errorf("folder ID = %q, want %q", cc.Folders[0].ID, "myfolder")
	}
}

func TestBuildClusterConfig_BothDevicesPresent(t *testing.T) {
	myID := protocol.LocalDeviceID
	peerID := protocol.GlobalDeviceID

	cc := buildClusterConfig(myID, peerID, []string{"f"})
	devices := cc.Folders[0].Devices
	if len(devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(devices))
	}
	ids := map[protocol.DeviceID]bool{devices[0].ID: true, devices[1].ID: true}
	if !ids[myID] {
		t.Error("myID not present in folder devices")
	}
	if !ids[peerID] {
		t.Error("peerID not present in folder devices")
	}
}

func TestBuildClusterConfig_Empty(t *testing.T) {
	cc := buildClusterConfig(protocol.LocalDeviceID, protocol.GlobalDeviceID, nil)
	if len(cc.Folders) != 0 {
		t.Errorf("got %d folders, want 0", len(cc.Folders))
	}
}

// --- NewClient ---

func TestNewClient_InvalidCert(t *testing.T) {
	_, err := NewClient("bad-cert", "bad-key")
	if err == nil {
		t.Error("NewClient() with invalid PEM should return an error")
	}
}

func TestNewClient_Valid(t *testing.T) {
	r := testCert(t)
	c, err := NewClient(r.CertPEM, r.KeyPEM)
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	if c == nil {
		t.Fatal("NewClient() returned nil client")
	}
}

func TestNewClient_DeviceIDMatchesCert(t *testing.T) {
	r := testCert(t)
	c, err := NewClient(r.CertPEM, r.KeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	if c.DeviceID() != r.DeviceID {
		t.Errorf("DeviceID() = %q, want %q", c.DeviceID(), r.DeviceID)
	}
}

func TestNewClient_NotConnectedInitially(t *testing.T) {
	c := testClient(t)
	if c.IsConnected() {
		t.Error("IsConnected() should be false before Connect()")
	}
}

func TestClient_CloseIdempotent(t *testing.T) {
	c := testClient(t)
	c.Close() // must not panic when called on a disconnected client
	c.Close()
}

// --- ListFolder via internal model injection ---

func TestListFolder_NotConnected(t *testing.T) {
	c := testClient(t)
	_, err := c.ListFolder("f")
	if err == nil {
		t.Error("ListFolder() should error when not connected")
	}
}

func TestListFolder_UnknownFolder(t *testing.T) {
	c := testClient(t)
	m := newPeerModel()
	c.mu.Lock()
	c.model = m
	c.mu.Unlock()

	_, err := c.ListFolder("nonexistent")
	if err == nil {
		t.Error("ListFolder() should error for folder not in index")
	}
}

func TestListFolder_FiltersDeletedAndInvalid(t *testing.T) {
	c := testClient(t)
	m := newPeerModel()
	m.Index(nil, &protocol.Index{
		Folder: "f",
		Files: []protocol.FileInfo{
			{Name: "regular.txt", Size: 100},
			{Name: "deleted.txt", Deleted: true},
			{Name: "invalid.txt", RawInvalid: true},
			{Name: "subdir", Type: protocol.FileInfoTypeDirectory},
		},
	})
	c.mu.Lock()
	c.model = m
	c.mu.Unlock()

	data, err := c.ListFolder("f")
	if err != nil {
		t.Fatalf("ListFolder() error: %v", err)
	}

	var entries []FileEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("unmarshal entries: %v", err)
	}
	// deleted and invalid should be filtered out; regular file and dir remain
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2 (regular + subdir): %+v", len(entries), entries)
	}
}

func TestListFolder_EmptyFolder(t *testing.T) {
	c := testClient(t)
	m := newPeerModel()
	m.Index(nil, &protocol.Index{Folder: "f"})
	c.mu.Lock()
	c.model = m
	c.mu.Unlock()

	data, err := c.ListFolder("f")
	if err != nil {
		t.Fatalf("ListFolder() error: %v", err)
	}
	if string(data) != "[]" {
		t.Errorf("empty folder should return %q, got %q", "[]", string(data))
	}
}

func TestListFolder_FileEntryFields(t *testing.T) {
	c := testClient(t)
	m := newPeerModel()
	m.Index(nil, &protocol.Index{
		Folder: "f",
		Files: []protocol.FileInfo{
			{Name: "docs/readme.txt", Size: 42, ModifiedS: 1700000000},
		},
	})
	c.mu.Lock()
	c.model = m
	c.mu.Unlock()

	data, err := c.ListFolder("f")
	if err != nil {
		t.Fatal(err)
	}
	var entries []FileEntry
	json.Unmarshal(data, &entries)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Name != "readme.txt" {
		t.Errorf("Name = %q, want %q", e.Name, "readme.txt")
	}
	if e.Path != "docs/readme.txt" {
		t.Errorf("Path = %q, want %q", e.Path, "docs/readme.txt")
	}
	if e.Size != 42 {
		t.Errorf("Size = %d, want 42", e.Size)
	}
	if e.Modified != 1700000000 {
		t.Errorf("Modified = %d, want 1700000000", e.Modified)
	}
	if e.IsDir {
		t.Error("IsDir should be false for a regular file")
	}
}

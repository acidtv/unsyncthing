package stclient

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/protocol"
)

// fakeConn satisfies protocol.Connection by embedding the (nil) interface and
// overriding only Request — the single method FetchFile exercises. Any other
// call would nil-panic, which is exactly what we want if FetchFile starts
// using more of the connection without updating these tests.
type fakeConn struct {
	protocol.Connection
	requestFn func(ctx context.Context, folder, name string, blockNo int, offset int64, size int, hash []byte, weakHash uint32, fromTemporary bool) ([]byte, error)
}

func (f *fakeConn) Request(ctx context.Context, folder, name string, blockNo int, offset int64, size int, hash []byte, weakHash uint32, fromTemporary bool) ([]byte, error) {
	return f.requestFn(ctx, folder, name, blockNo, offset, size, hash, weakHash, fromTemporary)
}

// recProgress records FetchProgress callbacks for assertions.
type recProgress struct {
	mu            sync.Mutex
	progressCalls int
	donePath      string
	doneCalled    bool
	errMsg        string
	errCalled     bool
}

func (r *recProgress) OnProgress(downloaded, total int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.progressCalls++
}

func (r *recProgress) OnDone(localPath string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.donePath = localPath
	r.doneCalled = true
}

func (r *recProgress) OnError(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errMsg = msg
	r.errCalled = true
}

// clientWithFile builds a Client whose model exposes a single-block file named
// "a.txt" in folder "f", wired to the given fake connection.
func clientWithFile(conn protocol.Connection, size int64) *Client {
	m := newPeerModel()
	m.folders["f"] = []protocol.FileInfo{{
		Name:   "a.txt",
		Size:   size,
		Blocks: []protocol.BlockInfo{{Offset: 0, Size: int(size)}},
	}}
	return &Client{conn: conn, model: m}
}

func TestFetchFile_CancelAborts(t *testing.T) {
	requestStarted := make(chan struct{})
	conn := &fakeConn{
		requestFn: func(ctx context.Context, _, _ string, _ int, _ int64, _ int, _ []byte, _ uint32, _ bool) ([]byte, error) {
			close(requestStarted)
			<-ctx.Done() // block until CancelFetch cancels the context
			return nil, ctx.Err()
		},
	}
	c := clientWithFile(conn, 1024)
	dest := filepath.Join(t.TempDir(), "out.bin")
	prog := &recProgress{}

	errCh := make(chan error, 1)
	go func() { errCh <- c.FetchFile("f", "a.txt", dest, prog) }()

	<-requestStarted
	c.CancelFetch()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("cancelled FetchFile should return nil, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FetchFile did not return after CancelFetch")
	}

	if prog.errCalled {
		t.Errorf("OnError must not fire on cancel; got %q", prog.errMsg)
	}
	if prog.doneCalled {
		t.Error("OnDone must not fire on cancel")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("partial file should be removed on cancel; stat err = %v", err)
	}
}

func TestFetchFile_RequestErrorReportsOnError(t *testing.T) {
	conn := &fakeConn{
		requestFn: func(context.Context, string, string, int, int64, int, []byte, uint32, bool) ([]byte, error) {
			return nil, fmt.Errorf("network boom")
		},
	}
	c := clientWithFile(conn, 512)
	dest := filepath.Join(t.TempDir(), "out.bin")
	prog := &recProgress{}

	err := c.FetchFile("f", "a.txt", dest, prog)
	if err == nil {
		t.Fatal("FetchFile should return an error when Request fails")
	}
	if !prog.errCalled {
		t.Error("OnError should fire for a non-cancel request failure")
	}
	if prog.doneCalled {
		t.Error("OnDone must not fire when the request failed")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("partial file should be removed on error; stat err = %v", err)
	}
}

func TestFetchFile_Success(t *testing.T) {
	const size = 2048
	conn := &fakeConn{
		requestFn: func(_ context.Context, _, _ string, _ int, _ int64, size int, _ []byte, _ uint32, _ bool) ([]byte, error) {
			return make([]byte, size), nil
		},
	}
	c := clientWithFile(conn, size)
	dest := filepath.Join(t.TempDir(), "out.bin")
	prog := &recProgress{}

	if err := c.FetchFile("f", "a.txt", dest, prog); err != nil {
		t.Fatalf("FetchFile returned error: %v", err)
	}
	if !prog.doneCalled {
		t.Error("OnDone should fire on success")
	}
	if prog.donePath != dest {
		t.Errorf("OnDone path = %q, want %q", prog.donePath, dest)
	}
	if prog.errCalled {
		t.Errorf("OnError must not fire on success; got %q", prog.errMsg)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("downloaded file missing: %v", err)
	}
	if fi.Size() != size {
		t.Errorf("file size = %d, want %d", fi.Size(), size)
	}
}

func TestFetchFile_NotConnected(t *testing.T) {
	c := &Client{} // no conn/model
	if err := c.FetchFile("f", "a.txt", filepath.Join(t.TempDir(), "x"), nil); err == nil {
		t.Error("FetchFile should error when not connected")
	}
}

func TestCancelFetch_NoActiveDownload(t *testing.T) {
	c := &Client{}
	c.CancelFetch() // must not panic when nothing is in flight
}

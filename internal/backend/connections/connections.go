package connections

import (
	"context"
	"io"

	"github.com/cenkalti/backoff/v4"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/sema"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
)

// make sure that ConnectionsBackend implements backend.Backend
var _ backend.Backend = &ConnectionsBackend{}

// ConnectionsBackend limits the number of concurrent operations.
type ConnectionsBackend struct {
	backend.Backend
	sem             sema.Semaphore
	connectionCount uint
}

// New returns a new backend that saves all data in a map in memory.
func New(be backend.Backend, connectionCount uint) *ConnectionsBackend {
	sem, err := sema.New(connectionCount)
	if err != nil {
		panic(err)
	}

	return &ConnectionsBackend{
		Backend:         be,
		sem:             sem,
		connectionCount: connectionCount,
	}
}

// Save adds new Data to the backend.
func (be *ConnectionsBackend) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	if err := h.Valid(); err != nil {
		return backoff.Permanent(err)
	}
	debug.Log("Save(%v, %v)", h, rd.Length())

	be.sem.GetToken()
	defer be.sem.ReleaseToken()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	return be.Backend.Save(ctx, h, rd)
}

// Load runs fn with a reader that yields the contents of the file at h at the
// given offset.
func (be *ConnectionsBackend) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(rd io.Reader) error) error {
	if err := h.Valid(); err != nil {
		return backoff.Permanent(err)
	}
	if offset < 0 {
		return backoff.Permanent(errors.New("offset is negative"))
	}

	if length < 0 {
		return backoff.Permanent(errors.Errorf("invalid length %d", length))
	}

	return be.Backend.Load(ctx, h, length, offset, fn)
}

func (be *ConnectionsBackend) WrapReaderFn(openReader backend.OpenReaderFn) backend.OpenReaderFn {
	return func(ctx context.Context, h backend.Handle, length int, offset int64) (io.ReadCloser, error) {
		debug.Log("Load(%v), length %v, offset %v", h, length, offset)

		be.sem.GetToken()
		ctx, cancel := context.WithCancel(ctx)
		rd, err := openReader(ctx, h, length, offset)
		if err != nil {
			cancel()
			be.sem.ReleaseToken()
			return rd, err
		}
		return be.sem.ReleaseTokenOnClose(rd, cancel), nil
	}
}

// Stat returns information about a file in the backend.
func (be *ConnectionsBackend) Stat(ctx context.Context, h backend.Handle) (backend.FileInfo, error) {
	if err := h.Valid(); err != nil {
		return backend.FileInfo{}, backoff.Permanent(err)
	}

	debug.Log("Stat(%v)", h)

	be.sem.GetToken()
	defer be.sem.ReleaseToken()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	return be.Backend.Stat(ctx, h)
}

// Remove deletes a file from the backend.
func (be *ConnectionsBackend) Remove(ctx context.Context, h backend.Handle) error {
	if err := h.Valid(); err != nil {
		return backoff.Permanent(err)
	}

	debug.Log("Remove(%v)", h)

	be.sem.GetToken()
	defer be.sem.ReleaseToken()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	err := be.Backend.Remove(ctx, h)
	debug.Log("Remove(%v) -> err %v", h, err)
	return err
}

func (be *ConnectionsBackend) List(ctx context.Context, t backend.FileType, fn func(backend.FileInfo) error) error {
	debug.Log("List(%v)", t)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	return be.Backend.List(ctx, t, fn)
}

func (be *ConnectionsBackend) Connections() uint {
	return be.connectionCount
}

func (be *ConnectionsBackend) Delete(ctx context.Context) error {
	debug.Log("Delete()")
	return be.Backend.Delete(ctx)
}

func (be *ConnectionsBackend) Close() error {
	debug.Log("Close()")
	return be.Backend.Close()
}

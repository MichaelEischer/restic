package b2

import (
	"context"
	"hash"
	"io"
	"net/http"
	"path"
	"sync"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/connections"
	"github.com/restic/restic/internal/backend/layout"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"

	"github.com/kurin/blazer/b2"
)

// b2Backend is a backend which stores its data on Backblaze B2.
type b2Backend struct {
	client       *b2.Client
	bucket       *b2.Bucket
	cfg          Config
	listMaxItems int
	layout.Layout
	openReaderFn backend.OpenReaderFn
}

// Billing happens in 1000 item granlarity, but we are more interested in reducing the number of network round trips
const defaultListMaxItems = 10 * 1000

// ensure statically that *b2Backend implements backend.Backend.
var _ backend.Backend = &b2Backend{}

type sniffingRoundTripper struct {
	sync.Mutex
	lastErr error
	http.RoundTripper
}

func (s *sniffingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := s.RoundTripper.RoundTrip(req)
	if err != nil {
		s.Lock()
		s.lastErr = err
		s.Unlock()
	}
	return res, err
}

func newClient(ctx context.Context, cfg Config, rt http.RoundTripper) (*b2.Client, error) {
	sniffer := &sniffingRoundTripper{RoundTripper: rt}
	opts := []b2.ClientOption{b2.Transport(sniffer)}

	// if the connection B2 fails, this can cause the client to hang
	// cancel the connection after a minute to at least provide some feedback to the user
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	c, err := b2.NewClient(ctx, cfg.AccountID, cfg.Key.Unwrap(), opts...)
	if err == context.DeadlineExceeded {
		if sniffer.lastErr != nil {
			return nil, sniffer.lastErr
		}
		return nil, errors.New("connection to B2 failed")
	} else if err != nil {
		return nil, errors.Wrap(err, "b2.NewClient")
	}
	return c, nil
}

// Open opens a connection to the B2 service.
func Open(ctx context.Context, cfg Config, rt http.RoundTripper) (backend.Backend, error) {
	debug.Log("cfg %#v", cfg)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	client, err := newClient(ctx, cfg, rt)
	if err != nil {
		return nil, err
	}

	bucket, err := client.Bucket(ctx, cfg.Bucket)
	if err != nil {
		return nil, errors.Wrap(err, "Bucket")
	}

	be := &b2Backend{
		client: client,
		bucket: bucket,
		cfg:    cfg,
		Layout: &layout.DefaultLayout{
			Join: path.Join,
			Path: cfg.Prefix,
		},
		listMaxItems: defaultListMaxItems,
	}
	cbe := connections.New(be, cfg.Connections)
	be.openReaderFn = cbe.WrapReaderFn(be.openReader)

	return cbe, nil
}

// Create opens a connection to the B2 service. If the bucket does not exist yet,
// it is created.
func Create(ctx context.Context, cfg Config, rt http.RoundTripper) (backend.Backend, error) {
	debug.Log("cfg %#v", cfg)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	client, err := newClient(ctx, cfg, rt)
	if err != nil {
		return nil, err
	}

	attr := b2.BucketAttrs{
		Type: b2.Private,
	}
	bucket, err := client.NewBucket(ctx, cfg.Bucket, &attr)
	if err != nil {
		return nil, errors.Wrap(err, "NewBucket")
	}

	be := &b2Backend{
		client: client,
		bucket: bucket,
		cfg:    cfg,
		Layout: &layout.DefaultLayout{
			Join: path.Join,
			Path: cfg.Prefix,
		},
		listMaxItems: defaultListMaxItems,
	}
	cbe := connections.New(be, cfg.Connections)
	be.openReaderFn = cbe.WrapReaderFn(be.openReader)

	_, err = cbe.Stat(ctx, backend.Handle{Type: backend.ConfigFile})
	if err != nil && !be.IsNotExist(err) {
		return nil, err
	}

	if err == nil {
		return nil, errors.New("config already exists")
	}

	return cbe, nil
}

// SetListMaxItems sets the number of list items to load per request.
func (be *b2Backend) SetListMaxItems(i int) {
	be.listMaxItems = i
}

func (be *b2Backend) Connections() uint {
	return be.cfg.Connections
}

// Location returns the location for the backend.
func (be *b2Backend) Location() string {
	return be.cfg.Bucket
}

// Hasher may return a hash function for calculating a content hash for the backend
func (be *b2Backend) Hasher() hash.Hash {
	return nil
}

// HasAtomicReplace returns whether Save() can atomically replace files
func (be *b2Backend) HasAtomicReplace() bool {
	return true
}

// IsNotExist returns true if the error is caused by a non-existing file.
func (be *b2Backend) IsNotExist(err error) bool {
	// blazer/b2 does not export its error types and values,
	// so we can't use errors.{As,Is}.
	for ; err != nil; err = errors.Unwrap(err) {
		if b2.IsNotExist(err) {
			return true
		}
	}
	return false
}

// Load runs fn with a reader that yields the contents of the file at h at the
// given offset.
func (be *b2Backend) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(rd io.Reader) error) error {
	return backend.DefaultLoad(ctx, h, length, offset, be.openReaderFn, fn)
}

func (be *b2Backend) openReader(ctx context.Context, h backend.Handle, length int, offset int64) (io.ReadCloser, error) {
	name := be.Layout.Filename(h)
	obj := be.bucket.Object(name)

	if offset == 0 && length == 0 {
		rd := obj.NewReader(ctx)
		return rd, nil
	}

	// pass a negative length to NewRangeReader so that the remainder of the
	// file is read.
	if length == 0 {
		length = -1
	}

	rd := obj.NewRangeReader(ctx, offset, int64(length))
	return rd, nil
}

// Save stores data in the backend at the handle.
func (be *b2Backend) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	name := be.Filename(h)
	obj := be.bucket.Object(name)

	// b2 always requires sha1 checksums for uploaded file parts
	w := obj.NewWriter(ctx)
	n, err := io.Copy(w, rd)
	debug.Log("  saved %d bytes, err %v", n, err)

	if err != nil {
		_ = w.Close()
		return errors.Wrap(err, "Copy")
	}

	// sanity check
	if n != rd.Length() {
		return errors.Errorf("wrote %d bytes instead of the expected %d bytes", n, rd.Length())
	}
	return errors.Wrap(w.Close(), "Close")
}

// Stat returns information about a blob.
func (be *b2Backend) Stat(ctx context.Context, h backend.Handle) (bi backend.FileInfo, err error) {
	name := be.Filename(h)
	obj := be.bucket.Object(name)
	info, err := obj.Attrs(ctx)
	if err != nil {
		debug.Log("Attrs() err %v", err)
		return backend.FileInfo{}, errors.Wrap(err, "Stat")
	}
	return backend.FileInfo{Size: info.Size, Name: h.Name}, nil
}

// Remove removes the blob with the given name and type.
func (be *b2Backend) Remove(ctx context.Context, h backend.Handle) error {
	// the retry backend will also repeat the remove method up to 10 times
	for i := 0; i < 3; i++ {
		obj := be.bucket.Object(be.Filename(h))
		err := obj.Delete(ctx)
		if err == nil {
			// keep deleting until we are sure that no leftover file versions exist
			continue
		}
		// consider a file as removed if b2 informs us that it does not exist
		if b2.IsNotExist(err) {
			return nil
		}
		return errors.Wrap(err, "Delete")
	}

	return errors.New("failed to delete all file versions")
}

// List returns a channel that yields all names of blobs of type t.
func (be *b2Backend) List(ctx context.Context, t backend.FileType, fn func(backend.FileInfo) error) error {
	prefix, _ := be.Basedir(t)
	iter := be.bucket.List(ctx, b2.ListPrefix(prefix), b2.ListPageSize(be.listMaxItems))

	for iter.Next() {
		obj := iter.Object()

		attrs, err := obj.Attrs(ctx)
		if err != nil {
			return err
		}

		fi := backend.FileInfo{
			Name: path.Base(obj.Name()),
			Size: attrs.Size,
		}

		if err := fn(fi); err != nil {
			return err
		}
	}
	if err := iter.Err(); err != nil {
		debug.Log("List: %v", err)
		return err
	}
	return nil
}

// Remove keys for a specified backend type.
func (be *b2Backend) removeKeys(ctx context.Context, t backend.FileType) error {
	debug.Log("removeKeys %v", t)
	return be.List(ctx, t, func(fi backend.FileInfo) error {
		return be.Remove(ctx, backend.Handle{Type: t, Name: fi.Name})
	})
}

// Delete removes all restic keys in the bucket. It will not remove the bucket itself.
func (be *b2Backend) Delete(ctx context.Context) error {
	alltypes := []backend.FileType{
		backend.PackFile,
		backend.KeyFile,
		backend.LockFile,
		backend.SnapshotFile,
		backend.IndexFile}

	for _, t := range alltypes {
		err := be.removeKeys(ctx, t)
		if err != nil {
			return nil
		}
	}
	err := be.Remove(ctx, backend.Handle{Type: backend.ConfigFile})
	if err != nil && be.IsNotExist(err) {
		err = nil
	}

	return err
}

// Close does nothing
func (be *b2Backend) Close() error { return nil }

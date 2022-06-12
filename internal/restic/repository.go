package restic

import (
	"context"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/crypto"
	"github.com/restic/restic/internal/ui/progress"
	"golang.org/x/sync/errgroup"
)

// Repository stores data in a backend. It provides high-level functions and
// transparently encrypts/decrypts data.
type Repository interface {

	// Backend returns the backend used by the repository
	Backend() backend.Backend
	// Connections returns the maximum number of concurrent backend operations
	Connections() uint

	Key() *crypto.Key

	Index() MasterIndex
	LoadIndex(context.Context) error
	SetIndex(MasterIndex) error
	LookupBlobSize(ID, BlobType) (uint, bool)

	Config() Config
	PackSize() uint

	// List calls the function fn for each file of type t in the repository.
	// When an error is returned by fn, processing stops and List() returns the
	// error.
	//
	// The function fn is called in the same Goroutine List() was called from.
	List(ctx context.Context, t backend.FileType, fn func(ID, int64) error) error

	// ListPack returns the list of blobs saved in the pack id and the length of
	// the the pack header.
	ListPack(context.Context, ID, int64) ([]Blob, uint32, error)

	LoadBlob(context.Context, BlobType, ID, []byte) ([]byte, error)
	SaveBlob(context.Context, BlobType, []byte, ID, bool) (ID, bool, int, error)

	// StartPackUploader start goroutines to upload new pack files. The errgroup
	// is used to immediately notify about an upload error. Flush() will also return
	// that error.
	StartPackUploader(ctx context.Context, wg *errgroup.Group)
	Flush(context.Context) error

	// LoadUnpacked loads and decrypts the file with the given type and ID,
	// using the supplied buffer (which must be empty). If the buffer is nil, a
	// new buffer will be allocated and returned.
	LoadUnpacked(ctx context.Context, t backend.FileType, id ID, buf []byte) (data []byte, err error)
	SaveUnpacked(context.Context, backend.FileType, []byte) (ID, error)
}

// Lister allows listing files in a backend.
type Lister interface {
	List(ctx context.Context, t backend.FileType, fn func(ID, int64) error) error
}

// LoaderUnpacked allows loading a blob not stored in a pack file
type LoaderUnpacked interface {
	// Connections returns the maximum number of concurrent backend operations
	Connections() uint
	LoadUnpacked(ctx context.Context, t backend.FileType, id ID, buf []byte) (data []byte, err error)
}

// SaverUnpacked allows saving a blob not stored in a pack file
type SaverUnpacked interface {
	// Connections returns the maximum number of concurrent backend operations
	Connections() uint
	SaveUnpacked(context.Context, backend.FileType, []byte) (ID, error)
}

type PackBlobs struct {
	PackID ID
	Blobs  []Blob
}

// MasterIndex keeps track of the blobs are stored within files.
type MasterIndex interface {
	Has(BlobHandle) bool
	Lookup(BlobHandle) []PackedBlob

	// Each runs fn on all blobs known to the index. When the context is cancelled,
	// the index iteration return immediately. This blocks any modification of the index.
	Each(ctx context.Context, fn func(PackedBlob))
	ListPacks(ctx context.Context, packs IDSet) <-chan PackBlobs

	Save(ctx context.Context, repo SaverUnpacked, packBlacklist IDSet, extraObsolete IDs, p *progress.Counter) (obsolete IDSet, err error)
}

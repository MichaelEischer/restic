package archiver

import (
	"context"

	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/restic"
	tomb "gopkg.in/tomb.v2"
)

// Saver allows saving a blob.
type Saver interface {
	SaveBlob(ctx context.Context, t restic.BlobType, data []byte, id restic.ID, storeDuplicate bool) (restic.ID, bool, error)
}

// BlobSaver concurrently saves incoming blobs to the repo.
type BlobSaver struct {
	repo Saver
	ch   chan<- saveBlobJob
}

// NewBlobSaver returns a new blob. A worker pool is started, it is stopped
// when ctx is cancelled.
func NewBlobSaver(ctx context.Context, t *tomb.Tomb, repo Saver, workers uint) *BlobSaver {
	ch := make(chan saveBlobJob)
	s := &BlobSaver{
		repo: repo,
		ch:   ch,
	}

	for i := uint(0); i < workers; i++ {
		t.Go(func() error {
			return s.worker(t.Context(ctx), ch)
		})
	}

	return s
}

// Save stores a blob in the repo. It checks the index and the known blobs
// before saving anything. It takes ownership of the buffer passed in.
func (s *BlobSaver) Save(ctx context.Context, t restic.BlobType, buf *Buffer) FutureBlob {
	ch := make(chan SaveBlobResponse, 1)
	select {
	case s.ch <- saveBlobJob{BlobType: t, buf: buf, ch: ch}:
	case <-ctx.Done():
		debug.Log("not sending job, context is cancelled")
		close(ch)
		return FutureBlob{ch: ch}
	}

	return FutureBlob{ch: ch}
}

// FutureBlob is returned by SaveBlob and will return the data once it has been processed.
type FutureBlob struct {
	ch <-chan SaveBlobResponse
}

func (s *FutureBlob) Poll() *SaveBlobResponse {
	select {
	case res, ok := <-s.ch:
		if ok {
			return &res
		}
	default:
	}
	return nil
}

// Take blocks until the result is available or the context is cancelled.
func (s *FutureBlob) Take(ctx context.Context) SaveBlobResponse {
	select {
	case res, ok := <-s.ch:
		if ok {
			return res
		}
	case <-ctx.Done():
	}
	return SaveBlobResponse{}
}

type saveBlobJob struct {
	restic.BlobType
	buf *Buffer
	ch  chan<- SaveBlobResponse
}

type SaveBlobResponse struct {
	id     restic.ID
	length int
	known  bool
}

func (s *BlobSaver) saveBlob(ctx context.Context, t restic.BlobType, buf []byte) (SaveBlobResponse, error) {
	id, known, err := s.repo.SaveBlob(ctx, t, buf, restic.ID{}, false)

	if err != nil {
		return SaveBlobResponse{}, err
	}

	return SaveBlobResponse{
		id:     id,
		length: len(buf),
		known:  known,
	}, nil
}

func (s *BlobSaver) worker(ctx context.Context, jobs <-chan saveBlobJob) error {
	for {
		var job saveBlobJob
		select {
		case <-ctx.Done():
			return nil
		case job = <-jobs:
		}

		res, err := s.saveBlob(ctx, job.BlobType, job.buf.Data)
		if err != nil {
			debug.Log("saveBlob returned error, exiting: %v", err)
			close(job.ch)
			return err
		}
		job.ch <- res
		close(job.ch)
		job.buf.Release()
	}
}

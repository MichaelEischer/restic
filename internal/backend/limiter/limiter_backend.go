package limiter

import (
	"context"
	"io"

	"github.com/restic/restic/internal/backend"
)

// LimitBackend wraps a Backend and applies rate limiting to Load() and Save()
// calls on the backend.
func LimitBackend(be backend.Backend, l Limiter) backend.Backend {
	return rateLimitedBackend{
		Backend: be,
		limiter: l,
	}
}

type rateLimitedBackend struct {
	backend.Backend
	limiter Limiter
}

func (r rateLimitedBackend) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	limited := limitedRewindReader{
		RewindReader: rd,
		limited:      r.limiter.Upstream(rd),
	}

	return r.Backend.Save(ctx, h, limited)
}

type limitedRewindReader struct {
	backend.RewindReader

	limited io.Reader
}

func (l limitedRewindReader) Read(b []byte) (int, error) {
	return l.limited.Read(b)
}

func (r rateLimitedBackend) Load(ctx context.Context, h backend.Handle, length int, offset int64, consumer func(rd io.Reader) error) error {
	return r.Backend.Load(ctx, h, length, offset, func(rd io.Reader) error {
		return consumer(newDownstreamLimitedReader(rd, r.limiter))
	})
}

type limitedReader struct {
	io.Reader
	writerTo io.WriterTo
	limiter  Limiter
}

func newDownstreamLimitedReader(rd io.Reader, limiter Limiter) io.Reader {
	lrd := limiter.Downstream(rd)
	if wt, ok := rd.(io.WriterTo); ok {
		lrd = &limitedReader{
			Reader:   lrd,
			writerTo: wt,
			limiter:  limiter,
		}
	}
	return lrd
}

func (l *limitedReader) WriteTo(w io.Writer) (int64, error) {
	return l.writerTo.WriteTo(l.limiter.DownstreamWriter(w))
}

var _ backend.Backend = (*rateLimitedBackend)(nil)

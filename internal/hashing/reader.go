package hashing

import (
	"hash"
	"io"
)

// Reader hashes all data read from the underlying reader.
type Reader struct {
	b io.Reader
	r io.Reader
	h hash.Hash
}

// NewReader returns a new Reader that uses the hash h.
func NewReader(r io.Reader, h hash.Hash) *Reader {
	return &Reader{
		h: h,
		r: io.TeeReader(r, h),
		b: r,
	}
}

func (h *Reader) Read(p []byte) (int, error) {
	return h.r.Read(p)
}

func (h *Reader) WriteTo(w io.Writer) (int64, error) {
	if _, ok := h.b.(io.WriterTo); !ok {
		return io.Copy(w, h.r)
	}
	n, err := h.b.(io.WriterTo).WriteTo(NewWriter(w, h.h))
	return n, err
}

// Sum returns the hash of the data read so far.
func (h *Reader) Sum(d []byte) []byte {
	return h.h.Sum(d)
}

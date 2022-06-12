package restic

import (
	"bytes"
	"context"
	"io"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
)

// LoadAll reads all data stored in the backend for the handle into the given
// buffer, which is truncated. If the buffer is not large enough or nil, a new
// one is allocated.
func LoadAll(ctx context.Context, buf []byte, be backend.Backend, h backend.Handle) ([]byte, error) {
	retriedInvalidData := false
	err := be.Load(ctx, h, 0, 0, func(rd io.Reader) error {
		// make sure this is idempotent, in case an error occurs this function may be called multiple times!
		wr := bytes.NewBuffer(buf[:0])
		_, cerr := io.Copy(wr, rd)
		if cerr != nil {
			return cerr
		}
		buf = wr.Bytes()

		// retry loading damaged data only once. If a file fails to download correctly
		// the second time, then it  is likely corrupted at the backend. Return the data
		// to the caller in that case to let it decide what to do with the data.
		if !retriedInvalidData && h.Type != backend.ConfigFile {
			id, err := ParseID(h.Name)
			if err == nil && !Hash(buf).Equal(id) {
				debug.Log("retry loading broken blob %v", h)
				retriedInvalidData = true
				return errors.Errorf("loadAll(%v): invalid data returned", h)
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return buf, nil
}
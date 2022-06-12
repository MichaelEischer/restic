package restic_test

import (
	"context"
	"testing"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

type saver struct {
	fn func(backend.FileType, []byte) (restic.ID, error)
}

func (s saver) SaveUnpacked(ctx context.Context, t backend.FileType, buf []byte) (restic.ID, error) {
	return s.fn(t, buf)
}

func (s saver) Connections() uint {
	return 2
}

type loader struct {
	fn func(backend.FileType, restic.ID, []byte) ([]byte, error)
}

func (l loader) LoadUnpacked(ctx context.Context, t backend.FileType, id restic.ID, buf []byte) (data []byte, err error) {
	return l.fn(t, id, buf)
}

func (l loader) Connections() uint {
	return 2
}

func TestConfig(t *testing.T) {
	var resultBuf []byte
	save := func(tpe backend.FileType, buf []byte) (restic.ID, error) {
		rtest.Assert(t, tpe == backend.ConfigFile,
			"wrong backend type: got %v, wanted %v",
			tpe, backend.ConfigFile)

		resultBuf = buf
		return restic.ID{}, nil
	}

	cfg1, err := restic.CreateConfig(restic.MaxRepoVersion)
	rtest.OK(t, err)

	err = restic.SaveConfig(context.TODO(), saver{save}, cfg1)
	rtest.OK(t, err)

	load := func(tpe backend.FileType, id restic.ID, in []byte) ([]byte, error) {
		rtest.Assert(t, tpe == backend.ConfigFile,
			"wrong backend type: got %v, wanted %v",
			tpe, backend.ConfigFile)

		return resultBuf, nil
	}

	cfg2, err := restic.LoadConfig(context.TODO(), loader{load})
	rtest.OK(t, err)

	rtest.Assert(t, cfg1 == cfg2,
		"configs aren't equal: %v != %v", cfg1, cfg2)
}

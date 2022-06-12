package repository_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

type idWithSize struct {
	ID   restic.ID
	Size int64
}

type mockLister struct {
	restic.Repository
	listFn func(ctx context.Context, t backend.FileType, fn func(id restic.ID, size int64) error) error
}

func (l mockLister) List(ctx context.Context, t backend.FileType, fn func(id restic.ID, size int64) error) error {
	return l.listFn(ctx, t, fn)
}

func TestMemoizeList(t *testing.T) {
	// setup backend to serve as data source for memoized list
	files := []idWithSize{
		{Size: 42, ID: restic.NewRandomID()},
		{Size: 45, ID: restic.NewRandomID()},
	}
	called := false
	be := mockLister{nil, func(ctx context.Context, t backend.FileType, fn func(id restic.ID, size int64) error) error {
		called = true
		if t != backend.SnapshotFile {
			return nil
		}
		for _, fi := range files {
			if err := fn(fi.ID, fi.Size); err != nil {
				return err
			}
		}
		return nil
	}}

	mem, err := repository.MemorizeList(context.TODO(), be, backend.SnapshotFile)
	rtest.OK(t, err)
	rtest.Assert(t, called, "did not query repo")

	called = false
	err = mem.List(context.TODO(), backend.IndexFile, func(id restic.ID, size int64) error {
		t.Fatal("shouldn't be called")
		return nil
	})
	rtest.OK(t, err)
	rtest.Assert(t, called, "did not query repo")

	var memFiles []idWithSize
	called = false
	err = mem.List(context.TODO(), backend.SnapshotFile, func(id restic.ID, size int64) error {
		memFiles = append(memFiles, idWithSize{id, size})
		return nil
	})
	rtest.OK(t, err)
	rtest.Equals(t, files, memFiles)
	rtest.Assert(t, !called, "must not query repo")
}

func TestMemoizeListError(t *testing.T) {
	// setup backend to serve as data source for memoized list
	be := mockLister{nil, func(ctx context.Context, t backend.FileType, fn func(id restic.ID, size int64) error) error {
		return fmt.Errorf("list error")
	}}
	_, err := repository.MemorizeList(context.TODO(), be, backend.SnapshotFile)
	rtest.Assert(t, err != nil, "missing error on list error")
}

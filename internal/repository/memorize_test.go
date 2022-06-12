package repository_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

type idWithSize struct {
	ID   restic.ID
	Size int64
}

type mockLister func(ctx context.Context, t restic.FileType, fn func(id restic.ID, size int64) error) error

func (l mockLister) List(ctx context.Context, t restic.FileType, fn func(id restic.ID, size int64) error) error {
	return l(ctx, t, fn)
}

func TestMemoizeList(t *testing.T) {
	// setup backend to serve as data source for memoized list
	files := []idWithSize{
		{Size: 42, ID: restic.NewRandomID()},
		{Size: 45, ID: restic.NewRandomID()},
	}
	be := mockLister(func(ctx context.Context, t restic.FileType, fn func(id restic.ID, size int64) error) error {
		for _, fi := range files {
			if err := fn(fi.ID, fi.Size); err != nil {
				return err
			}
		}
		return nil
	})

	mem, err := repository.MemorizeList(context.TODO(), be, restic.SnapshotFile)
	rtest.OK(t, err)

	err = mem.List(context.TODO(), restic.IndexFile, func(id restic.ID, size int64) error {
		t.Fatal("file type mismatch")
		return nil // the memoized lister must return an error by itself
	})
	rtest.Assert(t, err != nil, "missing error on file typ mismatch")

	var memFiles []idWithSize
	err = mem.List(context.TODO(), restic.SnapshotFile, func(id restic.ID, size int64) error {
		memFiles = append(memFiles, idWithSize{id, size})
		return nil
	})
	rtest.OK(t, err)
	rtest.Equals(t, files, memFiles)
}

func TestMemoizeListError(t *testing.T) {
	// setup backend to serve as data source for memoized list
	be := mockLister(func(ctx context.Context, t restic.FileType, fn func(id restic.ID, size int64) error) error {
		return fmt.Errorf("list error")
	})
	_, err := repository.MemorizeList(context.TODO(), be, restic.SnapshotFile)
	rtest.Assert(t, err != nil, "missing error on list error")
}

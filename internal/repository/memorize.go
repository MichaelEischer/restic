package repository

import (
	"context"
	"fmt"

	"github.com/restic/restic/internal/restic"
)

type idWithSize struct {
	id   restic.ID
	size int64
}

type memorizedLister struct {
	info []idWithSize
	tpe  restic.FileType
}

// statically assert that memorizedLister implements restic.Lister
var _ restic.Lister = &memorizedLister{}

func (m *memorizedLister) List(ctx context.Context, t restic.FileType, fn func(restic.ID, int64) error) error {
	if t != m.tpe {
		return fmt.Errorf("filetype mismatch, expected %s got %s", m.tpe, t)
	}
	for _, fi := range m.info {
		if ctx.Err() != nil {
			break
		}
		err := fn(fi.id, fi.size)
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func MemorizeList(ctx context.Context, repo restic.Lister, t restic.FileType) (restic.Lister, error) {
	if _, ok := repo.(*memorizedLister); ok {
		return repo, nil
	}

	var infos []idWithSize
	err := repo.List(ctx, t, func(id restic.ID, size int64) error {
		infos = append(infos, idWithSize{id, size})
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &memorizedLister{
		info: infos,
		tpe:  t,
	}, nil
}

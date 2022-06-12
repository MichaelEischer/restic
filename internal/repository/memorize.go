package repository

import (
	"context"

	"github.com/restic/restic/internal/restic"
)

type idWithSize struct {
	id   restic.ID
	size int64
}

type memorizedRepository struct {
	restic.Repository

	info []idWithSize
	tpe  restic.FileType
}

func (m *memorizedRepository) List(ctx context.Context, t restic.FileType, fn func(restic.ID, int64) error) error {
	if t != m.tpe {
		return m.Repository.List(ctx, t, fn)
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

func MemorizeList(ctx context.Context, repo restic.Repository, t restic.FileType) (restic.Repository, error) {
	if _, ok := repo.(*memorizedRepository); ok {
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

	return &memorizedRepository{
		Repository: repo,
		info:       infos,
		tpe:        t,
	}, nil
}

package data_test

import (
	"context"
	"testing"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/repository"
)

func TestFindLatestSnapshot(t *testing.T) {
	repo, cleanup := repository.TestRepository(t)
	defer cleanup()

	data.TestCreateSnapshot(t, repo, parseTimeUTC("2015-05-05 05:05:05"), 1, 0)
	data.TestCreateSnapshot(t, repo, parseTimeUTC("2017-07-07 07:07:07"), 1, 0)
	latestSnapshot := data.TestCreateSnapshot(t, repo, parseTimeUTC("2019-09-09 09:09:09"), 1, 0)

	sn, err := data.FindFilteredSnapshot(context.TODO(), repo, []string{"foo"}, []data.TagList{}, []string{}, nil, "latest")
	if err != nil {
		t.Fatalf("FindLatestSnapshot returned error: %v", err)
	}

	if *sn.ID() != *latestSnapshot.ID() {
		t.Errorf("FindLatestSnapshot returned wrong snapshot ID: %v", *sn.ID())
	}
}

func TestFindLatestSnapshotWithMaxTimestamp(t *testing.T) {
	repo, cleanup := repository.TestRepository(t)
	defer cleanup()

	data.TestCreateSnapshot(t, repo, parseTimeUTC("2015-05-05 05:05:05"), 1, 0)
	desiredSnapshot := data.TestCreateSnapshot(t, repo, parseTimeUTC("2017-07-07 07:07:07"), 1, 0)
	data.TestCreateSnapshot(t, repo, parseTimeUTC("2019-09-09 09:09:09"), 1, 0)

	maxTimestamp := parseTimeUTC("2018-08-08 08:08:08")

	sn, err := data.FindFilteredSnapshot(context.TODO(), repo, []string{"foo"}, []data.TagList{}, []string{}, &maxTimestamp, "latest")
	if err != nil {
		t.Fatalf("FindLatestSnapshot returned error: %v", err)
	}

	if *sn.ID() != *desiredSnapshot.ID() {
		t.Errorf("FindLatestSnapshot returned wrong snapshot ID: %v", *sn.ID())
	}
}

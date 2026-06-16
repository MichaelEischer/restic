package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/filter"
	"github.com/restic/restic/internal/global"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
	"github.com/restic/restic/internal/ui"
	"github.com/restic/restic/internal/ui/progress"
)

func testRunRewriteExclude(t testing.TB, gopts global.Options, excludes []string, forget bool, metadata snapshotMetadataArgs) {
	opts := RewriteOptions{
		ExcludePatternOptions: filter.ExcludePatternOptions{
			Excludes: excludes,
		},
		Forget:   forget,
		Metadata: metadata,
	}

	rtest.OK(t, withTermStatus(t, gopts, func(ctx context.Context, gopts global.Options) error {
		return runRewrite(context.TODO(), opts, gopts, nil, gopts.Term)
	}))
}

func testRunRewriteWithOpts(t testing.TB, opts RewriteOptions, gopts global.Options, args []string) error {
	rtest.OK(t, withTermStatus(t, gopts, func(ctx context.Context, gopts global.Options) error {
		return runRewrite(context.TODO(), opts, gopts, args, gopts.Term)
	}))
	return nil
}

// testLsOutputContainsCount runs restic ls with the given options and asserts that
// exactly expectedCount lines of the output contain substring.
func testLsOutputContainsCount(t testing.TB, gopts global.Options, lsOpts LsOptions, lsArgs []string, substring string, expectedCount int) {
	t.Helper()
	out := testRunLsWithOpts(t, gopts, lsOpts, lsArgs)
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, substring) {
			count++
		}
	}
	rtest.Assert(t, count == expectedCount, "expected %d lines containing %q, but got %d", expectedCount, substring, count)
}

func createBasicRewriteRepo(t testing.TB, env *testEnvironment) restic.ID {
	testSetupBackupData(t, env)

	// create backup
	testRunBackup(t, filepath.Dir(env.testdata), []string{"testdata"}, BackupOptions{}, env.gopts)
	snapshotIDs := testRunList(t, env.gopts, "snapshots")
	rtest.Assert(t, len(snapshotIDs) == 1, "expected one snapshot, got %v", snapshotIDs)
	testRunCheck(t, env.gopts)

	return snapshotIDs[0]
}

func createBasicRewriteRepoWithEmptyDirectory(t testing.TB, env *testEnvironment) restic.ID {
	testSetupBackupData(t, env)

	// make an empty directory named "empty-directory"
	rtest.OK(t, os.Mkdir(filepath.Join(env.testdata, "/0/tests", "empty-directory"), 0755))

	// create backup
	testRunBackup(t, filepath.Dir(env.testdata), []string{"testdata"}, BackupOptions{}, env.gopts)
	snapshotIDs := testRunList(t, env.gopts, "snapshots")
	rtest.Assert(t, len(snapshotIDs) == 1, "expected one snapshot, got %v", snapshotIDs)

	return snapshotIDs[0]
}

func withCopiedRewriteRepo(t *testing.T, template *testEnvironment) (*testEnvironment, func()) {
	t.Helper()
	env, cleanup := withTestEnvironment(t)
	rtest.CopyDir(t, template.repo, env.repo)
	return env, cleanup
}

func getSnapshot(t testing.TB, snapshotID restic.ID, env *testEnvironment) *data.Snapshot {
	t.Helper()

	var snapshots []*data.Snapshot
	err := withTermStatus(t, env.gopts, func(ctx context.Context, gopts global.Options) error {
		printer := progress.NewTerminalPrinter(gopts.JSON, gopts.Verbosity, gopts.Term)
		ctx, repo, unlock, err := openWithReadLock(ctx, gopts, false, printer)
		rtest.OK(t, err)
		defer unlock()

		snapshots, err = data.TestLoadAllSnapshots(ctx, repo, nil)
		return err
	})
	rtest.OK(t, err)

	for _, s := range snapshots {
		if *s.ID() == snapshotID {
			return s
		}
	}
	return nil
}

func TestRewrite(t *testing.T) {
	template, templateCleanup := withTestEnvironment(t)
	defer templateCleanup()
	templateSnapshotID := createBasicRewriteRepo(t, template)

	t.Run("Exclude", func(t *testing.T) {
		env, cleanup := withCopiedRewriteRepo(t, template)
		defer cleanup()

		testRunRewriteExclude(t, env.gopts, []string{"3"}, false, snapshotMetadataArgs{Hostname: "", Time: ""})
		snapshotIDs := testRunList(t, env.gopts, "snapshots")
		rtest.Assert(t, len(snapshotIDs) == 2, "expected two snapshots, got %v", snapshotIDs)
		testRunCheck(t, env.gopts)
	})

	t.Run("Unchanged", func(t *testing.T) {
		env, cleanup := withCopiedRewriteRepo(t, template)
		defer cleanup()

		testRunRewriteExclude(t, env.gopts, []string{"3dflkhjgdflhkjetrlkhjgfdlhkj"}, false, snapshotMetadataArgs{Hostname: "", Time: ""})
		newSnapshotIDs := testRunList(t, env.gopts, "snapshots")
		rtest.Assert(t, len(newSnapshotIDs) == 1, "expected one snapshot, got %v", newSnapshotIDs)
		rtest.Assert(t, templateSnapshotID == newSnapshotIDs[0], "snapshot id changed unexpectedly")
		testRunCheck(t, env.gopts)
	})

	t.Run("Replace", func(t *testing.T) {
		env, cleanup := withCopiedRewriteRepo(t, template)
		defer cleanup()

		snapshot := getSnapshot(t, templateSnapshotID, env)

		testRunRewriteExclude(t, env.gopts, []string{"3"}, true, snapshotMetadataArgs{Hostname: "", Time: ""})
		bytesExcluded, err := ui.ParseBytes("16K")
		rtest.OK(t, err)

		newSnapshotIDs := testListSnapshots(t, env.gopts, 1)
		rtest.Assert(t, templateSnapshotID != newSnapshotIDs[0], "snapshot id should have changed")

		newSnapshot := getSnapshot(t, newSnapshotIDs[0], env)

		rtest.Equals(t, snapshot.Summary.TotalFilesProcessed-1, newSnapshot.Summary.TotalFilesProcessed, "snapshot file count should have changed")
		rtest.Equals(t, snapshot.Summary.TotalBytesProcessed-uint64(bytesExcluded), newSnapshot.Summary.TotalBytesProcessed, "snapshot size should have changed")

		testRunPrune(t, env.gopts, PruneOptions{MaxUnused: "0"})
		testRunCheck(t, env.gopts)
	})

	newHost := "new host"
	newTime := "1999-01-01 11:11:11"
	for _, tc := range []struct {
		name     string
		metadata snapshotMetadataArgs
	}{
		{"MetadataTime", snapshotMetadataArgs{Hostname: "", Time: newTime}},
		{"MetadataHost", snapshotMetadataArgs{Hostname: newHost, Time: ""}},
		{"MetadataHostTime", snapshotMetadataArgs{Hostname: newHost, Time: newTime}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, cleanup := withCopiedRewriteRepo(t, template)
			defer cleanup()

			testRunRewriteExclude(t, env.gopts, []string{}, true, tc.metadata)

			var snapshots []*data.Snapshot
			err := withTermStatus(t, env.gopts, func(ctx context.Context, gopts global.Options) error {
				printer := progress.NewTerminalPrinter(gopts.JSON, gopts.Verbosity, gopts.Term)
				ctx, repo, unlock, err := openWithReadLock(ctx, gopts, false, printer)
				rtest.OK(t, err)
				defer unlock()

				snapshots, err = data.TestLoadAllSnapshots(ctx, repo, nil)
				return err
			})
			rtest.OK(t, err)
			rtest.Assert(t, len(snapshots) == 1, "expected one snapshot, got %v", len(snapshots))
			newSnapshot := snapshots[0]

			if tc.metadata.Time != "" {
				rtest.Assert(t, newSnapshot.Time.Format(global.TimeFormat) == tc.metadata.Time, "New snapshot should have time %s", tc.metadata.Time)
			}

			if tc.metadata.Hostname != "" {
				rtest.Assert(t, newSnapshot.Hostname == tc.metadata.Hostname, "New snapshot should have host %s", tc.metadata.Hostname)
			}
		})
	}

	t.Run("SnapshotSummary", func(t *testing.T) {
		env, cleanup := withCopiedRewriteRepo(t, template)
		defer cleanup()

		rtest.OK(t, withTermStatus(t, env.gopts, func(ctx context.Context, gopts global.Options) error {
			return runRewrite(context.TODO(), RewriteOptions{SnapshotSummary: true}, gopts, []string{}, gopts.Term)
		}))
		snapshots := testListSnapshots(t, env.gopts, 1)

		var oldSummary *data.SnapshotSummary
		err := withTermStatus(t, env.gopts, func(ctx context.Context, gopts global.Options) error {
			printer := progress.NewTerminalPrinter(gopts.JSON, gopts.Verbosity, gopts.Term)
			_, repo, unlock, err := openWithExclusiveLock(ctx, gopts, false, printer)
			rtest.OK(t, err)
			defer unlock()

			sn, err := data.LoadSnapshot(ctx, repo, snapshots[0])
			rtest.OK(t, err)
			oldSummary = sn.Summary
			sn.Summary = nil
			rtest.OK(t, repo.RemoveUnpacked(ctx, restic.WriteableSnapshotFile, snapshots[0]))
			snapshots[0], err = data.SaveSnapshot(ctx, repo, sn)
			return err
		})
		rtest.OK(t, err)

		rtest.OK(t, withTermStatus(t, env.gopts, func(ctx context.Context, gopts global.Options) error {
			return runRewrite(context.TODO(), RewriteOptions{SnapshotSummary: true}, gopts, []string{}, gopts.Term)
		}))
		newSnapshots := testListSnapshots(t, env.gopts, 2)
		newSnapshot := restic.NewIDSet(newSnapshots...).Sub(restic.NewIDSet(snapshots...)).List()[0]

		newSn := testLoadSnapshot(t, env.gopts, newSnapshot)
		rtest.Assert(t, newSn.Summary != nil, "snapshot should have summary attached")
		rtest.Equals(t, oldSummary.TotalBytesProcessed, newSn.Summary.TotalBytesProcessed, "unexpected TotalBytesProcessed value")
		rtest.Equals(t, oldSummary.TotalFilesProcessed, newSn.Summary.TotalFilesProcessed, "unexpected TotalFilesProcessed value")
	})

	for _, tc := range []struct {
		name                 string
		opts                 RewriteOptions
		lsSubstring          string
		lsExpectedCount      int
		summaryFilesExpected uint
	}{
		{"IncludeRelative", RewriteOptions{
			Forget:                true,
			IncludePatternOptions: filter.IncludePatternOptions{Includes: []string{"*.txt"}},
		}, ".txt", 2, 2},
		{"IncludeAbsolute", RewriteOptions{
			Forget: true,
			IncludePatternOptions: filter.IncludePatternOptions{Includes: []string{"/testdata/0/for_cmd_ls"}},
		}, "/testdata/0", 5, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, cleanup := withCopiedRewriteRepo(t, template)
			defer cleanup()
			snapshots := testListSnapshots(t, env.gopts, 1)

			rtest.OK(t, testRunRewriteWithOpts(t, tc.opts, env.gopts, []string{"latest"}))

			newSnapshots := testListSnapshots(t, env.gopts, 1)
			rtest.Assert(t, snapshots[0] != newSnapshots[0], "snapshot id should have changed")

			testLsOutputContainsCount(t, env.gopts, LsOptions{}, []string{"latest"}, tc.lsSubstring, tc.lsExpectedCount)
			sn := testLoadSnapshot(t, env.gopts, newSnapshots[0])
			rtest.Assert(t, sn.Summary != nil, "snapshot should have a summary attached")
			rtest.Assert(t, sn.Summary.TotalFilesProcessed == tc.summaryFilesExpected,
				"there should be %d files in the snapshot, but there are %d files", tc.summaryFilesExpected, sn.Summary.TotalFilesProcessed)
		})
	}

	t.Run("ExcludeFiles", func(t *testing.T) {
		env, cleanup := withCopiedRewriteRepo(t, template)
		defer cleanup()
		snapshots := testListSnapshots(t, env.gopts, 1)

		err := testRunRewriteWithOpts(t,
			RewriteOptions{
				Forget:                true,
				ExcludePatternOptions: filter.ExcludePatternOptions{Excludes: []string{"*.txt"}},
			},
			env.gopts,
			[]string{"latest"})
		rtest.OK(t, err)
		newSnapshots := testListSnapshots(t, env.gopts, 1)
		rtest.Assert(t, snapshots[0] != newSnapshots[0], "snapshot id should have changed")

		testLsOutputContainsCount(t, env.gopts, LsOptions{}, []string{"latest"}, ".txt", 0)
	})

	t.Run("IncludeNothing", func(t *testing.T) {
		env, cleanup := withCopiedRewriteRepo(t, template)
		defer cleanup()
		snapsBefore := testListSnapshots(t, env.gopts, 1)

		err := testRunRewriteWithOpts(t,
			RewriteOptions{
				Forget:                true,
				IncludePatternOptions: filter.IncludePatternOptions{Includes: []string{"nothing-whatsoever"}},
			},
			env.gopts,
			[]string{"latest"})
		rtest.OK(t, err)

		snapsAfter := testListSnapshots(t, env.gopts, 1)
		rtest.Assert(t, snapsBefore[0] == snapsAfter[0], "snapshots should be identical but are %s and %s",
			snapsBefore[0].Str(), snapsAfter[0].Str())
	})
}

func TestRewriteExcludeIncludeContradiction(t *testing.T) {
	env, cleanup := withTestEnvironment(t)
	defer cleanup()
	testRunInit(t, env.gopts)

	// test contradiction
	err := withTermStatus(t, env.gopts, func(ctx context.Context, gopts global.Options) error {
		return runRewrite(ctx,
			RewriteOptions{
				ExcludePatternOptions: filter.ExcludePatternOptions{Excludes: []string{"nonsense"}},
				IncludePatternOptions: filter.IncludePatternOptions{Includes: []string{"not allowed"}},
			},
			gopts, []string{"quack"}, env.gopts.Term)
	})
	rtest.Assert(t, err != nil && strings.Contains(err.Error(), "exclude and include patterns are mutually exclusive"), `expected to fail command with message "exclude and include patterns are mutually exclusive"`)
}

func TestRewriteIncludeEmptyDirectory(t *testing.T) {
	env, cleanup := withTestEnvironment(t)
	defer cleanup()
	snapIDEmpty := createBasicRewriteRepoWithEmptyDirectory(t, env)

	// restic rewrite <snapshots[0]> -i empty-directory --forget
	// exclude txt files
	err := testRunRewriteWithOpts(t,
		RewriteOptions{
			Forget:                true,
			IncludePatternOptions: filter.IncludePatternOptions{Includes: []string{"empty-directory"}},
		},
		env.gopts,
		[]string{"latest"})
	rtest.OK(t, err)
	newSnapshots := testListSnapshots(t, env.gopts, 1)
	rtest.Assert(t, snapIDEmpty != newSnapshots[0], "snapshot id should have changed")

	testLsOutputContainsCount(t, env.gopts, LsOptions{}, []string{"latest"}, "empty-directory", 1)
}

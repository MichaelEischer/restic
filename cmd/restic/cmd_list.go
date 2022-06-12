package main

import (
	"context"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/index"
	"github.com/restic/restic/internal/restic"

	"github.com/spf13/cobra"
)

var cmdList = &cobra.Command{
	Use:   "list [flags] [blobs|packs|index|snapshots|keys|locks]",
	Short: "List objects in the repository",
	Long: `
The "list" command allows listing objects in the repository based on type.

EXIT STATUS
===========

Exit status is 0 if the command was successful, and non-zero if there was any error.
`,
	DisableAutoGenTag: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runList(cmd.Context(), cmd, globalOptions, args)
	},
}

func init() {
	cmdRoot.AddCommand(cmdList)
}

func runList(ctx context.Context, cmd *cobra.Command, opts GlobalOptions, args []string) error {
	if len(args) != 1 {
		return errors.Fatal("type not specified, usage: " + cmd.Use)
	}

	repo, err := OpenRepository(ctx, opts)
	if err != nil {
		return err
	}

	if !opts.NoLock && args[0] != "locks" {
		var lock *data.Lock
		lock, ctx, err = lockRepo(ctx, repo)
		defer unlockRepo(lock)
		if err != nil {
			return err
		}
	}

	var t backend.FileType
	switch args[0] {
	case "packs":
		t = backend.PackFile
	case "index":
		t = backend.IndexFile
	case "snapshots":
		t = backend.SnapshotFile
	case "keys":
		t = backend.KeyFile
	case "locks":
		t = backend.LockFile
	case "blobs":
		return index.ForAllIndexes(ctx, repo, func(id restic.ID, idx *index.Index, oldFormat bool, err error) error {
			if err != nil {
				return err
			}
			idx.Each(ctx, func(blobs restic.PackedBlob) {
				Printf("%v %v\n", blobs.Type, blobs.ID)
			})
			return nil
		})
	default:
		return errors.Fatal("invalid type")
	}

	return repo.List(ctx, t, func(id restic.ID, size int64) error {
		Printf("%s\n", id)
		return nil
	})
}

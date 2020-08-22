// +build debug

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/pack"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

var cmdDebug = &cobra.Command{
	Use:   "debug",
	Short: "Debug commands",
}

const changeIDSafetyToken = "i-understand-that-this-could-break-my-repository-and-i-have-created-a-backup-of-the-config-file"

var cmdDebugChangeID = &cobra.Command{
	Use:   "changeID [" + changeIDSafetyToken + " oldRepoID]",
	Short: "Change repository id",
	Long: `
The "changeID" command will rewrite the config file of a repository and change its ID. Use with
caution! Always create a backup of the 'config' file of a repository first! If this operation fails,
the repository _WILL BECOME UNREADABLE_! To repair the damage, restore the old config file.

EXIT STATUS
===========

Exit status is 0 if the command was successful, and non-zero if there was any error.
`,
	DisableAutoGenTag: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDebugChangeID(globalOptions, args)
	},
}

var cmdDebugDump = &cobra.Command{
	Use:   "dump [indexes|snapshots|all|packs]",
	Short: "Dump data structures",
	Long: `
The "dump" command dumps data structures from the repository as JSON objects. It
is used for debugging purposes only.

EXIT STATUS
===========

Exit status is 0 if the command was successful, and non-zero if there was any error.
`,
	DisableAutoGenTag: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDebugDump(globalOptions, args)
	},
}

func init() {
	cmdRoot.AddCommand(cmdDebug)
	cmdDebug.AddCommand(cmdDebugChangeID)
	cmdDebug.AddCommand(cmdDebugDump)
}

func changeRepoID(r *repository.Repository, expectedRepoID string) error {
	var cfg restic.Config
	ctx := context.TODO()

	Verbosef("loading config file\n")
	err := r.LoadJSONUnpacked(ctx, restic.ConfigFile, restic.ID{}, &cfg)
	if err != nil {
		return err
	}

	if expectedRepoID != cfg.ID {
		return errors.Fatalf("expected repository id %v, found %v, aborting", expectedRepoID, cfg.ID)
	}

	cfg.ID = restic.NewRandomID().String()

	Verbosef("deleting old config file\n")
	err = r.Backend().Remove(ctx, restic.Handle{Type: restic.ConfigFile})
	if err != nil {
		return err
	}

	Verbosef("storing modified config file\n")
	_, err = r.SaveJSONUnpacked(ctx, restic.ConfigFile, cfg)
	if err == nil {
		Verbosef("operation succeeded\n")
	}
	return err
}

func runDebugChangeID(gopts GlobalOptions, args []string) error {
	if len(args) != 2 || args[0] != "i-understand-that-this-could-break-my-repository-and-i-have-created-a-backup-of-the-config-file" {
		return errors.Fatal("warning not acknowledged, aborting")
	}

	repo, err := OpenRepository(gopts)
	if err != nil {
		return err
	}

	lock, err := lockRepoExclusive(repo)
	defer unlockRepo(lock)
	if err != nil {
		return err
	}

	return changeRepoID(repo, args[1])
}

func prettyPrintJSON(wr io.Writer, item interface{}) error {
	buf, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return err
	}

	_, err = wr.Write(append(buf, '\n'))
	return err
}

func debugPrintSnapshots(repo *repository.Repository, wr io.Writer) error {
	return repo.List(context.TODO(), restic.SnapshotFile, func(id restic.ID, size int64) error {
		snapshot, err := restic.LoadSnapshot(context.TODO(), repo, id)
		if err != nil {
			return err
		}

		fmt.Fprintf(wr, "snapshot_id: %v\n", id)

		return prettyPrintJSON(wr, snapshot)
	})
}

// Pack is the struct used in printPacks.
type Pack struct {
	Name string `json:"name"`

	Blobs []Blob `json:"blobs"`
}

// Blob is the struct used in printPacks.
type Blob struct {
	Type   restic.BlobType `json:"type"`
	Length uint            `json:"length"`
	ID     restic.ID       `json:"id"`
	Offset uint            `json:"offset"`
}

func printPacks(repo *repository.Repository, wr io.Writer) error {

	return repo.List(context.TODO(), restic.PackFile, func(id restic.ID, size int64) error {
		h := restic.Handle{Type: restic.PackFile, Name: id.String()}

		blobs, err := pack.List(repo.Key(), restic.ReaderAt(repo.Backend(), h), size)
		if err != nil {
			Warnf("error for pack %v: %v\n", id.Str(), err)
			return nil
		}

		p := Pack{
			Name:  id.String(),
			Blobs: make([]Blob, len(blobs)),
		}
		for i, blob := range blobs {
			p.Blobs[i] = Blob{
				Type:   blob.Type,
				Length: blob.Length,
				ID:     blob.ID,
				Offset: blob.Offset,
			}
		}

		return prettyPrintJSON(wr, p)
	})
}

func dumpIndexes(repo restic.Repository, wr io.Writer) error {
	return repo.List(context.TODO(), restic.IndexFile, func(id restic.ID, size int64) error {
		Printf("index_id: %v\n", id)

		idx, err := repository.LoadIndex(context.TODO(), repo, id)
		if err != nil {
			return err
		}

		return idx.Dump(wr)
	})
}

func runDebugDump(gopts GlobalOptions, args []string) error {
	if len(args) != 1 {
		return errors.Fatal("type not specified")
	}

	repo, err := OpenRepository(gopts)
	if err != nil {
		return err
	}

	if !gopts.NoLock {
		lock, err := lockRepo(repo)
		defer unlockRepo(lock)
		if err != nil {
			return err
		}
	}

	tpe := args[0]

	switch tpe {
	case "indexes":
		return dumpIndexes(repo, gopts.stdout)
	case "snapshots":
		return debugPrintSnapshots(repo, gopts.stdout)
	case "packs":
		return printPacks(repo, gopts.stdout)
	case "all":
		Printf("snapshots:\n")
		err := debugPrintSnapshots(repo, gopts.stdout)
		if err != nil {
			return err
		}

		Printf("\nindexes:\n")
		err = dumpIndexes(repo, gopts.stdout)
		if err != nil {
			return err
		}

		return nil
	default:
		return errors.Fatalf("no such type %q", tpe)
	}
}

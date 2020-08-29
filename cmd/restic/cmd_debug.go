//go:build debug
// +build debug

package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/crypto"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
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
		return runDebugChangeID(cmd.Context(), globalOptions, args)
	},
}

var cmdDebugFakeConfig = &cobra.Command{
	Use:   "fakeConfig",
	Short: "Create a 'fake' config for a repository",
	Long: `
The "fakeConfig" command will create a fake config file for a repository in case the config file
is missing. It will only change anything if the supplied encryption key is valid and no config
file exists. Use with caution!

EXIT STATUS
===========

Exit status is 0 if the command was successful, and non-zero if there was any error.
`,
	DisableAutoGenTag: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDebugFakeConfig(cmd.Context(), globalOptions, debugFakeConfigOpts, args)
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

Exit status is 0 if the command was successful.
Exit status is 1 if there was any error.
Exit status is 10 if the repository does not exist.
Exit status is 11 if the repository is already locked.
`,
	DisableAutoGenTag: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDebugDump(cmd.Context(), globalOptions, args)
	},
}

type DebugExamineOptions struct {
	TryRepair     bool
	RepairByte    bool
	ExtractPack   bool
	ReuploadBlobs bool
}

var debugExamineOpts DebugExamineOptions

type DebugFakeConfigOptions struct {
	RepositoryVersion string
}

var debugFakeConfigOpts DebugFakeConfigOptions

func init() {
	cmdRoot.AddCommand(cmdDebug)
	cmdDebug.AddCommand(cmdDebugChangeID)
	cmdDebug.AddCommand(cmdDebugDump)
	cmdDebug.AddCommand(cmdDebugExamine)
	cmdDebugExamine.Flags().BoolVar(&debugExamineOpts.ExtractPack, "extract-pack", false, "write blobs to the current directory")
	cmdDebugExamine.Flags().BoolVar(&debugExamineOpts.ReuploadBlobs, "reupload-blobs", false, "reupload blobs to the repository")
	cmdDebugExamine.Flags().BoolVar(&debugExamineOpts.TryRepair, "try-repair", false, "try to repair broken blobs with single bit flips")
	cmdDebugExamine.Flags().BoolVar(&debugExamineOpts.RepairByte, "repair-byte", false, "try to repair broken blobs by trying bytes")

	cmdDebug.AddCommand(cmdDebugFakeConfig)
	cmdDebug.Flags().StringVar(&debugFakeConfigOpts.RepositoryVersion, "repository-version", "stable", "repository format version to use, allowed values are a format version, 'latest' and 'stable'")
}

func changeRepoID(ctx context.Context, r *repository.Repository, expectedRepoID string) error {

	Verbosef("loading config file\n")
	cfg, err := restic.LoadConfig(ctx, r)
	if err != nil {
		return err
	}

	if expectedRepoID != cfg.ID {
		return errors.Fatalf("expected repository id %v, found %v, aborting", expectedRepoID, cfg.ID)
	}

	cfg.ID = restic.NewRandomID().String()

	// use a separate context to prevent a user from accidentally breaking the repository
	safeCtx := context.Background()
	Verbosef("deleting old config file\n")
	err = r.RemoveUnpacked(safeCtx, restic.ConfigFile, restic.ID{})
	if err != nil {
		return err
	}

	Verbosef("storing modified config file\n")
	err = restic.SaveConfig(safeCtx, r, cfg)
	if err == nil {
		Verbosef("operation succeeded\n")
	}
	return err
}

func runDebugChangeID(ctx context.Context, gopts GlobalOptions, args []string) error {
	if len(args) != 2 || args[0] != "i-understand-that-this-could-break-my-repository-and-i-have-created-a-backup-of-the-config-file" {
		return errors.Fatal("warning not acknowledged, aborting")
	}

	ctx, repo, unlock, err := openWithExclusiveLock(ctx, gopts, false)
	if err != nil {
		return err
	}
	defer unlock()

	return changeRepoID(ctx, repo, args[1])
}

func runDebugFakeConfig(ctx context.Context, gopts GlobalOptions, opts DebugFakeConfigOptions, args []string) error {
	if len(args) != 0 {
		return errors.Fatal("unexpected parameters, aborting")
	}
	if gopts.Repo == "" {
		return errors.Fatal("Please specify repository location (-r)")
	}
	var version uint
	if opts.RepositoryVersion == "latest" || opts.RepositoryVersion == "" {
		version = restic.MaxRepoVersion
	} else if opts.RepositoryVersion == "stable" {
		version = restic.StableRepoVersion
	} else {
		v, err := strconv.ParseUint(opts.RepositoryVersion, 10, 32)
		if err != nil {
			return errors.Fatal("invalid repository version")
		}
		version = uint(v)
	}
	if version < restic.MinRepoVersion || version > restic.MaxRepoVersion {
		return errors.Fatalf("only repository versions between %v and %v are allowed", restic.MinRepoVersion, restic.MaxRepoVersion)
	}

	// open the repository
	be, err := innerOpen(ctx, gopts.Repo, gopts, gopts.extended, false)
	if err != nil {
		return fmt.Errorf("innerOpen: %w", err)
	}

	report := func(msg string, err error, d time.Duration) {
		if d >= 0 {
			Warnf("%v returned error, retrying after %v: %v\n", msg, d, err)
		} else {
			Warnf("%v failed: %v\n", msg, err)
		}
	}
	success := func(msg string, retries int) {
		Warnf("%v operation successful after %d retries\n", msg, retries)
	}
	be = retry.New(be, 15*time.Minute, report, success)

	s, err := repository.New(be, repository.Options{
		Compression: gopts.Compression,
		PackSize:    gopts.PackSize * 1024 * 1024,
	})
	if err != nil {
		return fmt.Errorf("repository.New: %w", err)
	}

	// try to find a matching key, but make sure there's no config file
	gopts.password, err = ReadPassword(ctx, gopts, "enter password for repository: ")
	if err != nil {
		return err
	}
	err = s.SearchKey(ctx, gopts.password, maxKeys, gopts.KeyHint, true)
	if err == nil {
		return errors.Fatalf("successfully loaded the repository config, will NOT overwrite it")
		// abort config file is intact
	}
	if s.Key() == nil {
		// Failed to decrypt the key
		return err
	}

	// check again
	_, err = be.Stat(ctx, backend.Handle{Type: restic.ConfigFile})
	if err == nil {
		return errors.Fatalf("found an (invalid?) config file in the repository, will NOT overwrite it")
	}

	// no need for locking as without a config file noone else can open the repository
	// write a new config file. (Most?) backends will not overwrite files, which provides another layer of protection
	cfg, err := restic.CreateConfig(version)
	if err != nil {
		return err
	}
	err = restic.SaveConfig(ctx, s, cfg)
	if err != nil {
		return err
	}

	fmt.Println("\nSuccessfully created fake config file.")
	return nil
}

func prettyPrintJSON(wr io.Writer, item interface{}) error {
	buf, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return err
	}

	_, err = wr.Write(append(buf, '\n'))
	return err
}

func debugPrintSnapshots(ctx context.Context, repo *repository.Repository, wr io.Writer) error {
	return restic.ForAllSnapshots(ctx, repo, repo, nil, func(id restic.ID, snapshot *restic.Snapshot, err error) error {
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

func printPacks(ctx context.Context, repo *repository.Repository, wr io.Writer) error {

	var m sync.Mutex
	return restic.ParallelList(ctx, repo, restic.PackFile, repo.Connections(), func(ctx context.Context, id restic.ID, size int64) error {
		blobs, _, err := repo.ListPack(ctx, id, size)
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

		m.Lock()
		defer m.Unlock()
		return prettyPrintJSON(wr, p)
	})
}

func dumpIndexes(ctx context.Context, repo restic.ListerLoaderUnpacked, wr io.Writer) error {
	return index.ForAllIndexes(ctx, repo, repo, func(id restic.ID, idx *index.Index, oldFormat bool, err error) error {
		Printf("index_id: %v\n", id)
		if err != nil {
			return err
		}

		return idx.Dump(wr)
	})
}

func runDebugDump(ctx context.Context, gopts GlobalOptions, args []string) error {
	if len(args) != 1 {
		return errors.Fatal("type not specified")
	}

	ctx, repo, unlock, err := openWithReadLock(ctx, gopts, gopts.NoLock)
	if err != nil {
		return err
	}
	defer unlock()

	tpe := args[0]

	switch tpe {
	case "indexes":
		return dumpIndexes(ctx, repo, globalOptions.stdout)
	case "snapshots":
		return debugPrintSnapshots(ctx, repo, globalOptions.stdout)
	case "packs":
		return printPacks(ctx, repo, globalOptions.stdout)
	case "all":
		Printf("snapshots:\n")
		err := debugPrintSnapshots(ctx, repo, globalOptions.stdout)
		if err != nil {
			return err
		}

		Printf("\nindexes:\n")
		err = dumpIndexes(ctx, repo, globalOptions.stdout)
		if err != nil {
			return err
		}

		return nil
	default:
		return errors.Fatalf("no such type %q", tpe)
	}
}

var cmdDebugExamine = &cobra.Command{
	Use:               "examine pack-ID...",
	Short:             "Examine a pack file",
	DisableAutoGenTag: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDebugExamine(cmd.Context(), globalOptions, debugExamineOpts, args)
	},
}

func tryRepairWithBitflip(ctx context.Context, key *crypto.Key, input []byte, bytewise bool) []byte {
	if bytewise {
		Printf("        trying to repair blob by finding a broken byte\n")
	} else {
		Printf("        trying to repair blob with single bit flip\n")
	}

	ch := make(chan int)
	var wg errgroup.Group
	done := make(chan struct{})
	var fixed []byte
	var found bool

	workers := runtime.GOMAXPROCS(0)
	Printf("         spinning up %d worker functions\n", runtime.GOMAXPROCS(0))
	for i := 0; i < workers; i++ {
		wg.Go(func() error {
			// make a local copy of the buffer
			buf := make([]byte, len(input))
			copy(buf, input)

			testFlip := func(idx int, pattern byte) bool {
				// flip bits
				buf[idx] ^= pattern

				nonce, plaintext := buf[:key.NonceSize()], buf[key.NonceSize():]
				plaintext, err := key.Open(plaintext[:0], nonce, plaintext, nil)
				if err == nil {
					Printf("\n")
					Printf("        blob could be repaired by XORing byte %v with 0x%02x\n", idx, pattern)
					Printf("        hash is %v\n", restic.Hash(plaintext))
					close(done)
					found = true
					fixed = plaintext
					return true
				}

				// flip bits back
				buf[idx] ^= pattern
				return false
			}

			for i := range ch {
				if bytewise {
					for j := 0; j < 255; j++ {
						if testFlip(i, byte(j)) {
							return nil
						}
					}
				} else {
					for j := 0; j < 7; j++ {
						// flip each bit once
						if testFlip(i, (1 << uint(j))) {
							return nil
						}
					}
				}
			}
			return nil
		})
	}

	wg.Go(func() error {
		defer close(ch)

		start := time.Now()
		info := time.Now()
		for i := range input {
			select {
			case ch <- i:
			case <-done:
				Printf("     done after %v\n", time.Since(start))
				return nil
			}

			if time.Since(info) > time.Second {
				secs := time.Since(start).Seconds()
				gps := float64(i) / secs
				remaining := len(input) - i
				eta := time.Duration(float64(remaining)/gps) * time.Second

				Printf("\r%d byte of %d done (%.2f%%), %.0f byte per second, ETA %v",
					i, len(input), float32(i)/float32(len(input))*100, gps, eta)
				info = time.Now()
			}
		}
		return nil
	})
	err := wg.Wait()
	if err != nil {
		panic("all go routines can only return nil")
	}

	if !found {
		Printf("\n        blob could not be repaired\n")
	}
	return fixed
}

func decryptUnsigned(ctx context.Context, k *crypto.Key, buf []byte) []byte {
	// strip signature at the end
	l := len(buf)
	nonce, ct := buf[:16], buf[16:l-16]
	out := make([]byte, len(ct))

	c, err := aes.NewCipher(k.EncryptionKey[:])
	if err != nil {
		panic(fmt.Sprintf("unable to create cipher: %v", err))
	}
	e := cipher.NewCTR(c, nonce)
	e.XORKeyStream(out, ct)

	return out
}

func loadBlobs(ctx context.Context, opts DebugExamineOptions, repo restic.Repository, packID restic.ID, list []restic.Blob) error {
	dec, err := zstd.NewReader(nil)
	if err != nil {
		panic(err)
	}

	pack, err := repo.LoadRaw(ctx, restic.PackFile, packID)
	// allow processing broken pack files
	if pack == nil {
		return err
	}

	wg, ctx := errgroup.WithContext(ctx)

	if opts.ReuploadBlobs {
		repo.StartPackUploader(ctx, wg)
	}

	wg.Go(func() error {
		for _, blob := range list {
			Printf("      loading blob %v at %v (length %v)\n", blob.ID, blob.Offset, blob.Length)
			if int(blob.Offset+blob.Length) > len(pack) {
				Warnf("skipping truncated blob\n")
				continue
			}
			buf := pack[blob.Offset : blob.Offset+blob.Length]
			key := repo.Key()

			nonce, plaintext := buf[:key.NonceSize()], buf[key.NonceSize():]
			plaintext, err = key.Open(plaintext[:0], nonce, plaintext, nil)
			outputPrefix := ""
			filePrefix := ""
			if err != nil {
				Warnf("error decrypting blob: %v\n", err)
				if opts.TryRepair || opts.RepairByte {
					plaintext = tryRepairWithBitflip(ctx, key, buf, opts.RepairByte)
				}
				if plaintext != nil {
					outputPrefix = "repaired "
					filePrefix = "repaired-"
				} else {
					plaintext = decryptUnsigned(ctx, key, buf)
					err = storePlainBlob(blob.ID, "damaged-", plaintext)
					if err != nil {
						return err
					}
					continue
				}
			}

			if blob.IsCompressed() {
				decompressed, err := dec.DecodeAll(plaintext, nil)
				if err != nil {
					Printf("         failed to decompress blob %v\n", blob.ID)
				}
				if decompressed != nil {
					plaintext = decompressed
				}
			}

			id := restic.Hash(plaintext)
			var prefix string
			if !id.Equal(blob.ID) {
				Printf("         successfully %vdecrypted blob (length %v), hash is %v, ID does not match, wanted %v\n", outputPrefix, len(plaintext), id, blob.ID)
				prefix = "wrong-hash-"
			} else {
				Printf("         successfully %vdecrypted blob (length %v), hash is %v, ID matches\n", outputPrefix, len(plaintext), id)
				prefix = "correct-"
			}
			if opts.ExtractPack {
				err = storePlainBlob(id, filePrefix+prefix, plaintext)
				if err != nil {
					return err
				}
			}
			if opts.ReuploadBlobs {
				_, _, _, err := repo.SaveBlob(ctx, blob.Type, plaintext, id, true)
				if err != nil {
					return err
				}
				Printf("         uploaded %v %v\n", blob.Type, id)
			}
		}

		if opts.ReuploadBlobs {
			return repo.Flush(ctx)
		}
		return nil
	})

	return wg.Wait()
}

func storePlainBlob(id restic.ID, prefix string, plain []byte) error {
	filename := fmt.Sprintf("%s%s.bin", prefix, id)
	f, err := os.Create(filename)
	if err != nil {
		return err
	}

	_, err = f.Write(plain)
	if err != nil {
		_ = f.Close()
		return err
	}

	err = f.Close()
	if err != nil {
		return err
	}

	Printf("decrypt of blob %v stored at %v\n", id, filename)
	return nil
}

func runDebugExamine(ctx context.Context, gopts GlobalOptions, opts DebugExamineOptions, args []string) error {
	if opts.ExtractPack && gopts.NoLock {
		return fmt.Errorf("--extract-pack and --no-lock are mutually exclusive")
	}

	ctx, repo, unlock, err := openWithAppendLock(ctx, gopts, gopts.NoLock)
	if err != nil {
		return err
	}
	defer unlock()

	ids := make([]restic.ID, 0)
	for _, name := range args {
		id, err := restic.ParseID(name)
		if err != nil {
			id, err = restic.Find(ctx, repo, restic.PackFile, name)
			if err != nil {
				Warnf("error: %v\n", err)
				continue
			}
		}
		ids = append(ids, id)
	}

	if len(ids) == 0 {
		return errors.Fatal("no pack files to examine")
	}

	bar := newIndexProgress(gopts.Quiet, gopts.JSON)
	err = repo.LoadIndex(ctx, bar)
	if err != nil {
		return err
	}

	for _, id := range ids {
		err := examinePack(ctx, opts, repo, id)
		if err != nil {
			Warnf("error: %v\n", err)
		}
		if err == context.Canceled {
			break
		}
	}
	return nil
}

func examinePack(ctx context.Context, opts DebugExamineOptions, repo restic.Repository, id restic.ID) error {
	Printf("examine %v\n", id)

	buf, err := repo.LoadRaw(ctx, restic.PackFile, id)
	// also process damaged pack files
	if buf == nil {
		return err
	}
	Printf("  file size is %v\n", len(buf))
	gotID := restic.Hash(buf)
	if !id.Equal(gotID) {
		Printf("  wanted hash %v, got %v\n", id, gotID)
	} else {
		Printf("  hash for file content matches\n")
	}

	Printf("  ========================================\n")
	Printf("  looking for info in the indexes\n")

	blobsLoaded := false
	// examine all data the indexes have for the pack file
	for b := range repo.ListPacksFromIndex(ctx, restic.NewIDSet(id)) {
		blobs := b.Blobs
		if len(blobs) == 0 {
			continue
		}

		checkPackSize(blobs, len(buf))

		err = loadBlobs(ctx, opts, repo, id, blobs)
		if err != nil {
			Warnf("error: %v\n", err)
		} else {
			blobsLoaded = true
		}
	}

	Printf("  ========================================\n")
	Printf("  inspect the pack itself\n")

	blobs, _, err := repo.ListPack(ctx, id, int64(len(buf)))
	if err != nil {
		return fmt.Errorf("pack %v: %v", id.Str(), err)
	}
	checkPackSize(blobs, len(buf))

	if !blobsLoaded {
		return loadBlobs(ctx, opts, repo, id, blobs)
	}
	return nil
}

func checkPackSize(blobs []restic.Blob, fileSize int) {
	// track current size and offset
	var size, offset uint64

	sort.Slice(blobs, func(i, j int) bool {
		return blobs[i].Offset < blobs[j].Offset
	})

	for _, pb := range blobs {
		Printf("      %v blob %v, offset %-6d, raw length %-6d\n", pb.Type, pb.ID, pb.Offset, pb.Length)
		if offset != uint64(pb.Offset) {
			Printf("      hole in file, want offset %v, got %v\n", offset, pb.Offset)
		}
		offset = uint64(pb.Offset + pb.Length)
		size += uint64(pb.Length)
	}
	size += uint64(pack.CalculateHeaderSize(blobs))

	if uint64(fileSize) != size {
		Printf("      file sizes do not match: computed %v, file size is %v\n", size, fileSize)
	} else {
		Printf("      file sizes match\n")
	}
}

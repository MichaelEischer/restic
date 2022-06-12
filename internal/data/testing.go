package data

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"testing"
	"time"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/restic"
	"golang.org/x/sync/errgroup"
)

// fakeFile returns a reader which yields deterministic pseudo-random data.
func fakeFile(seed, size int64) io.Reader {
	return io.LimitReader(rand.New(rand.NewSource(seed)), size)
}

type fakeFileSystem struct {
	t           testing.TB
	repo        restic.Repository
	duplication float32
	buf         []byte
	chunker     *chunker.Chunker
	rand        *rand.Rand
}

// saveFile reads from rd and saves the blobs in the repository. The list of
// IDs is returned.
func (fs *fakeFileSystem) saveFile(ctx context.Context, rd io.Reader) (blobs restic.IDs) {
	if fs.buf == nil {
		fs.buf = make([]byte, chunker.MaxSize)
	}

	if fs.chunker == nil {
		fs.chunker = chunker.New(rd, fs.repo.Config().ChunkerPolynomial)
	} else {
		fs.chunker.Reset(rd, fs.repo.Config().ChunkerPolynomial)
	}

	blobs = restic.IDs{}
	for {
		chunk, err := fs.chunker.Next(fs.buf)
		if err == io.EOF {
			break
		}

		if err != nil {
			fs.t.Fatalf("unable to save chunk in repo: %v", err)
		}

		id := restic.Hash(chunk.Data)
		if !fs.blobIsKnown(restic.BlobHandle{ID: id, Type: restic.DataBlob}) {
			_, _, _, err := fs.repo.SaveBlob(ctx, restic.DataBlob, chunk.Data, id, true)
			if err != nil {
				fs.t.Fatalf("error saving chunk: %v", err)
			}

		}

		blobs = append(blobs, id)
	}

	return blobs
}

const (
	maxFileSize = 20000
	maxSeed     = 32
	maxNodes    = 15
)

func (fs *fakeFileSystem) treeIsKnown(tree *Tree) (bool, []byte, restic.ID) {
	data, err := json.Marshal(tree)
	if err != nil {
		fs.t.Fatalf("json.Marshal(tree) returned error: %v", err)
		return false, nil, restic.ID{}
	}
	data = append(data, '\n')

	id := restic.Hash(data)
	return fs.blobIsKnown(restic.BlobHandle{ID: id, Type: restic.TreeBlob}), data, id
}

func (fs *fakeFileSystem) blobIsKnown(bh restic.BlobHandle) bool {
	if fs.rand.Float32() < fs.duplication {
		return false
	}

	if fs.repo.Index().Has(bh) {
		return true
	}

	return false
}

// saveTree saves a tree of fake files in the repo and returns the ID.
func (fs *fakeFileSystem) saveTree(ctx context.Context, seed int64, depth int) restic.ID {
	rnd := rand.NewSource(seed)
	numNodes := int(rnd.Int63() % maxNodes)

	var tree Tree
	for i := 0; i < numNodes; i++ {

		// randomly select the type of the node, either tree (p = 1/4) or file (p = 3/4).
		if depth > 1 && rnd.Int63()%4 == 0 {
			treeSeed := rnd.Int63() % maxSeed
			id := fs.saveTree(ctx, treeSeed, depth-1)

			node := &Node{
				Name:    fmt.Sprintf("dir-%v", treeSeed),
				Type:    "dir",
				Mode:    0755,
				Subtree: &id,
			}

			tree.Nodes = append(tree.Nodes, node)
			continue
		}

		fileSeed := rnd.Int63() % maxSeed
		fileSize := (maxFileSize / maxSeed) * fileSeed

		node := &Node{
			Name: fmt.Sprintf("file-%v", fileSeed),
			Type: "file",
			Mode: 0644,
			Size: uint64(fileSize),
		}

		node.Content = fs.saveFile(ctx, fakeFile(fileSeed, fileSize))
		tree.Nodes = append(tree.Nodes, node)
	}

	known, buf, id := fs.treeIsKnown(&tree)
	if known {
		return id
	}

	_, _, _, err := fs.repo.SaveBlob(ctx, restic.TreeBlob, buf, id, false)
	if err != nil {
		fs.t.Fatal(err)
	}

	return id
}

// TestCreateSnapshot creates a snapshot filled with fake data. The
// fake data is generated deterministically from the timestamp `at`, which is
// also used as the snapshot's timestamp. The tree's depth can be specified
// with the parameter depth. The parameter duplication is a probability that
// the same blob will saved again.
func TestCreateSnapshot(t testing.TB, repo restic.Repository, at time.Time, depth int, duplication float32) *Snapshot {
	seed := at.Unix()
	t.Logf("create fake snapshot at %s with seed %d", at, seed)

	fakedir := fmt.Sprintf("fakedir-at-%v", at.Format("2006-01-02 15:04:05"))
	snapshot, err := NewSnapshot([]string{fakedir}, []string{"test"}, "foo", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Time = at

	fs := fakeFileSystem{
		t:           t,
		repo:        repo,
		duplication: duplication,
		rand:        rand.New(rand.NewSource(seed)),
	}

	var wg errgroup.Group
	repo.StartPackUploader(context.TODO(), &wg)

	treeID := fs.saveTree(context.TODO(), seed, depth)
	snapshot.Tree = &treeID

	err = repo.Flush(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	id, err := SaveSnapshot(context.TODO(), repo, snapshot)
	if err != nil {
		t.Fatal(err)
	}

	snapshot.id = &id

	t.Logf("saved snapshot %v", id.Str())

	return snapshot
}

// TestSetSnapshotID sets the snapshot's ID.
func TestSetSnapshotID(t testing.TB, sn *Snapshot, id restic.ID) {
	sn.id = &id
}

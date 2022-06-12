package data

import (
	"context"
	"fmt"
	"os/user"
	"path/filepath"
	"sync"
	"time"

	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/restic"
)

// Snapshot is the state of a resource at one point in time.
type Snapshot struct {
	Time     time.Time  `json:"time"`
	Parent   *restic.ID `json:"parent,omitempty"`
	Tree     *restic.ID `json:"tree"`
	Paths    []string   `json:"paths"`
	Hostname string     `json:"hostname,omitempty"`
	Username string     `json:"username,omitempty"`
	UID      uint32     `json:"uid,omitempty"`
	GID      uint32     `json:"gid,omitempty"`
	Excludes []string   `json:"excludes,omitempty"`
	Tags     []string   `json:"tags,omitempty"`
	Original *restic.ID `json:"original,omitempty"`

	id *restic.ID // plaintext ID, used during restore
}

// NewSnapshot returns an initialized snapshot struct for the current user and
// time.
func NewSnapshot(paths []string, tags []string, hostname string, time time.Time) (*Snapshot, error) {
	absPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		p, err := filepath.Abs(path)
		if err == nil {
			absPaths = append(absPaths, p)
		} else {
			absPaths = append(absPaths, path)
		}
	}

	sn := &Snapshot{
		Paths:    absPaths,
		Time:     time,
		Tags:     tags,
		Hostname: hostname,
	}

	err := sn.fillUserInfo()
	if err != nil {
		return nil, err
	}

	return sn, nil
}

// LoadSnapshot loads the snapshot with the id and returns it.
func LoadSnapshot(ctx context.Context, loader restic.LoaderUnpacked, id restic.ID) (*Snapshot, error) {
	sn := &Snapshot{id: &id}
	err := restic.LoadJSONUnpacked(ctx, loader, restic.SnapshotFile, id, sn)
	if err != nil {
		return nil, err
	}

	return sn, nil
}

// SaveSnapshot saves the snapshot sn and returns its ID.
func SaveSnapshot(ctx context.Context, repo restic.SaverUnpacked, sn *Snapshot) (restic.ID, error) {
	return restic.SaveJSONUnpacked(ctx, repo, restic.SnapshotFile, sn)
}

// ForAllSnapshots reads all snapshots in parallel and calls the
// given function. It is guaranteed that the function is not run concurrently.
// If the called function returns an error, this function is cancelled and
// also returns this error.
// If a snapshot ID is in excludeIDs, it will be ignored.
func ForAllSnapshots(ctx context.Context, repo ListLoader, excludeIDs restic.IDSet, fn func(restic.ID, *Snapshot, error) error) error {
	var m sync.Mutex

	// For most snapshots decoding is nearly for free, thus just assume were only limited by IO
	return restic.ParallelList(ctx, repo, restic.SnapshotFile, repo.Connections(), func(ctx context.Context, id restic.ID, size int64) error {
		if excludeIDs.Has(id) {
			return nil
		}

		sn, err := LoadSnapshot(ctx, repo, id)
		m.Lock()
		defer m.Unlock()
		return fn(id, sn, err)
	})
}

func (sn Snapshot) String() string {
	return fmt.Sprintf("<Snapshot %s of %v at %s by %s@%s>",
		sn.id.Str(), sn.Paths, sn.Time, sn.Username, sn.Hostname)
}

// ID returns the snapshot's ID.
func (sn Snapshot) ID() *restic.ID {
	return sn.id
}

func (sn *Snapshot) fillUserInfo() error {
	usr, err := user.Current()
	if err != nil {
		return nil
	}
	sn.Username = usr.Username

	// set userid and groupid
	sn.UID, sn.GID, err = uidGidInt(usr)
	return err
}

// AddTags adds the given tags to the snapshots tags, preventing duplicates.
// It returns true if any changes were made.
func (sn *Snapshot) AddTags(addTags []string) (changed bool) {
nextTag:
	for _, add := range addTags {
		for _, tag := range sn.Tags {
			if tag == add {
				continue nextTag
			}
		}
		sn.Tags = append(sn.Tags, add)
		changed = true
	}
	return
}

// RemoveTags removes the given tags from the snapshots tags and
// returns true if any changes were made.
func (sn *Snapshot) RemoveTags(removeTags []string) (changed bool) {
	for _, remove := range removeTags {
		for i, tag := range sn.Tags {
			if tag == remove {
				// https://github.com/golang/go/wiki/SliceTricks
				sn.Tags[i] = sn.Tags[len(sn.Tags)-1]
				sn.Tags[len(sn.Tags)-1] = ""
				sn.Tags = sn.Tags[:len(sn.Tags)-1]

				changed = true
				break
			}
		}
	}
	return
}

func (sn *Snapshot) hasTag(tag string) bool {
	for _, snTag := range sn.Tags {
		if tag == snTag {
			return true
		}
	}
	return false
}

// HasTags returns true if the snapshot has all the tags in l.
func (sn *Snapshot) HasTags(l []string) bool {
	for _, tag := range l {
		if tag == "" && len(sn.Tags) == 0 {
			return true
		}
		if !sn.hasTag(tag) {
			return false
		}
	}

	return true
}

// HasTagList returns true if either
//   - the snapshot satisfies at least one TagList, so there is a TagList in l
//     for which all tags are included in sn, or
//   - l is empty
func (sn *Snapshot) HasTagList(l []TagList) bool {
	debug.Log("testing snapshot with tags %v against list: %v", sn.Tags, l)

	if len(l) == 0 {
		return true
	}

	for _, tags := range l {
		if sn.HasTags(tags) {
			debug.Log("  snapshot satisfies %v %v", tags, l)
			return true
		}
	}

	return false
}

// HasPaths returns true if the snapshot has all of the paths.
func (sn *Snapshot) HasPaths(paths []string) bool {
	m := make(map[string]struct{}, len(sn.Paths))
	for _, snPath := range sn.Paths {
		m[snPath] = struct{}{}
	}
	for _, path := range paths {
		if _, ok := m[path]; !ok {
			return false
		}
	}

	return true
}

// HasHostname returns true if either
// - the snapshot hostname is in the list of the given hostnames, or
// - the list of given hostnames is empty
func (sn *Snapshot) HasHostname(hostnames []string) bool {
	if len(hostnames) == 0 {
		return true
	}

	for _, hostname := range hostnames {
		if sn.Hostname == hostname {
			return true
		}
	}

	return false
}

// Snapshots is a list of snapshots.
type Snapshots []*Snapshot

// Len returns the number of snapshots in sn.
func (sn Snapshots) Len() int {
	return len(sn)
}

// Less returns true iff the ith snapshot has been made after the jth.
func (sn Snapshots) Less(i, j int) bool {
	return sn[i].Time.After(sn[j].Time)
}

// Swap exchanges the two snapshots.
func (sn Snapshots) Swap(i, j int) {
	sn[i], sn[j] = sn[j], sn[i]
}
package restic

import "github.com/restic/restic/internal/backend"

type FileType backend.FileType

const (
	PackFile     FileType = FileType(backend.PackFile)
	KeyFile      FileType = FileType(backend.KeyFile)
	LockFile     FileType = FileType(backend.LockFile)
	SnapshotFile FileType = FileType(backend.SnapshotFile)
	IndexFile    FileType = FileType(backend.IndexFile)
	ConfigFile   FileType = FileType(backend.ConfigFile)
)

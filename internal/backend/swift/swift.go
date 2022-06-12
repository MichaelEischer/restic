package swift

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/connections"
	"github.com/restic/restic/internal/backend/layout"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"

	"github.com/ncw/swift/v2"
)

// beSwift is a backend which stores the data on a swift endpoint.
type beSwift struct {
	conn        *swift.Connection
	connections uint
	container   string // Container name
	prefix      string // Prefix of object names in the container
	layout.Layout
	openReaderFn backend.OpenReaderFn
}

// ensure statically that *beSwift implements backend.Backend.
var _ backend.Backend = &beSwift{}

// Open opens the swift backend at a container in region. The container is
// created if it does not exist yet.
func Open(ctx context.Context, cfg Config, rt http.RoundTripper) (backend.Backend, error) {
	debug.Log("config %#v", cfg)

	be := &beSwift{
		conn: &swift.Connection{
			UserName:                    cfg.UserName,
			UserId:                      cfg.UserID,
			Domain:                      cfg.Domain,
			DomainId:                    cfg.DomainID,
			ApiKey:                      cfg.APIKey,
			AuthUrl:                     cfg.AuthURL,
			Region:                      cfg.Region,
			Tenant:                      cfg.Tenant,
			TenantId:                    cfg.TenantID,
			TenantDomain:                cfg.TenantDomain,
			TenantDomainId:              cfg.TenantDomainID,
			TrustId:                     cfg.TrustID,
			StorageUrl:                  cfg.StorageURL,
			AuthToken:                   cfg.AuthToken.Unwrap(),
			ApplicationCredentialId:     cfg.ApplicationCredentialID,
			ApplicationCredentialName:   cfg.ApplicationCredentialName,
			ApplicationCredentialSecret: cfg.ApplicationCredentialSecret.Unwrap(),
			ConnectTimeout:              time.Minute,
			Timeout:                     time.Minute,

			Transport: rt,
		},
		connections: cfg.Connections,
		container:   cfg.Container,
		prefix:      cfg.Prefix,
		Layout: &layout.DefaultLayout{
			Path: cfg.Prefix,
			Join: path.Join,
		},
	}

	// Authenticate if needed
	if !be.conn.Authenticated() {
		if err := be.conn.Authenticate(ctx); err != nil {
			return nil, errors.Wrap(err, "conn.Authenticate")
		}
	}

	// Ensure container exists
	switch _, _, err := be.conn.Container(ctx, be.container); err {
	case nil:
		// Container exists

	case swift.ContainerNotFound:
		err = be.createContainer(ctx, cfg.DefaultContainerPolicy)
		if err != nil {
			return nil, errors.Wrap(err, "beSwift.createContainer")
		}

	default:
		return nil, errors.Wrap(err, "conn.Container")
	}

	cbe := connections.New(be, cfg.Connections)
	be.openReaderFn = cbe.WrapReaderFn(be.openReader)
	return cbe, nil
}

func (be *beSwift) createContainer(ctx context.Context, policy string) error {
	var h swift.Headers
	if policy != "" {
		h = swift.Headers{
			"X-Storage-Policy": policy,
		}
	}

	return be.conn.ContainerCreate(ctx, be.container, h)
}

func (be *beSwift) Connections() uint {
	return be.connections
}

// Location returns this backend's location (the container name).
func (be *beSwift) Location() string {
	return be.container
}

// Hasher may return a hash function for calculating a content hash for the backend
func (be *beSwift) Hasher() hash.Hash {
	return md5.New()
}

// HasAtomicReplace returns whether Save() can atomically replace files
func (be *beSwift) HasAtomicReplace() bool {
	return true
}

// Load runs fn with a reader that yields the contents of the file at h at the
// given offset.
func (be *beSwift) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(rd io.Reader) error) error {
	return backend.DefaultLoad(ctx, h, length, offset, be.openReaderFn, fn)
}

func (be *beSwift) openReader(ctx context.Context, h backend.Handle, length int, offset int64) (io.ReadCloser, error) {
	objName := be.Filename(h)

	headers := swift.Headers{}
	if offset > 0 {
		headers["Range"] = fmt.Sprintf("bytes=%d-", offset)
	}

	if length > 0 {
		headers["Range"] = fmt.Sprintf("bytes=%d-%d", offset, offset+int64(length)-1)
	}

	if _, ok := headers["Range"]; ok {
		debug.Log("Load(%v) send range %v", h, headers["Range"])
	}

	obj, _, err := be.conn.ObjectOpen(ctx, be.container, objName, false, headers)
	if err != nil {
		debug.Log("  err %v", err)
		return nil, errors.Wrap(err, "conn.ObjectOpen")
	}

	return obj, nil
}

// Save stores data in the backend at the handle.
func (be *beSwift) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	objName := be.Filename(h)
	encoding := "binary/octet-stream"

	debug.Log("PutObject(%v, %v, %v)", be.container, objName, encoding)
	hdr := swift.Headers{"Content-Length": strconv.FormatInt(rd.Length(), 10)}
	_, err := be.conn.ObjectPut(ctx,
		be.container, objName, rd, true, hex.EncodeToString(rd.Hash()),
		encoding, hdr)
	// swift does not return the upload length
	debug.Log("%v, err %#v", objName, err)

	return errors.Wrap(err, "client.PutObject")
}

// Stat returns information about a blob.
func (be *beSwift) Stat(ctx context.Context, h backend.Handle) (bi backend.FileInfo, err error) {
	objName := be.Filename(h)
	obj, _, err := be.conn.Object(ctx, be.container, objName)
	if err != nil {
		debug.Log("Object() err %v", err)
		return backend.FileInfo{}, errors.Wrap(err, "conn.Object")
	}

	return backend.FileInfo{Size: obj.Bytes, Name: h.Name}, nil
}

// Remove removes the blob with the given name and type.
func (be *beSwift) Remove(ctx context.Context, h backend.Handle) error {
	objName := be.Filename(h)

	err := be.conn.ObjectDelete(ctx, be.container, objName)
	return errors.Wrap(err, "conn.ObjectDelete")
}

// List runs fn for each file in the backend which has the type t. When an
// error occurs (or fn returns an error), List stops and returns it.
func (be *beSwift) List(ctx context.Context, t backend.FileType, fn func(backend.FileInfo) error) error {
	prefix, _ := be.Basedir(t)
	prefix += "/"

	err := be.conn.ObjectsWalk(ctx, be.container, &swift.ObjectsOpts{Prefix: prefix},
		func(ctx context.Context, opts *swift.ObjectsOpts) (interface{}, error) {
			newObjects, err := be.conn.Objects(ctx, be.container, opts)

			if err != nil {
				return nil, errors.Wrap(err, "conn.ObjectNames")
			}
			for _, obj := range newObjects {
				m := path.Base(strings.TrimPrefix(obj.Name, prefix))
				if m == "" {
					continue
				}

				fi := backend.FileInfo{
					Name: m,
					Size: obj.Bytes,
				}

				err := fn(fi)
				if err != nil {
					return nil, err
				}

				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
			}
			return newObjects, nil
		})

	if err != nil {
		return err
	}

	return ctx.Err()
}

// Remove keys for a specified backend type.
func (be *beSwift) removeKeys(ctx context.Context, t backend.FileType) error {
	return be.List(ctx, t, func(fi backend.FileInfo) error {
		return be.Remove(ctx, backend.Handle{Type: t, Name: fi.Name})
	})
}

// IsNotExist returns true if the error is caused by a not existing file.
func (be *beSwift) IsNotExist(err error) bool {
	var e *swift.Error
	return errors.As(err, &e) && e.StatusCode == http.StatusNotFound
}

// Delete removes all restic objects in the container.
// It will not remove the container itself.
func (be *beSwift) Delete(ctx context.Context) error {
	alltypes := []backend.FileType{
		backend.PackFile,
		backend.KeyFile,
		backend.LockFile,
		backend.SnapshotFile,
		backend.IndexFile}

	for _, t := range alltypes {
		err := be.removeKeys(ctx, t)
		if err != nil {
			return nil
		}
	}

	err := be.Remove(ctx, backend.Handle{Type: backend.ConfigFile})
	if err != nil && !be.IsNotExist(err) {
		return err
	}

	return nil
}

// Close does nothing
func (be *beSwift) Close() error { return nil }

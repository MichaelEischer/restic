package test

import (
	"archive/tar"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/restic/restic/internal/errors"
)

// Assert fails the test if the condition is false.
func Assert(tb testing.TB, condition bool, msg string, v ...interface{}) {
	tb.Helper()
	if !condition {
		tb.Fatalf("\033[31m"+msg+"\033[39m\n\n", v...)
	}
}

// OK fails the test if an err is not nil.
func OK(tb testing.TB, err error) {
	tb.Helper()
	if err != nil {
		tb.Fatalf("\033[31munexpected error: %+v\033[39m\n\n", err)
	}
}

// OKs fails the test if any error from errs is not nil.
func OKs(tb testing.TB, errs []error) {
	tb.Helper()
	errFound := false
	for _, err := range errs {
		if err != nil {
			errFound = true
			tb.Logf("\033[31munexpected error: %+v\033[39m\n\n", err.Error())
		}
	}
	if errFound {
		tb.FailNow()
	}
}

// Equals fails the test if exp is not equal to act.
// msg is optional message to be printed, first param being format string and rest being arguments.
func Equals[T any](tb testing.TB, exp, act T, msgs ...string) {
	tb.Helper()
	if !reflect.DeepEqual(exp, act) {
		var msgString string
		length := len(msgs)
		if length == 1 {
			msgString = msgs[0]
		} else if length > 1 {
			args := make([]interface{}, length-1)
			for i, msg := range msgs[1:] {
				args[i] = msg
			}
			msgString = fmt.Sprintf(msgs[0], args...)
		}
		tb.Fatalf("\033[31m\n\n\t"+msgString+"\n\n\texp: %#v\n\n\tgot: %#v\033[39m\n\n", exp, act)
	}
}

// Random returns size bytes of pseudo-random data derived from the seed.
func Random(seed, count int) []byte {
	p := make([]byte, count)

	rnd := rand.New(rand.NewSource(int64(seed)))

	for i := 0; i < len(p); i += 8 {
		val := rnd.Int63()
		var data = []byte{
			byte((val >> 0) & 0xff),
			byte((val >> 8) & 0xff),
			byte((val >> 16) & 0xff),
			byte((val >> 24) & 0xff),
			byte((val >> 32) & 0xff),
			byte((val >> 40) & 0xff),
			byte((val >> 48) & 0xff),
			byte((val >> 56) & 0xff),
		}

		for j := range data {
			cur := i + j
			if cur >= len(p) {
				break
			}
			p[cur] = data[j]
		}
	}

	return p
}

// SetupTarTestFixture extracts the tarFile to outputDir.
func SetupTarTestFixture(t testing.TB, outputDir, tarFile string) {
	t.Helper()
	input, err := os.Open(tarFile)
	OK(t, err)
	defer func() {
		OK(t, input.Close())
	}()

	var rd io.Reader
	switch filepath.Ext(tarFile) {
	case ".gz":
		r, err := gzip.NewReader(input)
		OK(t, err)

		defer func() {
			OK(t, r.Close())
		}()
		rd = r
	case ".bzip2":
		rd = bzip2.NewReader(input)
	default:
		rd = input
	}

	extractTar(t, rd, outputDir)
}

func extractTar(t testing.TB, rd io.Reader, outputDir string) {
	t.Helper()
	outputDir = filepath.Clean(outputDir) + string(os.PathSeparator)

	tr := tar.NewReader(rd)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return
		}
		OK(t, err)

		if !validTarPath(outputDir, hdr.Name) {
			t.Fatalf("invalid path in tar archive: %q", hdr.Name)
		}

		target := filepath.Join(outputDir, hdr.Name)
		mode := os.FileMode(hdr.Mode)

		switch hdr.Typeflag {
		case tar.TypeDir:
			OK(t, os.MkdirAll(target, mode))
		case tar.TypeReg, 0: // 0 is legacy TypeRegA
			OK(t, os.MkdirAll(filepath.Dir(target), 0755))
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			OK(t, err)
			_, err = io.Copy(f, tr)
			OK(t, err)
			OK(t, f.Close())
		case tar.TypeSymlink:
			OK(t, os.MkdirAll(filepath.Dir(target), 0755))
			OK(t, os.Symlink(hdr.Linkname, target))
		case tar.TypeLink:
			linkTarget := filepath.Join(outputDir, hdr.Linkname)
			if !validTarPath(outputDir, hdr.Linkname) {
				t.Fatalf("invalid hard link target in tar archive: %q", hdr.Linkname)
			}
			OK(t, os.MkdirAll(filepath.Dir(target), 0755))
			OK(t, os.Link(linkTarget, target))
		default:
			t.Fatalf("unsupported tar entry type %c for %q", hdr.Typeflag, hdr.Name)
		}
	}
}

func validTarPath(outputDir, name string) bool {
	if filepath.IsAbs(name) {
		return false
	}
	target := filepath.Clean(filepath.Join(outputDir, name))
	return strings.HasPrefix(target+string(os.PathSeparator), outputDir) || target+string(os.PathSeparator) == outputDir
}

// CopyDir recursively copies the contents of src into dst. dst must exist.
func CopyDir(t testing.TB, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}

		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}

		if !isFile(info) {
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}

		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()

		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer func() { _ = out.Close() }()

		_, err = io.Copy(out, in)
		return err
	})
	OK(t, err)
}

// Env creates a test environment and extracts the repository fixture.
// Returned is the repo path and a cleanup function.
func Env(t testing.TB, repoFixture string) string {
	t.Helper()

	var tempdir string
	if TestCleanupTempDirs {
		tempdir = t.TempDir()
	} else {
		var err error
		tempdir, err = os.MkdirTemp(TestTempDir, "restic-test-env-")
		OK(t, err)
		t.Logf("leaving temporary directory %v used for test", tempdir)
	}

	fd, err := os.Open(repoFixture)
	if err != nil {
		t.Fatal(err)
	}
	OK(t, fd.Close())

	SetupTarTestFixture(t, tempdir, repoFixture)

	return filepath.Join(tempdir, "repo")
}

func isFile(fi os.FileInfo) bool {
	return fi.Mode()&(os.ModeType|os.ModeCharDevice) == 0
}

// resetReadOnly recursively resets the read-only flag recursively for dir.
// This is mainly used for tests on Windows, which is unable to delete a file
// set read-only.
func resetReadOnly(t testing.TB, dir string) {
	t.Helper()
	err := filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if fi == nil {
			return err
		}

		if fi.IsDir() {
			return os.Chmod(path, 0777)
		}

		if isFile(fi) {
			return os.Chmod(path, 0666)
		}

		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	OK(t, err)
}

// RemoveAll recursively resets the read-only flag of all files and dirs and
// afterwards uses os.RemoveAll() to remove the path.
func RemoveAll(t testing.TB, path string) {
	t.Helper()
	var err error
	err = os.RemoveAll(path)
	if err != nil {
		resetReadOnly(t, path)
		err = os.RemoveAll(path)
	}
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	OK(t, err)
}

// TempDir returns a temporary directory that is removed by t.Cleanup,
// except if TestCleanupTempDirs is set to false.
func TempDir(t testing.TB) string {
	t.Helper()
	tempdir, err := os.MkdirTemp(TestTempDir, "restic-test-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if !TestCleanupTempDirs {
			t.Logf("leaving temporary directory %v used for test", tempdir)
			return
		}

		RemoveAll(t, tempdir)
	})
	return tempdir
}

// Chdir changes the current directory to dest.
// The function back returns to the previous directory.
func Chdir(t testing.TB, dest string) (back func()) {
	t.Helper()

	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("chdir to %v", dest)
	err = os.Chdir(dest)
	if err != nil {
		t.Fatal(err)
	}

	return func() {
		t.Helper()
		t.Logf("chdir back to %v", prev)
		err = os.Chdir(prev)
		if err != nil {
			t.Fatal(err)
		}
	}
}

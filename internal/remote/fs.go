// Package remote implements the transport layer for vrs export/import:
// target parsing, SSH/SFTP connections, filesystem abstraction, and the
// sync engines (snapshot → target, target → working tree).
package remote

import (
	"os"
	"path"
	"time"
)

// Reserved names at export/import targets. They belong to vrs, never to
// user content.
const (
	ManifestName = ".vrs-manifest.json"
	TrashDirName = ".vrs-trash"
)

// FileInfo is the minimal file description the sync engine needs, uniform
// across local and SFTP filesystems.
type FileInfo struct {
	Name    string
	Size    int64
	Mode    uint32 // permission bits
	Mtime   int64  // unix nanos; 0 = unknown
	IsDir   bool
	Regular bool // plain file (not a symlink, socket, …)
}

// Fs abstracts the filesystem the sync engine talks to. Implementations:
// local directories (LocalFs) and SFTP connections (SftpFs).
type Fs interface {
	ReadDir(dir string) ([]FileInfo, error)
	Stat(p string) (FileInfo, error)
	ReadFile(p string) ([]byte, error)
	// WriteFile writes data to p atomically (temp + rename) and applies mode.
	WriteFile(p string, data []byte, mode uint32) error
	Chmod(p string, mode uint32) error
	SetMtime(p string, mtime int64) error
	MkdirAll(dir string) error
	Remove(p string) error
	Rename(oldPath, newPath string) error
	Close() error
}

// IsNotExist reports whether an Fs error means "does not exist", for both
// the local and the SFTP backend.
func IsNotExist(err error) bool {
	return os.IsNotExist(err) || isSftpNotExist(err)
}

// posixJoin joins target-rooted paths. Targets are POSIX (remote) and the
// supported local platforms use the same separator.
func posixJoin(parts ...string) string { return path.Join(parts...) }

// LocalFs is the local-filesystem implementation of Fs.
type LocalFs struct{}

// NewLocalFs returns a filesystem backed by the caller's machine.
func NewLocalFs() *LocalFs { return &LocalFs{} }

func localInfo(fi os.FileInfo) FileInfo {
	return FileInfo{
		Name:    fi.Name(),
		Size:    fi.Size(),
		Mode:    uint32(fi.Mode().Perm()),
		Mtime:   fi.ModTime().UnixNano(),
		IsDir:   fi.IsDir(),
		Regular: fi.Mode().IsRegular(),
	}
}

func (l *LocalFs) ReadDir(dir string) ([]FileInfo, error) {
	dirents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]FileInfo, 0, len(dirents))
	for _, d := range dirents {
		fi, err := d.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, localInfo(fi))
	}
	return out, nil
}

func (l *LocalFs) Stat(p string) (FileInfo, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return FileInfo{}, err
	}
	return localInfo(fi), nil
}

func (l *LocalFs) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }

func (l *LocalFs) WriteFile(p string, data []byte, mode uint32) error {
	dir := path.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".vrs-tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), os.FileMode(mode)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

func (l *LocalFs) Chmod(p string, mode uint32) error { return os.Chmod(p, os.FileMode(mode)) }

func (l *LocalFs) SetMtime(p string, mtime int64) error {
	t := time.Unix(0, mtime)
	return os.Chtimes(p, t, t)
}

func (l *LocalFs) MkdirAll(dir string) error { return os.MkdirAll(dir, 0o755) }
func (l *LocalFs) Remove(p string) error     { return os.Remove(p) }
func (l *LocalFs) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}
func (l *LocalFs) Close() error { return nil }

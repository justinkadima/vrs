package remote

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"time"

	"github.com/pkg/sftp"
)

func isSftpNotExist(err error) bool {
	var status *sftp.StatusError
	if errors.As(err, &status) {
		return status.Code == uint32(sftp.ErrSSHFxNoSuchFile)
	}
	return false
}

// SftpFs is the SFTP implementation of Fs.
type SftpFs struct {
	c    *sftp.Client
	conn io.Closer // underlying ssh connection, if vrs opened it
}

// NewSftpFs wraps an established SFTP client.
func NewSftpFs(c *sftp.Client) *SftpFs { return &SftpFs{c: c} }

// newSftpFsWithConn wraps a client plus the ssh connection it rides on, so
// Close tears down the transport too.
func newSftpFsWithConn(c *sftp.Client, conn io.Closer) *SftpFs {
	return &SftpFs{c: c, conn: conn}
}

func sftpInfo(fi os.FileInfo) FileInfo {
	return FileInfo{
		Name:    fi.Name(),
		Size:    fi.Size(),
		Mode:    uint32(fi.Mode().Perm()),
		Mtime:   fi.ModTime().UnixNano(),
		IsDir:   fi.IsDir(),
		Regular: fi.Mode().IsRegular(),
	}
}

func (s *SftpFs) ReadDir(dir string) ([]FileInfo, error) {
	fis, err := s.c.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]FileInfo, 0, len(fis))
	for _, fi := range fis {
		out = append(out, sftpInfo(fi))
	}
	return out, nil
}

func (s *SftpFs) Stat(p string) (FileInfo, error) {
	fi, err := s.c.Stat(p)
	if err != nil {
		return FileInfo{}, err
	}
	return sftpInfo(fi), nil
}

func (s *SftpFs) ReadFile(p string) ([]byte, error) {
	f, err := s.c.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func (s *SftpFs) WriteFile(p string, data []byte, mode uint32) error {
	dir := path.Dir(p)
	if err := s.c.MkdirAll(dir); err != nil {
		return err
	}
	tmp := path.Join(dir, fmt.Sprintf(".vrs-tmp-%d", time.Now().UnixNano()))
	f, err := s.c.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		s.c.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		s.c.Remove(tmp)
		return err
	}
	if err := s.c.Chmod(tmp, os.FileMode(mode)); err != nil {
		s.c.Remove(tmp)
		return err
	}
	// PosixRename overwrites; plain SFTP rename fails if the target exists.
	if err := s.c.PosixRename(tmp, p); err != nil {
		s.c.Remove(tmp)
		return err
	}
	return nil
}

func (s *SftpFs) Chmod(p string, mode uint32) error { return s.c.Chmod(p, os.FileMode(mode)) }

func (s *SftpFs) SetMtime(p string, mtime int64) error {
	t := time.Unix(0, mtime)
	return s.c.Chtimes(p, t, t)
}

func (s *SftpFs) MkdirAll(dir string) error { return s.c.MkdirAll(dir) }
func (s *SftpFs) Remove(p string) error     { return s.c.Remove(p) }
func (s *SftpFs) Rename(oldPath, newPath string) error {
	// SFTP rename fails if the target exists; the trash destination is
	// fresh per run, so plain rename is correct here.
	return s.c.Rename(oldPath, newPath)
}
func (s *SftpFs) Close() error {
	// Close the transport first: it unblocks the client's reader goroutine,
	// which a bare sftp-client close would otherwise wait on forever while
	// the server side sits idle.
	if s.conn != nil {
		_ = s.conn.Close()
	}
	return s.c.Close()
}

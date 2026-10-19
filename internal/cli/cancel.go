package cli

import (
	"context"
	"io"
	"io/fs"

	"github.com/connerohnesorge/gohard/internal/env"
)

// cancelFS is the file system a command sees: once the run's context is
// cancelled (SIGINT, SIGTERM) every call fails at once without touching
// the disk, so discovery and reads drain in microseconds and the run
// reports `interrupted` well within main's grace period instead of
// finishing its work first. The --output file and crash reports are
// written through the unwrapped FS.
type cancelFS struct {
	env.FS
	ctx context.Context
}

// err is what every call returns after cancellation.
func (c cancelFS) err() error {
	if c.ctx.Err() == nil {
		return nil
	}
	return &fs.PathError{Op: "gohard", Path: "",
		Err: context.Cause(c.ctx)}
}

func (c cancelFS) Getwd() (string, error) {
	if err := c.err(); err != nil {
		return "", err
	}
	return c.FS.Getwd()
}

func (c cancelFS) ReadFile(name string) ([]byte, error) {
	if err := c.err(); err != nil {
		return nil, err
	}
	return c.FS.ReadFile(name)
}

func (c cancelFS) WriteFile(name string, data []byte,
	perm fs.FileMode,
) error {
	if err := c.err(); err != nil {
		return err
	}
	return c.FS.WriteFile(name, data, perm)
}

func (c cancelFS) WriteTemp(dir, prefix string, data []byte) (string,
	error,
) {
	if err := c.err(); err != nil {
		return "", err
	}
	return c.FS.WriteTemp(dir, prefix, data)
}

func (c cancelFS) Rename(oldname, newname string) error {
	if err := c.err(); err != nil {
		return err
	}
	return c.FS.Rename(oldname, newname)
}

func (c cancelFS) Remove(name string) error {
	if err := c.err(); err != nil {
		return err
	}
	return c.FS.Remove(name)
}

func (c cancelFS) MkdirAll(name string, perm fs.FileMode) error {
	if err := c.err(); err != nil {
		return err
	}
	return c.FS.MkdirAll(name, perm)
}

func (c cancelFS) Stat(name string) (fs.FileInfo, error) {
	if err := c.err(); err != nil {
		return nil, err
	}
	return c.FS.Stat(name)
}

func (c cancelFS) Lstat(name string) (fs.FileInfo, error) {
	if err := c.err(); err != nil {
		return nil, err
	}
	return c.FS.Lstat(name)
}

func (c cancelFS) Readlink(name string) (string, error) {
	if err := c.err(); err != nil {
		return "", err
	}
	return c.FS.Readlink(name)
}

func (c cancelFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := c.err(); err != nil {
		return nil, err
	}
	return c.FS.ReadDir(name)
}

func (c cancelFS) Open(name string) (io.ReadCloser, error) {
	if err := c.err(); err != nil {
		return nil, err
	}
	return c.FS.Open(name)
}

func (c cancelFS) Create(name string) (io.WriteCloser, error) {
	if err := c.err(); err != nil {
		return nil, err
	}
	return c.FS.Create(name)
}

func (c cancelFS) Append(name string) (io.WriteCloser, error) {
	if err := c.err(); err != nil {
		return nil, err
	}
	return c.FS.Append(name)
}

var _ env.FS = cancelFS{}

package studip

import (
	"context"
	"time"

	"github.com/rclone/rclone/fs"
)

type Directory struct {
	fs      *Fs
	id      string
	name    string
	items   int64
	modTime time.Time
	remote  string
}

func (dir *Directory) Fs() fs.Info {
	return dir.fs
}

func (dir *Directory) ID() string {
	return dir.id
}

func (dir *Directory) Items() int64 {
	return dir.items
}

func (dir *Directory) String() string {
	return dir.name
}

func (dir *Directory) ModTime(context.Context) time.Time {
	return dir.modTime
}

func (dir *Directory) Remote() string {
	return dir.remote
}

func (dir *Directory) Size() int64 {
	return -1
}

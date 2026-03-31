package studip

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
)

type Object struct {
	fs             *Fs
	remote         string
	id             string
	size           int64
	isReadable     bool
	isEditable     bool
	isWritable     bool
	IsDownloadable bool
	contentType    string
	modTime        time.Time
}

func (o *Object) fieldsForLog() string {
	if o == nil {
		return "<nil>"
	}

	return fmt.Sprintf(
		"remote=%q id=%q size=%d isReadable=%t isEditable=%t isWritable=%t isDownloadable=%t contentType=%q modTime=%q",
		o.remote,
		o.id,
		o.size,
		o.isReadable,
		o.isEditable,
		o.isWritable,
		o.IsDownloadable,
		o.contentType,
		o.modTime.Format(time.RFC3339Nano),
	)
}

func (o *Object) Fs() fs.Info {
	return o.fs
}

func (o *Object) String() string {
	if o == nil {
		return "<nil>"
	}
	return o.remote
}

func (o *Object) Remote() string {
	return o.remote
}

func (o *Object) Hash(ctx context.Context, r hash.Type) (string, error) {
	return "", hash.ErrUnsupported
}

func (o *Object) Size() int64 {
	return o.size
}

// ModTime returns the modification time of the remote http file
func (o *Object) ModTime(ctx context.Context) time.Time {
	return o.modTime
}

func (o *Object) SetModTime(ctx context.Context, t time.Time) error {
	o.modTime = t

	return nil
}
func (o *Object) MimeType(ctx context.Context) string { return "" }

func (o *Object) Open(
	ctx context.Context,
	options ...fs.OpenOption,
) (io.ReadCloser, error) {
	if o == nil {
		return nil, fmt.Errorf("object is nil")
	}
	if o.fs == nil {
		return nil, fmt.Errorf("object fs is nil")
	}

	fs.Debugf(o.fs, "Object.Open: start fields={%s} options=%d", o.fieldsForLog(), len(options))
	if ctx.Err() != nil {
		fs.Debugf(o.fs, "Object.Open: context canceled remote=%q err=%v", o.remote, ctx.Err())
		return nil, ctx.Err()
	}

	if !o.isReadable && !o.IsDownloadable {
		fs.Debugf(o.fs, "Object.Open: permission denied fields={%s}", o.fieldsForLog())
		return nil, fs.ErrorPermissionDenied
	}

	rc, err := o.fs.studIPOpenFileContent(ctx, o.id, options...)
	if err != nil {
		fs.Debugf(o.fs, "Object.Open: failed fields={%s} err=%v", o.fieldsForLog(), err)
		return nil, err
	}
	fs.Debugf(o.fs, "Object.Open: success fields={%s}", o.fieldsForLog())
	return rc, nil
}

func (o *Object) Storable() bool {
	return true
}

func (o *Object) Update(
	ctx context.Context,
	in io.Reader,
	src fs.ObjectInfo,
	options ...fs.OpenOption,
) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	Assert(
		o != nil,
		fmt.Sprintf(
			"o must be not nil; o=%q",
			o,
		),
	)

	Assert(
		o.fs != nil,
		fmt.Sprintf(
			"o.fs must be not nil; o.fs=%q",
			o.fs,
		),
	)

	Assert(
		in != nil,
		fmt.Sprintf(
			"in must be not nil; in=%q",
			in,
		),
	)

	Assert(
		src != nil,
		fmt.Sprintf(
			"src must be not nil; src=%q",
			src,
		),
	)

	srcRemote := src.Remote()
	fs.Debugf(o.fs, "Object.Update: start srcRemote=%q fields={%s}", srcRemote, o.fieldsForLog())

	unlockCourses := lockMutationCourses(o.fs)
	defer unlockCourses()

	if err := o.fs.ensureCurrentFileTree(ctx); err != nil {
		return err
	}

	o.fs.beginMutation()
	defer o.fs.endMutation()

	if !o.isEditable || !o.isWritable {
		fs.Debugf(o.fs, "Object.Update: permission denied srcRemote=%q fields={%s}", srcRemote, o.fieldsForLog())
		return fs.ErrorPermissionDenied
	}

	if o.id == "" {
		fs.Debugf(o.fs, "Object.Update: missing file-ref id for srcRemote=%q", srcRemote)
		return fmt.Errorf("cannot update %q: object id is empty", srcRemote)
	}

	// This weird branch fixed a case where sometimes a file update get's truncated to it's size instead of updating the size
	sizeChanged := src.Size() >= 0 && o.size >= 0 && src.Size() != o.size
	var location string
	var err error
	if sizeChanged {
		fs.Debugf(
			o.fs,
			"Object.Update: size changed old=%d new=%d remote=%q; recreating file",
			o.size,
			src.Size(),
			o.remote,
		)
		location, err = o.recreateForSizeChangingUpdate(ctx, in, src)
	} else {
		location, err = o.fs.studIPUpdateFileContent(ctx, o.id, in, basePath(o.remote), src.Size())
	}
	if err != nil {
		fs.Debugf(o.fs, "Object.Update: upload phase failed srcRemote=%q fields={%s} err=%v", srcRemote, o.fieldsForLog(), err)
		return err
	}

	o.id, err = fileRefIDFromLocation(location)
	if err != nil {
		return err
	}

	o.size = src.Size()
	o.modTime = src.ModTime(ctx)
	o.contentType = fs.MimeType(ctx, src)

	o.fs.mu.Lock()
	if o.fs.ft.relativeRoot != nil {
		if node := o.fs.ft.relativeRoot.GetNodeAtPath(o.remote); node != nil && !node.IsDir {
			node.ID = o.id
			node.Size = o.size
			node.ChDate = o.modTime
			node.ContentType = o.contentType
		}
	}
	o.fs.bumpTreeGenerationAndMarkCurrent()
	o.fs.mu.Unlock()

	err = o.SetTermsOfUse(ctx, o.fs.opt.License)
	if err != nil {
		return err
	}

	fs.Debugf(o.fs, "Object.Update: success srcRemote=%q location=%q fields={%s}", srcRemote, location, o.fieldsForLog())
	return nil
}

func (o *Object) recreateForSizeChangingUpdate(
	ctx context.Context,
	in io.Reader,
	src fs.ObjectInfo,
) (string, error) {
	parentNode, err := o.fs.CreateParentDirectories(ctx, joinPath(cleanPath(o.fs.relativeRootPath), dirPath(o.remote)))
	if err != nil {
		return "", err
	}
	if parentNode == nil || !parentNode.IsDir || parentNode.ID == "" {
		return "", fmt.Errorf("failed to resolve parent directory for %q", o.remote)
	}

	if err = o.fs.studIPDeleteFile(ctx, o.id); err != nil {
		return "", err
	}

	location, err := o.fs.studIPCreateFileContent(
		ctx,
		parentNode.ID,
		in,
		basePath(o.remote),
		src.Size(),
	)
	if err != nil {
		return location, err
	}

	newID, err := fileRefIDFromLocation(location)
	if err != nil {
		return location, err
	}
	o.id = newID
	o.size = src.Size()
	o.modTime = src.ModTime(ctx)
	o.contentType = fs.MimeType(ctx, src)

	o.fs.mu.Lock()
	if o.fs.ft.relativeRoot != nil {
		if node := o.fs.ft.relativeRoot.GetNodeAtPath(o.remote); node != nil && !node.IsDir {
			node.ID = o.id
			node.Size = o.size
			node.ChDate = o.modTime
			node.ContentType = o.contentType
		}
	}
	o.fs.bumpTreeGenerationAndMarkCurrent()
	o.fs.mu.Unlock()

	err = o.SetTermsOfUse(ctx, o.fs.opt.License)
	if err != nil {
		return location, err
	}

	return location, nil
}

func (o *Object) SetTermsOfUse(
	ctx context.Context,
	licenseID string,
) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	Assert(
		o != nil,
		fmt.Sprintf(
			"o must be not nil; o=%q",
			o,
		),
	)

	Assert(
		o.fs != nil,
		fmt.Sprintf(
			"o.fs must be not nil; o.fs=%q",
			o.fs,
		),
	)

	Assert(
		o.id != "",
		fmt.Sprintf(
			"o.id must be not empty; o.id=%q",
			o.id,
		),
	)

	fs.Debugf(o.fs, "Object.SetTermsOfUse: start license=%q fields={%s}", licenseID, o.fieldsForLog())

	if licenseID == "" {
		return fmt.Errorf("licenseID is empty")
	}

	if !o.isEditable {
		fs.Debugf(o.fs, "Object.SetTermsOfUse: permission denied fields={%s}", o.fieldsForLog())
		return fs.ErrorPermissionDenied
	}

	err := o.fs.studIPSetTermsOfUse(ctx, o.id, licenseID)
	if err != nil {
		fs.Debugf(o.fs, "Object.SetTermsOfUse: failed license=%q fields={%s} err=%v", licenseID, o.fieldsForLog(), err)
		return err
	}

	fs.Debugf(o.fs, "Object.SetTermsOfUse: success license=%q fields={%s}", licenseID, o.fieldsForLog())

	return nil
}

func (o *Object) Remove(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	Assert(
		o != nil,
		fmt.Sprintf(
			"o must be not nil; o=%q",
			o,
		),
	)

	Assert(
		o.fs != nil,
		fmt.Sprintf(
			"o.fs must be not nil; o.fs=%q",
			o.fs,
		),
	)

	Assert(
		o.id != "",
		fmt.Sprintf(
			"o.id must be not empty; o.id=%q",
			o.id,
		),
	)

	fs.Debugf(o.fs, "Object.Remove: start fields={%s}", o.fieldsForLog())

	unlockCourses := lockMutationCourses(o.fs)
	defer unlockCourses()

	if err := o.fs.ensureCurrentFileTree(ctx); err != nil {
		return err
	}

	o.fs.beginMutation()
	defer o.fs.endMutation()

	if !o.isEditable && !o.isWritable {
		fs.Debugf(o.fs, "Object.Remove: permission denied fields={%s}", o.fieldsForLog())
		return fs.ErrorPermissionDenied
	}

	err := o.fs.studIPDeleteFile(ctx, o.id)
	if err != nil {
		fs.Debugf(o.fs, "Object.Remove: failed fields={%s} err=%v", o.fieldsForLog(), err)
		return err
	}

	o.fs.mu.Lock()
	defer o.fs.mu.Unlock()
	removed := false
	if o.fs.ft.root != nil {
		absoluteRemote := joinPath(cleanPath(o.fs.relativeRootPath), o.remote)
		node := o.fs.ft.root.GetNodeAtPath(absoluteRemote)
		if node == nil {
			node = o.fs.ft.root.GetNodeAtPath(absoluteRemote)
		}
		if node != nil && node.Parent != nil {
			index := slices.Index(node.Parent.Children, node)
			if index >= 0 {
				node.Parent.Children = slices.Delete(node.Parent.Children, index, index+1)
				removed = true
			}
		}
	}

	if !removed && o.fs.ft.relativeRoot != nil {
		parentDir := dirPath(o.remote)
		parent := o.fs.ft.relativeRoot.GetNodeAtPath(parentDir)
		if parent != nil {
			filename := basePath(o.remote)
			for i, child := range parent.Children {
				if child != nil && !child.IsDir && strings.EqualFold(child.Name, filename) {
					parent.Children = slices.Delete(parent.Children, i, i+1)
					removed = true
					break
				}
			}
		}
	}

	if removed {
		o.fs.bumpTreeGenerationAndMarkCurrent()
	} else {
		o.fs.fileTreeGenerationCounter().Add(1)
	}

	fs.Debugf(o.fs, "Object.Remove: success fields={%s}", o.fieldsForLog())

	return nil
}

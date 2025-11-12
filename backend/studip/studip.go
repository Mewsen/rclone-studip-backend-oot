package studip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	"github.com/rclone/rclone/fs/config/obscure"
	"github.com/rclone/rclone/fs/fshttp"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/lib/rest"
)

func init() {
	fs.Register(&fs.RegInfo{
		Name:        "Stud.IP",
		Description: "Stud.IP – read only",
		NewFs:       NewFs,
		Options: []fs.Option{{
			Name:     "base_url",
			Help:     "Base URL of Stud.IP installation",
			Default:  "https://elearning.uni-bremen.de/jsonapi.php/v1/",
			Required: true,
		}, {
			Name:     "username",
			Help:     "Stud.IP login name",
			Required: true,
		}, {
			Name:       "password",
			Help:       "Stud.IP password",
			IsPassword: true,
			Required:   true,
		}, {
			Name:     "course_id",
			Help:     "Course ID",
			Required: true,
		},
		},
	})
}

type Options struct {
	BaseURL  string `config:"base_url"`
	Username string `config:"username"`
	Password string `config:"password"`
	CourseID string `config:"course_id"`
}

type Fs struct {
	name   string
	opt    *Options
	client *rest.Client
	root   string

	rootNode *Node
}

type Object struct {
	fs          *Fs
	remote      string
	id          string
	size        int64
	isDir       bool
	contentType string
	modTime     time.Time
}

func NewFs(
	ctx context.Context,
	name,
	root string,
	m configmap.Mapper,
) (fs.Fs, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	fs.Debugf(name, "initializing studip backend for root %q", root)
	opt := new(Options)
	if err := configstruct.Set(m, opt); err != nil {
		return nil, err
	}
	if opt.CourseID == "" {
		return nil, errors.New("course_id is required")
	}

	base, err := url.Parse(opt.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}

	var httpClient *rest.Client
	{
		c := fshttp.NewClient(context.Background())
		httpClient = rest.NewClient(c)
	}

	httpClient.SetRoot(base.String())
	httpClient.SetHeader("Accept", "application/vnd.api+json")
	httpClient.SetUserPass(opt.Username, obscure.MustReveal(opt.Password))
	httpClient.SetErrorHandler(func(resp *http.Response) error {
		fmt.Println("Status: " + resp.Status)
		fmt.Println("URL: " + resp.Request.URL.String())
		return errors.New("")
	})

	f := &Fs{
		name:   name,
		opt:    opt,
		client: httpClient,
		root:   root,
	}

	if err := f.TestConnection(ctx); err != nil {
		return nil, err
	}

	rootID, err := f.RetrieveRootFolderID(ctx)
	if err != nil {
		return nil, err
	}

	rootNode, err := f.GetCourseFileTree(ctx, rootID)
	if err != nil {
		return nil, err
	}

	f.rootNode = rootNode

	if root != "" {
		pathSplit := splitPath(filepath.Dir(root))
		f.rootNode = GetNodeAtPath(f.rootNode, pathSplit)
	}

	return f, nil
}

type StudIPFolders struct {
	Meta struct {
		Page struct {
			Offset int `json:"offset"`
			Limit  int `json:"limit"`
			Total  int `json:"total"`
		} `json:"page"`
	} `json:"meta"`
	Data []StudIPFoldersData `json:"data"`
}

type StudIPFoldersData struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Attributes struct {
		FolderType         string    `json:"folder-type"`
		Name               string    `json:"name"`
		Description        string    `json:"description"`
		Mkdate             time.Time `json:"mkdate"`
		Chdate             time.Time `json:"chdate"`
		IsVisible          bool      `json:"is-visible"`
		IsReadable         bool      `json:"is-readable"`
		IsWritable         bool      `json:"is-writable"`
		IsEditable         bool      `json:"is-editable"`
		IsEmpty            bool      `json:"is-empty"`
		IsSubfolderAllowed bool      `json:"is-subfolder-allowed"`
	} `json:"attributes"`
}

type StudIPFiles struct {
	Meta struct {
		Page struct {
			Offset int `json:"offset"`
			Limit  int `json:"limit"`
			Total  int `json:"total"`
		} `json:"page"`
	} `json:"meta"`
	Links struct {
		First string `json:"first"`
		Last  string `json:"last"`
	} `json:"links"`
	Data []struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			Name           string    `json:"name"`
			Description    string    `json:"description"`
			Mkdate         time.Time `json:"mkdate"`
			Chdate         time.Time `json:"chdate"`
			Downloads      int       `json:"downloads"`
			Filesize       int64     `json:"filesize"`
			MimeType       string    `json:"mime-type"`
			IsReadable     bool      `json:"is-readable"`
			IsDownloadable bool      `json:"is-downloadable"`
			IsEditable     bool      `json:"is-editable"`
			IsWritable     bool      `json:"is-writable"`
		} `json:"attributes"`
	} `json:"data"`
}

type StudIPCourses struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			CourseNumber   string `json:"course-number"`
			Title          string `json:"title"`
			CourseType     int    `json:"course-type"`
			CourseTypeText string `json:"course-type-text"`
			Description    string `json:"description"`
			Dates          string `json:"dates"`
		} `json:"attributes"`
	} `json:"data"`
}

func (f *Fs) TestConnection(
	ctx context.Context,
) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	URL := fmt.Sprintf("courses/%s", f.opt.CourseID)

	responseJSON := new(StudIPCourses)
	res, err := f.client.Call(
		ctx,
		&rest.Opts{Method: "GET", Path: URL},
	)
	if err != nil {
		return err
	}

	defer res.Body.Close()
	decoder := json.NewDecoder(res.Body)
	err = decoder.Decode(responseJSON)
	if err != nil {
		return err
	}

	if responseJSON.Data.ID != f.opt.CourseID {
		return fmt.Errorf("received courseID doesn't match"+
			" configured courseID, received: %s, want: %s",
			responseJSON.Data.ID, f.opt.CourseID)
	}

	return nil
}

func (f *Fs) Name() string { return f.name }

func (f *Fs) Root() string             { return f.root }
func (f *Fs) String() string           { return f.opt.BaseURL }
func (f *Fs) Precision() time.Duration { return time.Second }

func (f *Fs) Hashes() hash.Set { return hash.Set(hash.None) }
func (f *Fs) Features() *fs.Features {
	return (&fs.Features{CanHaveEmptyDirectories: true}).
		Fill(context.Background(), f)
}

func (f *Fs) NewObject(ctx context.Context, remote string) (fs.Object, error) {
	return nil, fs.ErrorNotImplemented
}

// List the objects and directories in dir into entries.  The
// entries can be returned in any order but should be for a
// complete directory.
//
// dir should be "" to list the root, and should not have
// trailing slashes.
//
// This should return ErrDirNotFound if the directory isn't
// found.
func (f *Fs) List(
	ctx context.Context,
	dir string,
) (entries fs.DirEntries, err error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	pathSplit := strings.Split(dir, string(os.PathSeparator))

	node := GetNodeAtPath(f.rootNode, pathSplit)
	if !node.IsDir || node == nil {
		return nil, fs.ErrorDirNotFound
	}

	for _, entry := range node.Children {
		if entry.IsDir {
			directory := new(Directory)
			directory.fs = f
			directory.remote = filepath.Join(dir, entry.Name)
			directory.id = entry.Id
			directory.items = int64(len(entry.Children))
			directory.name = entry.Name
			directory.modTime = entry.ChDate

			entries = append(entries, directory)

		} else {
			object := new(Object)
			object.fs = f
			object.remote = filepath.Join(dir, entry.Name)
			object.id = entry.Id
			object.size = entry.Size
			object.contentType = entry.ContentType
			object.modTime = entry.ChDate
			object.isDir = entry.IsDir

			entries = append(entries, object)
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries.Less(i, j)
	})

	return entries, nil
}

type Node struct {
	Children    []*Node
	Name        string
	Id          string
	IsDir       bool
	ChDate      time.Time
	Size        int64
	ContentType string
}

func GetNodeAtPath(
	node *Node,
	pathSplit []string,
) *Node {
	if len(pathSplit) == 0 {
		return node
	}

	if pathSplit[0] == "." {
		return node
	}

	if pathSplit[0] == "" {
		return node
	}

	for _, children := range node.Children {
		if children.Name == pathSplit[0] {
			return GetNodeAtPath(children, pathSplit[1:])
		}
	}

	return nil
}

func (f *Fs) GetCourseFileTree(
	ctx context.Context,
	rootFolderID string,
) (*Node, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	rootNode := new(Node)
	rootNode.IsDir = true
	rootNode.Id = rootFolderID

	err := f.FillFolderNode(ctx, rootNode)
	if err != nil {
		return nil, err
	}

	return rootNode, nil
}

func (f *Fs) FillFolderNode(
	ctx context.Context,
	folderNode *Node,
) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if !folderNode.IsDir {
		return errors.New("node isn't a folder")
	}

	folders, err := f.RetrieveFoldersOfFolder(ctx, folderNode.Id)
	if err != nil {
		return err
	}

	folderNode.Children = slices.Grow(folderNode.Children, len(folders.Data))

	for _, folder := range folders.Data {
		childrenNode := new(Node)
		childrenNode.Id = folder.ID
		childrenNode.IsDir = true
		childrenNode.Name = folder.Attributes.Name
		childrenNode.ChDate = folder.Attributes.Chdate
		childrenNode.Size = -1

		folderNode.Children = append(folderNode.Children, childrenNode)
	}

	{
		errChan := make(chan error)
		length := len(folderNode.Children)
		{
			for _, childrenNode := range folderNode.Children {
				go func() {
					errChan <- f.FillFolderNode(ctx, childrenNode)
				}()
			}
		}

		for range length {
			err := <-errChan
			if err != nil {
				return err
			}
		}
	}

	files, err := f.RetrieveFilesOfFolder(ctx, folderNode.Id)
	if err != nil {
		return err
	}

	folderNode.Children = slices.Grow(folderNode.Children, len(files.Data))

	for _, file := range files.Data {
		if !file.Attributes.IsReadable || !file.Attributes.IsDownloadable {
			continue
		}

		childrenNode := new(Node)
		childrenNode.Id = file.ID
		childrenNode.IsDir = false
		childrenNode.Name = file.Attributes.Name
		childrenNode.ChDate = file.Attributes.Chdate
		childrenNode.Size = file.Attributes.Filesize
		childrenNode.ContentType = file.Attributes.MimeType

		folderNode.Children = append(folderNode.Children, childrenNode)
	}

	return nil
}

func (f *Fs) RetrieveFoldersOfFolder(
	ctx context.Context,
	folderID string,
) (*StudIPFolders, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	URL := fmt.Sprintf("folders/%s/folders", folderID)

	responseJSON := &StudIPFolders{}
	res, err := f.client.CallJSON(ctx,
		&rest.Opts{Method: "GET", Path: URL},
		nil,
		responseJSON,
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	return responseJSON, nil
}

func (f *Fs) RetrieveFilesOfFolder(
	ctx context.Context,
	folderID string,
) (*StudIPFiles, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	URL := fmt.Sprintf("folders/%s/file-refs", folderID)

	responseJSON := &StudIPFiles{}
	res, err := f.client.CallJSON(
		ctx,
		&rest.Opts{Method: "GET", Path: URL},
		nil,
		responseJSON,
	)
	if err != nil {
		return nil, err
	}

	defer res.Body.Close()

	return responseJSON, nil
}

func (f *Fs) RetrieveRootFolderID(
	ctx context.Context,
) (id string, err error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	URL := fmt.Sprintf("courses/%s/folders", f.opt.CourseID)

	responseJSON := &StudIPFolders{}
	res, err := f.client.CallJSON(ctx,
		&rest.Opts{Method: "GET", Path: URL}, nil, responseJSON)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	index := slices.IndexFunc(responseJSON.Data,
		func(e StudIPFoldersData) bool { return e.Attributes.FolderType == "RootFolder" },
	)

	if index == -1 {
		return "", errors.New("response doesn't contain a RootFolder")
	}

	return responseJSON.Data[index].ID, nil
}

func (f *Fs) Put(
	ctx context.Context,
	in io.Reader,
	src fs.ObjectInfo,
	options ...fs.OpenOption,
) (fs.Object, error) {
	return nil, fs.ErrorPermissionDenied
}
func (f *Fs) Mkdir(ctx context.Context, dir string) error { return fs.ErrorPermissionDenied }
func (f *Fs) Rmdir(ctx context.Context, dir string) error { return fs.ErrorPermissionDenied }
func (f *Fs) Purge(ctx context.Context, dir string) error {
	return fs.ErrorPermissionDenied
}
func (f *Fs) Copy(ctx context.Context, src fs.Object, remote string) (fs.Object, error) {
	return nil, fs.ErrorPermissionDenied
}
func (f *Fs) Move(ctx context.Context, src fs.Object, remote string) (fs.Object, error) {
	return nil, fs.ErrorPermissionDenied
}
func (f *Fs) DirMove(ctx context.Context, src fs.Fs, srcRemote, dstRemote string) error {
	return fs.ErrorPermissionDenied
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

func (o *Object) SetModTime(ctx context.Context, t time.Time) error { return fs.ErrorNotImplemented }
func (o *Object) MimeType(ctx context.Context) string               { return o.contentType }

func (o *Object) Open(
	ctx context.Context,
	options ...fs.OpenOption,
) (io.ReadCloser, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	URL := fmt.Sprintf("file-refs/%s/content", o.id)

	opts := rest.Opts{Method: "GET", Path: URL}
	var err error
	opts.Options = options
	res, err := o.fs.client.Call(ctx, &opts)
	if err != nil {
		return nil, err
	}
	if res.StatusCode/100 != 2 {
		defer res.Body.Close()
		return nil, fmt.Errorf("HTTP %s", res.Status)

	}
	return res.Body, nil
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
	return fs.ErrorNotImplemented
}

func (o *Object) Remove(ctx context.Context) error { return fs.ErrorNotImplemented }

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

// Check the interfaces are satisfied
var (
	_ fs.Info      = &Fs{}
	_ fs.Fs        = &Fs{}
	_ fs.Object    = &Object{}
	_ fs.MimeTyper = &Object{}
	_ fs.Directory = &Directory{}
)

func splitPath(p string) []string {
	p = path.Clean(p)
	if p == "/" {
		return []string{}
	}

	return strings.Split(p, "/")
}

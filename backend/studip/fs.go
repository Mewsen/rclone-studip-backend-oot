package studip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/lib/rest"

	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	"github.com/rclone/rclone/fs/config/obscure"
	"github.com/rclone/rclone/fs/fshttp"
)

type Fs struct {
	name   string
	opt    *Options
	client *rest.Client
	// This is the path that rclone uses as the root
	relativeRootPath      string
	ft                    FileTree
	treeGeneration        uint64
	treeRefreshGeneration uint64
	activeMutations       atomic.Int64
	// mu guards in-memory file tree reads and writes.
	mu sync.RWMutex
}

var fileTreeGenerations sync.Map
var fileTreeSnapshots sync.Map
var fileTreeMutationLocks sync.Map

type fileTreeSnapshot struct {
	generation uint64
	root       *Node
}

func NewFs(
	ctx context.Context,
	name,
	rootPath string,
	m configmap.Mapper,
) (fs.Fs, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	fs.Debugf(name, "initializing studip backend for root %q", rootPath)

	opt := new(Options)
	if err := configstruct.Set(m, opt); err != nil {
		fs.Debugf(name, "failed to parse backend config: %v", err)

		return nil, err
	}

	fs.Debugf(name, "loaded backend config for course_id=%q base_url=%q", opt.CourseID, opt.BaseURL)

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
		if resp == nil {
			return fmt.Errorf("http error: nil response")
		}

		var b strings.Builder
		b.WriteString("====== HTTP ERROR ======\n")

		req := resp.Request

		// ---- Request ----
		if req != nil {
			fmt.Fprintf(&b, "Request: %s %s\n", req.Method, req.URL.String())

			b.WriteString("Request Headers:\n")
			for k, v := range req.Header {
				fmt.Fprintf(&b, "  %s: %v\n", k, v)
			}

			if req.Body != nil {
				defer req.Body.Close()

				reqBody, err := io.ReadAll(req.Body)
				if err == nil {
					b.WriteString("Request Body:\n")
					b.Write(reqBody)
					b.WriteString("\n")

					// restore body
					req.Body = io.NopCloser(bytes.NewBuffer(reqBody))
				}
			}
		}

		// ---- Response ----
		fmt.Fprintf(&b, "Response Status: %s\n", resp.Status)

		b.WriteString("Response Headers:\n")
		for k, v := range resp.Header {
			fmt.Fprintf(&b, "  %s: %v\n", k, v)
		}

		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err == nil {
			b.WriteString("Response Body:\n")
			b.Write(respBody)
			b.WriteString("\n")

			// restore body
			resp.Body = io.NopCloser(bytes.NewBuffer(respBody))
		}

		b.WriteString("========================")

		return fmt.Errorf("%s", b.String())
	})
	fs.Debugf(name, "configured HTTP client root=%q username=%q", base.String(), opt.Username)

	f := &Fs{
		name:             name,
		opt:              opt,
		client:           httpClient,
		relativeRootPath: rootPath,
		ft:               FileTree{},
	}

	fs.Debugf(f, "testing Stud.IP connection")

	if err := f.TestConnection(ctx); err != nil {
		fs.Debugf(f, "connection test failed: %v", err)
		return nil, err
	}

	fs.Debugf(f, "connection test successful")

	fs.Debugf(f, "building course file tree")
	f.ft.root = f.clonedSnapshotForCurrentGeneration()
	if f.ft.root == nil {
		f.ft.root, err = f.GetCourseFileTree(ctx)
		if err != nil {
			return nil, err
		}
		f.storeFileTreeSnapshot(f.ft.root, f.fileTreeGenerationCounter().Load())
	}

	fs.Debugf(f, "course file tree initialized")

	if rootPath == "" {
		f.ft.relativeRoot = f.ft.root
		f.treeGeneration = f.fileTreeGenerationCounter().Load()
		f.treeRefreshGeneration = f.treeGeneration

		return f, nil
	}

	f.ft.relativeRoot = f.ft.root.GetNodeAtPath(rootPath)
	if f.ft.relativeRoot == nil {
		fs.Debugf(f, "relative root %q not found in file tree", rootPath)
	} else {
		fs.Debugf(f, "relative root resolved path=%q id=%q", f.relativeRootPath, f.ft.relativeRoot.ID)

		if !f.ft.relativeRoot.IsDir {
			f.ft.relativeRoot = f.ft.relativeRoot.Parent
			f.relativeRootPath = dirPath(f.relativeRootPath)
			f.treeGeneration = f.fileTreeGenerationCounter().Load()
			f.treeRefreshGeneration = f.treeGeneration

			return f, fs.ErrorIsFile
		}
	}

	f.treeGeneration = f.fileTreeGenerationCounter().Load()
	f.treeRefreshGeneration = f.treeGeneration
	return f, nil
}

func cloneNode(root *Node, parent *Node) *Node {
	if root == nil {
		return nil
	}

	cloned := &Node{
		Parent:             parent,
		Name:               root.Name,
		Path:               root.Path,
		ID:                 root.ID,
		IsReadable:         root.IsReadable,
		IsWritable:         root.IsWritable,
		IsDownloadable:     root.IsDownloadable,
		IsEditable:         root.IsEditable,
		IsSubfolderAllowed: root.IsSubfolderAllowed,
		IsDir:              root.IsDir,
		ChDate:             root.ChDate,
		Size:               root.Size,
		ContentType:        root.ContentType,
	}

	if len(root.Children) == 0 {
		return cloned
	}

	cloned.Children = make([]*Node, 0, len(root.Children))
	for _, child := range root.Children {
		if child == nil {
			cloned.Children = append(cloned.Children, nil)
			continue
		}
		cloned.Children = append(cloned.Children, cloneNode(child, cloned))
	}

	return cloned
}

func (f *Fs) fileTreeKey() string {
	return f.opt.BaseURL + "|" + f.opt.CourseID
}

func (f *Fs) fileTreeGenerationCounter() *atomic.Uint64 {
	key := f.fileTreeKey()
	counterAny, _ := fileTreeGenerations.LoadOrStore(key, &atomic.Uint64{})

	return counterAny.(*atomic.Uint64)
}

func (f *Fs) fileTreeMutationLock() *sync.Mutex {
	key := f.fileTreeKey()
	lockAny, _ := fileTreeMutationLocks.LoadOrStore(key, &sync.Mutex{})

	return lockAny.(*sync.Mutex)
}

func (f *Fs) markTreeCurrent(generation uint64) {
	f.treeGeneration = generation
	f.treeRefreshGeneration = generation
}

// Caller must hold f.mu.
func (f *Fs) bumpTreeGenerationAndMarkCurrent() uint64 {
	generation := f.fileTreeGenerationCounter().Add(1)
	f.markTreeCurrent(generation)
	f.storeCurrentFileTreeSnapshotLocked(generation)

	return generation
}

func (f *Fs) clonedSnapshotForCurrentGeneration() *Node {
	key := f.fileTreeKey()
	snapshotAny, ok := fileTreeSnapshots.Load(key)
	if !ok {
		return nil
	}

	snapshot := snapshotAny.(*fileTreeSnapshot)
	current := f.fileTreeGenerationCounter().Load()
	if snapshot.generation != current || snapshot.root == nil {
		return nil
	}

	return cloneNode(snapshot.root, nil)
}

func (f *Fs) storeFileTreeSnapshot(root *Node, generation uint64) {
	if root == nil {
		return
	}

	fileTreeSnapshots.Store(
		f.fileTreeKey(),
		&fileTreeSnapshot{
			generation: generation,
			root:       cloneNode(root, nil),
		},
	)
}

func (f *Fs) storeCurrentFileTreeSnapshotLocked(generation uint64) {
	if f.ft.root == nil {
		return
	}

	f.storeFileTreeSnapshot(f.ft.root, generation)
}

func (f *Fs) beginMutation() {
	f.activeMutations.Add(1)
}

func (f *Fs) endMutation() {
	f.activeMutations.Add(-1)
}

func (f *Fs) hasActiveMutations() bool {
	return f.activeMutations.Load() != 0
}

func (f *Fs) fileTreeNeedsRefresh() bool {
	if f.hasActiveMutations() {
		return false
	}

	current := f.fileTreeGenerationCounter().Load()
	f.mu.RLock()
	defer f.mu.RUnlock()

	return f.treeGeneration != current || f.treeRefreshGeneration != f.treeGeneration
}

func (f *Fs) refreshFileTree(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	root := f.clonedSnapshotForCurrentGeneration()
	if root == nil {
		var err error
		root, err = f.GetCourseFileTree(ctx)
		if err != nil {
			return err
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.ft.root = root
	generation := f.fileTreeGenerationCounter().Load()
	f.treeGeneration = generation
	f.treeRefreshGeneration = generation
	f.ft.relativeRoot = nil
	if f.relativeRootPath == "" {
		f.ft.relativeRoot = root
		f.storeCurrentFileTreeSnapshotLocked(generation)
		return nil
	}

	f.ft.relativeRoot = root.GetNodeAtPath(f.relativeRootPath)
	if f.ft.relativeRoot != nil && !f.ft.relativeRoot.IsDir {
		f.ft.relativeRoot = f.ft.relativeRoot.Parent
	}

	f.storeCurrentFileTreeSnapshotLocked(generation)

	return nil
}

func (f *Fs) ensureCurrentFileTree(ctx context.Context) error {
	if !f.fileTreeNeedsRefresh() {
		return nil
	}
	return f.refreshFileTree(ctx)
}

func (f *Fs) sameCourse(other *Fs) bool {
	return f != nil &&
		other != nil &&
		f.opt != nil &&
		other.opt != nil &&
		f.opt.BaseURL == other.opt.BaseURL &&
		f.opt.CourseID == other.opt.CourseID
}

func beginMutations(fss ...*Fs) func() {
	seen := make(map[*Fs]struct{}, len(fss))
	order := make([]*Fs, 0, len(fss))

	for _, fsys := range fss {
		if fsys == nil {
			continue
		}
		if _, ok := seen[fsys]; ok {
			continue
		}
		seen[fsys] = struct{}{}
		fsys.beginMutation()
		order = append(order, fsys)
	}

	return func() {
		for i := len(order) - 1; i >= 0; i-- {
			order[i].endMutation()
		}
	}
}

func lockMutationCourses(fss ...*Fs) func() {
	type courseLock struct {
		key string
		mu  *sync.Mutex
	}

	seen := make(map[string]struct{}, len(fss))
	locks := make([]courseLock, 0, len(fss))

	for _, fsys := range fss {
		if fsys == nil || fsys.opt == nil {
			continue
		}
		key := fsys.fileTreeKey()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		locks = append(locks, courseLock{
			key: key,
			mu:  fsys.fileTreeMutationLock(),
		})
	}

	slices.SortFunc(locks, func(a, b courseLock) int {
		return strings.Compare(a.key, b.key)
	})

	for _, lock := range locks {
		lock.mu.Lock()
	}

	return func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].mu.Unlock()
		}
	}
}

func updateSubtreePaths(node *Node) {
	if node == nil {
		return
	}

	if node.Parent != nil {
		node.Path = joinPath(node.Parent.Path, node.Name)
	}

	for _, child := range node.Children {
		if child == nil {
			continue
		}
		updateSubtreePaths(child)
	}
}


func (f *Fs) GetCourseFileTree(
	ctx context.Context,
) (*Node, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	rootFolder, err := f.RetrieveRootFolder(ctx)
	if err != nil {
		return nil, err
	}

	rootNode := new(Node)
	rootNode.Name = "root"
	rootNode.Path = ""
	rootNode.ID = rootFolder.ID
	rootNode.IsReadable = rootFolder.Attributes.IsReadable
	rootNode.IsWritable = rootFolder.Attributes.IsWritable
	rootNode.IsEditable = rootFolder.Attributes.IsEditable
	rootNode.IsSubfolderAllowed = rootFolder.Attributes.IsSubfolderAllowed
	rootNode.IsDir = true
	rootNode.ChDate = rootFolder.Attributes.Chdate

	err = f.FillFolderNode(ctx, rootNode, rootNode.Path)
	if err != nil {
		return nil, err
	}

	return rootNode, nil
}

func (f *Fs) FillFolderNode(
	ctx context.Context,
	folderNode *Node,
	path string,
) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	Assert(
		folderNode != nil,
		fmt.Sprintf(
			"folderNode must be not nil; folderNode=%q",
			folderNode,
		),
	)

	if !folderNode.IsDir {
		return fs.ErrorIsFile
	}

	if !folderNode.IsReadable {
		return fs.ErrorPermissionDenied
	}

	folders, err := f.studIPGetFoldersOfFolder(ctx, folderNode.ID)
	if err != nil {
		return err
	}

	folderNode.Children = slices.Grow(folderNode.Children, len(folders.Data))

	for _, folder := range folders.Data {
		childrenNode := new(Node)
		childrenNode.IsWritable = folder.Attributes.IsWritable
		childrenNode.IsReadable = folder.Attributes.IsReadable
		childrenNode.IsEditable = folder.Attributes.IsEditable
		childrenNode.IsSubfolderAllowed = folder.Attributes.IsSubfolderAllowed
		childrenNode.Parent = folderNode
		childrenNode.ID = folder.ID
		childrenNode.IsDir = true
		childrenNode.Name = f.opt.Enc.ToStandardName(folder.Attributes.Name)
		childrenNode.ChDate = folder.Attributes.Chdate
		childrenNode.Path = joinPath(path, childrenNode.Name)
		childrenNode.Size = -1

		folderNode.Children = append(folderNode.Children, childrenNode)
	}

	{
		errChan := make(chan error)
		length := 0
		{
			for _, childrenNode := range folderNode.Children {
				if childrenNode.IsReadable {
					length++
					go func() {
						errChan <- f.FillFolderNode(ctx, childrenNode, joinPath(path, childrenNode.Name))
					}()
				}
			}
		}

		for range length {
			err := <-errChan
			if err != nil {
				return err
			}
		}
	}

	files, err := f.RetrieveFilesOfFolder(ctx, folderNode.ID)
	if err != nil {
		return err
	}

	folderNode.Children = slices.Grow(folderNode.Children, len(files.Data))

	for _, file := range files.Data {
		childrenNode := new(Node)
		childrenNode.IsDownloadable = file.Attributes.IsDownloadable
		childrenNode.IsWritable = file.Attributes.IsWritable
		childrenNode.IsReadable = file.Attributes.IsReadable
		childrenNode.IsEditable = file.Attributes.IsEditable
		childrenNode.ID = file.ID
		childrenNode.IsDir = false
		childrenNode.Parent = folderNode
		childrenNode.Name = f.opt.Enc.ToStandardName(file.Attributes.Name)
		childrenNode.ChDate = file.Attributes.Chdate
		childrenNode.Size = file.Attributes.Filesize
		childrenNode.Path = joinPath(path, childrenNode.Name)
		childrenNode.ContentType = file.Attributes.MimeType

		folderNode.Children = append(folderNode.Children, childrenNode)
	}

	return nil
}

func (f *Fs) RetrieveFilesOfFolder(
	ctx context.Context,
	folderID string,
) (*StudIPFiles, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	return f.studIPGetFilesOfFolder(ctx, folderID)
}

func (f *Fs) RetrieveRootFolder(
	ctx context.Context,
) (folder StudIPFoldersData, err error) {
	if ctx.Err() != nil {
		return folder, ctx.Err()
	}

	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	responseJSON, err := f.studIPGetCourseFolders(ctx)
	if err != nil {
		return folder, err
	}

	index := slices.IndexFunc(responseJSON.Data,
		func(e StudIPFoldersData) bool { return e.Attributes.FolderType == "RootFolder" },
	)

	if index == -1 {
		return folder, errors.New("response doesn't contain a RootFolder")
	}

	folder = responseJSON.Data[index]

	return folder, nil
}

func (f *Fs) Put(
	ctx context.Context,
	in io.Reader,
	src fs.ObjectInfo,
	options ...fs.OpenOption,
) (fs.Object, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	Assert(
		src != nil,
		fmt.Sprintf(
			"src must be not nil; src=%q",
			src,
		),
	)

	Assert(
		in != nil,
		fmt.Sprintf(
			"in must be not nil; in=%q",
			in,
		),
	)

	remotePath := src.Remote()
	if remotePath == "" {
		return nil, fmt.Errorf("invalid remote path %q", remotePath)
	}

	unlockCourses := lockMutationCourses(f)
	defer unlockCourses()

	if err := f.ensureCurrentFileTree(ctx); err != nil {
		return nil, err
	}

	f.beginMutation()
	defer f.endMutation()

	existingAny, err := f.NewObject(ctx, remotePath)
	if err == nil {
		existing, ok := existingAny.(*Object)
		if !ok {
			return nil, fmt.Errorf("unexpected object type %T for remote %q", existingAny, remotePath)
		}
		if existing.id == "" {
			return nil, fmt.Errorf("existing object has empty id for remote %q", remotePath)
		}
		if !existing.isEditable || !existing.isWritable {
			return nil, fs.ErrorPermissionDenied
		}

		location, err := f.studIPUpdateFileContent(
			ctx,
			existing.id,
			in,
			basePath(remotePath),
			src.Size(),
		)
		if err != nil {
			return nil, err
		}

		existing.id, err = fileRefIDFromLocation(location)
		if err != nil {
			return nil, err
		}

		existing.size = src.Size()
		existing.modTime = src.ModTime(ctx)
		existing.contentType = fs.MimeType(ctx, src)

		f.mu.Lock()
		if f.ft.relativeRoot != nil {
			if node := f.ft.relativeRoot.GetNodeAtPath(remotePath); node != nil && !node.IsDir {
				node.ID = existing.id
				node.Size = existing.size
				node.ChDate = existing.modTime
				node.ContentType = existing.contentType
			}
		}
		f.bumpTreeGenerationAndMarkCurrent()
		f.mu.Unlock()

		err = existing.SetTermsOfUse(ctx, f.opt.License)
		if err != nil {
			return nil, err
		}

		fs.Debugf(
			f,
			"Put: updated existing object remote=%q id=%q location=%q",
			remotePath,
			existing.id,
			location,
		)
		return existing, nil
	}
	if !errors.Is(err, fs.ErrorObjectNotFound) {
		return nil, err
	}

	object := &Object{
		fs:             f,
		remote:         remotePath,
		size:           src.Size(),
		isReadable:     true,
		isEditable:     true,
		isWritable:     true,
		IsDownloadable: true,
		modTime:        src.ModTime(ctx),
		contentType:    fs.MimeType(ctx, src),
	}

	parentDir := dirPath(remotePath)
	cleanRoot := cleanPath(f.relativeRootPath)

	directoryNode, err := f.CreateParentDirectories(ctx, joinPath(cleanRoot, parentDir))
	if err != nil {
		return nil, err
	}
	if directoryNode == nil {
		return nil, fmt.Errorf("failed to resolve parent directory for %q", remotePath)
	}
	if !directoryNode.IsDir {
		return nil, fmt.Errorf("resolved parent node is not a directory: %q", directoryNode.Path)
	}
	if directoryNode.ID == "" {
		return nil, fmt.Errorf("resolved parent directory has empty id for %q", remotePath)
	}

	location, err := f.studIPCreateFileContent(
		ctx,
		directoryNode.ID,
		in,
		basePath(remotePath),
		src.Size(),
	)
	if err != nil {
		return nil, err
	}

	object.id, err = fileRefIDFromLocation(location)
	if err != nil {
		return nil, err
	}

	err = object.SetTermsOfUse(ctx, f.opt.License)
	if err != nil {
		return nil, err
	}

	filename := basePath(remotePath)
	f.mu.Lock()
	defer f.mu.Unlock()
	updatedNode := false
	for _, child := range directoryNode.Children {
		if child == nil || child.IsDir || !strings.EqualFold(child.Name, filename) {
			continue
		}

		child.ID = object.id
		child.IsReadable = object.isReadable
		child.IsWritable = object.isWritable
		child.IsEditable = object.isEditable
		child.IsDownloadable = object.IsDownloadable
		child.IsDir = false
		child.ChDate = object.modTime
		child.Size = object.size
		child.ContentType = object.contentType
		updatedNode = true
		break
	}

	if !updatedNode {
		directoryNode.Children = append(directoryNode.Children, &Node{
			Parent:         directoryNode,
			Name:           filename,
			Path:           joinPath(directoryNode.Path, filename),
			ID:             object.id,
			IsReadable:     object.isReadable,
			IsWritable:     object.isWritable,
			IsDownloadable: object.IsDownloadable,
			IsEditable:     object.isEditable,
			IsDir:          false,
			ChDate:         object.modTime,
			Size:           object.size,
			ContentType:    object.contentType,
		})
	}
	f.bumpTreeGenerationAndMarkCurrent()

	return object, nil
}

func (f *Fs) Mkdir(ctx context.Context, dir string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	unlockCourses := lockMutationCourses(f)
	defer unlockCourses()

	if err := f.ensureCurrentFileTree(ctx); err != nil {
		return err
	}

	f.beginMutation()
	defer f.endMutation()
	var parentNode *Node
	var dirname string
	var err error

	// creating relativeRoot
	if dir == "" {
		f.mu.RLock()
		relativeRootReady := f.ft.relativeRoot != nil
		f.mu.RUnlock()
		if !relativeRootReady {
			fs.Debugf(f, "Mkdir: rootNode nil, creating parent chain for %q", dirPath(f.relativeRootPath))
			parentNode, err = f.CreateParentDirectories(ctx, dirPath(cleanPath(f.relativeRootPath)))
			if err != nil {
				return err
			}
		} else {
			return nil
		}
		dirname = basePath(f.relativeRootPath)
	} else {
		// creating dir inside relativeRoot
		dirname = basePath(dir)
		f.mu.RLock()
		if f.ft.relativeRoot != nil {
			parentNode = f.ft.relativeRoot.GetNodeAtPath(dirPath(dir))
		}
		f.mu.RUnlock()
		if parentNode == nil {
			fs.Debugf(f, "Mkdir: parent missing for %q, creating chain", dir)
			parentNode, err = f.CreateParentDirectories(ctx, joinPath(cleanPath(f.relativeRootPath), dirPath(dir)))
			if err != nil {
				return err
			}
		}
	}

	if dirname == "" {
		return fmt.Errorf("invalid directory name %q", dirname)
	}

	if parentNode == nil {
		return fs.ErrorDirNotFound
	}

	if !parentNode.IsDir {
		return fmt.Errorf("parent node is not a directory: %q", parentNode.Path)
	}

	if parentNode.ID == "" {
		return fmt.Errorf("parent node has empty id: %q", parentNode.Path)
	}

	if !parentNode.IsSubfolderAllowed {
		return fs.ErrorPermissionDenied
	}

	fs.Debugf(
		f,
		"Mkdir: resolved parent path=%q id=%q for dirname=%q",
		parentNode.Path,
		parentNode.ID,
		dirname,
	)

	f.mu.RLock()
	if f.findDirectoryNodeByName(parentNode, dirname) != nil {
		f.mu.RUnlock()
		return nil
	}
	f.mu.RUnlock()

	fs.Debugf(f, "Mkdir: creating directory %q under parent id=%q", dirname, parentNode.ID)
	apiDirname := f.opt.Enc.FromStandardName(dirname)
	if err := f.studIPMkDir(ctx, parentNode.ID, apiDirname); err != nil {
		return err
	}

	createdDirectory, err := f.findDirectoryByName(ctx, parentNode.ID, dirname)
	if err != nil {
		return err
	}

	fs.Debugf(f, "Mkdir: created directory %q with id=%q", dirname, createdDirectory.ID)

	createdDirectoryNode := &Node{
		Parent:             parentNode,
		Name:               dirname,
		Path:               joinPath(parentNode.Path, dirname),
		ID:                 createdDirectory.ID,
		IsReadable:         createdDirectory.Attributes.IsReadable,
		IsWritable:         createdDirectory.Attributes.IsWritable,
		IsEditable:         createdDirectory.Attributes.IsEditable,
		IsSubfolderAllowed: createdDirectory.Attributes.IsSubfolderAllowed,
		IsDir:              true,
		ChDate:             createdDirectory.Attributes.Chdate,
		Size:               -1,
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.findDirectoryNodeByName(parentNode, dirname) != nil {
		return nil
	}

	parentNode.Children = append(parentNode.Children, createdDirectoryNode)

	f.updateRelativeRootFromTree()
	f.bumpTreeGenerationAndMarkCurrent()

	return nil
}

func (f *Fs) findDirectoryNodeByName(parentNode *Node, name string) *Node {
	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	Assert(
		parentNode != nil,
		fmt.Sprintf(
			"parentNode must be not nil; parentNode=%q",
			parentNode,
		),
	)

	Assert(
		name != "",
		fmt.Sprintf(
			"name must be not empty; name=%q",
			name,
		),
	)

	for _, child := range parentNode.Children {
		if child != nil && child.IsDir && strings.EqualFold(child.Name, name) {
			return child
		}
	}

	return nil
}

func (f *Fs) findDirectoryByName(
	ctx context.Context,
	parentFolderID string,
	name string,
) (StudIPFoldersData, error) {
	if ctx.Err() != nil {
		return StudIPFoldersData{}, ctx.Err()
	}

	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	Assert(
		parentFolderID != "",
		fmt.Sprintf(
			"parentFolderID must be not empty; parentFolderID=%q",
			parentFolderID,
		),
	)

	Assert(
		name != "",
		fmt.Sprintf(
			"name must be not empty; name=%q",
			name,
		),
	)

	folders, err := f.studIPGetFoldersOfFolder(ctx, parentFolderID)
	if err != nil {
		return StudIPFoldersData{}, err
	}

	for _, folder := range folders.Data {
		if strings.EqualFold(f.opt.Enc.ToStandardName(folder.Attributes.Name), name) {
			return folder, nil
		}
	}

	return StudIPFoldersData{}, fs.ErrorDirNotFound
}

// CreateParentDirectories creates missing directory segments for a path relative
// to the course root, regardless of the current relative root state.
func (f *Fs) CreateParentDirectories(
	ctx context.Context,
	targetPath string,
) (*Node, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	f.mu.Lock()
	defer f.mu.Unlock()

	targetPath = cleanPath(targetPath)
	fs.Debugf(f, "CreateParentDirectories: normalized targetPath=%q", targetPath)

	targetNode := f.ft.root.GetNodeAtPath(targetPath)
	if targetNode != nil {
		if !targetNode.IsDir {
			return nil, fmt.Errorf("target path is not a directory: %q", targetPath)
		}
		if targetNode.ID == "" {
			return nil, fmt.Errorf("target directory has empty id: %q", targetPath)
		}
		fs.Debugf(
			f,
			"CreateParentDirectories: target already exists path=%q id=%q",
			targetNode.Path,
			targetNode.ID,
		)
		f.updateRelativeRootFromTree()
		return targetNode, nil
	}

	stack := NewStack[string]()
	currentPath := targetPath
	createdAny := false

	for {
		candidate := f.ft.root.GetNodeAtPath(currentPath)
		if candidate != nil {
			if !candidate.IsDir {
				return nil, fmt.Errorf("existing path segment is not a directory: %q", currentPath)
			}
			if candidate.ID == "" {
				return nil, fmt.Errorf("existing path segment has empty id: %q", currentPath)
			}
			if !candidate.IsWritable {
				return nil, fs.ErrorPermissionDenied
			}
			targetNode = candidate
			break
		}

		if currentPath == "" {
			return nil, fs.ErrorDirNotFound
		}

		stack.Push(basePath(currentPath))
		currentPath = dirPath(currentPath)
	}
	fs.Debugf(f, "CreateParentDirectories: creating %d missing segments", stack.Len())

	for stack.Len() > 0 {
		dirname, ok := stack.Pop()
		Assert(ok, "stack.Pop() must return a value")
		if dirname == "" {
			return nil, fmt.Errorf("invalid directory segment %q", dirname)
		}
		if targetNode == nil || !targetNode.IsDir || targetNode.ID == "" {
			return nil, fmt.Errorf("invalid parent node while creating %q", dirname)
		}
		fs.Debugf(
			f,
			"CreateParentDirectories: creating segment=%q under parent path=%q id=%q",
			dirname,
			targetNode.Path,
			targetNode.ID,
		)

		apiDirname := f.opt.Enc.FromStandardName(dirname)
		if err := f.studIPMkDir(ctx, targetNode.ID, apiDirname); err != nil {
			return nil, err
		}

		createdDirectory, err := f.findDirectoryByName(ctx, targetNode.ID, dirname)
		if err != nil {
			return nil, err
		}
		if createdDirectory.ID == "" {
			return nil, fmt.Errorf(
				"created directory %q but failed to resolve id",
				dirname,
			)
		}
		fs.Debugf(f, "CreateParentDirectories: created segment=%q id=%q", dirname, createdDirectory.ID)

		createdNode := &Node{
			Parent:             targetNode,
			Name:               dirname,
			Path:               joinPath(targetNode.Path, dirname),
			ID:                 createdDirectory.ID,
			IsReadable:         createdDirectory.Attributes.IsReadable,
			IsWritable:         createdDirectory.Attributes.IsWritable,
			IsEditable:         createdDirectory.Attributes.IsEditable,
			IsSubfolderAllowed: createdDirectory.Attributes.IsSubfolderAllowed,
			IsDir:              true,
			ChDate:             createdDirectory.Attributes.Chdate,
			Size:               -1,
		}

		targetNode.Children = append(targetNode.Children, createdNode)
		targetNode = createdNode
		createdAny = true
	}
	fs.Debugf(
		f,
		"CreateParentDirectories: done path=%q id=%q",
		targetNode.Path,
		targetNode.ID,
	)
	f.updateRelativeRootFromTree()
	if createdAny {
		f.bumpTreeGenerationAndMarkCurrent()
	}

	return targetNode, nil
}

// updateRelativeRootFromTree resolves f.ft.relativeRoot after directories were created.
// This is needed when the backend starts with a non-existent root path and that path is
// created lazily during Put/Mkdir operations.
func (f *Fs) updateRelativeRootFromTree() {
	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	Assert(
		f.ft.root != nil,
		"f.ft.root must be not nil",
	)

	// f.ft.relativeRoot is set nothing todo here
	if f.ft.relativeRoot != nil {
		return
	}

	if f.relativeRootPath == "" {
		f.ft.relativeRoot = f.ft.root
		return
	}

	rootNode := f.ft.root.GetNodeAtPath(f.relativeRootPath)
	if rootNode == nil || !rootNode.IsDir {
		return
	}

	f.ft.relativeRoot = rootNode
	fs.Debugf(f, "resolved relative root path=%q id=%q", rootNode.Path, rootNode.ID)
}

func (f *Fs) Rmdir(ctx context.Context, dir string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	unlockCourses := lockMutationCourses(f)
	defer unlockCourses()

	if f.fileTreeNeedsRefresh() {
		if err := f.refreshFileTree(ctx); err != nil {
			fs.Debugf(f, "Rmdir: refresh before lookup failed dir=%q err=%v", dir, err)
		}
	}

	f.beginMutation()
	defer f.endMutation()

	lookupNode := func() (*Node, error) {
		f.mu.RLock()
		defer f.mu.RUnlock()

		if f.ft.relativeRoot == nil {
			return nil, fs.ErrorDirNotFound
		}

		node := f.ft.relativeRoot.GetNodeAtPath(dir)
		if node == nil {
			return nil, fs.ErrorDirNotFound
		}

		if !node.IsEditable {
			return nil, fs.ErrorPermissionDenied
		}

		// if Directory is root
		if node.Parent == nil && node.Name == f.ft.root.Name && node.Path == f.ft.root.Path {
			return nil, fs.ErrorCantPurge
		}

		return node, nil
	}

	node, err := lookupNode()
	if err != nil {
		return err
	}
	if len(node.Children) > 0 {
		if refreshErr := f.refreshFileTree(ctx); refreshErr != nil {
			fs.Debugf(f, "Rmdir: refresh before empty check failed dir=%q err=%v", dir, refreshErr)
		} else {
			node, err = lookupNode()
			if err != nil {
				return err
			}
		}

		if len(node.Children) > 0 {
			return fs.ErrorDirectoryNotEmpty
		}
	}

	err = f.studIPDeleteFolder(ctx, node.ID)
	if err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	// if the deleted node was the relativeRootPath we have to nil it
	if f.ft.relativeRoot != nil && f.ft.relativeRoot.ID == node.ID {
		f.ft.relativeRoot = nil
	}

	if node.Parent != nil {
		index := slices.Index(node.Parent.Children, node)
		if index >= 0 {
			node.Parent.Children = slices.Delete(node.Parent.Children, index, index+1)
		}
	}
	f.bumpTreeGenerationAndMarkCurrent()

	return nil
}

func (f *Fs) TestConnection(
	ctx context.Context,
) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	responseJSON, err := f.studIPGetCourse(ctx)
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

func (f *Fs) Root() string             { return f.relativeRootPath }
func (f *Fs) String() string           { return f.opt.BaseURL }
func (f *Fs) Precision() time.Duration { return fs.ModTimeNotSupported }

func (f *Fs) Hashes() hash.Set { return hash.Set(hash.None) }
func (f *Fs) Features() *fs.Features {
	return (&fs.Features{
		CanHaveEmptyDirectories: true,
		CaseInsensitive:         true,
		//ReadMimeType:            true,
		// TODO: Implement these
		Copy:    nil,
		Move:    nil,
		DirMove: nil,
		// implement this
		Purge: nil,
	}).
		Fill(context.Background(), f)
}

func (f *Fs) NewObject(ctx context.Context, remote string) (fs.Object, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	fs.Debugf(f, "NewObject: start remote=%q", remote)
	remote = cleanPath(remote)
	fs.Debugf(f, "NewObject: normalized remote=%q", remote)
	if remote == "" {
		fs.Debugf(f, "NewObject: empty normalized path, returning not found")
		return nil, fs.ErrorObjectNotFound
	}

	if f.fileTreeNeedsRefresh() {
		if err := f.refreshFileTree(ctx); err != nil {
			fs.Debugf(f, "NewObject: refresh before lookup failed remote=%q err=%v", remote, err)
		}
	}

	var (
		object              *Object
		relativeRootMissing bool
		nodeMissing         bool
		nodeIsDir           bool
		nodeIDMissing       bool
	)

	f.mu.RLock()
	if f.ft.relativeRoot == nil {
		relativeRootMissing = true
	} else {
		node := f.ft.relativeRoot.GetNodeAtPath(remote)
		switch {
		case node == nil:
			nodeMissing = true
		case node.IsDir:
			nodeIsDir = true
		case node.ID == "":
			nodeIDMissing = true
		default:
			object = &Object{
				fs:             f,
				remote:         remote,
				id:             node.ID,
				size:           node.Size,
				isReadable:     node.IsReadable,
				isEditable:     node.IsEditable,
				isWritable:     node.IsWritable,
				IsDownloadable: node.IsDownloadable,
				contentType:    node.ContentType,
				modTime:        node.ChDate,
			}
		}
	}
	f.mu.RUnlock()

	if object == nil {
		if relativeRootMissing {
			fs.Debugf(f, "NewObject: relative root is not available for %q", remote)
		} else if nodeMissing {
			fs.Debugf(f, "NewObject: node not found for %q", remote)
		} else if nodeIsDir {
			fs.Debugf(f, "NewObject: path %q is a directory", remote)
		} else if nodeIDMissing {
			fs.Debugf(f, "NewObject: node for %q has empty id", remote)
		}
		return nil, fs.ErrorObjectNotFound
	}

	fs.Debugf(
		f,
		"NewObject: resolved remote=%q id=%q size=%d contentType=%q",
		remote,
		object.id,
		object.size,
		object.contentType,
	)

	return object, nil
}

func (f *Fs) List(
	ctx context.Context,
	dir string,
) (entries fs.DirEntries, err error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	fs.Debugf(f, "List: start dir=%q rootPath=%q", dir, f.relativeRootPath)

	if f.fileTreeNeedsRefresh() {
		if err := f.refreshFileTree(ctx); err != nil {
			fs.Debugf(f, "List: refresh before lookup failed dir=%q err=%v", dir, err)
		}
	}

	fixCase := fs.GetConfig(ctx).FixCase
	listEntries := func() (fs.DirEntries, error) {
		f.mu.RLock()
		defer f.mu.RUnlock()
		if fixCase {
			entries, err := f.ft.ListEntries(f, dir)
			if err == nil || !errors.Is(err, fs.ErrorDirNotFound) {
				return entries, err
			}
		}
		return f.ft.ListEntries(f, dir)
	}

	entries, err = listEntries()
	if err != nil {
		fs.Debugf(f, "List: failed dir=%q err=%v", dir, err)
		return nil, err
	}

	fs.Debugf(f, "List: done dir=%q entries=%d", dir, len(entries))
	return entries, nil
}

func fileRefIDFromLocation(location string) (string, error) {
	if location == "" {
		return "", errors.New("upload location is empty")
	}

	u, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("invalid upload location %q: %w", location, err)
	}

	pathParts := splitPath(u.Path)
	if len(pathParts) == 0 {
		return "", fmt.Errorf("upload location path is empty: %q", location)
	}

	last := pathParts[len(pathParts)-1]
	if last == "content" {
		if len(pathParts) < 2 {
			return "", fmt.Errorf("upload location missing file-ref id: %q", location)
		}
		last = pathParts[len(pathParts)-2]
	}

	id := cleanPath(last)
	if id == "" {
		return "", fmt.Errorf("invalid upload location path %q", u.Path)
	}

	return id, nil
}

// cleanPath returns the shortest path name equivalent to path
func cleanPath(p string) string {
	cleanedPath := path.Clean(p)
	if cleanedPath == "." || cleanedPath == "/" {
		return ""
	}

	return strings.TrimPrefix(cleanedPath, "/")
}

func joinPath(parts ...string) string {
	return cleanPath(path.Join(parts...))
}

// dirPath returns all but the last element of path, typically the path's directory.
// If the path is empty, Dir returns "".
func dirPath(p string) string {
	return cleanPath(path.Dir(p))
}

func splitPath(p string) []string {
	p = cleanPath(p)
	if p == "" {
		return []string{}
	}

	return strings.Split(p, "/")
}

// basePath returns the last element of path.
// Trailing slashes are removed before extracting the last element.
// If the path is empty, Base returns "".
// If the path consists entirely of slashes, Base returns "".
func basePath(p string) string {
	return cleanPath(path.Base(p))
}

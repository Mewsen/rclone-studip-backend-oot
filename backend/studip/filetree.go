package studip

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rclone/rclone/fs"
)

type Node struct {
	Children           []*Node
	Parent             *Node
	Name               string
	Path               string
	ID                 string
	IsReadable         bool
	IsWritable         bool
	IsDownloadable     bool
	IsEditable         bool
	IsSubfolderAllowed bool
	IsDir              bool
	ChDate             time.Time
	Size               int64
	ContentType        string
}

func (n *Node) String() string {
	if n == nil {
		return "<nil Node>"
	}

	parentPath := "<nil>"
	if n.Parent != nil {
		parentPath = n.Parent.Path
	}

	return fmt.Sprintf(
		"Node{name=%q path=%q id=%q isDir=%t size=%d children=%d readable=%t writable=%t editable=%t downloadable=%t subfolderAllowed=%t contentType=%q parentPath=%q}",
		n.Name,
		n.Path,
		n.ID,
		n.IsDir,
		n.Size,
		len(n.Children),
		n.IsReadable,
		n.IsWritable,
		n.IsEditable,
		n.IsDownloadable,
		n.IsSubfolderAllowed,
		n.ContentType,
		parentPath,
	)
}

type FileTree struct {
	root *Node
	// This is the root from rclone's perspective
	// most functions should use the relativeRoot
	relativeRoot *Node
}

func (ft *FileTree) String() string {
	if ft == nil {
		return "<nil FileTree>"
	}

	rootPath := "<nil>"
	rootID := "<nil>"
	rootChildren := -1
	if ft.root != nil {
		rootPath = ft.root.Path
		rootID = ft.root.ID
		rootChildren = len(ft.root.Children)
	}

	relativeRootPath := "<nil>"
	relativeRootID := "<nil>"
	relativeRootChildren := -1
	if ft.relativeRoot != nil {
		relativeRootPath = ft.relativeRoot.Path
		relativeRootID = ft.relativeRoot.ID
		relativeRootChildren = len(ft.relativeRoot.Children)
	}

	return fmt.Sprintf(
		"FileTree{rootPath=%q rootID=%q rootChildren=%d relativeRootPath=%q relativeRootID=%q relativeRootChildren=%d sameRoot=%t}",
		rootPath,
		rootID,
		rootChildren,
		relativeRootPath,
		relativeRootID,
		relativeRootChildren,
		ft.root == ft.relativeRoot,
	)
}

func (root *Node) GetNodeAtPath(path string) *Node {
	Assert(root != nil, fmt.Sprintf("root must be not nil; root=%q", root))

	pathSplit := splitPath(path)
	if len(pathSplit) == 0 {
		return root
	}

	currentNode := root

	for len(pathSplit) > 0 {
		if pathSplit[0] == "." {
			pathSplit = pathSplit[1:]
			continue
		}

		if pathSplit[0] == "" {
			pathSplit = pathSplit[1:]
			continue
		}

		found := false
		for _, children := range currentNode.Children {
			if strings.EqualFold(children.Name, pathSplit[0]) {
				currentNode = children
				pathSplit = pathSplit[1:]
				found = true
				break
			}
		}

		if !found {
			return nil
		}
	}

	return currentNode
}

func (ft *FileTree) ListEntries(fsys *Fs, dir string) (entries fs.DirEntries, err error) {
	Assert(ft != nil, fmt.Sprintf("ft must be not nil; ft=%q", ft))
	Assert(fsys != nil, fmt.Sprintf("fsys must be not nil; fsys=%q", fsys))

	if ft.relativeRoot == nil {
		return nil, fs.ErrorDirNotFound
	}

	if !ft.relativeRoot.IsDir {
		return nil, fs.ErrorIsFile
	}

	node := ft.relativeRoot.GetNodeAtPath(dir)
	if node == nil {
		return nil, fs.ErrorDirNotFound
	}

	if !node.IsDir {
		return nil, fs.ErrorIsFile
	}

	for _, child := range node.Children {
		Assert(
			child != nil,
			fmt.Sprintf(
				"child node must be not nil; dir=%q child=%q",
				dir, child,
			),
		)

		Assert(
			child.ID != "",
			fmt.Sprintf(
				"child node id must be not empty; dir=%q childID=%q",
				dir, child.ID,
			),
		)
		Assert(
			child.Name != "",
			fmt.Sprintf(
				"child node name must be not empty; dir=%q childID=%q",
				dir, child.ID,
			),
		)

		Assert(
			!child.ChDate.IsZero(),
			fmt.Sprintf(
				"child node chdate must be not zero; dir=%q chdate=%q",
				dir, child.ChDate,
			),
		)

		if child.IsDir {
			Assert(
				child.Size == -1,
				fmt.Sprintf(
					"child node size must be -1; dir=%q child=%q id=%q got=%d",
					dir, child.Name, child.ID, child.Size,
				),
			)

			directory := new(Directory)
			directory.fs = fsys
			directory.remote = joinPath(dir, child.Name)
			directory.id = child.ID
			directory.items = int64(len(child.Children))
			directory.name = child.Name
			directory.modTime = child.ChDate

			entries = append(entries, directory)
		} else {

			Assert(
				child.Size >= 0,
				fmt.Sprintf(
					"file node size must be >= 0; dir=%q child=%q id=%q got=%d",
					dir, child.Name, child.ID, child.Size,
				),
			)

			Assert(
				child.ContentType != "",
				fmt.Sprintf(
					"file node contenttype must be not empty; dir=%q child=%q id=%q contenttype=%q",
					dir, child.Name, child.ID, child.ContentType,
				),
			)

			object := new(Object)
			object.fs = fsys
			object.remote = joinPath(dir, child.Name)
			object.id = child.ID
			object.size = child.Size
			object.isReadable = child.IsReadable
			object.isEditable = child.IsEditable
			object.isWritable = child.IsWritable
			object.IsDownloadable = child.IsDownloadable
			object.contentType = child.ContentType
			object.modTime = child.ChDate

			entries = append(entries, object)
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries.Less(i, j)
	})

	return entries, nil
}

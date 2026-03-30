package studip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/rest"
)

// If the Directory already exists, studip will create a Directory with a suffix
// TODO: Return the directoryID from the Location so we can check if a duplicate directory was created so we can delete it in that case
func (f *Fs) studIPMkDir(
	ctx context.Context,
	parentDirectoryID string,
	filename string,
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
		parentDirectoryID != "",
		fmt.Sprintf(
			"parentDirectoryID must be not empty; parentDirectoryID=%q",
			parentDirectoryID,
		),
	)

	Assert(
		filename != "",
		fmt.Sprintf(
			"filename must be not empty; filename=%q",
			filename,
		),
	)

	URL := fmt.Sprintf("courses/%s/folders", f.opt.CourseID)

	fs.Debugf(
		f,
		"studIPMkDir: request parentID=%q name=%q path=%q",
		parentDirectoryID,
		filename,
		URL,
	)

	payload := struct {
		Data struct {
			Type       string `json:"type"`
			Attributes struct {
				Name string `json:"name"`
			} `json:"attributes"`
			Relationships struct {
				Parent struct {
					Data struct {
						Type string `json:"type"`
						ID   string `json:"id"`
					} `json:"data"`
				} `json:"parent"`
			} `json:"relationships"`
		} `json:"data"`
	}{}

	payload.Data.Type = "folders"
	payload.Data.Attributes.Name = filename
	payload.Data.Relationships.Parent.Data.Type = "folders"
	payload.Data.Relationships.Parent.Data.ID = parentDirectoryID

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	res, err := f.client.Call(ctx, &rest.Opts{
		Method:      "POST",
		Path:        URL,
		ContentType: "application/vnd.api+json",
		Body:        bytes.NewReader(body),
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()

	fs.Debugf(f, "studIPMkDir: created name=%q under parentID=%q", filename, parentDirectoryID)

	return nil
}

func (f *Fs) studIPGetFoldersOfFolder(
	ctx context.Context,
	folderID string,
) (*StudIPFolders, error) {
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

	URL := fmt.Sprintf("folders/%s/folders", folderID)

	responseJSON := &StudIPFolders{}
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

func (f *Fs) studIPGetFilesOfFolder(
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

func (f *Fs) studIPGetCourseFolders(ctx context.Context) (*StudIPFolders, error) {
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

	URL := fmt.Sprintf("courses/%s/folders", f.opt.CourseID)

	responseJSON := &StudIPFolders{}
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

func (f *Fs) studIPDeleteFolder(ctx context.Context, folderID string) error {
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

	URL := fmt.Sprintf("folders/%s", folderID)

	res, err := f.client.Call(ctx, &rest.Opts{
		Method: "DELETE",
		Path:   URL,
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()

	return nil
}

func (f *Fs) studIPGetCourse(ctx context.Context) (*StudIPCourses, error) {
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

	URL := fmt.Sprintf("courses/%s", f.opt.CourseID)

	responseJSON := new(StudIPCourses)
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

func (f *Fs) studIPOpenFileContent(
	ctx context.Context,
	fileRefID string,
	options ...fs.OpenOption,
) (io.ReadCloser, error) {
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

	if fileRefID == "" {
		return nil, errors.New("fileRefID is empty")
	}

	URL := fmt.Sprintf("file-refs/%s/content", fileRefID)

	opts := rest.Opts{Method: "GET", Path: URL}
	opts.Options = options

	res, err := f.client.Call(ctx, &opts)
	if err != nil {
		return nil, err
	}

	return res.Body, nil
}

func (f *Fs) studIPCreateFileContent(
	ctx context.Context,
	parentFolderID string,
	in io.Reader,
	filename string,
	size int64,
) (location string, err error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	if parentFolderID == "" {
		return "", errors.New("parent folder id is empty")
	}
	if in == nil {
		return "", errors.New("input reader is nil")
	}
	if filename == "" {
		return "", fmt.Errorf("invalid filename %q", filename)
	}
	if size < -1 {
		return "", fmt.Errorf("invalid size %d", size)
	}

	URL := fmt.Sprintf("folders/%s/file-refs", parentFolderID)
	fs.Debugf(
		f,
		"studIPCreateFileContent: start parentFolderID=%q filename=%q path=%q",
		parentFolderID,
		filename,
		URL,
	)

	return f.studIPUploadFileContentToPath(ctx, URL, in, filename, size)
}

func (f *Fs) studIPUpdateFileContent(
	ctx context.Context,
	fileRefID string,
	in io.Reader,
	filename string,
	size int64,
) (location string, err error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	if fileRefID == "" {
		return "", errors.New("fileRefID is empty")
	}
	if in == nil {
		return "", errors.New("input reader is nil")
	}
	if filename == "" {
		return "", fmt.Errorf("invalid filename %q", filename)
	}

	URL := fmt.Sprintf("file-refs/%s/content", fileRefID)
	fs.Debugf(
		f,
		"studIPUpdateFileContent: start fileRefID=%q filename=%q path=%q",
		fileRefID,
		filename,
		URL,
	)

	return f.studIPUploadFileContentToPath(ctx, URL, in, filename, size)
}

func (f *Fs) studIPUploadFileContentToPath(
	ctx context.Context,
	URL string,
	in io.Reader,
	filename string,
	size int64,
) (location string, err error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	Assert(
		f != nil,
		fmt.Sprintf(
			"f must be not nil; f=%q",
			f,
		),
	)

	if URL == "" {
		return "", errors.New("URL is empty")
	}
	if in == nil {
		return "", errors.New("input reader is nil")
	}
	if filename == "" {
		return "", fmt.Errorf("invalid filename %q", filename)
	}

	// Read first 512 bytes for content type detection.
	buffer := make([]byte, 512)
	n, err := io.ReadFull(in, buffer)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", err
	}

	detectedType := http.DetectContentType(buffer[:n])
	fs.Debugf(
		f,
		"studIPUploadFileContentToPath: detected content-type=%q sampled-bytes=%d",
		detectedType,
		n,
	)

	// Reconstruct reader so we don't lose the first bytes.
	fullReader := io.MultiReader(bytes.NewReader(buffer[:n]), in)

	multipartBody, multipartType, overhead, err := rest.MultipartUpload(
		ctx,
		fullReader,
		url.Values{},
		"file",
		filename,
		detectedType,
	)
	if err != nil {
		return "", err
	}

	defer multipartBody.Close()

	opts := &rest.Opts{
		Method:      "POST",
		Path:        URL,
		ContentType: multipartType,
		Body:        multipartBody,
	}

	if size >= 0 {
		contentLength := size + overhead
		opts.ContentLength = &contentLength
		fs.Debugf(
			f,
			"studIPUploadFileContentToPath: using content-length=%d (file=%d overhead=%d)",
			contentLength,
			size,
			overhead,
		)
	}
	res, err := f.client.Call(ctx, opts)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.Request != nil {
		requestURL := ""
		if res.Request.URL != nil {
			requestURL = res.Request.URL.String()
		}
		fs.Debugf(
			f,
			"StudIP upload request: method=%s url=%s content-type=%q content-length=%d transfer-encoding=%v\n",
			res.Request.Method,
			requestURL,
			res.Request.Header.Get("Content-Type"),
			res.Request.ContentLength,
			res.Request.TransferEncoding,
		)
	}
	fs.Debugf(
		f,
		"StudIP upload response: status=%s location=%q\n",
		res.Status,
		res.Header.Get("Location"),
	)

	location = res.Header.Get("Location")
	if location == "" {
		return "", errors.New("no Location header returned")
	}
	fs.Debugf(f, "studIPUploadFileContentToPath: success path=%q location=%q", URL, location)

	return location, nil
}

func (f *Fs) studIPSetTermsOfUse(
	ctx context.Context,
	fileRefID string,
	licenseID string,
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

	if fileRefID == "" {
		return errors.New("fileRefID is empty")
	}

	if fileRefID == "" {
		return errors.New("licenseID is empty")
	}

	URL := fmt.Sprintf("file-refs/%s/relationships/terms-of-use", fileRefID)

	payload := struct {
		Data struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"data"`
	}{}
	payload.Data.Type = "terms-of-use"
	payload.Data.ID = licenseID

	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	res, err := f.client.Call(ctx, &rest.Opts{
		Method:      "PATCH",
		Path:        URL,
		ContentType: "application/vnd.api+json",
		Body:        bytes.NewReader(b),
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()

	return nil
}

func (f *Fs) studIPCopyDirectory(ctx context.Context, directoryID string, destinationDirectoryID string) error {
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

	if directoryID == "" {
		return errors.New("directoryID is empty")
	}

	URL := fmt.Sprintf("folders/%s/copy", directoryID)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if err := writer.WriteField("destination", destinationDirectoryID); err != nil {
		return fmt.Errorf("write multipart field destination: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}

	res, err := f.client.Call(ctx, &rest.Opts{
		Method:      "POST",
		Path:        URL,
		Body:        &body,
		ContentType: writer.FormDataContentType(),
	})
	if err != nil {
		return err
	}

	defer res.Body.Close()

	return nil
}

func (f *Fs) studIPCopyFile(
	ctx context.Context,
	resourceID string,
	destinationDirectoryID string,
	filename string,
	licenseID string,
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

	if resourceID == "" {
		return errors.New("resourceID is empty")
	}

	if destinationDirectoryID == "" {
		return errors.New("destinationID is empty")
	}

	if filename == "" {
		return errors.New("filename is empty")
	}

	URL := fmt.Sprintf("folders/%s/file-refs", destinationDirectoryID)

	payload := struct {
		Data struct {
			Type       string `json:"type"`
			Attributes struct {
				Name string `json:"name"`
			} `json:"attributes"`
			Relationships struct {
				TermsOfUse struct {
					Data struct {
						Type string `json:"type,omitempty"`
						ID   string `json:"id,omitempty"`
					} `json:"data,omitempty"`
				} `json:"terms-of-use,omitempty"`
				File struct {
					Data struct {
						Type string `json:"type"`
						ID   string `json:"id"`
					} `json:"data"`
				} `json:"file"`
			} `json:"relationships"`
		} `json:"data"`
	}{}

	payload.Data.Type = "file-refs"
	payload.Data.Attributes.Name = filename
	payload.Data.Relationships.File.Data.ID = resourceID
	payload.Data.Relationships.File.Data.Type = "files"
	if f.opt.License != "" {
		payload.Data.Relationships.TermsOfUse.Data.Type = "terms-of-use"
		payload.Data.Relationships.TermsOfUse.Data.ID = f.opt.License
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	res, err := f.client.Call(ctx, &rest.Opts{
		Method:      "POST",
		Path:        URL,
		ContentType: "application/vnd.api+json",
		Body:        bytes.NewReader(body),
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()

	return nil
}

func (f *Fs) studIPDeleteFile(ctx context.Context, fileRefID string) error {
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

	if fileRefID == "" {
		return errors.New("fileRefID is empty")
	}

	URL := fmt.Sprintf("file-refs/%s", fileRefID)

	res, err := f.client.Call(ctx, &rest.Opts{
		Method: "DELETE",
		Path:   URL,
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()

	return nil
}

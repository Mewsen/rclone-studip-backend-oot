package studip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

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

	encodedFilename := f.opt.Enc.FromStandardName(filename)

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
	payload.Data.Attributes.Name = encodedFilename
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

func pagedPath(basePath string, offset, limit int) string {
	values := url.Values{}
	values.Set("page[offset]", strconv.Itoa(offset))
	values.Set("page[limit]", strconv.Itoa(limit))
	return basePath + "?" + values.Encode()
}

func nextPageLimit(limit, loaded int) int {
	switch {
	case limit > 0:
		return limit
	case loaded > 0:
		return loaded
	default:
		return 30
	}
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

	for len(responseJSON.Data) < responseJSON.Meta.Page.Total {
		offset := len(responseJSON.Data)
		limit := nextPageLimit(responseJSON.Meta.Page.Limit, len(responseJSON.Data))
		page := &StudIPFolders{}
		res, err := f.client.CallJSON(
			ctx,
			&rest.Opts{Method: "GET", Path: pagedPath(URL, offset, limit)},
			nil,
			page,
		)
		if err != nil {
			return nil, err
		}
		res.Body.Close()
		if len(page.Data) == 0 {
			break
		}

		responseJSON.Data = append(responseJSON.Data, page.Data...)
		responseJSON.Meta = page.Meta
	}

	for i := 0; i < len(responseJSON.Data); i++ {
		responseJSON.Data[i].Attributes.Name = f.opt.Enc.ToStandardName(responseJSON.Data[i].Attributes.Name)
	}

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

	for len(responseJSON.Data) < responseJSON.Meta.Page.Total {
		offset := len(responseJSON.Data)
		limit := nextPageLimit(responseJSON.Meta.Page.Limit, len(responseJSON.Data))
		page := &StudIPFiles{}
		res, err := f.client.CallJSON(
			ctx,
			&rest.Opts{Method: "GET", Path: pagedPath(URL, offset, limit)},
			nil,
			page,
		)
		if err != nil {
			return nil, err
		}
		res.Body.Close()
		if len(page.Data) == 0 {
			break
		}

		responseJSON.Data = append(responseJSON.Data, page.Data...)
		responseJSON.Meta = page.Meta
		responseJSON.Links = page.Links
	}

	for i := 0; i < len(responseJSON.Data); i++ {
		responseJSON.Data[i].Attributes.Name = f.opt.Enc.ToStandardName(responseJSON.Data[i].Attributes.Name)
	}

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

	for len(responseJSON.Data) < responseJSON.Meta.Page.Total {
		offset := len(responseJSON.Data)
		limit := nextPageLimit(responseJSON.Meta.Page.Limit, len(responseJSON.Data))
		page := &StudIPFolders{}
		res, err := f.client.CallJSON(
			ctx,
			&rest.Opts{Method: "GET", Path: pagedPath(URL, offset, limit)},
			nil,
			page,
		)
		if err != nil {
			return nil, err
		}
		res.Body.Close()
		if len(page.Data) == 0 {
			break
		}
		responseJSON.Data = append(responseJSON.Data, page.Data...)
		responseJSON.Meta = page.Meta
	}

	for i := 0; i < len(responseJSON.Data); i++ {
		responseJSON.Data[i].Attributes.Name = f.opt.Enc.ToStandardName(responseJSON.Data[i].Attributes.Name)
	}

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

func (f *Fs) studIPGetFileRef(
	ctx context.Context,
	fileRefID string,
) (*StudIPFileRef, error) {
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

	URL := fmt.Sprintf("file-refs/%s", fileRefID)

	responseJSON := new(StudIPFileRef)
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

func (f *Fs) studIPUpdateFileRef(
	ctx context.Context,
	fileRefID string,
	filename string,
	description string,
	termsOfUseID string,
) (*StudIPFileRefData, error) {
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

	if filename == "" {
		return nil, errors.New("name is empty")
	}

	if termsOfUseID == "" {
		return nil, errors.New("termsOfUseID is empty")
	}

	encodedFilename := f.opt.Enc.FromStandardName(filename)

	URL := fmt.Sprintf("file-refs/%s", fileRefID)

	payload := struct {
		Data struct {
			Type       string `json:"type"`
			Attributes struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"attributes"`
			Relationships struct {
				TermsOfUse struct {
					Data struct {
						Type string `json:"type"`
						ID   string `json:"id"`
					} `json:"data"`
				} `json:"terms-of-use"`
			} `json:"relationships"`
		} `json:"data"`
	}{}
	payload.Data.Type = "file-refs"
	payload.Data.Attributes.Name = encodedFilename
	payload.Data.Attributes.Description = description
	payload.Data.Relationships.TermsOfUse.Data.Type = "terms-of-use"
	payload.Data.Relationships.TermsOfUse.Data.ID = termsOfUseID

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	responseJSON := new(StudIPFileRef)
	res, err := f.client.CallJSON(
		ctx,
		&rest.Opts{
			Method:      "PATCH",
			Path:        URL,
			ContentType: "application/vnd.api+json",
			Body:        bytes.NewReader(body),
		},
		nil,
		responseJSON,
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	return &responseJSON.Data, nil
}

func (f *Fs) studIPCreateFileRefByReference(
	ctx context.Context,
	parentFolderID string,
	fileID string,
	filename string,
	description string,
	termsOfUseID string,
) (*StudIPFileRefData, error) {
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

	if parentFolderID == "" {
		return nil, errors.New("parentFolderID is empty")
	}

	if fileID == "" {
		return nil, errors.New("fileID is empty")
	}

	if filename == "" {
		return nil, errors.New("name is empty")
	}

	if termsOfUseID == "" {
		return nil, errors.New("termsOfUseID is empty")
	}

	encodedFilename := f.opt.Enc.FromStandardName(filename)

	URL := fmt.Sprintf("folders/%s/file-refs", parentFolderID)

	payload := struct {
		Data struct {
			Type       string `json:"type"`
			Attributes struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"attributes"`
			Relationships struct {
				File struct {
					Data struct {
						Type string `json:"type"`
						ID   string `json:"id"`
					} `json:"data"`
				} `json:"file"`
				TermsOfUse struct {
					Data struct {
						Type string `json:"type"`
						ID   string `json:"id"`
					} `json:"data"`
				} `json:"terms-of-use"`
			} `json:"relationships"`
		} `json:"data"`
	}{}

	payload.Data.Type = "file-refs"
	payload.Data.Attributes.Name = encodedFilename
	payload.Data.Attributes.Description = description
	payload.Data.Relationships.File.Data.Type = "files"
	payload.Data.Relationships.File.Data.ID = fileID
	payload.Data.Relationships.TermsOfUse.Data.Type = "terms-of-use"
	payload.Data.Relationships.TermsOfUse.Data.ID = termsOfUseID

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	responseJSON := new(StudIPFileRef)
	res, err := f.client.CallJSON(
		ctx,
		&rest.Opts{
			Method:      "POST",
			Path:        URL,
			ContentType: "application/vnd.api+json",
			Body:        bytes.NewReader(body),
		},
		nil,
		responseJSON,
	)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	return &responseJSON.Data, nil
}

func (f *Fs) studIPUpdateFolder(
	ctx context.Context,
	folderID string,
	filename string,
	parentFolderID string,
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

	if folderID == "" {
		return errors.New("folderID is empty")
	}

	if filename == "" {
		return errors.New("filename is empty")
	}

	if parentFolderID == "" {
		return errors.New("parentFolderID is empty")
	}

	encodedFilename := f.opt.Enc.FromStandardName(filename)

	URL := fmt.Sprintf("folders/%s", folderID)

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
	payload.Data.Attributes.Name = encodedFilename
	payload.Data.Relationships.Parent.Data.Type = "folders"
	payload.Data.Relationships.Parent.Data.ID = parentFolderID

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	res, err := f.client.Call(ctx, &rest.Opts{
		Method:      "PATCH",
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

	encodedFilename := f.opt.Enc.FromStandardName(filename)

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
		encodedFilename,
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
		if res != nil {
			defer res.Body.Close()
			if res.StatusCode == http.StatusNotFound {
				fs.Debugf(f, "studIPDeleteFile: ignoring missing file-ref id=%q", fileRefID)
				return nil
			}
		}
		return err
	}
	defer res.Body.Close()

	return nil
}

package studip

import "time"

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
	Relationships struct {
		Parent struct {
			Data struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"data"`
		} `json:"parent"`
	} `json:"relationships"`
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

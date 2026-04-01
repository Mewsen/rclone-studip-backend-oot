package studip

import "time"

type Meta struct {
	Page Page
}

type Page struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
	Total  int `json:"total"`
}

type Links struct {
	First string `json:"first"`
	Last  string `json:"last"`
}

type StudIPFolders struct {
	Meta Meta
	Data []StudIPFoldersData `json:"data"`
}

type Attributes struct {
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
	Downloads          int       `json:"downloads"`
	Filesize           int64     `json:"filesize"`
	MimeType           string    `json:"mime-type"`
	CourseNumber       string    `json:"course-number"`
	Title              string    `json:"title"`
	CourseType         int       `json:"course-type"`
	CourseTypeText     string    `json:"course-type-text"`
	Dates              string    `json:"dates"`
	IsDownloadable     bool      `json:"is-downloadable"`
}

type StudIPFoldersData struct {
	Type          string `json:"type"`
	ID            string `json:"id"`
	Attributes    Attributes
	Relationships struct {
		Parent StudIPRelationship `json:"parent"`
	} `json:"relationships"`
}

type StudIPFiles struct {
	Meta  Meta
	Links Links
	Data  []struct {
		StudIPResourceIdentifier
		Attributes Attributes
	} `json:"data"`
}

type StudIPResourceIdentifier struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type StudIPRelationship struct {
	Data StudIPResourceIdentifier `json:"data"`
}

type StudIPNullableRelationship struct {
	Data *StudIPResourceIdentifier `json:"data"`
}

type StudIPFileRefData struct {
	StudIPResourceIdentifier
	Attributes    Attributes
	Relationships struct {
		File       StudIPRelationship         `json:"file"`
		Parent     StudIPRelationship         `json:"parent"`
		TermsOfUse StudIPNullableRelationship `json:"terms-of-use"`
	} `json:"relationships"`
}

type StudIPFileRef struct {
	Data StudIPFileRefData `json:"data"`
}

type StudIPCourses struct {
	Data struct {
		StudIPResourceIdentifier
		Attributes Attributes
	} `json:"data"`
}

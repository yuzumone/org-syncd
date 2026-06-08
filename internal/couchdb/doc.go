package couchdb

type FileDoc struct {
	ID            string `json:"_id,omitempty"`
	Rev           string `json:"_rev,omitempty"`
	Type          string `json:"type"`
	Path          string `json:"path"`
	Content       string `json:"content"`
	ContentSHA256 string `json:"content_sha256"`
	MTime         string `json:"mtime"`
	Deleted       bool   `json:"deleted"`
	UpdatedBy     string `json:"updated_by"`
}

type allDocsResponse struct {
	Rows []struct {
		ID  string  `json:"id"`
		Doc FileDoc `json:"doc"`
	} `json:"rows"`
}

type changesResponse struct {
	LastSeq any `json:"last_seq"`
	Results []struct {
		ID  string  `json:"id"`
		Seq any     `json:"seq"`
		Doc FileDoc `json:"doc"`
	} `json:"results"`
}

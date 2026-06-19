package couchdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL  *url.URL
	database string
	username string
	password string
	http     *http.Client
}

var ErrConflict = errors.New("couchdb document conflict")

func IsConflict(err error) bool {
	return errors.Is(err, ErrConflict)
}

func New(baseURL, database, username, password string) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, err
	}
	return &Client{
		baseURL:  u,
		database: database,
		username: username,
		password: password,
		http:     &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (c *Client) EnsureDB(ctx context.Context) error {
	req, err := c.newRequest(ctx, http.MethodPut, c.database, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusPreconditionFailed || resp.StatusCode == http.StatusAccepted {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("ensure database failed: status=%d body=%s", resp.StatusCode, string(body))
}

func (c *Client) GetDoc(ctx context.Context, id string) (FileDoc, bool, error) {
	req, err := c.newRequest(ctx, http.MethodGet, c.database+"/"+url.PathEscape(id), nil)
	if err != nil {
		return FileDoc{}, false, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return FileDoc{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return FileDoc{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return FileDoc{}, false, fmt.Errorf("get doc failed: id=%s status=%d body=%s", id, resp.StatusCode, string(body))
	}
	var doc FileDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return FileDoc{}, false, err
	}
	return doc, true, nil
}

func (c *Client) PutDoc(ctx context.Context, doc FileDoc) (string, error) {
	if doc.ID == "" {
		return "", errors.New("doc _id is required")
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	req, err := c.newRequest(ctx, http.MethodPut, c.database+"/"+url.PathEscape(doc.ID), bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("%w: id=%s body=%s", ErrConflict, doc.ID, string(body))
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("put doc failed: id=%s status=%d body=%s", doc.ID, resp.StatusCode, string(body))
	}
	var out struct {
		Rev string `json:"rev"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Rev, nil
}

func (c *Client) AllDocs(ctx context.Context) ([]FileDoc, error) {
	req, err := c.newRequest(ctx, http.MethodGet, c.database+"/_all_docs?include_docs=true", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("all docs failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	var out allDocsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	docs := make([]FileDoc, 0, len(out.Rows))
	for _, row := range out.Rows {
		if row.Doc.Type == "file" {
			docs = append(docs, row.Doc)
		}
	}
	return docs, nil
}

func (c *Client) Changes(ctx context.Context, since string) ([]FileDoc, string, error) {
	q := url.Values{}
	q.Set("include_docs", "true")
	if since != "" {
		q.Set("since", since)
	}
	req, err := c.newRequest(ctx, http.MethodGet, c.database+"/_changes?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", fmt.Errorf("changes failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	var out changesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, "", err
	}
	docs := make([]FileDoc, 0, len(out.Results))
	for _, row := range out.Results {
		if row.Doc.Type == "file" {
			docs = append(docs, row.Doc)
		}
	}
	lastSeq := ""
	if out.LastSeq != nil {
		b, _ := json.Marshal(out.LastSeq)
		lastSeq = strings.Trim(string(b), `"`)
	}
	return docs, lastSeq, nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	u := *c.baseURL
	if before, after, ok := strings.Cut(path, "?"); ok {
		u.Path = strings.TrimRight(u.Path, "/") + "/" + before
		u.RawQuery = after
	} else {
		u.Path = strings.TrimRight(u.Path, "/") + "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	return req, nil
}

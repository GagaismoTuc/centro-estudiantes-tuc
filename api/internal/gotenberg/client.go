// Package gotenberg converts Office documents to PDF using Gotenberg.
package gotenberg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	url  string
	http *http.Client
}

// New creates a converter with the required 30-second deadline.
func New(baseURL string) *Client {
	return &Client{url: strings.TrimRight(baseURL, "/") + "/forms/libreoffice/convert", http: &http.Client{Timeout: 30 * time.Second}}
}

// Convert sends one DOCX and returns the converted PDF, without writing files.
func (c *Client) Convert(ctx context.Context, doc []byte) ([]byte, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("files", "report.docx")
	if err != nil {
		return nil, errors.New("create converter request")
	}
	if _, err := part.Write(doc); err != nil {
		return nil, errors.New("create converter request")
	}
	if err := form.Close(); err != nil {
		return nil, errors.New("create converter request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, &body)
	if err != nil {
		return nil, errors.New("invalid converter URL")
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, errors.New("converter timeout")
		}
		if errors.Is(err, context.Canceled) {
			return nil, errors.New("converter request cancelled")
		}
		return nil, errors.New("converter connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("converter returned status %d", resp.StatusCode)
	}
	pdf, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.New("converter response read failed")
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		return nil, errors.New("converter returned invalid PDF")
	}
	return pdf, nil
}

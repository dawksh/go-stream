package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const openSubBaseURL = "https://api.opensubtitles.com/api/v1"

type OpenSubClient struct {
	apiKey     string
	httpClient *http.Client
}

type SubSearchResult struct {
	FileID   int    `json:"fileId"`
	FileName string `json:"fileName"`
	Language string `json:"language"`
	Release  string `json:"release"`
	Rating   float64 `json:"rating"`
	Downloads int    `json:"downloads"`
}

func NewOpenSubClient(apiKey string) *OpenSubClient {
	return &OpenSubClient{
		apiKey: apiKey,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *OpenSubClient) Search(query, lang string) ([]SubSearchResult, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("OpenSubtitles API key not configured")
	}

	params := url.Values{}
	params.Set("query", query)
	if lang != "" {
		params.Set("languages", lang)
	}

	req, err := http.NewRequest("GET", openSubBaseURL+"/subtitles?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("User-Agent", "go-torrent-stream v1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("OpenSubtitles API error %d: %s", resp.StatusCode, string(body))
	}

	var apiResp struct {
		Data []struct {
			Attributes struct {
				Language     string  `json:"language"`
				Release      string  `json:"release"`
				Rating       float64 `json:"ratings"`
				DownloadCount int    `json:"download_count"`
				Files        []struct {
					FileID   int    `json:"file_id"`
					FileName string `json:"file_name"`
				} `json:"files"`
			} `json:"attributes"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	var results []SubSearchResult
	for _, d := range apiResp.Data {
		for _, f := range d.Attributes.Files {
			results = append(results, SubSearchResult{
				FileID:    f.FileID,
				FileName:  f.FileName,
				Language:  d.Attributes.Language,
				Release:   d.Attributes.Release,
				Rating:    d.Attributes.Rating,
				Downloads: d.Attributes.DownloadCount,
			})
		}
	}

	return results, nil
}

func (c *OpenSubClient) Download(fileID int) ([]byte, string, error) {
	if c.apiKey == "" {
		return nil, "", fmt.Errorf("OpenSubtitles API key not configured")
	}

	body := fmt.Sprintf(`{"file_id":%d}`, fileID)
	req, err := http.NewRequest("POST", openSubBaseURL+"/download", strings.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "go-torrent-stream v1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, "", fmt.Errorf("download API error %d: %s", resp.StatusCode, string(respBody))
	}

	var dlResp struct {
		Link     string `json:"link"`
		FileName string `json:"file_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dlResp); err != nil {
		return nil, "", fmt.Errorf("decode download response: %w", err)
	}

	// Fetch the actual subtitle file
	fileResp, err := c.httpClient.Get(dlResp.Link)
	if err != nil {
		return nil, "", fmt.Errorf("fetch subtitle file: %w", err)
	}
	defer fileResp.Body.Close()

	content, err := io.ReadAll(io.LimitReader(fileResp.Body, 10<<20))
	if err != nil {
		return nil, "", fmt.Errorf("read subtitle file: %w", err)
	}

	return content, dlResp.FileName, nil
}

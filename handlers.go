package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type APIResponse struct {
	OK    bool        `json:"ok"`
	Data  interface{} `json:"data,omitempty"`
	Error string      `json:"error,omitempty"`
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(APIResponse{OK: false, Error: msg})
}

func jsonOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(APIResponse{OK: true, Data: data})
}

func handleIndex(tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.ExecuteTemplate(w, "index.html", nil); err != nil {
			log.Printf("template error: %v", err)
			http.Error(w, "Internal Server Error", 500)
		}
	}
}

func handleAddMagnet(manager *TorrentManager) http.HandlerFunc {
	type request struct {
		Magnet string `json:"magnet"`
	}
	type response struct {
		ID    string     `json:"id"`
		Name  string     `json:"name"`
		Files []FileInfo `json:"files"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Magnet == "" {
			jsonError(w, "magnet link is required", http.StatusBadRequest)
			return
		}

		mt, err := manager.AddMagnet(r.Context(), req.Magnet)
		if err != nil {
			if strings.Contains(err.Error(), "timeout") {
				jsonError(w, "metadata timeout — no peers found", http.StatusGatewayTimeout)
				return
			}
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		mt.mu.Lock()
		resp := response{ID: mt.ID, Name: mt.Name, Files: mt.Files}
		mt.mu.Unlock()

		jsonOK(w, resp)
	}
}

func handleSelectFile(manager *TorrentManager) http.HandlerFunc {
	type request struct {
		FileIndex int `json:"fileIndex"`
	}
	type subtitleEntry struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	type response struct {
		StreamURL string          `json:"streamUrl"`
		Subtitles []subtitleEntry `json:"subtitles"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		torrentID := r.PathValue("torrentId")
		if torrentID == "" {
			jsonError(w, "torrent ID required", http.StatusBadRequest)
			return
		}

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request body", http.StatusBadRequest)
			return
		}

		mt, err := manager.SelectFile(torrentID, req.FileIndex)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				jsonError(w, err.Error(), http.StatusNotFound)
				return
			}
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		mt.mu.Lock()
		var subs []subtitleEntry
		for _, s := range mt.Subtitles {
			subs = append(subs, subtitleEntry{
				Name: s.Name,
				URL:  fmt.Sprintf("/subs/%s/%d", torrentID, s.Index),
			})
		}
		mt.mu.Unlock()

		jsonOK(w, response{
			StreamURL: "/stream/" + torrentID,
			Subtitles: subs,
		})
	}
}

func handleStream(manager *TorrentManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		torrentID := r.PathValue("torrentId")
		if torrentID == "" {
			http.Error(w, "torrent ID required", http.StatusBadRequest)
			return
		}

		reader, file, err := manager.GetFileReader(torrentID)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer reader.Close()

		ext := strings.ToLower(filepath.Ext(file.DisplayPath()))
		if ct := contentTypeForExt(ext); ct != "" {
			w.Header().Set("Content-Type", ct)
		}

		http.ServeContent(w, r, file.DisplayPath(), time.Time{}, reader)
	}
}

func handleSubtitle(manager *TorrentManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		torrentID := r.PathValue("torrentId")
		fileIndexStr := r.PathValue("fileIndex")
		if torrentID == "" || fileIndexStr == "" {
			http.Error(w, "missing parameters", http.StatusBadRequest)
			return
		}

		fileIndex, err := strconv.Atoi(fileIndexStr)
		if err != nil {
			http.Error(w, "invalid file index", http.StatusBadRequest)
			return
		}

		mt, ok := manager.GetTorrent(torrentID)
		if !ok {
			http.Error(w, "torrent not found", http.StatusNotFound)
			return
		}

		mt.mu.Lock()
		var content []byte
		for _, s := range mt.Subtitles {
			if s.Index == fileIndex {
				content = s.Content
				break
			}
		}
		mt.mu.Unlock()

		if content == nil {
			http.Error(w, "subtitle not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write(content)
	}
}

func handleUploadSubtitle(manager *TorrentManager) http.HandlerFunc {
	const maxUploadSize = 10 << 20 // 10MB

	return func(w http.ResponseWriter, r *http.Request) {
		torrentID := r.PathValue("torrentId")
		if torrentID == "" {
			jsonError(w, "torrent ID required", http.StatusBadRequest)
			return
		}

		mt, ok := manager.GetTorrent(torrentID)
		if !ok {
			jsonError(w, "torrent not found", http.StatusNotFound)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
		if err := r.ParseMultipartForm(maxUploadSize); err != nil {
			jsonError(w, "file too large (max 10MB)", http.StatusRequestEntityTooLarge)
			return
		}

		file, header, err := r.FormFile("subtitle")
		if err != nil {
			jsonError(w, "subtitle file required", http.StatusBadRequest)
			return
		}
		defer file.Close()

		content, err := io.ReadAll(io.LimitReader(file, maxUploadSize))
		if err != nil {
			jsonError(w, "failed to read file", http.StatusInternalServerError)
			return
		}

		name := header.Filename
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".srt" {
			content = ConvertSRTtoVTT(content)
			name = strings.TrimSuffix(name, filepath.Ext(name)) + ".vtt"
		}

		mt.mu.Lock()
		// Use negative indices for uploaded subtitles to avoid collision
		uploadIndex := -(len(mt.Subtitles) + 1)
		mt.Subtitles = append(mt.Subtitles, SubtitleInfo{
			Name:    name,
			Index:   uploadIndex,
			Content: content,
		})
		mt.mu.Unlock()

		type response struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		}
		jsonOK(w, response{
			Name: name,
			URL:  fmt.Sprintf("/subs/%s/%d", torrentID, uploadIndex),
		})
	}
}

func handleSearchSubtitles(manager *TorrentManager, subClient *OpenSubClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		torrentID := r.PathValue("torrentId")
		if torrentID == "" {
			jsonError(w, "torrent ID required", http.StatusBadRequest)
			return
		}

		mt, ok := manager.GetTorrent(torrentID)
		if !ok {
			jsonError(w, "torrent not found", http.StatusNotFound)
			return
		}

		query := r.URL.Query().Get("query")
		if query == "" {
			// Default to torrent name or selected file name
			mt.mu.Lock()
			if mt.SelectedFile >= 0 && mt.SelectedFile < len(mt.Files) {
				query = stripExt(filepath.Base(mt.Files[mt.SelectedFile].Path))
			} else {
				query = mt.Name
			}
			mt.mu.Unlock()
		}

		lang := r.URL.Query().Get("lang")
		if lang == "" {
			lang = "en"
		}

		results, err := subClient.Search(query, lang)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadGateway)
			return
		}

		jsonOK(w, results)
	}
}

func handleDownloadSubtitle(manager *TorrentManager, subClient *OpenSubClient) http.HandlerFunc {
	type request struct {
		FileID int `json:"fileId"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		torrentID := r.PathValue("torrentId")
		if torrentID == "" {
			jsonError(w, "torrent ID required", http.StatusBadRequest)
			return
		}

		mt, ok := manager.GetTorrent(torrentID)
		if !ok {
			jsonError(w, "torrent not found", http.StatusNotFound)
			return
		}

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.FileID == 0 {
			jsonError(w, "fileId is required", http.StatusBadRequest)
			return
		}

		content, fileName, err := subClient.Download(req.FileID)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadGateway)
			return
		}

		// Convert SRT to VTT if needed
		ext := strings.ToLower(filepath.Ext(fileName))
		if ext == ".srt" {
			content = ConvertSRTtoVTT(content)
			fileName = strings.TrimSuffix(fileName, filepath.Ext(fileName)) + ".vtt"
		}

		mt.mu.Lock()
		uploadIndex := -(len(mt.Subtitles) + 1)
		mt.Subtitles = append(mt.Subtitles, SubtitleInfo{
			Name:    fileName,
			Index:   uploadIndex,
			Content: content,
		})
		mt.mu.Unlock()

		type response struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		}
		jsonOK(w, response{
			Name: fileName,
			URL:  fmt.Sprintf("/subs/%s/%d", torrentID, uploadIndex),
		})
	}
}

func handleCleanup(manager *TorrentManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := manager.RemoveAll(); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, "all torrents and data removed")
	}
}

func contentTypeForExt(ext string) string {
	switch ext {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	default:
		return ""
	}
}


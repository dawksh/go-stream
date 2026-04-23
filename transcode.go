package main

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
)

func handleTranscode(manager *TorrentManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !manager.SupportsCompatibilityStreaming() {
			http.Error(w, "compatibility streaming is unavailable: ffmpeg not found", http.StatusServiceUnavailable)
			return
		}

		torrentID := r.PathValue("torrentId")
		if torrentID == "" {
			http.Error(w, "torrent ID required", http.StatusBadRequest)
			return
		}

		fileIndex := -1
		if rawIndex := r.URL.Query().Get("f"); rawIndex != "" {
			parsedIndex, err := strconv.Atoi(rawIndex)
			if err != nil {
				http.Error(w, "invalid file index", http.StatusBadRequest)
				return
			}
			fileIndex = parsedIndex
		}

		mt, ok := manager.GetTorrent(torrentID)
		if !ok {
			http.Error(w, "torrent not found", http.StatusNotFound)
			return
		}

		mt.mu.Lock()
		selectedIdx := mt.SelectedFile
		if fileIndex < 0 {
			fileIndex = selectedIdx
		}
		if fileIndex < 0 || fileIndex >= len(mt.Files) {
			mt.mu.Unlock()
			http.Error(w, "file index out of range", http.StatusBadRequest)
			return
		}
		fileInfo := mt.Files[fileIndex]
		mt.mu.Unlock()

		if !fileInfo.IsVideo {
			http.Error(w, "selected file is not a video", http.StatusBadRequest)
			return
		}

		reader, file, err := manager.GetFileReader(torrentID, fileIndex)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer reader.Close()

		cmd := exec.CommandContext(r.Context(), manager.ffmpeg, compatibilityFFmpegArgs()...)
		cmd.Stdin = reader

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			http.Error(w, "failed to start transcoder", http.StatusInternalServerError)
			return
		}

		stderr, err := cmd.StderrPipe()
		if err != nil {
			http.Error(w, "failed to start transcoder", http.StatusInternalServerError)
			return
		}

		var stderrBuf bytes.Buffer
		stderrDone := make(chan struct{})
		go func() {
			defer close(stderrDone)
			io.Copy(&stderrBuf, stderr)
		}()

		if err := cmd.Start(); err != nil {
			http.Error(w, "failed to start transcoder", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Accept-Ranges", "none")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		_, copyErr := io.Copy(w, stdout)
		waitErr := cmd.Wait()
		<-stderrDone

		if r.Context().Err() != nil || context.Cause(r.Context()) != nil {
			return
		}
		if copyErr != nil {
			log.Printf("compatibility stream write failed for %s[%d]: %v", torrentID, fileIndex, copyErr)
			return
		}
		if waitErr != nil {
			log.Printf("ffmpeg failed for %s[%d] (%s): %v %s", torrentID, fileIndex, file.DisplayPath(), waitErr, strings.TrimSpace(stderrBuf.String()))
		}
	}
}

func compatibilityFFmpegArgs() []string {
	return []string{
		"-hide_banner",
		"-loglevel", "error",
		"-fflags", "+genpts",
		"-i", "pipe:0",
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-sn",
		"-dn",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-pix_fmt", "yuv420p",
		"-profile:v", "main",
		"-c:a", "aac",
		"-b:a", "192k",
		"-ac", "2",
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof",
		"-f", "mp4",
		"pipe:1",
	}
}

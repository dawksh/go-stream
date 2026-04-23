package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type MediaProbe struct {
	Container  string `json:"container"`
	VideoCodec string `json:"videoCodec"`
	AudioCodec string `json:"audioCodec"`
}

type PlaybackPlan struct {
	StreamURL  string
	CompatMode bool
	Reason     string
}

func lookupExecutable(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

func directStreamURL(torrentID string, fileIndex int) string {
	return fmt.Sprintf("/stream/%s?f=%d", torrentID, fileIndex)
}

func compatStreamURL(torrentID string, fileIndex int) string {
	return fmt.Sprintf("/transcode/%s?f=%d", torrentID, fileIndex)
}

func (m *TorrentManager) SupportsCompatibilityStreaming() bool {
	return m.ffmpeg != ""
}

func (m *TorrentManager) BuildPlaybackPlan(ctx context.Context, id string, fileIndex int) (PlaybackPlan, error) {
	mt, ok := m.GetTorrent(id)
	if !ok {
		return PlaybackPlan{}, fmt.Errorf("torrent not found")
	}

	mt.mu.Lock()
	if fileIndex < 0 || fileIndex >= len(mt.Files) {
		mt.mu.Unlock()
		return PlaybackPlan{}, fmt.Errorf("file index out of range")
	}
	fi := mt.Files[fileIndex]
	probe, cached := mt.MediaProbes[fileIndex]
	mt.mu.Unlock()

	directURL := directStreamURL(id, fileIndex)
	if fi.IsImage || !fi.IsVideo {
		return PlaybackPlan{StreamURL: directURL}, nil
	}

	ext := strings.ToLower(filepath.Ext(fi.Path))
	if !m.SupportsCompatibilityStreaming() {
		return PlaybackPlan{StreamURL: directURL}, nil
	}

	if shouldPreferCompatibilityByExt(ext) {
		return PlaybackPlan{
			StreamURL:  compatStreamURL(id, fileIndex),
			CompatMode: true,
			Reason:     "container is not browser-friendly",
		}, nil
	}

	if !cached && m.ffprobe != "" {
		if detected, err := m.ProbeFile(ctx, id, fileIndex); err == nil {
			probe = detected
			cached = true
		}
	}

	if cached && !isDirectPlaybackCompatible(ext, probe) {
		return PlaybackPlan{
			StreamURL:  compatStreamURL(id, fileIndex),
			CompatMode: true,
			Reason:     compatibilityReason(ext, probe),
		}, nil
	}

	return PlaybackPlan{StreamURL: directURL}, nil
}

func (m *TorrentManager) ProbeFile(ctx context.Context, id string, fileIndex int) (MediaProbe, error) {
	if m.ffprobe == "" {
		return MediaProbe{}, fmt.Errorf("ffprobe not available")
	}

	mt, ok := m.GetTorrent(id)
	if !ok {
		return MediaProbe{}, fmt.Errorf("torrent not found")
	}

	mt.mu.Lock()
	if fileIndex < 0 || fileIndex >= len(mt.Files) {
		mt.mu.Unlock()
		return MediaProbe{}, fmt.Errorf("file index out of range")
	}
	if probe, ok := mt.MediaProbes[fileIndex]; ok {
		mt.mu.Unlock()
		return probe, nil
	}
	mt.mu.Unlock()

	reader, _, err := m.GetFileReader(id, fileIndex)
	if err != nil {
		return MediaProbe{}, err
	}
	defer reader.Close()

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(
		probeCtx,
		m.ffprobe,
		"-v", "error",
		"-show_streams",
		"-show_format",
		"-print_format", "json",
		"pipe:0",
	)
	cmd.Stdin = reader

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if probeCtx.Err() != nil {
			return MediaProbe{}, fmt.Errorf("ffprobe timed out")
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return MediaProbe{}, fmt.Errorf("ffprobe failed: %s", msg)
	}

	var parsed struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
		Format struct {
			FormatName string `json:"format_name"`
		} `json:"format"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		return MediaProbe{}, fmt.Errorf("parse ffprobe output: %w", err)
	}

	probe := MediaProbe{Container: parsed.Format.FormatName}
	for _, stream := range parsed.Streams {
		switch stream.CodecType {
		case "video":
			if probe.VideoCodec == "" {
				probe.VideoCodec = stream.CodecName
			}
		case "audio":
			if probe.AudioCodec == "" {
				probe.AudioCodec = stream.CodecName
			}
		}
	}

	mt.mu.Lock()
	if mt.MediaProbes == nil {
		mt.MediaProbes = make(map[int]MediaProbe)
	}
	mt.MediaProbes[fileIndex] = probe
	mt.mu.Unlock()

	return probe, nil
}

func shouldPreferCompatibilityByExt(ext string) bool {
	switch ext {
	case ".mkv", ".avi", ".mov":
		return true
	default:
		return false
	}
}

func isDirectPlaybackCompatible(ext string, probe MediaProbe) bool {
	switch ext {
	case ".mp4", ".m4v":
		return isMP4CompatibleCodec(probe.VideoCodec) && isMP4CompatibleAudio(probe.AudioCodec)
	case ".webm":
		return isWebMCompatibleCodec(probe.VideoCodec) && isWebMCompatibleAudio(probe.AudioCodec)
	default:
		return false
	}
}

func compatibilityReason(ext string, probe MediaProbe) string {
	switch ext {
	case ".mp4", ".m4v":
		if !isMP4CompatibleCodec(probe.VideoCodec) {
			return "video codec is not broadly supported in browsers"
		}
		if !isMP4CompatibleAudio(probe.AudioCodec) {
			return "audio codec is not broadly supported in browsers"
		}
	case ".webm":
		if !isWebMCompatibleCodec(probe.VideoCodec) {
			return "video codec is not supported in WebM playback"
		}
		if !isWebMCompatibleAudio(probe.AudioCodec) {
			return "audio codec is not supported in WebM playback"
		}
	}
	return "browser compatibility fallback"
}

func isMP4CompatibleCodec(codec string) bool {
	switch strings.ToLower(codec) {
	case "h264", "avc1", "hevc", "h265", "av1":
		return true
	default:
		return false
	}
}

func isMP4CompatibleAudio(codec string) bool {
	switch strings.ToLower(codec) {
	case "", "aac", "mp3", "mp2":
		return true
	default:
		return false
	}
}

func isWebMCompatibleCodec(codec string) bool {
	switch strings.ToLower(codec) {
	case "vp8", "vp9", "av1":
		return true
	default:
		return false
	}
}

func isWebMCompatibleAudio(codec string) bool {
	switch strings.ToLower(codec) {
	case "", "opus", "vorbis":
		return true
	default:
		return false
	}
}

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/storage"
)

var (
	videoExtensions    = map[string]bool{".mkv": true, ".mp4": true, ".avi": true, ".webm": true, ".mov": true, ".m4v": true}
	subtitleExtensions = map[string]bool{".srt": true, ".vtt": true, ".ass": true, ".sub": true}
)

type FileInfo struct {
	Index      int    `json:"index"`
	Path       string `json:"path"`
	Length     int64  `json:"length"`
	IsVideo    bool   `json:"isVideo"`
	IsSubtitle bool   `json:"isSubtitle"`
}

type SubtitleInfo struct {
	Name    string `json:"name"`
	Index   int    `json:"index"`
	Content []byte `json:"-"`
}

type ManagedTorrent struct {
	mu           sync.Mutex
	Torrent      *torrent.Torrent
	ID           string
	Name         string
	Files        []FileInfo
	SelectedFile int
	Subtitles    []SubtitleInfo
	LastAccessed time.Time
}

type TorrentManager struct {
	mu       sync.RWMutex
	client   *torrent.Client
	torrents map[string]*ManagedTorrent
	dataDir  string
}

func NewTorrentManager(dataDir string) (*TorrentManager, error) {
	cfg := torrent.NewDefaultClientConfig()
	cfg.Seed = false
	cfg.ListenPort = 0
	cfg.DefaultStorage = storage.NewFileByInfoHash(dataDir)

	client, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("create torrent client: %w", err)
	}

	return &TorrentManager{
		client:   client,
		torrents: make(map[string]*ManagedTorrent),
		dataDir:  dataDir,
	}, nil
}

func (m *TorrentManager) AddMagnet(ctx context.Context, uri string) (*ManagedTorrent, error) {
	t, err := m.client.AddMagnet(uri)
	if err != nil {
		return nil, fmt.Errorf("add magnet: %w", err)
	}

	id := t.InfoHash().HexString()

	// Return existing if already managed
	m.mu.RLock()
	if mt, ok := m.torrents[id]; ok {
		m.mu.RUnlock()
		mt.mu.Lock()
		mt.LastAccessed = time.Now()
		mt.mu.Unlock()
		return mt, nil
	}
	m.mu.RUnlock()

	// Wait for metadata with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	select {
	case <-t.GotInfo():
	case <-timeoutCtx.Done():
		t.Drop()
		return nil, fmt.Errorf("metadata timeout: %w", timeoutCtx.Err())
	}

	files := classifyFiles(t)

	mt := &ManagedTorrent{
		Torrent:      t,
		ID:           id,
		Name:         t.Name(),
		Files:        files,
		SelectedFile: -1,
		LastAccessed: time.Now(),
	}

	m.mu.Lock()
	// Double-check after acquiring write lock
	if existing, ok := m.torrents[id]; ok {
		m.mu.Unlock()
		t.Drop()
		return existing, nil
	}
	m.torrents[id] = mt
	m.mu.Unlock()

	return mt, nil
}

func (m *TorrentManager) GetTorrent(id string) (*ManagedTorrent, bool) {
	m.mu.RLock()
	mt, ok := m.torrents[id]
	m.mu.RUnlock()
	if ok {
		mt.mu.Lock()
		mt.LastAccessed = time.Now()
		mt.mu.Unlock()
	}
	return mt, ok
}

func (m *TorrentManager) SelectFile(id string, fileIndex int) (*ManagedTorrent, error) {
	mt, ok := m.GetTorrent(id)
	if !ok {
		return nil, fmt.Errorf("torrent not found")
	}

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if fileIndex < 0 || fileIndex >= len(mt.Files) {
		return nil, fmt.Errorf("file index out of range")
	}

	// Prioritize the selected file
	torrentFiles := mt.Torrent.Files()
	for _, f := range torrentFiles {
		f.SetPriority(torrent.PiecePriorityNone)
	}
	torrentFiles[fileIndex].SetPriority(torrent.PiecePriorityNormal)
	mt.SelectedFile = fileIndex

	// Discover subtitles from the torrent — prefer ones matching the video name
	mt.Subtitles = nil
	videoBase := stripExt(filepath.Base(mt.Files[fileIndex].Path))
	videoDir := filepath.Dir(mt.Files[fileIndex].Path)

	var matched, other []SubtitleInfo
	for i, fi := range mt.Files {
		if !fi.IsSubtitle {
			continue
		}
		tf := torrentFiles[i]
		tf.SetPriority(torrent.PiecePriorityNow)

		content, err := readTorrentFile(tf)
		if err != nil {
			continue
		}

		ext := strings.ToLower(filepath.Ext(fi.Path))
		if ext == ".srt" {
			content = ConvertSRTtoVTT(content)
		}

		sub := SubtitleInfo{
			Name:    filepath.Base(fi.Path),
			Index:   i,
			Content: content,
		}

		// Match if same base name or same directory as the video
		subBase := stripExt(filepath.Base(fi.Path))
		subDir := filepath.Dir(fi.Path)
		if strings.HasPrefix(subBase, videoBase) || subDir == videoDir {
			matched = append(matched, sub)
		} else {
			other = append(other, sub)
		}
	}
	mt.Subtitles = append(matched, other...)

	return mt, nil
}

func (m *TorrentManager) GetFileReader(id string) (torrent.Reader, *torrent.File, error) {
	mt, ok := m.GetTorrent(id)
	if !ok {
		return nil, nil, fmt.Errorf("torrent not found")
	}

	mt.mu.Lock()
	selectedIdx := mt.SelectedFile
	mt.mu.Unlock()

	if selectedIdx < 0 {
		return nil, nil, fmt.Errorf("no file selected")
	}

	file := mt.Torrent.Files()[selectedIdx]
	reader := file.NewReader()

	// Scale readahead with file size: 16MB base, up to 64MB for large files
	readahead := int64(16 * 1024 * 1024)
	if file.Length() > 2*1024*1024*1024 { // >2GB
		readahead = 64 * 1024 * 1024
	} else if file.Length() > 500*1024*1024 { // >500MB
		readahead = 32 * 1024 * 1024
	}
	reader.SetReadahead(readahead)
	reader.SetResponsive()

	return reader, file, nil
}

func (m *TorrentManager) RemoveTorrent(id string) {
	m.mu.Lock()
	mt, ok := m.torrents[id]
	if ok {
		delete(m.torrents, id)
	}
	m.mu.Unlock()

	if ok {
		mt.Torrent.Drop()
	}
}

func (m *TorrentManager) RemoveAll() error {
	m.mu.Lock()
	ids := make([]string, 0, len(m.torrents))
	for id := range m.torrents {
		ids = append(ids, id)
	}
	for _, id := range ids {
		if mt, ok := m.torrents[id]; ok {
			mt.Torrent.Drop()
			delete(m.torrents, id)
		}
	}
	m.mu.Unlock()

	// Remove downloaded data on disk
	entries, err := os.ReadDir(m.dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read data dir: %w", err)
	}
	for _, e := range entries {
		os.RemoveAll(filepath.Join(m.dataDir, e.Name()))
	}
	return nil
}

func (m *TorrentManager) CleanupLoop(ctx context.Context, maxAge time.Duration) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.cleanup(maxAge)
		}
	}
}

func (m *TorrentManager) cleanup(maxAge time.Duration) {
	m.mu.RLock()
	var stale []string
	for id, mt := range m.torrents {
		mt.mu.Lock()
		if time.Since(mt.LastAccessed) > maxAge {
			stale = append(stale, id)
		}
		mt.mu.Unlock()
	}
	m.mu.RUnlock()

	for _, id := range stale {
		m.RemoveTorrent(id)
	}
}

func (m *TorrentManager) Close() {
	m.client.Close()
}

func classifyFiles(t *torrent.Torrent) []FileInfo {
	var files []FileInfo
	for i, f := range t.Files() {
		ext := strings.ToLower(filepath.Ext(f.DisplayPath()))
		files = append(files, FileInfo{
			Index:      i,
			Path:       f.DisplayPath(),
			Length:     f.Length(),
			IsVideo:    videoExtensions[ext],
			IsSubtitle: subtitleExtensions[ext],
		})
	}
	return files
}

func readTorrentFile(f *torrent.File) ([]byte, error) {
	reader := f.NewReader()
	defer reader.Close()
	return io.ReadAll(reader)
}

func stripExt(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

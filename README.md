# Torrent Stream

Self-hosted Go backend that accepts magnet links, fetches torrent metadata, lists files, and streams video over HTTP with Range request support for browser playback. Includes subtitle support (embedded from torrent + manual upload with SRT→VTT conversion).

## Setup

Requires Go 1.22+.

```bash
go build -o go-stream .
./go-stream -port 8080 -data /tmp/go-stream
```

Open `http://localhost:8080`, paste a magnet link, and play.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | `8080` | HTTP server port |
| `-data` | `/tmp/go-stream` | Directory for downloaded torrent data |
| `-osapi` | `""` | OpenSubtitles API key (or set `OPENSUBTITLES_API_KEY` env var) |

### Subtitle Search

Get a free API key from [opensubtitles.com](https://www.opensubtitles.com/consumers) to enable subtitle search. Pass it via flag or env var:

```bash
export OPENSUBTITLES_API_KEY=your_key_here
./go-stream
```

## Deploy (PM2)

```bash
# On your server, inside ~/go-stream
go build -o go-stream .
pm2 start ecosystem.config.js
pm2 save
pm2 startup
```

## API

| Method | Path | Description |
|--------|------|-------------|
| `GET /` | Serves the web UI |
| `POST /api/magnet` | Add a magnet link (`{"magnet":"..."}`) |
| `POST /api/select/{torrentId}` | Select a file to stream (`{"fileIndex":N}`) |
| `GET /stream/{torrentId}` | Video stream (supports Range requests) |
| `GET /subs/{torrentId}/{fileIndex}` | Serve subtitle as VTT |
| `POST /api/subtitle/{torrentId}` | Upload subtitle file (multipart, max 10MB) |
| `GET /api/subtitles/{torrentId}` | Search OpenSubtitles (`?query=...&lang=en`) |
| `POST /api/subtitles/{torrentId}/download` | Download & attach subtitle (`{"fileId":N}`) |

## Tests

```bash
go test ./...
```

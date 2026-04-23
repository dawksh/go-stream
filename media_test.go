package main

import "testing"

func TestShouldPreferCompatibilityByExt(t *testing.T) {
	tests := []struct {
		ext  string
		want bool
	}{
		{ext: ".mkv", want: true},
		{ext: ".avi", want: true},
		{ext: ".mov", want: true},
		{ext: ".mp4", want: false},
		{ext: ".webm", want: false},
	}

	for _, tt := range tests {
		if got := shouldPreferCompatibilityByExt(tt.ext); got != tt.want {
			t.Fatalf("shouldPreferCompatibilityByExt(%q) = %v, want %v", tt.ext, got, tt.want)
		}
	}
}

func TestIsDirectPlaybackCompatible(t *testing.T) {
	tests := []struct {
		name  string
		ext   string
		probe MediaProbe
		want  bool
	}{
		{
			name: "mp4 h264 aac",
			ext:  ".mp4",
			probe: MediaProbe{
				VideoCodec: "h264",
				AudioCodec: "aac",
			},
			want: true,
		},
		{
			name: "mp4 h264 ac3",
			ext:  ".mp4",
			probe: MediaProbe{
				VideoCodec: "h264",
				AudioCodec: "ac3",
			},
			want: false,
		},
		{
			name: "webm vp9 opus",
			ext:  ".webm",
			probe: MediaProbe{
				VideoCodec: "vp9",
				AudioCodec: "opus",
			},
			want: true,
		},
		{
			name: "webm h264 aac",
			ext:  ".webm",
			probe: MediaProbe{
				VideoCodec: "h264",
				AudioCodec: "aac",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDirectPlaybackCompatible(tt.ext, tt.probe); got != tt.want {
				t.Fatalf("isDirectPlaybackCompatible(%q, %+v) = %v, want %v", tt.ext, tt.probe, got, tt.want)
			}
		})
	}
}

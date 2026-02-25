package main

import (
	"strings"
	"testing"
)

func TestConvertSRTtoVTT(t *testing.T) {
	tests := []struct {
		name string
		srt  string
		want string
	}{
		{
			name: "basic SRT",
			srt: `1
00:00:01,000 --> 00:00:04,000
Hello world

2
00:00:05,000 --> 00:00:08,000
Second line
`,
			want: `WEBVTT

00:00:01.000 --> 00:00:04.000
Hello world

00:00:05.000 --> 00:00:08.000
Second line
`,
		},
		{
			name: "SRT with BOM",
			srt:  "\xEF\xBB\xBF1\n00:00:01,500 --> 00:00:03,500\nWith BOM\n",
			want: "WEBVTT\n\n00:00:01.500 --> 00:00:03.500\nWith BOM\n",
		},
		{
			name: "multi-line cues",
			srt: `1
00:00:01,000 --> 00:00:04,000
Line one
Line two

2
00:00:05,000 --> 00:00:08,000
Another cue
`,
			want: `WEBVTT

00:00:01.000 --> 00:00:04.000
Line one
Line two

00:00:05.000 --> 00:00:08.000
Another cue
`,
		},
		{
			name: "empty input",
			srt:  "",
			want: "WEBVTT\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(ConvertSRTtoVTT([]byte(tt.srt)))
			if !strings.EqualFold(normalizeNewlines(got), normalizeNewlines(tt.want)) {
				t.Errorf("ConvertSRTtoVTT():\ngot:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

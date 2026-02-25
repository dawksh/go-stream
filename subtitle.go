package main

import (
	"bufio"
	"bytes"
	"strings"
)

// ConvertSRTtoVTT converts SRT subtitle content to WebVTT format.
func ConvertSRTtoVTT(srt []byte) []byte {
	// Strip BOM
	srt = bytes.TrimPrefix(srt, []byte("\xEF\xBB\xBF"))

	var buf bytes.Buffer
	buf.WriteString("WEBVTT\n\n")

	scanner := bufio.NewScanner(bytes.NewReader(srt))
	afterBlank := true

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip numeric index lines (digits only after a blank line)
		if afterBlank && isDigitsOnly(trimmed) {
			afterBlank = false
			continue
		}

		// Replace comma with dot in timestamp lines
		if strings.Contains(line, "-->") {
			line = strings.ReplaceAll(line, ",", ".")
		}

		afterBlank = trimmed == ""
		buf.WriteString(line)
		buf.WriteByte('\n')
	}

	return buf.Bytes()
}

func isDigitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

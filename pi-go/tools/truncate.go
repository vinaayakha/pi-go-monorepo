package tools

import (
	"fmt"
	"strings"
)

const (
	DefaultMaxLines      = 2000
	DefaultMaxBytes      = 50 * 1024 // 50KB
	GrepMaxLineLength    = 500
)

type TruncationResult struct {
	Content              string
	Truncated            bool
	TruncatedBy          string // "lines", "bytes", or ""
	TotalLines           int
	TotalBytes           int
	OutputLines          int
	OutputBytes          int
	LastLinePartial      bool
	FirstLineExceedsLimit bool
	MaxLines             int
	MaxBytes             int
}

func formatSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	} else if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

// truncateHead keeps first N lines/bytes. Suitable for file reads.
func truncateHead(content string, maxLines, maxBytes int) TruncationResult {
	if maxLines <= 0 {
		maxLines = DefaultMaxLines
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}

	totalBytes := len(content)
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content: content, Truncated: false, TotalLines: totalLines,
			TotalBytes: totalBytes, OutputLines: totalLines, OutputBytes: totalBytes,
			MaxLines: maxLines, MaxBytes: maxBytes,
		}
	}

	firstLineBytes := len(lines[0])
	if firstLineBytes > maxBytes {
		return TruncationResult{
			Content: "", Truncated: true, TruncatedBy: "bytes",
			TotalLines: totalLines, TotalBytes: totalBytes,
			FirstLineExceedsLimit: true, MaxLines: maxLines, MaxBytes: maxBytes,
		}
	}

	var outputLines []string
	outputBytesCount := 0
	truncatedBy := "lines"

	for i := 0; i < len(lines) && i < maxLines; i++ {
		lineBytes := len(lines[i])
		if i > 0 {
			lineBytes++ // newline
		}
		if outputBytesCount+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		outputLines = append(outputLines, lines[i])
		outputBytesCount += lineBytes
	}

	if len(outputLines) >= maxLines && outputBytesCount <= maxBytes {
		truncatedBy = "lines"
	}

	outputContent := strings.Join(outputLines, "\n")
	return TruncationResult{
		Content: outputContent, Truncated: true, TruncatedBy: truncatedBy,
		TotalLines: totalLines, TotalBytes: totalBytes,
		OutputLines: len(outputLines), OutputBytes: len(outputContent),
		MaxLines: maxLines, MaxBytes: maxBytes,
	}
}

// truncateTail keeps last N lines/bytes. Suitable for bash output.
func truncateTail(content string, maxLines, maxBytes int) TruncationResult {
	if maxLines <= 0 {
		maxLines = DefaultMaxLines
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}

	totalBytes := len(content)
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content: content, Truncated: false, TotalLines: totalLines,
			TotalBytes: totalBytes, OutputLines: totalLines, OutputBytes: totalBytes,
			MaxLines: maxLines, MaxBytes: maxBytes,
		}
	}

	var outputLines []string
	outputBytesCount := 0
	truncatedBy := "lines"
	lastLinePartial := false

	for i := len(lines) - 1; i >= 0 && len(outputLines) < maxLines; i-- {
		lineBytes := len(lines[i])
		if len(outputLines) > 0 {
			lineBytes++ // newline
		}
		if outputBytesCount+lineBytes > maxBytes {
			truncatedBy = "bytes"
			if len(outputLines) == 0 {
				// Take end of line
				if len(lines[i]) > maxBytes {
					outputLines = append([]string{lines[i][len(lines[i])-maxBytes:]}, outputLines...)
					lastLinePartial = true
				}
			}
			break
		}
		outputLines = append([]string{lines[i]}, outputLines...)
		outputBytesCount += lineBytes
	}

	if len(outputLines) >= maxLines && outputBytesCount <= maxBytes {
		truncatedBy = "lines"
	}

	outputContent := strings.Join(outputLines, "\n")
	return TruncationResult{
		Content: outputContent, Truncated: true, TruncatedBy: truncatedBy,
		TotalLines: totalLines, TotalBytes: totalBytes,
		OutputLines: len(outputLines), OutputBytes: len(outputContent),
		LastLinePartial: lastLinePartial,
		MaxLines: maxLines, MaxBytes: maxBytes,
	}
}

func truncateLine(line string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		maxChars = GrepMaxLineLength
	}
	if len(line) <= maxChars {
		return line, false
	}
	return line[:maxChars] + "... [truncated]", true
}

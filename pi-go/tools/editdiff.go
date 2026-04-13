package tools

import (
	"fmt"
	"strings"
	"unicode"
)

// Edit represents a single oldText->newText replacement.
type Edit struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

// normalizeToLF replaces all line endings with LF.
func normalizeToLF(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

// detectLineEnding detects whether the file uses CRLF or LF.
func detectLineEnding(content string) string {
	crlfIdx := strings.Index(content, "\r\n")
	lfIdx := strings.Index(content, "\n")
	if lfIdx == -1 {
		return "\n"
	}
	if crlfIdx == -1 {
		return "\n"
	}
	if crlfIdx < lfIdx {
		return "\r\n"
	}
	return "\n"
}

func restoreLineEndings(text, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

// stripBom removes UTF-8 BOM if present.
func stripBom(content string) (bom, text string) {
	if strings.HasPrefix(content, "\xEF\xBB\xBF") {
		return "\xEF\xBB\xBF", content[3:]
	}
	if strings.HasPrefix(content, "\uFEFF") {
		return "\uFEFF", strings.TrimPrefix(content, "\uFEFF")
	}
	return "", content
}

// normalizeForFuzzyMatch strips trailing whitespace, normalizes smart quotes/dashes.
func normalizeForFuzzyMatch(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	result := strings.Join(lines, "\n")

	// Smart single quotes -> '
	for _, r := range []rune{'\u2018', '\u2019', '\u201A', '\u201B'} {
		result = strings.ReplaceAll(result, string(r), "'")
	}
	// Smart double quotes -> "
	for _, r := range []rune{'\u201C', '\u201D', '\u201E', '\u201F'} {
		result = strings.ReplaceAll(result, string(r), "\"")
	}
	// Various dashes -> -
	for _, r := range []rune{'\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212'} {
		result = strings.ReplaceAll(result, string(r), "-")
	}
	// Special spaces -> regular space
	for _, r := range []rune{'\u00A0', '\u202F', '\u205F', '\u3000'} {
		result = strings.ReplaceAll(result, string(r), " ")
	}

	return result
}

// fuzzyFindText tries exact match first, then fuzzy match.
func fuzzyFindText(content, oldText string) (index, matchLen int, usedFuzzy bool, contentForReplacement string) {
	idx := strings.Index(content, oldText)
	if idx != -1 {
		return idx, len(oldText), false, content
	}

	fuzzyContent := normalizeForFuzzyMatch(content)
	fuzzyOldText := normalizeForFuzzyMatch(oldText)
	idx = strings.Index(fuzzyContent, fuzzyOldText)
	if idx == -1 {
		return -1, 0, false, content
	}
	return idx, len(fuzzyOldText), true, fuzzyContent
}

// ApplyEdits applies multiple oldText->newText replacements to normalized content.
// Returns base content (used for matching) and new content after replacements.
func ApplyEdits(normalizedContent string, edits []Edit, path string) (baseContent, newContent string, err error) {
	normalizedEdits := make([]Edit, len(edits))
	for i, e := range edits {
		normalizedEdits[i] = Edit{OldText: normalizeToLF(e.OldText), NewText: normalizeToLF(e.NewText)}
	}

	for i, e := range normalizedEdits {
		if len(e.OldText) == 0 {
			if len(normalizedEdits) == 1 {
				return "", "", fmt.Errorf("oldText must not be empty in %s", path)
			}
			return "", "", fmt.Errorf("edits[%d].oldText must not be empty in %s", i, path)
		}
	}

	// Check if any edit needs fuzzy matching
	anyFuzzy := false
	for _, e := range normalizedEdits {
		_, _, usedFuzzy, _ := fuzzyFindText(normalizedContent, e.OldText)
		if usedFuzzy {
			anyFuzzy = true
			break
		}
	}

	base := normalizedContent
	if anyFuzzy {
		base = normalizeForFuzzyMatch(normalizedContent)
	}

	type matchedEdit struct {
		editIndex  int
		matchIndex int
		matchLen   int
		newText    string
	}

	var matched []matchedEdit
	for i, e := range normalizedEdits {
		idx, mLen, _, _ := fuzzyFindText(base, e.OldText)
		if idx == -1 {
			if len(normalizedEdits) == 1 {
				return "", "", fmt.Errorf("could not find the exact text in %s", path)
			}
			return "", "", fmt.Errorf("could not find edits[%d] in %s", i, path)
		}

		// Check uniqueness
		count := strings.Count(normalizeForFuzzyMatch(base), normalizeForFuzzyMatch(e.OldText))
		if count > 1 {
			if len(normalizedEdits) == 1 {
				return "", "", fmt.Errorf("found %d occurrences of the text in %s; must be unique", count, path)
			}
			return "", "", fmt.Errorf("found %d occurrences of edits[%d] in %s; must be unique", count, i, path)
		}

		matched = append(matched, matchedEdit{editIndex: i, matchIndex: idx, matchLen: mLen, newText: e.NewText})
	}

	// Sort by match position
	for i := 1; i < len(matched); i++ {
		for j := i; j > 0 && matched[j].matchIndex < matched[j-1].matchIndex; j-- {
			matched[j], matched[j-1] = matched[j-1], matched[j]
		}
	}

	// Check for overlaps
	for i := 1; i < len(matched); i++ {
		prev := matched[i-1]
		curr := matched[i]
		if prev.matchIndex+prev.matchLen > curr.matchIndex {
			return "", "", fmt.Errorf("edits[%d] and edits[%d] overlap in %s", prev.editIndex, curr.editIndex, path)
		}
	}

	// Apply in reverse order
	result := base
	for i := len(matched) - 1; i >= 0; i-- {
		e := matched[i]
		result = result[:e.matchIndex] + e.newText + result[e.matchIndex+e.matchLen:]
	}

	if base == result {
		return "", "", fmt.Errorf("no changes made to %s", path)
	}

	return base, result, nil
}

// GenerateDiff produces a unified diff string with line numbers.
func GenerateDiff(oldContent, newContent string, contextLines int) (diff string, firstChangedLine int) {
	if contextLines <= 0 {
		contextLines = 4
	}

	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	// Simple line-by-line diff using LCS
	type change struct {
		kind    byte // ' ', '+', '-'
		oldLine int  // 1-indexed, 0 if added
		newLine int  // 1-indexed, 0 if removed
		text    string
	}

	// Build changes using a simple sequential comparison
	var changes []change
	oi, ni := 0, 0
	for oi < len(oldLines) && ni < len(newLines) {
		if oldLines[oi] == newLines[ni] {
			changes = append(changes, change{' ', oi + 1, ni + 1, oldLines[oi]})
			oi++
			ni++
		} else {
			// Look ahead for sync point
			foundSync := false
			for lookahead := 1; lookahead < 5 && !foundSync; lookahead++ {
				if ni+lookahead < len(newLines) && oi < len(oldLines) && oldLines[oi] == newLines[ni+lookahead] {
					for k := 0; k < lookahead; k++ {
						changes = append(changes, change{'+', 0, ni + k + 1, newLines[ni+k]})
					}
					ni += lookahead
					foundSync = true
				}
				if oi+lookahead < len(oldLines) && ni < len(newLines) && oldLines[oi+lookahead] == newLines[ni] {
					for k := 0; k < lookahead; k++ {
						changes = append(changes, change{'-', oi + k + 1, 0, oldLines[oi+k]})
					}
					oi += lookahead
					foundSync = true
				}
			}
			if !foundSync {
				changes = append(changes, change{'-', oi + 1, 0, oldLines[oi]})
				changes = append(changes, change{'+', 0, ni + 1, newLines[ni]})
				oi++
				ni++
			}
		}
	}
	for ; oi < len(oldLines); oi++ {
		changes = append(changes, change{'-', oi + 1, 0, oldLines[oi]})
	}
	for ; ni < len(newLines); ni++ {
		changes = append(changes, change{'+', 0, ni + 1, newLines[ni]})
	}

	// Format with context
	maxLineNum := len(oldLines)
	if len(newLines) > maxLineNum {
		maxLineNum = len(newLines)
	}
	lineNumWidth := len(fmt.Sprintf("%d", maxLineNum))

	var output []string
	firstChanged := 0
	for i, c := range changes {
		if c.kind == ' ' {
			// Only show if near a change
			nearChange := false
			for d := 1; d <= contextLines; d++ {
				if i-d >= 0 && changes[i-d].kind != ' ' {
					nearChange = true
					break
				}
				if i+d < len(changes) && changes[i+d].kind != ' ' {
					nearChange = true
					break
				}
			}
			if nearChange {
				ln := c.oldLine
				if ln == 0 {
					ln = c.newLine
				}
				output = append(output, fmt.Sprintf(" %*d %s", lineNumWidth, ln, c.text))
			}
		} else if c.kind == '+' {
			if firstChanged == 0 {
				firstChanged = c.newLine
			}
			output = append(output, fmt.Sprintf("+%*d %s", lineNumWidth, c.newLine, c.text))
		} else {
			if firstChanged == 0 {
				firstChanged = c.oldLine
			}
			output = append(output, fmt.Sprintf("-%*d %s", lineNumWidth, c.oldLine, c.text))
		}
	}

	return strings.Join(output, "\n"), firstChanged
}

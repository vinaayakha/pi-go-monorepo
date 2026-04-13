// Package tools provides the pi coding-agent tool set ported to Go.
//
// Tools: read, write, edit, bash, grep, find, ls
//
// Each tool mirrors the behavior of its TypeScript counterpart in
// packages/coding-agent/src/core/tools/.
package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vinaayakha/pi-go/agent"
	"github.com/vinaayakha/pi-go/ai"
)

func textResult(text string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []ai.ContentBlock{{Text: &ai.TextContent{Type: "text", Text: text}}},
	}
}

// ═══════════════════════════════════════════════════════════════════
// READ
// ═══════════════════════════════════════════════════════════════════

// ReadTool reads file contents with optional offset/limit (1-indexed lines).
// Supports truncation to DefaultMaxLines / DefaultMaxBytes.
func ReadTool(cwd string) agent.AgentTool {
	return agent.AgentTool{
		Tool: ai.Tool{
			Name: "read",
			Description: fmt.Sprintf(
				"Read the contents of a file. Supports text files and images (jpg, png, gif, webp). "+
					"Images are sent as attachments. For text files, output is truncated to %d lines or %dKB "+
					"(whichever is hit first). Use offset/limit for large files. When you need the full file, "+
					"continue with offset until complete.", DefaultMaxLines, DefaultMaxBytes/1024),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":   map[string]any{"type": "string", "description": "Path to the file to read (relative or absolute)"},
					"offset": map[string]any{"type": "number", "description": "Line number to start reading from (1-indexed)"},
					"limit":  map[string]any{"type": "number", "description": "Maximum number of lines to read"},
				},
				"required": []string{"path"},
			},
		},
		Label: "read",
		Execute: func(ctx context.Context, _ string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
			path, _ := params["path"].(string)
			if path == "" {
				return agent.AgentToolResult{}, fmt.Errorf("path is required")
			}
			absPath := resolveToCwd(path, cwd)

			data, err := os.ReadFile(absPath)
			if err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("failed to read file: %w", err)
			}

			content := string(data)
			allLines := strings.Split(content, "\n")
			totalFileLines := len(allLines)

			// Parse offset (1-indexed)
			startLine := 0
			if off, ok := params["offset"].(float64); ok && off > 0 {
				startLine = int(off) - 1
			}
			if startLine >= len(allLines) {
				return agent.AgentToolResult{}, fmt.Errorf("offset %d is beyond end of file (%d lines total)", startLine+1, totalFileLines)
			}

			// Apply limit
			var selectedContent string
			var userLimitedLines int
			hasUserLimit := false
			if lim, ok := params["limit"].(float64); ok && lim > 0 {
				endLine := startLine + int(lim)
				if endLine > len(allLines) {
					endLine = len(allLines)
				}
				selectedContent = strings.Join(allLines[startLine:endLine], "\n")
				userLimitedLines = endLine - startLine
				hasUserLimit = true
			} else {
				selectedContent = strings.Join(allLines[startLine:], "\n")
			}

			trunc := truncateHead(selectedContent, DefaultMaxLines, DefaultMaxBytes)
			startLineDisplay := startLine + 1

			var outputText string
			if trunc.FirstLineExceedsLimit {
				firstLineSize := formatSize(len(allLines[startLine]))
				outputText = fmt.Sprintf("[Line %d is %s, exceeds %s limit. Use bash: sed -n '%dp' %s | head -c %d]",
					startLineDisplay, firstLineSize, formatSize(DefaultMaxBytes), startLineDisplay, path, DefaultMaxBytes)
			} else if trunc.Truncated {
				endLineDisplay := startLineDisplay + trunc.OutputLines - 1
				nextOffset := endLineDisplay + 1
				outputText = trunc.Content
				if trunc.TruncatedBy == "lines" {
					outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use offset=%d to continue.]",
						startLineDisplay, endLineDisplay, totalFileLines, nextOffset)
				} else {
					outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Use offset=%d to continue.]",
						startLineDisplay, endLineDisplay, totalFileLines, formatSize(DefaultMaxBytes), nextOffset)
				}
			} else if hasUserLimit && startLine+userLimitedLines < len(allLines) {
				remaining := len(allLines) - (startLine + userLimitedLines)
				nextOffset := startLine + userLimitedLines + 1
				outputText = fmt.Sprintf("%s\n\n[%d more lines in file. Use offset=%d to continue.]",
					trunc.Content, remaining, nextOffset)
			} else {
				outputText = trunc.Content
			}

			return textResult(outputText), nil
		},
	}
}

// ═══════════════════════════════════════════════════════════════════
// WRITE
// ═══════════════════════════════════════════════════════════════════

// WriteTool writes content to a file, creating parent dirs if needed.
func WriteTool(cwd string) agent.AgentTool {
	return agent.AgentTool{
		Tool: ai.Tool{
			Name:        "write",
			Description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "Path to the file to write (relative or absolute)"},
					"content": map[string]any{"type": "string", "description": "Content to write to the file"},
				},
				"required": []string{"path", "content"},
			},
		},
		Label: "write",
		Execute: func(ctx context.Context, _ string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
			path, _ := params["path"].(string)
			content, _ := params["content"].(string)
			if path == "" {
				return agent.AgentToolResult{}, fmt.Errorf("path is required")
			}
			absPath := resolveToCwd(path, cwd)
			dir := filepath.Dir(absPath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("failed to create directory: %w", err)
			}
			if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("failed to write file: %w", err)
			}
			return textResult(fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path)), nil
		},
	}
}

// ═══════════════════════════════════════════════════════════════════
// EDIT
// ═══════════════════════════════════════════════════════════════════

// EditTool edits a file using exact text replacement.
// Supports multiple disjoint edits in one call. Uses fuzzy matching
// as fallback (trailing whitespace, smart quotes, Unicode dashes).
func EditTool(cwd string) agent.AgentTool {
	return agent.AgentTool{
		Tool: ai.Tool{
			Name: "edit",
			Description: "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, " +
				"non-overlapping region of the original file. If two changes affect the same block or nearby lines, " +
				"merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions " +
				"just to connect distant changes.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "Path to the file to edit (relative or absolute)"},
					"edits": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"oldText": map[string]any{"type": "string", "description": "Exact text to find"},
								"newText": map[string]any{"type": "string", "description": "Replacement text"},
							},
							"required": []string{"oldText", "newText"},
						},
						"description": "One or more targeted replacements matched against the original file.",
					},
				},
				"required": []string{"path", "edits"},
			},
		},
		Label: "edit",
		Execute: func(ctx context.Context, _ string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
			path, _ := params["path"].(string)
			if path == "" {
				return agent.AgentToolResult{}, fmt.Errorf("path is required")
			}
			absPath := resolveToCwd(path, cwd)

			data, err := os.ReadFile(absPath)
			if err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("file not found: %s", path)
			}
			rawContent := string(data)

			// Parse edits
			editsRaw, _ := params["edits"].([]any)
			if len(editsRaw) == 0 {
				// Support legacy single-edit format
				oldText, _ := params["oldText"].(string)
				newText, _ := params["newText"].(string)
				if oldText != "" {
					editsRaw = []any{map[string]any{"oldText": oldText, "newText": newText}}
				} else {
					return agent.AgentToolResult{}, fmt.Errorf("edits must contain at least one replacement")
				}
			}

			var edits []Edit
			for _, e := range editsRaw {
				m, ok := e.(map[string]any)
				if !ok {
					return agent.AgentToolResult{}, fmt.Errorf("invalid edit entry")
				}
				oldText, _ := m["oldText"].(string)
				newText, _ := m["newText"].(string)
				edits = append(edits, Edit{OldText: oldText, NewText: newText})
			}

			bom, content := stripBom(rawContent)
			originalEnding := detectLineEnding(content)
			normalizedContent := normalizeToLF(content)

			_, newContent, err := ApplyEdits(normalizedContent, edits, path)
			if err != nil {
				return agent.AgentToolResult{}, err
			}

			finalContent := bom + restoreLineEndings(newContent, originalEnding)
			if err := os.WriteFile(absPath, []byte(finalContent), 0644); err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("failed to write file: %w", err)
			}

			return textResult(fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(edits), path)), nil
		},
	}
}

// ═══════════════════════════════════════════════════════════════════
// BASH
// ═══════════════════════════════════════════════════════════════════

// BashTool executes a bash command and returns stdout/stderr.
// Output is truncated to the last DefaultMaxLines / DefaultMaxBytes.
func BashTool(cwd string) agent.AgentTool {
	return agent.AgentTool{
		Tool: ai.Tool{
			Name: "bash",
			Description: fmt.Sprintf(
				"Execute a bash command in the current working directory. Returns stdout and stderr. "+
					"Output is truncated to last %d lines or %dKB (whichever is hit first). "+
					"If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.",
				DefaultMaxLines, DefaultMaxBytes/1024),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string", "description": "Bash command to execute"},
					"timeout": map[string]any{"type": "number", "description": "Timeout in seconds (optional, no default timeout)"},
				},
				"required": []string{"command"},
			},
		},
		Label: "bash",
		Execute: func(ctx context.Context, _ string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
			command, _ := params["command"].(string)
			if command == "" {
				return agent.AgentToolResult{}, fmt.Errorf("command is required")
			}

			// Handle timeout
			execCtx := ctx
			if timeout, ok := params["timeout"].(float64); ok && timeout > 0 {
				var cancel context.CancelFunc
				execCtx, cancel = context.WithTimeout(ctx, time.Duration(timeout*float64(time.Second)))
				defer cancel()
			}

			cmd := exec.CommandContext(execCtx, "bash", "-c", command)
			cmd.Dir = cwd
			output, err := cmd.CombinedOutput()
			fullOutput := string(output)

			trunc := truncateTail(fullOutput, DefaultMaxLines, DefaultMaxBytes)
			outputText := trunc.Content
			if outputText == "" {
				outputText = "(no output)"
			}

			if trunc.Truncated {
				startLine := trunc.TotalLines - trunc.OutputLines + 1
				endLine := trunc.TotalLines
				if trunc.TruncatedBy == "lines" {
					outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d.]", startLine, endLine, trunc.TotalLines)
				} else {
					outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit).]",
						startLine, endLine, trunc.TotalLines, formatSize(DefaultMaxBytes))
				}
			}

			if err != nil {
				exitCode := -1
				if cmd.ProcessState != nil {
					exitCode = cmd.ProcessState.ExitCode()
				}
				if execCtx.Err() == context.DeadlineExceeded {
					outputText += fmt.Sprintf("\n\nCommand timed out after %.0f seconds", params["timeout"].(float64))
				} else if exitCode != 0 {
					outputText += fmt.Sprintf("\n\nCommand exited with code %d", exitCode)
				}
				return agent.AgentToolResult{}, fmt.Errorf("%s", outputText)
			}

			return textResult(outputText), nil
		},
	}
}

// ═══════════════════════════════════════════════════════════════════
// GREP
// ═══════════════════════════════════════════════════════════════════

// GrepTool searches file contents using ripgrep (rg). Falls back to
// grep if rg is not available.
func GrepTool(cwd string) agent.AgentTool {
	return agent.AgentTool{
		Tool: ai.Tool{
			Name: "grep",
			Description: fmt.Sprintf(
				"Search file contents for a pattern. Returns matching lines with file paths and line numbers. "+
					"Respects .gitignore. Output is truncated to 100 matches or %dKB (whichever is hit first). "+
					"Long lines are truncated to %d chars.", DefaultMaxBytes/1024, GrepMaxLineLength),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern":    map[string]any{"type": "string", "description": "Search pattern (regex or literal string)"},
					"path":       map[string]any{"type": "string", "description": "Directory or file to search (default: current directory)"},
					"glob":       map[string]any{"type": "string", "description": "Filter files by glob pattern, e.g. '*.ts'"},
					"ignoreCase": map[string]any{"type": "boolean", "description": "Case-insensitive search (default: false)"},
					"literal":    map[string]any{"type": "boolean", "description": "Treat pattern as literal string (default: false)"},
					"limit":      map[string]any{"type": "number", "description": "Maximum number of matches (default: 100)"},
				},
				"required": []string{"pattern"},
			},
		},
		Label: "grep",
		Execute: func(ctx context.Context, _ string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
			pattern, _ := params["pattern"].(string)
			if pattern == "" {
				return agent.AgentToolResult{}, fmt.Errorf("pattern is required")
			}
			searchDir, _ := params["path"].(string)
			if searchDir == "" {
				searchDir = "."
			}
			searchPath := resolveToCwd(searchDir, cwd)
			glob, _ := params["glob"].(string)
			ignoreCase, _ := params["ignoreCase"].(bool)
			literal, _ := params["literal"].(bool)
			limit := 100
			if lim, ok := params["limit"].(float64); ok && lim > 0 {
				limit = int(lim)
			}

			// Try ripgrep first, fall back to grep
			rgPath, err := exec.LookPath("rg")
			if err != nil {
				// Fallback to grep
				return grepFallback(ctx, pattern, searchPath, ignoreCase, literal, limit)
			}

			args := []string{"--line-number", "--color=never", "--hidden", "--max-count", fmt.Sprintf("%d", limit)}
			if ignoreCase {
				args = append(args, "--ignore-case")
			}
			if literal {
				args = append(args, "--fixed-strings")
			}
			if glob != "" {
				args = append(args, "--glob", glob)
			}
			args = append(args, pattern, searchPath)

			cmd := exec.CommandContext(ctx, rgPath, args...)
			output, _ := cmd.CombinedOutput()
			text := strings.TrimSpace(string(output))
			if text == "" {
				return textResult("No matches found"), nil
			}

			// Truncate long lines
			lines := strings.Split(text, "\n")
			linesTruncated := false
			for i, line := range lines {
				truncated, wasTruncated := truncateLine(line, GrepMaxLineLength)
				if wasTruncated {
					lines[i] = truncated
					linesTruncated = true
				}
			}
			text = strings.Join(lines, "\n")

			trunc := truncateHead(text, 0, DefaultMaxBytes)
			result := trunc.Content
			var notices []string
			if len(lines) >= limit {
				notices = append(notices, fmt.Sprintf("%d matches limit reached", limit))
			}
			if trunc.Truncated {
				notices = append(notices, fmt.Sprintf("%s limit reached", formatSize(DefaultMaxBytes)))
			}
			if linesTruncated {
				notices = append(notices, fmt.Sprintf("Some lines truncated to %d chars", GrepMaxLineLength))
			}
			if len(notices) > 0 {
				result += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
			}

			return textResult(result), nil
		},
	}
}

func grepFallback(ctx context.Context, pattern, searchPath string, ignoreCase, literal bool, limit int) (agent.AgentToolResult, error) {
	args := []string{"-rn", "--color=never"}
	if ignoreCase {
		args = append(args, "-i")
	}
	if literal {
		args = append(args, "-F")
	}
	args = append(args, "-m", fmt.Sprintf("%d", limit), pattern, searchPath)
	cmd := exec.CommandContext(ctx, "grep", args...)
	output, _ := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if text == "" {
		return textResult("No matches found"), nil
	}
	return textResult(text), nil
}

// ═══════════════════════════════════════════════════════════════════
// FIND
// ═══════════════════════════════════════════════════════════════════

// FindTool searches for files by glob pattern.
func FindTool(cwd string) agent.AgentTool {
	return agent.AgentTool{
		Tool: ai.Tool{
			Name: "find",
			Description: fmt.Sprintf(
				"Search for files by glob pattern. Returns matching file paths relative to the search directory. "+
					"Respects .gitignore. Output is truncated to 1000 results or %dKB (whichever is hit first).",
				DefaultMaxBytes/1024),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{"type": "string", "description": "Glob pattern to match files, e.g. '*.ts', '**/*.json'"},
					"path":    map[string]any{"type": "string", "description": "Directory to search in (default: current directory)"},
					"limit":   map[string]any{"type": "number", "description": "Maximum number of results (default: 1000)"},
				},
				"required": []string{"pattern"},
			},
		},
		Label: "find",
		Execute: func(ctx context.Context, _ string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
			pattern, _ := params["pattern"].(string)
			if pattern == "" {
				return agent.AgentToolResult{}, fmt.Errorf("pattern is required")
			}
			searchDir, _ := params["path"].(string)
			if searchDir == "" {
				searchDir = "."
			}
			searchPath := resolveToCwd(searchDir, cwd)
			limit := 1000
			if lim, ok := params["limit"].(float64); ok && lim > 0 {
				limit = int(lim)
			}

			// Try fd first, fall back to find
			fdPath, err := exec.LookPath("fd")
			if err != nil {
				return findFallback(ctx, pattern, searchPath, limit)
			}

			args := []string{"--glob", "--color=never", "--hidden", "--max-results", fmt.Sprintf("%d", limit), pattern, searchPath}
			cmd := exec.CommandContext(ctx, fdPath, args...)
			output, _ := cmd.CombinedOutput()
			text := strings.TrimSpace(string(output))
			if text == "" {
				return textResult("No files found matching pattern"), nil
			}

			// Relativize paths
			lines := strings.Split(text, "\n")
			var relativized []string
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				rel, err := filepath.Rel(searchPath, line)
				if err == nil {
					relativized = append(relativized, filepath.ToSlash(rel))
				} else {
					relativized = append(relativized, filepath.ToSlash(line))
				}
			}

			rawOutput := strings.Join(relativized, "\n")
			trunc := truncateHead(rawOutput, 0, DefaultMaxBytes)
			result := trunc.Content
			var notices []string
			if len(relativized) >= limit {
				notices = append(notices, fmt.Sprintf("%d results limit reached", limit))
			}
			if trunc.Truncated {
				notices = append(notices, fmt.Sprintf("%s limit reached", formatSize(DefaultMaxBytes)))
			}
			if len(notices) > 0 {
				result += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
			}
			return textResult(result), nil
		},
	}
}

func findFallback(ctx context.Context, pattern, searchPath string, limit int) (agent.AgentToolResult, error) {
	cmd := exec.CommandContext(ctx, "find", searchPath, "-name", pattern, "-not", "-path", "*/.git/*", "-not", "-path", "*/node_modules/*")
	output, _ := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if text == "" {
		return textResult("No files found matching pattern"), nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > limit {
		lines = lines[:limit]
	}
	return textResult(strings.Join(lines, "\n")), nil
}

// ═══════════════════════════════════════════════════════════════════
// LS
// ═══════════════════════════════════════════════════════════════════

// LsTool lists directory contents, sorted alphabetically, with / suffix for dirs.
func LsTool(cwd string) agent.AgentTool {
	return agent.AgentTool{
		Tool: ai.Tool{
			Name: "ls",
			Description: fmt.Sprintf(
				"List directory contents. Returns entries sorted alphabetically, with '/' suffix for directories. "+
					"Includes dotfiles. Output is truncated to 500 entries or %dKB (whichever is hit first).",
				DefaultMaxBytes/1024),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":  map[string]any{"type": "string", "description": "Directory to list (default: current directory)"},
					"limit": map[string]any{"type": "number", "description": "Maximum number of entries (default: 500)"},
				},
			},
		},
		Label: "ls",
		Execute: func(ctx context.Context, _ string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
			dir, _ := params["path"].(string)
			if dir == "" {
				dir = "."
			}
			dirPath := resolveToCwd(dir, cwd)
			limit := 500
			if lim, ok := params["limit"].(float64); ok && lim > 0 {
				limit = int(lim)
			}

			info, err := os.Stat(dirPath)
			if err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("path not found: %s", dirPath)
			}
			if !info.IsDir() {
				return agent.AgentToolResult{}, fmt.Errorf("not a directory: %s", dirPath)
			}

			entries, err := os.ReadDir(dirPath)
			if err != nil {
				return agent.AgentToolResult{}, fmt.Errorf("cannot read directory: %w", err)
			}

			sort.Slice(entries, func(i, j int) bool {
				return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
			})

			var results []string
			for _, entry := range entries {
				if len(results) >= limit {
					break
				}
				name := entry.Name()
				if entry.IsDir() {
					name += "/"
				}
				results = append(results, name)
			}

			if len(results) == 0 {
				return textResult("(empty directory)"), nil
			}

			rawOutput := strings.Join(results, "\n")
			trunc := truncateHead(rawOutput, 0, DefaultMaxBytes)
			output := trunc.Content
			var notices []string
			if len(results) >= limit {
				notices = append(notices, fmt.Sprintf("%d entries limit reached", limit))
			}
			if trunc.Truncated {
				notices = append(notices, fmt.Sprintf("%s limit reached", formatSize(DefaultMaxBytes)))
			}
			if len(notices) > 0 {
				output += fmt.Sprintf("\n\n[%s]", strings.Join(notices, ". "))
			}
			return textResult(output), nil
		},
	}
}

// ═══════════════════════════════════════════════════════════════════
// Tool collections
// ═══════════════════════════════════════════════════════════════════

// CodingTools returns the standard coding tool set: read, bash, edit, write.
func CodingTools(cwd string) []agent.AgentTool {
	return []agent.AgentTool{ReadTool(cwd), BashTool(cwd), EditTool(cwd), WriteTool(cwd)}
}

// ReadOnlyTools returns read-only tools: read, grep, find, ls.
func ReadOnlyTools(cwd string) []agent.AgentTool {
	return []agent.AgentTool{ReadTool(cwd), GrepTool(cwd), FindTool(cwd), LsTool(cwd)}
}

// AllTools returns all tools: read, bash, edit, write, grep, find, ls.
func AllTools(cwd string) []agent.AgentTool {
	return []agent.AgentTool{
		ReadTool(cwd), BashTool(cwd), EditTool(cwd), WriteTool(cwd),
		GrepTool(cwd), FindTool(cwd), LsTool(cwd),
	}
}

// ═══════════════════════════════════════════════════════════════════
// Config-based constructors (for remote sandboxes)
// ═══════════════════════════════════════════════════════════════════

// CodingToolsWithConfig returns coding tools using pluggable operations.
func CodingToolsWithConfig(cfg ToolsConfig) []agent.AgentTool {
	return []agent.AgentTool{
		ReadToolWithConfig(cfg), BashToolWithConfig(cfg),
		EditToolWithConfig(cfg), WriteToolWithConfig(cfg),
	}
}

// AllToolsWithConfig returns all tools using pluggable operations.
func AllToolsWithConfig(cfg ToolsConfig) []agent.AgentTool {
	return []agent.AgentTool{
		ReadToolWithConfig(cfg), BashToolWithConfig(cfg),
		EditToolWithConfig(cfg), WriteToolWithConfig(cfg),
		GrepTool(cfg.Cwd), FindTool(cfg.Cwd), LsToolWithConfig(cfg),
	}
}

// ReadToolWithConfig creates a read tool using pluggable file operations.
func ReadToolWithConfig(cfg ToolsConfig) agent.AgentTool {
	tool := ReadTool(cfg.Cwd)
	origExec := tool.Execute
	tool.Execute = func(ctx context.Context, id string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
		path, _ := params["path"].(string)
		if path == "" {
			return agent.AgentToolResult{}, fmt.Errorf("path is required")
		}
		absPath := resolveToCwd(path, cfg.Cwd)
		data, err := cfg.FileOps.ReadFile(absPath)
		if err != nil {
			return agent.AgentToolResult{}, fmt.Errorf("failed to read file: %w", err)
		}
		// Reuse original logic by writing to a temp approach — simpler: just inline
		content := string(data)
		allLines := strings.Split(content, "\n")
		totalFileLines := len(allLines)
		startLine := 0
		if off, ok := params["offset"].(float64); ok && off > 0 {
			startLine = int(off) - 1
		}
		if startLine >= len(allLines) {
			return agent.AgentToolResult{}, fmt.Errorf("offset %d is beyond end of file (%d lines total)", startLine+1, totalFileLines)
		}
		var selectedContent string
		var userLimitedLines int
		hasUserLimit := false
		if lim, ok := params["limit"].(float64); ok && lim > 0 {
			endLine := startLine + int(lim)
			if endLine > len(allLines) {
				endLine = len(allLines)
			}
			selectedContent = strings.Join(allLines[startLine:endLine], "\n")
			userLimitedLines = endLine - startLine
			hasUserLimit = true
		} else {
			selectedContent = strings.Join(allLines[startLine:], "\n")
		}
		trunc := truncateHead(selectedContent, DefaultMaxLines, DefaultMaxBytes)
		startLineDisplay := startLine + 1
		var outputText string
		if trunc.FirstLineExceedsLimit {
			outputText = fmt.Sprintf("[Line %d exceeds %s limit.]", startLineDisplay, formatSize(DefaultMaxBytes))
		} else if trunc.Truncated {
			endLineDisplay := startLineDisplay + trunc.OutputLines - 1
			nextOffset := endLineDisplay + 1
			outputText = trunc.Content + fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use offset=%d to continue.]",
				startLineDisplay, endLineDisplay, totalFileLines, nextOffset)
		} else if hasUserLimit && startLine+userLimitedLines < len(allLines) {
			remaining := len(allLines) - (startLine + userLimitedLines)
			nextOffset := startLine + userLimitedLines + 1
			outputText = fmt.Sprintf("%s\n\n[%d more lines in file. Use offset=%d to continue.]", trunc.Content, remaining, nextOffset)
		} else {
			outputText = trunc.Content
		}
		return textResult(outputText), nil
	}
	_ = origExec // suppress unused
	return tool
}

// WriteToolWithConfig creates a write tool using pluggable file operations.
func WriteToolWithConfig(cfg ToolsConfig) agent.AgentTool {
	tool := WriteTool(cfg.Cwd)
	tool.Execute = func(ctx context.Context, id string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
		path, _ := params["path"].(string)
		content, _ := params["content"].(string)
		if path == "" {
			return agent.AgentToolResult{}, fmt.Errorf("path is required")
		}
		absPath := resolveToCwd(path, cfg.Cwd)
		dir := filepath.Dir(absPath)
		if err := cfg.FileOps.MkdirAll(dir, 0755); err != nil {
			return agent.AgentToolResult{}, fmt.Errorf("failed to create directory: %w", err)
		}
		if err := cfg.FileOps.WriteFile(absPath, []byte(content), 0644); err != nil {
			return agent.AgentToolResult{}, fmt.Errorf("failed to write file: %w", err)
		}
		return textResult(fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path)), nil
	}
	return tool
}

// EditToolWithConfig creates an edit tool using pluggable file operations.
func EditToolWithConfig(cfg ToolsConfig) agent.AgentTool {
	tool := EditTool(cfg.Cwd)
	tool.Execute = func(ctx context.Context, id string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
		path, _ := params["path"].(string)
		if path == "" {
			return agent.AgentToolResult{}, fmt.Errorf("path is required")
		}
		absPath := resolveToCwd(path, cfg.Cwd)
		data, err := cfg.FileOps.ReadFile(absPath)
		if err != nil {
			return agent.AgentToolResult{}, fmt.Errorf("file not found: %s", path)
		}
		rawContent := string(data)
		editsRaw, _ := params["edits"].([]any)
		if len(editsRaw) == 0 {
			oldText, _ := params["oldText"].(string)
			newText, _ := params["newText"].(string)
			if oldText != "" {
				editsRaw = []any{map[string]any{"oldText": oldText, "newText": newText}}
			} else {
				return agent.AgentToolResult{}, fmt.Errorf("edits must contain at least one replacement")
			}
		}
		var edits []Edit
		for _, e := range editsRaw {
			m, ok := e.(map[string]any)
			if !ok {
				return agent.AgentToolResult{}, fmt.Errorf("invalid edit entry")
			}
			oldText, _ := m["oldText"].(string)
			newText, _ := m["newText"].(string)
			edits = append(edits, Edit{OldText: oldText, NewText: newText})
		}
		bom, content := stripBom(rawContent)
		originalEnding := detectLineEnding(content)
		normalizedContent := normalizeToLF(content)
		_, newContent, err := ApplyEdits(normalizedContent, edits, path)
		if err != nil {
			return agent.AgentToolResult{}, err
		}
		finalContent := bom + restoreLineEndings(newContent, originalEnding)
		if err := cfg.FileOps.WriteFile(absPath, []byte(finalContent), 0644); err != nil {
			return agent.AgentToolResult{}, fmt.Errorf("failed to write file: %w", err)
		}
		return textResult(fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(edits), path)), nil
	}
	return tool
}

// BashToolWithConfig creates a bash tool using pluggable exec operations.
func BashToolWithConfig(cfg ToolsConfig) agent.AgentTool {
	tool := BashTool(cfg.Cwd)
	tool.Execute = func(ctx context.Context, id string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
		command, _ := params["command"].(string)
		if command == "" {
			return agent.AgentToolResult{}, fmt.Errorf("command is required")
		}
		execCtx := ctx
		if timeout, ok := params["timeout"].(float64); ok && timeout > 0 {
			var cancel context.CancelFunc
			execCtx, cancel = context.WithTimeout(ctx, time.Duration(timeout*float64(time.Second)))
			defer cancel()
		}
		output, err := cfg.ExecOps.Exec(execCtx, command, cfg.Cwd)
		fullOutput := string(output)
		trunc := truncateTail(fullOutput, DefaultMaxLines, DefaultMaxBytes)
		outputText := trunc.Content
		if outputText == "" {
			outputText = "(no output)"
		}
		if trunc.Truncated {
			startLine := trunc.TotalLines - trunc.OutputLines + 1
			endLine := trunc.TotalLines
			outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d.]", startLine, endLine, trunc.TotalLines)
		}
		if err != nil {
			outputText += fmt.Sprintf("\n\nCommand failed: %v", err)
			return agent.AgentToolResult{}, fmt.Errorf("%s", outputText)
		}
		return textResult(outputText), nil
	}
	return tool
}

// LsToolWithConfig creates an ls tool using pluggable file operations.
func LsToolWithConfig(cfg ToolsConfig) agent.AgentTool {
	tool := LsTool(cfg.Cwd)
	tool.Execute = func(ctx context.Context, id string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
		dir, _ := params["path"].(string)
		if dir == "" {
			dir = "."
		}
		dirPath := resolveToCwd(dir, cfg.Cwd)
		limit := 500
		if lim, ok := params["limit"].(float64); ok && lim > 0 {
			limit = int(lim)
		}
		info, err := cfg.FileOps.Stat(dirPath)
		if err != nil {
			return agent.AgentToolResult{}, fmt.Errorf("path not found: %s", dirPath)
		}
		if !info.IsDir {
			return agent.AgentToolResult{}, fmt.Errorf("not a directory: %s", dirPath)
		}
		entries, err := cfg.FileOps.ReadDir(dirPath)
		if err != nil {
			return agent.AgentToolResult{}, fmt.Errorf("cannot read directory: %w", err)
		}
		sort.Slice(entries, func(i, j int) bool {
			return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
		})
		var results []string
		for _, entry := range entries {
			if len(results) >= limit {
				break
			}
			name := entry.Name
			if entry.IsDir {
				name += "/"
			}
			results = append(results, name)
		}
		if len(results) == 0 {
			return textResult("(empty directory)"), nil
		}
		return textResult(strings.Join(results, "\n")), nil
	}
	return tool
}



package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BuildSystemPromptOptions configures system prompt generation.
type BuildSystemPromptOptions struct {
	// CustomPrompt replaces the entire default prompt.
	CustomPrompt string
	// SelectedTools lists tool names to include. Default: ["read","bash","edit","write"]
	SelectedTools []string
	// ToolSnippets maps tool name -> one-line description.
	ToolSnippets map[string]string
	// PromptGuidelines are extra bullet points appended to guidelines.
	PromptGuidelines []string
	// AppendSystemPrompt is text appended after the main prompt body.
	AppendSystemPrompt string
	// Cwd is the working directory. Default: os.Getwd()
	Cwd string
	// ContextFiles are pre-loaded AGENTS.md / CLAUDE.md files.
	ContextFiles []ContextFile
}

// ContextFile is a project context file (like AGENTS.md).
type ContextFile struct {
	Path    string
	Content string
}

// DefaultToolSnippets returns one-line descriptions for the standard tools.
func DefaultToolSnippets() map[string]string {
	return map[string]string{
		"read":  "Read file contents",
		"bash":  "Execute bash commands (ls, grep, find, etc.)",
		"edit":  "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
		"write": "Create or overwrite files",
		"grep":  "Search file contents for patterns (respects .gitignore)",
		"find":  "Find files by glob pattern (respects .gitignore)",
		"ls":    "List directory contents",
	}
}

// LoadProjectContextFiles discovers AGENTS.md or CLAUDE.md files walking up
// from cwd to the filesystem root.
func LoadProjectContextFiles(cwd string) []ContextFile {
	var files []ContextFile
	seen := map[string]bool{}

	dir := cwd
	for {
		for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				abs, _ := filepath.Abs(p)
				if !seen[abs] {
					data, err := os.ReadFile(abs)
					if err == nil {
						files = append(files, ContextFile{Path: abs, Content: string(data)})
						seen[abs] = true
					}
				}
				break // only load first match per directory
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	// Reverse so root is first, cwd is last (matches pi's ancestor ordering)
	for i, j := 0, len(files)-1; i < j; i, j = i+1, j-1 {
		files[i], files[j] = files[j], files[i]
	}
	return files
}

// BuildSystemPrompt constructs the system prompt, mirroring
// packages/coding-agent/src/core/system-prompt.ts.
func BuildSystemPrompt(opts BuildSystemPromptOptions) string {
	cwd := opts.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	promptCwd := strings.ReplaceAll(cwd, "\\", "/")
	date := time.Now().Format("2006-01-02")

	appendSection := ""
	if opts.AppendSystemPrompt != "" {
		appendSection = "\n\n" + opts.AppendSystemPrompt
	}

	contextFiles := opts.ContextFiles

	// Custom prompt path
	if opts.CustomPrompt != "" {
		prompt := opts.CustomPrompt + appendSection
		if len(contextFiles) > 0 {
			prompt += "\n\n# Project Context\n\nProject-specific instructions and guidelines:\n\n"
			for _, cf := range contextFiles {
				prompt += fmt.Sprintf("## %s\n\n%s\n\n", cf.Path, cf.Content)
			}
		}
		prompt += fmt.Sprintf("\nCurrent date: %s", date)
		prompt += fmt.Sprintf("\nCurrent working directory: %s", promptCwd)
		return prompt
	}

	// Default tool set
	tools := opts.SelectedTools
	if len(tools) == 0 {
		tools = []string{"read", "bash", "edit", "write"}
	}

	snippets := opts.ToolSnippets
	if snippets == nil {
		snippets = DefaultToolSnippets()
	}

	// Build tools list
	var toolLines []string
	for _, name := range tools {
		if desc, ok := snippets[name]; ok {
			toolLines = append(toolLines, fmt.Sprintf("- %s: %s", name, desc))
		}
	}
	toolsList := "(none)"
	if len(toolLines) > 0 {
		toolsList = strings.Join(toolLines, "\n")
	}

	// Build guidelines
	guidelinesSet := map[string]bool{}
	var guidelinesList []string
	addGuideline := func(g string) {
		g = strings.TrimSpace(g)
		if g == "" || guidelinesSet[g] {
			return
		}
		guidelinesSet[g] = true
		guidelinesList = append(guidelinesList, g)
	}

	hasBash := contains(tools, "bash")
	hasGrep := contains(tools, "grep")
	hasFind := contains(tools, "find")
	hasLs := contains(tools, "ls")

	if hasBash && !hasGrep && !hasFind && !hasLs {
		addGuideline("Use bash for file operations like ls, rg, find")
	} else if hasBash && (hasGrep || hasFind || hasLs) {
		addGuideline("Prefer grep/find/ls tools over bash for file exploration (faster, respects .gitignore)")
	}

	for _, g := range opts.PromptGuidelines {
		addGuideline(g)
	}

	addGuideline("Be concise in your responses")
	addGuideline("Show file paths clearly when working with files")

	guidelines := ""
	for _, g := range guidelinesList {
		guidelines += fmt.Sprintf("- %s\n", g)
	}

	prompt := fmt.Sprintf(`You are an expert coding assistant. You help users by reading files, executing commands, editing code, and writing new files.

Available tools:
%s

In addition to the tools above, you may have access to other custom tools depending on the project.

Guidelines:
%s`, toolsList, guidelines)

	prompt += appendSection

	// Append project context files
	if len(contextFiles) > 0 {
		prompt += "\n\n# Project Context\n\nProject-specific instructions and guidelines:\n\n"
		for _, cf := range contextFiles {
			prompt += fmt.Sprintf("## %s\n\n%s\n\n", cf.Path, cf.Content)
		}
	}

	prompt += fmt.Sprintf("\nCurrent date: %s", date)
	prompt += fmt.Sprintf("\nCurrent working directory: %s", promptCwd)

	return prompt
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

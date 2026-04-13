package tools

import (
	"context"
	"os"
	"os/exec"
)

// ── Pluggable operation interfaces ──────────────────────────────────
// These allow tools to work against remote sandboxes, SSH, containers, etc.

// FileInfo is a minimal stat result.
type FileInfo struct {
	Name  string
	IsDir bool
	Size  int64
}

// DirEntry is a minimal directory entry.
type DirEntry struct {
	Name  string
	IsDir bool
}

// FileOps abstracts filesystem operations.
type FileOps interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, content []byte, perm os.FileMode) error
	MkdirAll(path string, perm os.FileMode) error
	Stat(path string) (FileInfo, error)
	ReadDir(path string) ([]DirEntry, error)
	Exists(path string) bool
	Access(path string) error
}

// ExecOps abstracts command execution.
type ExecOps interface {
	Exec(ctx context.Context, command, cwd string) (output []byte, err error)
}

// ToolsConfig bundles all operations needed by tools.
type ToolsConfig struct {
	Cwd     string
	FileOps FileOps
	ExecOps ExecOps
}

// DefaultToolsConfig creates a config using local filesystem and os/exec.
func DefaultToolsConfig(cwd string) ToolsConfig {
	return ToolsConfig{
		Cwd:     cwd,
		FileOps: &LocalFileOps{},
		ExecOps: &LocalExecOps{},
	}
}

// ── Local implementations ───────────────────────────────────────────

// LocalFileOps implements FileOps using the local filesystem.
type LocalFileOps struct{}

func (o *LocalFileOps) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (o *LocalFileOps) WriteFile(path string, content []byte, perm os.FileMode) error {
	return os.WriteFile(path, content, perm)
}

func (o *LocalFileOps) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (o *LocalFileOps) Stat(path string) (FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Name: info.Name(), IsDir: info.IsDir(), Size: info.Size()}, nil
}

func (o *LocalFileOps) ReadDir(path string) ([]DirEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	result := make([]DirEntry, len(entries))
	for i, e := range entries {
		result[i] = DirEntry{Name: e.Name(), IsDir: e.IsDir()}
	}
	return result, nil
}

func (o *LocalFileOps) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (o *LocalFileOps) Access(path string) error {
	_, err := os.Stat(path)
	return err
}

// LocalExecOps implements ExecOps using os/exec.
type LocalExecOps struct{}

func (o *LocalExecOps) Exec(ctx context.Context, command, cwd string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = cwd
	return cmd.CombinedOutput()
}

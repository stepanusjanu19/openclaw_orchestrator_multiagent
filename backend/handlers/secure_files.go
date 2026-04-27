package handlers

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	secureDirPerm        os.FileMode = 0700
	secureFilePerm       os.FileMode = 0600
	maxScaffoldFileBytes             = 512 * 1024
)

func mkdirSecure(path string) error {
	if err := os.MkdirAll(path, secureDirPerm); err != nil {
		return err
	}
	return os.Chmod(path, secureDirPerm)
}

func writeFileNoSymlink(path string, data []byte) error {
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write symlink: %s", filepath.Base(path))
		}
		if st.IsDir() {
			return fmt.Errorf("refusing to write directory: %s", filepath.Base(path))
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := mkdirSecure(filepath.Dir(path)); err != nil {
		return err
	}
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_TRUNC|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, uint32(secureFilePerm))
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), path)
	if f == nil {
		syscall.Close(fd)
		return fmt.Errorf("failed to open %s", filepath.Base(path))
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Chmod(secureFilePerm)
}

func copyRegularFileNoClobber(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	st, err := os.Lstat(src)
	if err != nil {
		return nil
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
		return nil
	}
	if st.Size() > maxScaffoldFileBytes {
		return nil
	}
	f, err := os.Open(src)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxScaffoldFileBytes+1))
	if err != nil || len(data) > maxScaffoldFileBytes {
		return nil
	}
	return writeFileNoSymlink(dst, data)
}

func readRegularFileNoSymlink(path string, maxBytes int64) ([]byte, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("refusing to read non-regular file: %s", filepath.Base(path))
	}
	if st.Size() > maxBytes {
		return nil, fmt.Errorf("file too large: %s", filepath.Base(path))
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("file too large: %s", filepath.Base(path))
	}
	return data, nil
}

func isManagedAgentWorkspace(ocWorkspaceDir, workspaceDir, agentID string) bool {
	if agentID == "" || agentID == "main" || filepath.Base(workspaceDir) == "workspace" {
		return false
	}
	baseAbs, err := filepath.Abs(ocWorkspaceDir)
	if err != nil {
		return false
	}
	workspaceAbs, err := filepath.Abs(workspaceDir)
	if err != nil {
		return false
	}
	if filepath.Base(workspaceAbs) != "workspace-"+agentID {
		return false
	}
	rel, err := filepath.Rel(baseAbs, workspaceAbs)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

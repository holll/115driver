package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mkdirCall struct{ parentID, name string }

type uploadCall struct {
	dirID, fileName string
	size            int64
}

type fakeFolderUploadClient struct {
	nextID      int
	mkdirCalls  []mkdirCall
	uploadCalls []uploadCall
	failUploads map[string]bool
	failRoot    bool
}

func (f *fakeFolderUploadClient) Mkdir(parentID, name string) (string, error) {
	if f.failRoot {
		return "", errors.New("injected root mkdir failure")
	}
	f.nextID++
	id := fmt.Sprintf("d%d", f.nextID)
	f.mkdirCalls = append(f.mkdirCalls, mkdirCall{parentID, name})
	return id, nil
}

func (f *fakeFolderUploadClient) RapidUploadOrByMultipart(dirID, fileName string, fileSize int64, r *os.File, opts ...driver.UploadMultipartOption) error {
	f.uploadCalls = append(f.uploadCalls, uploadCall{dirID, fileName, fileSize})
	if f.failUploads[fileName] {
		return errors.New("injected upload failure")
	}
	return nil
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func resultText(result *mcp.CallToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	if tc, ok := result.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestUploadFolderRecursiveCreatesFoldersAndUploadsFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a.txt"), "a")
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(filepath.Join(sub, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(sub, "b.txt"), "b")

	fake := &fakeFolderUploadClient{}
	ft := &FileTools{}
	stats := &folderUploadStats{}
	ft.uploadFolderRecursive(context.Background(), root, "0", fake, stats)

	if len(fake.mkdirCalls) != 2 {
		t.Fatalf("expected 2 mkdir calls, got %d: %+v", len(fake.mkdirCalls), fake.mkdirCalls)
	}
	if fake.mkdirCalls[0] != (mkdirCall{"0", "sub"}) {
		t.Fatalf("unexpected first mkdir: %+v", fake.mkdirCalls[0])
	}
	if fake.mkdirCalls[1] != (mkdirCall{"d1", "empty"}) {
		t.Fatalf("unexpected second mkdir: %+v", fake.mkdirCalls[1])
	}

	if len(fake.uploadCalls) != 2 {
		t.Fatalf("expected 2 upload calls, got %d: %+v", len(fake.uploadCalls), fake.uploadCalls)
	}
	if fake.uploadCalls[0].dirID != "0" || fake.uploadCalls[0].fileName != "a.txt" {
		t.Fatalf("unexpected first upload: %+v", fake.uploadCalls[0])
	}
	if fake.uploadCalls[1].dirID != "d1" || fake.uploadCalls[1].fileName != "b.txt" {
		t.Fatalf("unexpected second upload: %+v", fake.uploadCalls[1])
	}

	if stats.FoldersCreated != 2 || stats.FilesUploaded != 2 {
		t.Fatalf("unexpected stats: folders=%d files=%d", stats.FoldersCreated, stats.FilesUploaded)
	}
	if len(stats.Errors) != 0 {
		t.Fatalf("expected no errors, got %v", stats.Errors)
	}
}

func TestUploadFolderRecursiveContinuesAfterFileError(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "ok.txt"), "ok")
	writeTestFile(t, filepath.Join(root, "bad.txt"), "bad")

	fake := &fakeFolderUploadClient{failUploads: map[string]bool{"bad.txt": true}}
	ft := &FileTools{}
	stats := &folderUploadStats{}
	ft.uploadFolderRecursive(context.Background(), root, "0", fake, stats)

	if len(fake.uploadCalls) != 2 {
		t.Fatalf("expected both files attempted, got %d", len(fake.uploadCalls))
	}
	if stats.FilesUploaded != 1 {
		t.Fatalf("expected 1 successful upload, got %d", stats.FilesUploaded)
	}
	if len(stats.Errors) != 1 {
		t.Fatalf("expected 1 error, got %v", stats.Errors)
	}
}

func TestUploadFolderRecursiveSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "real.txt"), "real")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeTestFile(t, outside, "outside")
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlink not supported on this platform: %v", err)
	}

	fake := &fakeFolderUploadClient{}
	ft := &FileTools{}
	stats := &folderUploadStats{}
	ft.uploadFolderRecursive(context.Background(), root, "0", fake, stats)

	if len(fake.uploadCalls) != 1 || fake.uploadCalls[0].fileName != "real.txt" {
		t.Fatalf("expected symlink to be skipped, got %+v", fake.uploadCalls)
	}
}

func TestUploadFolderFromLocalCreatesRemoteRootFolder(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "docs")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(folder, "note.txt"), "content")

	fake := &fakeFolderUploadClient{}
	ft := &FileTools{}
	args := UploadFromLocalArgs{DirID: "10", LocalPath: folder}
	result := ft.uploadFolderFromLocal(context.Background(), args, folder, fake)

	if result.IsError {
		t.Fatalf("expected success, got error result: %s", resultText(result))
	}
	if len(fake.mkdirCalls) != 1 || fake.mkdirCalls[0] != (mkdirCall{"10", "docs"}) {
		t.Fatalf("expected root mkdir for 'docs' under '10', got %+v", fake.mkdirCalls)
	}
	if len(fake.uploadCalls) != 1 || fake.uploadCalls[0].dirID != "d1" || fake.uploadCalls[0].fileName != "note.txt" {
		t.Fatalf("expected note.txt under new folder d1, got %+v", fake.uploadCalls)
	}
}

func TestUploadFolderFromLocalUsesFileNameOverride(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "docs")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(folder, "a.txt"), "a")

	fake := &fakeFolderUploadClient{}
	ft := &FileTools{}
	args := UploadFromLocalArgs{DirID: "0", LocalPath: folder, FileName: "renamed"}
	result := ft.uploadFolderFromLocal(context.Background(), args, folder, fake)

	if result.IsError {
		t.Fatalf("expected success, got error result: %s", resultText(result))
	}
	if len(fake.mkdirCalls) != 1 || fake.mkdirCalls[0].name != "renamed" {
		t.Fatalf("expected mkdir named 'renamed', got %+v", fake.mkdirCalls)
	}
}

func TestUploadFolderFromLocalRootMkdirFailureReturnsError(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "docs")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(folder, "a.txt"), "a")

	fake := &fakeFolderUploadClient{failRoot: true}
	ft := &FileTools{}
	args := UploadFromLocalArgs{DirID: "0", LocalPath: folder}
	result := ft.uploadFolderFromLocal(context.Background(), args, folder, fake)

	if !result.IsError {
		t.Fatal("expected error result on root mkdir failure")
	}
	if len(fake.mkdirCalls) != 0 || len(fake.uploadCalls) != 0 {
		t.Fatalf("expected no mkdir/upload after root failure, got mkdir=%d upload=%d", len(fake.mkdirCalls), len(fake.uploadCalls))
	}
}
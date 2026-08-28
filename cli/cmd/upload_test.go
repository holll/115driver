package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/SheltonZhu/115driver/pkg/driver"
)

type cliMkdirCall struct{ parentID, name string }

type cliUploadCall struct {
	dirID, fileName string
	size            int64
}

type fakeCLIUploadClient struct {
	nextID      int
	mkdirCalls  []cliMkdirCall
	uploadCalls []cliUploadCall
	failUploads map[string]bool
	failRoot    bool
}

func (f *fakeCLIUploadClient) Mkdir(parentID, name string) (string, error) {
	if f.failRoot {
		return "", errors.New("injected root mkdir failure")
	}
	f.nextID++
	id := fmt.Sprintf("d%d", f.nextID)
	f.mkdirCalls = append(f.mkdirCalls, cliMkdirCall{parentID, name})
	return id, nil
}

func (f *fakeCLIUploadClient) RapidUploadOrByMultipart(dirID, fileName string, fileSize int64, r *os.File, opts ...driver.UploadMultipartOption) error {
	f.uploadCalls = append(f.uploadCalls, cliUploadCall{dirID, fileName, fileSize})
	if f.failUploads[fileName] {
		return errors.New("injected upload failure")
	}
	return nil
}

func writeCLITestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCLIUploadFolderCreatesRemoteRootAndWalksTree(t *testing.T) {
	root := t.TempDir()
	writeCLITestFile(t, filepath.Join(root, "a.txt"), "a")
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(filepath.Join(sub, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(sub, "b.txt"), "b")

	fake := &fakeCLIUploadClient{}
	summary, err := uploadFolder(root, "0", fake)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(fake.mkdirCalls) != 3 {
		t.Fatalf("expected 3 mkdir calls (root+sub+empty), got %d: %+v", len(fake.mkdirCalls), fake.mkdirCalls)
	}
	if fake.mkdirCalls[0].parentID != "0" || fake.mkdirCalls[0].name != filepath.Base(root) {
		t.Fatalf("unexpected root mkdir: %+v", fake.mkdirCalls[0])
	}
	if fake.mkdirCalls[1] != (cliMkdirCall{"d1", "sub"}) {
		t.Fatalf("unexpected second mkdir: %+v", fake.mkdirCalls[1])
	}
	if fake.mkdirCalls[2] != (cliMkdirCall{"d2", "empty"}) {
		t.Fatalf("unexpected third mkdir: %+v", fake.mkdirCalls[2])
	}

	if len(fake.uploadCalls) != 2 {
		t.Fatalf("expected 2 upload calls, got %d: %+v", len(fake.uploadCalls), fake.uploadCalls)
	}
	if fake.uploadCalls[0].dirID != "d1" || fake.uploadCalls[0].fileName != "a.txt" {
		t.Fatalf("unexpected first upload: %+v", fake.uploadCalls[0])
	}
	if fake.uploadCalls[1].dirID != "d2" || fake.uploadCalls[1].fileName != "b.txt" {
		t.Fatalf("unexpected second upload: %+v", fake.uploadCalls[1])
	}

	if summary.foldersCreated != 2 || summary.filesUploaded != 2 {
		t.Fatalf("unexpected summary: folders=%d files=%d", summary.foldersCreated, summary.filesUploaded)
	}
	if len(summary.errors) != 0 {
		t.Fatalf("expected no errors, got %v", summary.errors)
	}
}

func TestCLIUploadFolderContinuesAfterFileError(t *testing.T) {
	root := t.TempDir()
	writeCLITestFile(t, filepath.Join(root, "ok.txt"), "ok")
	writeCLITestFile(t, filepath.Join(root, "bad.txt"), "bad")

	fake := &fakeCLIUploadClient{failUploads: map[string]bool{"bad.txt": true}}
	summary, err := uploadFolder(root, "0", fake)
	if err != nil {
		t.Fatalf("expected no root error, got %v", err)
	}

	if len(fake.uploadCalls) != 2 {
		t.Fatalf("expected both files attempted, got %d", len(fake.uploadCalls))
	}
	if summary.filesUploaded != 1 {
		t.Fatalf("expected 1 successful upload, got %d", summary.filesUploaded)
	}
	if len(summary.errors) != 1 {
		t.Fatalf("expected 1 error, got %v", summary.errors)
	}
}

func TestCLIUploadFolderSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	writeCLITestFile(t, filepath.Join(root, "real.txt"), "real")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeCLITestFile(t, outside, "outside")
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlink not supported on this platform: %v", err)
	}

	fake := &fakeCLIUploadClient{}
	summary, err := uploadFolder(root, "0", fake)
	if err != nil {
		t.Fatalf("expected no root error, got %v", err)
	}

	if len(fake.uploadCalls) != 1 || fake.uploadCalls[0].fileName != "real.txt" {
		t.Fatalf("expected symlink to be skipped, got %+v", fake.uploadCalls)
	}
	if summary.filesUploaded != 1 {
		t.Fatalf("expected 1 uploaded file, got %d", summary.filesUploaded)
	}
}

func TestCLIUploadFolderRootMkdirFailureReturnsError(t *testing.T) {
	root := t.TempDir()
	writeCLITestFile(t, filepath.Join(root, "a.txt"), "a")

	fake := &fakeCLIUploadClient{failRoot: true}
	if _, err := uploadFolder(root, "0", fake); err == nil {
		t.Fatal("expected error from failed root mkdir")
	}
	if len(fake.uploadCalls) != 0 {
		t.Fatalf("expected no uploads after root mkdir failure, got %+v", fake.uploadCalls)
	}
}
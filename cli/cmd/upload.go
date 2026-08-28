package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/SheltonZhu/115driver/cli/internal/output"
	"github.com/SheltonZhu/115driver/cli/internal/resolver"
	"github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/cheggaaa/pb/v3"
	"github.com/spf13/cobra"
)

var uploadCmd = &cobra.Command{
	Use:   "upload <local_path>... <remote_dir>",
	Short: "Upload files or directories to a remote directory",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// The last argument is the remote directory; all preceding arguments are
		// local sources, so shell globs like `upload AV-Done/* /Emby/AV-cli`
		// upload every matched file directly into the target directory.
		localPaths, remoteDir := splitUploadArgs(args)

		dirID, err := resolver.ResolveDir(client, remoteDir)
		if err != nil {
			return &exitError{code: output.ExitNotFound, msg: fmt.Sprintf("Remote directory not found: %s", remoteDir)}
		}

		var (
			filesUploaded  int
			foldersCreated int
			filesSkipped   int
			errors         []string
		)
		for _, localPath := range localPaths {
			info, err := os.Stat(localPath)
			if err != nil {
				errors = append(errors, fmt.Sprintf("cannot access %s: %v", localPath, err))
				continue
			}

			if info.IsDir() {
				summary, uerr := uploadFolder(localPath, dirID, client)
				if uerr != nil {
					errors = append(errors, uerr.Error())
					continue
				}
				foldersCreated += summary.foldersCreated
				filesUploaded += summary.filesUploaded
				filesSkipped += summary.skipped
				errors = append(errors, summary.errors...)
				if !jsonOutput {
					fmt.Printf("Folder upload complete: %s -> %s (%d folders, %d files)\n", filepath.Base(localPath), remoteDir, summary.foldersCreated, summary.filesUploaded)
				}
				continue
			}

			ok, err := uploadSingleFile(client, localPath, info.Size(), dirID)
			if err != nil {
				errors = append(errors, fmt.Sprintf("upload %s: %v", localPath, err))
				continue
			}
			if ok {
				filesUploaded++
			}
		}

		result := map[string]interface{}{
			"remote_dir":      remoteDir,
			"files_uploaded":  filesUploaded,
			"folders_created": foldersCreated,
			"files_skipped":   filesSkipped,
		}
		if len(errors) > 0 {
			result["errors"] = errors
			errMsg := fmt.Sprintf("%d of %d source(s) failed", len(errors), len(localPaths))
			printer.PrintResult(result, errMsg, output.ExitError)
			return &exitError{code: output.ExitError, msg: errMsg}
		}
		printer.PrintSuccess(result)
		if !jsonOutput {
			fmt.Printf("Upload complete: %d file(s), %d folder(s) -> %s\n", filesUploaded, foldersCreated, remoteDir)
		}
		return nil
	},
}

// uploadSingleFile uploads one local file into dirID, rendering a two-stage
// progress bar (SHA1 digest, then OSS transfer) on a terminal. It returns
// whether the upload succeeded.
func uploadSingleFile(client *driver.Pan115Client, localPath string, size int64, dirID string) (bool, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return false, fmt.Errorf("cannot open local file: %w", err)
	}
	defer f.Close()

	fileName := filepath.Base(localPath)

	if !jsonOutput {
		fmt.Printf("Uploading %s (%s)...\n", fileName, output.FormatFileSize(size))
	}

	var hashBar, uploadBar *pb.ProgressBar
	if !jsonOutput {
		hashBar = output.CreateProgressBar(size)
		if hashBar != nil {
			hashBar.SetTemplateString(`{{string . "stage" }} {{counters . }} {{bar . }} {{percent . }} {{speed . }}`)
			hashBar.Set("stage", "Computing SHA1")
		}
	}
	progress := &driver.UploadProgressCallbacks{
		Hash: func(n int64) {
			if hashBar != nil {
				hashBar.SetCurrent(n)
			}
		},
		Upload: func(n int64) {
			if hashBar != nil && uploadBar == nil {
				hashBar.Finish()
				hashBar = nil
				uploadBar = output.CreateProgressBar(size)
				if uploadBar != nil {
					uploadBar.SetTemplateString(`{{string . "stage" }} {{counters . }} {{bar . }} {{percent . }} {{speed . }}`)
					uploadBar.Set("stage", "Uploading to 115")
				}
			}
			if uploadBar != nil {
				uploadBar.SetCurrent(n)
			}
		},
	}

	err = client.RapidUploadOrByOSSWithProgress(dirID, fileName, size, f, progress)
	output.FinishProgress(hashBar)
	output.FinishProgress(uploadBar)
	if err != nil {
		return false, fmt.Errorf("upload failed: %w", err)
	}
	if !jsonOutput {
		fmt.Printf("Upload complete: %s -> %s\n", fileName, dirID)
	}
	return true, nil
}

func init() {
	rootCmd.AddCommand(uploadCmd)
}

// splitUploadArgs separates upload arguments into local sources and the final
// remote directory, e.g. `upload a b c /dest` -> sources [a b c], remote /dest.
func splitUploadArgs(args []string) (localPaths []string, remoteDir string) {
	return args[:len(args)-1], args[len(args)-1]
}

// uploadFolderClient is the subset of Pan115Client operations needed to upload a
// directory tree. It is implemented by *driver.Pan115Client and by fakes in tests.
type uploadFolderClient interface {
	Mkdir(parentID, name string) (string, error)
	RapidUploadOrByMultipart(dirID, fileName string, fileSize int64, r *os.File, opts ...driver.UploadMultipartOption) error
	RapidUploadOrByMultipartWithProgress(dirID, fileName string, fileSize int64, r *os.File, onUploadedParts func(current, total int), opts ...driver.UploadMultipartOption) error
}

// folderUploadSummary collects the outcome of a recursive folder upload.
type folderUploadSummary struct {
	foldersCreated int
	filesUploaded  int
	skipped        int
	errors         []string
}

// uploadFolder uploads a local directory tree under the remote dirID, creating a
// root folder named after the source directory, mirroring each local subdirectory
// and uploading every regular file. Symlinks are skipped to avoid escaping the
// local filesystem root or creating cycles. Root folder creation failures are
// returned as the error; per-file/per-folder failures are collected in summary.
func uploadFolder(localPath, dirID string, client uploadFolderClient) (folderUploadSummary, error) {
	var summary folderUploadSummary

	folderName := filepath.Base(localPath)
	newDirID, err := client.Mkdir(dirID, folderName)
	if err != nil {
		return summary, fmt.Errorf("create remote folder %q: %w", folderName, err)
	}

	walkFolder(localPath, newDirID, client, &summary)
	return summary, nil
}

func walkFolder(localDir, dirID string, client uploadFolderClient, summary *folderUploadSummary) {
	entries, err := os.ReadDir(localDir)
	if err != nil {
		summary.errors = append(summary.errors, fmt.Sprintf("read directory %s: %v", localDir, err))
		return
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		childPath := filepath.Join(localDir, entry.Name())
		if entry.IsDir() {
			childID, err := client.Mkdir(dirID, entry.Name())
			if err != nil {
				summary.errors = append(summary.errors, fmt.Sprintf("create remote folder for %s: %v", childPath, err))
				continue
			}
			summary.foldersCreated++
			walkFolder(childPath, childID, client, summary)
			continue
		}
		if entry.Type().IsRegular() {
			uploadLocalFile(client, childPath, dirID, summary, &folderUploadProgress{})
			continue
		}
		// socket/FIFO/device entries cannot be uploaded; count them so users
		// know something was skipped rather than silently dropped.
		summary.skipped++
	}
}

// uploadLocalFile opens and uploads a single file into the remote directory,
// recording any failure in summary so the surrounding folder upload continues.
// progress, when non-nil, receives per-file lifecycle events for terminal UX.
func uploadLocalFile(client uploadFolderClient, localPath, dirID string, summary *folderUploadSummary, progress *folderUploadProgress) {
	f, err := os.Open(localPath)
	if err != nil {
		summary.errors = append(summary.errors, fmt.Sprintf("open %s: %v", localPath, err))
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		summary.errors = append(summary.errors, fmt.Sprintf("stat %s: %v", localPath, err))
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		summary.errors = append(summary.errors, fmt.Sprintf("seek %s: %v", localPath, err))
		return
	}

	if progress != nil {
		progress.fileStart(filepath.Base(localPath))
	}

	var uploaded int
	err = client.RapidUploadOrByMultipartWithProgress(dirID, info.Name(), info.Size(), f, func(current, total int) {
		if progress != nil {
			progress.parts(current, total)
		}
	})
	if progress != nil {
		progress.fileDone(uploaded)
	}
	if err != nil {
		summary.errors = append(summary.errors, fmt.Sprintf("upload %s: %v", localPath, err))
		return
	}
	summary.filesUploaded++
}

// folderUploadProgress renders lightweight per-file feedback during a folder
// upload: a header line with the file name, then a progress bar while the
// multipart parts are transferred. All output is skipped for non-terminals or
// JSON mode.
type folderUploadProgress struct {
	bar        *pb.ProgressBar
	termChecked bool
	isTerm      bool
}

func (p *folderUploadProgress) fileStart(name string) {
	p.fileDone(0)
	if jsonOutput || !p.terminal() {
		return
	}
	fmt.Printf("Uploading %s...\n", name)
}

// parts advances the progress bar to current/total uploaded parts. Off a
// terminal CreateProgressBar returns nil and the call is a no-op.
func (p *folderUploadProgress) parts(current, total int) {
	if jsonOutput || !p.terminal() {
		return
	}
	if p.bar == nil {
		p.bar = output.CreateProgressBar(int64(total))
		if p.bar == nil {
			return
		}
		p.bar.SetTemplateString(`{{counters . }} {{bar . }} {{percent . }}`)
	}
	p.bar.SetCurrent(int64(current))
}

// terminal reports whether stdout is a TTY. It is evaluated lazily so piping
// output (e.g. into a log file) keeps the folder upload quiet.
func (p *folderUploadProgress) terminal() bool {
	if p.termChecked {
		return p.isTerm
	}
	p.termChecked = true
	p.isTerm = output.IsTerminal()
	return p.isTerm
}

// fileDone finalizes any active progress bar.
func (p *folderUploadProgress) fileDone(int) {
	if p.bar != nil {
		p.bar.Finish()
		p.bar = nil
	}
}

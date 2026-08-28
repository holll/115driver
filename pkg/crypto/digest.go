package hash

import (
	"crypto/sha1"
	"encoding/hex"
	"io"
	"strings"
)

const (
	hashPreSize = 128 * 1024
)

type DigestResult struct {
	Size    int64
	PreID   string
	QuickID string
	// MD5 is deprecated and intentionally left empty: the digest of the file
	// never used it, so computing it was pure overhead. It is kept only for
	// API compatibility.
	MD5 string
}

func Digest(r io.Reader, result *DigestResult) (err error) {
	return DigestWithProgress(r, result, nil)
}

// DigestWithProgress is Digest with an optional progress callback. When non-nil,
// onProgress is invoked with the number of bytes hashed so far after each read,
// letting callers render progress while a large file is being digested.
func DigestWithProgress(r io.Reader, result *DigestResult, onProgress func(n int64)) (err error) {
	h := sha1.New()
	// Calculate SHA1 hash of first 128K, which is used as PreID
	result.Size, err = io.CopyN(progressWriter{onProgress, h}, r, hashPreSize)
	if err != nil && err != io.EOF {
		return
	}
	result.PreID = strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
	// Write remain data. hash.Sum does not reset the hash, so a second Sum
	// after reading the rest yields the full-file SHA1 used as QuickID.
	if err == nil {
		var n int64
		if n, err = io.Copy(progressWriter{onProgress, h}, r); err != nil {
			return
		}
		result.Size += n
		result.QuickID = strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
	} else {
		result.QuickID = result.PreID
	}
	return nil
}

// progressWriter forwards writes to the underlying writer and reports the
// cumulative byte count via onProgress after every write chunk.
type progressWriter struct {
	onProgress func(n int64)
	w          io.Writer
}

func (p progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	if p.onProgress != nil {
		p.onProgress(int64(n))
	}
	return n, err
}

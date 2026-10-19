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
	writer := &progressWriter{onProgress: onProgress, w: h}
	// Calculate SHA1 hash of first 128K, which is used as PreID
	result.Size, err = io.CopyN(writer, r, hashPreSize)
	if err != nil && err != io.EOF {
		return
	}
	result.PreID = strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
	// Write remain data. hash.Sum does not reset the hash, so a second Sum
	// after reading the rest yields the full-file SHA1 used as QuickID.
	if err == nil {
		var n int64
		if n, err = io.Copy(writer, r); err != nil {
			return
		}
		result.Size += n
		result.QuickID = strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
	} else {
		result.QuickID = result.PreID
	}
	return nil
}

// progressWriter 将写入转发给底层 writer，并在每次写入后报告累计字节数。
type progressWriter struct {
	onProgress func(n int64)
	w          io.Writer
	written    int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.written += int64(n)
	if p.onProgress != nil && n > 0 {
		p.onProgress(p.written)
	}
	return n, err
}

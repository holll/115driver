package hash

import (
	"crypto/sha1"
	"fmt"
	"strings"
	"testing"
)

func TestDigestWithProgressReportsBytesHashed(t *testing.T) {
	content := "0123456789abcdef"
	var total int64
	var calls int
	result := &DigestResult{}
	err := DigestWithProgress(strings.NewReader(content), result, func(n int64) {
		total = n
		calls++
	})
	if err != nil {
		t.Fatalf("DigestWithProgress failed: %v", err)
	}
	if result.Size != int64(len(content)) {
		t.Fatalf("unexpected size: %d", result.Size)
	}
	if total != int64(len(content)) {
		t.Fatalf("expected progress total %d, got %d", len(content), total)
	}
	if calls == 0 {
		t.Fatal("expected progress callback to be invoked")
	}
	if result.PreID != result.QuickID {
		t.Fatalf("small file should have PreID == QuickID, got %q != %q", result.PreID, result.QuickID)
	}
}

func TestDigestWithProgressReportsCumulativeBytes(t *testing.T) {
	for _, size := range []int{0, hashPreSize - 1, hashPreSize, hashPreSize + 1, hashPreSize * 4} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			content := strings.Repeat("a", size)
			var current int64
			var calls int
			result := &DigestResult{}
			err := DigestWithProgress(strings.NewReader(content), result, func(n int64) {
				if n <= current || n > int64(size) {
					t.Errorf("progress must increase within file size: previous=%d current=%d size=%d", current, n, size)
				}
				current = n
				calls++
			})
			if err != nil {
				t.Fatal(err)
			}
			if current != int64(size) || result.Size != int64(size) {
				t.Fatalf("expected %d bytes, got progress=%d size=%d", size, current, result.Size)
			}
			if size > hashPreSize && calls < 2 {
				t.Fatalf("expected intermediate progress updates, got %d", calls)
			}
			preSize := size
			if preSize > hashPreSize {
				preSize = hashPreSize
			}
			if want := fmt.Sprintf("%X", sha1.Sum([]byte(content[:preSize]))); result.PreID != want {
				t.Fatalf("PreID=%s, want %s", result.PreID, want)
			}
			if want := fmt.Sprintf("%X", sha1.Sum([]byte(content))); result.QuickID != want {
				t.Fatalf("QuickID=%s, want %s", result.QuickID, want)
			}
		})
	}
}

func TestDigestMatchesDigestWithProgress(t *testing.T) {
	content := strings.Repeat("hello world ", 1000)
	plain := &DigestResult{}
	if err := Digest(strings.NewReader(content), plain); err != nil {
		t.Fatalf("Digest failed: %v", err)
	}
	progressed := &DigestResult{}
	if err := DigestWithProgress(strings.NewReader(content), progressed, nil); err != nil {
		t.Fatalf("DigestWithProgress failed: %v", err)
	}
	if plain.PreID != progressed.PreID || plain.QuickID != progressed.QuickID || plain.Size != progressed.Size {
		t.Fatalf("DigestWithProgress diverged: plain=%+v progressed=%+v", plain, progressed)
	}
}

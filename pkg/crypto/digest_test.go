package hash

import (
	"strings"
	"testing"
)

func TestDigestWithProgressReportsBytesHashed(t *testing.T) {
	content := "0123456789abcdef"
	var total int64
	var calls int
	result := &DigestResult{}
	err := DigestWithProgress(strings.NewReader(content), result, func(n int64) {
		total += n
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
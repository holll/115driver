package tools

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateUploadURLRejectsUnsafeTargets(t *testing.T) {
	tests := []string{
		"file:///etc/passwd",
		"ftp://example.com/file.bin",
		"http://127.0.0.1/file.bin",
		"http://localhost/file.bin",
		"http://[::1]/file.bin",
	}

	for _, rawURL := range tests {
		t.Run(rawURL, func(t *testing.T) {
			if _, err := validateUploadURL(rawURL); err == nil {
				t.Fatalf("expected %q to be rejected", rawURL)
			}
		})
	}
}

func TestValidateUploadURLAcceptsHTTPSURL(t *testing.T) {
	got, err := validateUploadURL("https://example.com/path/file.bin?token=abc")
	if err != nil {
		t.Fatalf("expected URL to be accepted: %v", err)
	}
	if got.String() != "https://example.com/path/file.bin?token=abc" {
		t.Fatalf("unexpected parsed URL: %s", got.String())
	}
}

func TestValidateLocalPathRequiresConfiguredRoot(t *testing.T) {
	if _, err := validateLocalPath("", "/tmp/out.bin", false); err == nil {
		t.Fatal("expected empty local root to reject local file access")
	}
}

func TestValidateLocalPathRejectsPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	if _, err := validateLocalPath(root, root+"/../outside.bin", false); err == nil {
		t.Fatal("expected path outside root to be rejected")
	}
}

func TestValidateLocalPathAcceptsPathInsideRoot(t *testing.T) {
	root := t.TempDir()
	got, err := validateLocalPath(root, root+"/nested/out.bin", false)
	if err != nil {
		t.Fatalf("expected path inside root to be accepted: %v", err)
	}
	if !strings.HasPrefix(got, root) {
		t.Fatalf("expected %q to stay under %q", got, root)
	}
}

func TestValidateLocalPathRejectsExistingSymlinkFileOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	if _, err := validateLocalPath(root, link, false); err == nil {
		t.Fatal("expected symlink target outside root to be rejected")
	}
}

func TestValidateLocalPathRejectsMissingPathUnderSymlinkedParentOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(link, "missing", "file.txt")

	if _, err := validateLocalPath(root, target, false); err == nil {
		t.Fatal("expected missing path below external symlink parent to be rejected")
	}
}

func TestCopyHTTPResponseRequiresStatusOK(t *testing.T) {
	var out strings.Builder
	resp := &http.Response{
		StatusCode: http.StatusForbidden,
		Body:       io.NopCloser(strings.NewReader("denied")),
	}

	err := copyHTTPResponse(&out, resp, 1024)
	if err == nil {
		t.Fatal("expected non-200 response to fail")
	}
	if !errors.Is(err, errUnexpectedHTTPStatus) {
		t.Fatalf("expected errUnexpectedHTTPStatus, got %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected response body not to be copied, wrote %d bytes", out.Len())
	}
}

func TestCopyHTTPResponseEnforcesSizeLimit(t *testing.T) {
	var out strings.Builder
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("abcdef")),
	}

	err := copyHTTPResponse(&out, resp, 3)
	if err == nil {
		t.Fatal("expected oversized response to fail")
	}
	if !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("expected errResponseTooLarge, got %v", err)
	}
}

func TestCopyHTTPResponseAllowsUnlimitedSizeWhenLimitIsZero(t *testing.T) {
	var out strings.Builder
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("abcdef")),
	}

	if err := copyHTTPResponse(&out, resp, 0); err != nil {
		t.Fatalf("expected zero limit to allow response: %v", err)
	}
	if out.String() != "abcdef" {
		t.Fatalf("unexpected copied body: %q", out.String())
	}
}

func TestCopyHTTPResponseRejectsNegativeSizeLimit(t *testing.T) {
	var out strings.Builder
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("abcdef")),
	}

	err := copyHTTPResponse(&out, resp, -1)
	if err == nil {
		t.Fatal("expected negative size limit to fail")
	}
	if !errors.Is(err, errInvalidSizeLimit) {
		t.Fatalf("expected errInvalidSizeLimit, got %v", err)
	}
}

func TestSaveHTTPResponseToFileRejectsNegativeSizeLimit(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target.txt")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("abcdef")),
	}

	err := saveHTTPResponseToFile(target, resp, -1)
	if err == nil {
		t.Fatal("expected negative size limit to fail")
	}
	if !errors.Is(err, errInvalidSizeLimit) {
		t.Fatalf("expected errInvalidSizeLimit, got %v", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("expected target not to be created, stat err: %v", statErr)
	}
}

func TestMCPDefaultDownloadSizeAllowsLargeDownloads(t *testing.T) {
	if defaultMCPDownloadMaxBytes != 0 {
		t.Fatalf("expected default MCP download size to be unlimited, got %d", defaultMCPDownloadMaxBytes)
	}
}

func TestMCPDefaultURLUploadSizeRemainsBounded(t *testing.T) {
	if defaultMCPURLUploadMaxBytes <= 0 {
		t.Fatalf("expected URL upload default size to be bounded, got %d", defaultMCPURLUploadMaxBytes)
	}
}

func TestMCPHTTPClientUsesConfiguredTimeout(t *testing.T) {
	client := newMCPHTTPClient(90 * time.Second)
	if client.Timeout != 90*time.Second {
		t.Fatalf("expected configured timeout, got %s", client.Timeout)
	}
}

func TestMCPHTTPClientRejectsUnsafeRedirect(t *testing.T) {
	client := newMCPHTTPClient(90 * time.Second)
	req := &http.Request{URL: mustParseURL(t, "http://127.0.0.1/private")}
	if err := client.CheckRedirect(req, nil); err == nil {
		t.Fatal("expected redirect to unsafe host to be rejected")
	}
}

func TestSharedAddressRangeValidation(t *testing.T) {
	tests := []struct {
		address string
		unsafe  bool
	}{
		{"100.64.0.0", true},
		{"100.100.100.200", true},
		{"100.127.255.255", true},
		{"100.63.255.255", false},
		{"100.128.0.0", false},
		{"::ffff:100.64.0.0", true},
		{"::ffff:100.100.100.200", true},
		{"::ffff:100.127.255.255", true},
		{"::ffff:100.63.255.255", false},
		{"::ffff:100.128.0.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			ip := net.ParseIP(tt.address)
			if ip == nil {
				t.Fatal("invalid test IP")
			}
			if got := isUnsafeIP(ip); got != tt.unsafe {
				t.Errorf("isUnsafeIP = %v, want %v", got, tt.unsafe)
			}

			// 只调用校验函数和重定向回调，不发送真实网络请求。
			rawURL := "http://" + net.JoinHostPort(tt.address, "80") + "/file.bin"
			if _, err := validateUploadURL(rawURL); (err != nil) != tt.unsafe {
				t.Errorf("validateUploadURL error = %v, want rejection %v", err, tt.unsafe)
			}
			ips := []net.IP{ip}
			if err := validateResolvedIPs("example.com", ips); (err != nil) != tt.unsafe {
				t.Errorf("validateResolvedIPs error = %v, want rejection %v", err, tt.unsafe)
			}
			client := newMCPHTTPClient(time.Second)
			req := &http.Request{URL: mustParseURL(t, rawURL)}
			if err := client.CheckRedirect(req, nil); (err != nil) != tt.unsafe {
				t.Errorf("CheckRedirect error = %v, want rejection %v", err, tt.unsafe)
			}

			if tt.unsafe {
				// 混合解析结果中的共享地址必须使整个拨号失败，不能先连接公网地址。
				ips = append([]net.IP{net.ParseIP("100.128.0.0")}, ips...)
				attempted := false
				conn, err := dialResolvedIPs(context.Background(), "tcp", "example.com", "80", ips, func(context.Context, string, string) (net.Conn, error) {
					attempted = true
					return nil, errors.New("unexpected dial")
				})
				if conn != nil {
					conn.Close()
				}
				if err == nil || attempted {
					t.Errorf("expected rejection before dialing, got error %v, attempted %v", err, attempted)
				}
			}
		})
	}
}

func TestValidateResolvedIPsRejectsPrivateAddress(t *testing.T) {
	if err := validateResolvedIPs("example.com", []net.IP{net.ParseIP("10.0.0.1")}); err == nil {
		t.Fatal("expected private resolved IP to be rejected")
	}
}

func TestDialResolvedIPsFallsBackToLaterAddresses(t *testing.T) {
	ips := []net.IP{
		net.ParseIP("203.0.113.1"),
		net.ParseIP("203.0.113.2"),
	}
	var attempted []string
	conn, err := dialResolvedIPs(context.Background(), "tcp", "example.com", "443", ips, func(ctx context.Context, network, address string) (net.Conn, error) {
		attempted = append(attempted, address)
		if len(attempted) == 1 {
			return nil, errors.New("first address unreachable")
		}
		client, server := net.Pipe()
		server.Close()
		return client, nil
	})
	if err != nil {
		t.Fatalf("expected second address to be used: %v", err)
	}
	conn.Close()

	want := []string{
		net.JoinHostPort("203.0.113.1", "443"),
		net.JoinHostPort("203.0.113.2", "443"),
	}
	if strings.Join(attempted, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected dial attempts: got %v want %v", attempted, want)
	}
}

func TestSaveHTTPResponseToFilePreservesExistingFileOnFailure(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("abcdef")),
	}

	if err := saveHTTPResponseToFile(target, resp, 3); err == nil {
		t.Fatal("expected oversized response to fail")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("expected existing file to be preserved, got %q", string(got))
	}
}

func TestMCPDefaultDownloadTimeoutAllowsLargeDownloads(t *testing.T) {
	if defaultMCPDownloadTimeout < time.Hour {
		t.Fatalf("expected default timeout to allow large downloads, got %s", defaultMCPDownloadTimeout)
	}
}

func mustParseURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

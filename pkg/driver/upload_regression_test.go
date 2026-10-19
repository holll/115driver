package driver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

func TestProgressReaderReportsCumulativeBytes(t *testing.T) {
	var progress []int64
	r := &progressReader{r: strings.NewReader("abcdefghij"), onProgress: func(n int64) {
		progress = append(progress, n)
	}}
	buf := make([]byte, 3)
	for {
		_, err := r.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if want := []int64{3, 6, 9, 10}; !reflect.DeepEqual(progress, want) {
		t.Fatalf("progress=%v, want %v", progress, want)
	}
}

func TestSplitFileNineGiBBoundaryReturnsFileError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	_, err := SplitFile(missing, 9*GB)
	if !os.IsNotExist(err) {
		t.Fatalf("expected missing file error at 9 GiB boundary, got %v", err)
	}
}

func TestSplitFileCoversEntireFile(t *testing.T) {
	for _, size := range []int64{2 * KB, 100 * KB, 250 * KB} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := newMultipartTestFile(t, int(size))
			chunks, err := SplitFile(f.Name(), size)
			if err != nil {
				t.Fatal(err)
			}
			var end int64
			for i, chunk := range chunks {
				if chunk.Offset != end || chunk.Number != i+1 || chunk.Size <= 0 {
					t.Fatalf("invalid chunk: %+v", chunk)
				}
				end += chunk.Size
			}
			if end != size {
				t.Fatalf("covered=%d, want %d", end, size)
			}
		})
	}
}

type multipartRegressionTransport func(*http.Request) (*http.Response, error)

func (f multipartRegressionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newMultipartTestBucket(t *testing.T, transport multipartRegressionTransport, options ...oss.ClientOption) *oss.Bucket {
	t.Helper()
	options = append(options, oss.HTTPClient(&http.Client{Transport: transport}))
	client, err := oss.New("https://oss.invalid", "test-ak", "test-sk", options...)
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := client.Bucket("test-bucket")
	if err != nil {
		t.Fatal(err)
	}
	return bucket
}

func newMultipartTestFile(t *testing.T, size int) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "parts-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if _, err := f.Write(bytes.Repeat([]byte{'x'}, size)); err != nil {
		t.Fatal(err)
	}
	return f
}

func multipartSuccessResponse(req *http.Request) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Etag": {"test-etag"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}
}

func newMultipartRegressionClient() *Pan115Client {
	c := New()
	c.storeOSSToken(&UploadOSSTokenResp{AccessKeyID: "test-ak", AccessKeySecret: "test-sk", SecurityToken: "test-token", Expiration: time.Now().Add(time.Hour), StatusCode: "200"})
	return c
}

func TestMultipartWorkersReportAllParts(t *testing.T) {
	var requests atomic.Int64
	bucket := newMultipartTestBucket(t, func(req *http.Request) (*http.Response, error) {
		requests.Add(1)
		if _, err := io.Copy(io.Discard, req.Body); err != nil {
			return nil, err
		}
		return multipartSuccessResponse(req), nil
	})
	f := newMultipartTestFile(t, 12)
	chunks := []oss.FileChunk{{Number: 1, Offset: 0, Size: 4}, {Number: 2, Offset: 4, Size: 4}, {Number: 3, Offset: 8, Size: 4}}
	var updates []int
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	parts, err := newMultipartRegressionClient().uploadMultipartParts(ctx, bucket, &UploadOSSParams{}, oss.InitiateMultipartUploadResult{Key: "file", UploadID: "upload"}, f, chunks, 2, time.Hour, func(current, total int) {
		if total != len(chunks) {
			t.Errorf("total=%d, want %d", total, len(chunks))
		}
		updates = append(updates, current)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 || requests.Load() != 3 || !reflect.DeepEqual(updates, []int{1, 2, 3}) {
		t.Fatalf("parts=%v requests=%d progress=%v", parts, requests.Load(), updates)
	}
}

func TestMultipartWorkersCancelAndJoinOnFailure(t *testing.T) {
	started := make(chan struct{})
	var active atomic.Int64
	bucket := newMultipartTestBucket(t, func(req *http.Request) (*http.Response, error) {
		active.Add(1)
		defer active.Add(-1)
		if req.URL.Query().Get("partNumber") == "1" {
			select {
			case <-started:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
			return nil, fmt.Errorf("injected upload failure")
		}
		close(started)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	f := newMultipartTestFile(t, 8)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := newMultipartRegressionClient().uploadMultipartParts(ctx, bucket, &UploadOSSParams{}, oss.InitiateMultipartUploadResult{Key: "file", UploadID: "upload"}, f, []oss.FileChunk{{Number: 1, Offset: 0, Size: 4}, {Number: 2, Offset: 4, Size: 4}}, 2, time.Hour, nil)
	if err == nil || !strings.Contains(err.Error(), "injected upload failure") {
		t.Fatalf("expected upload failure, got %v", err)
	}
	if active.Load() != 0 {
		t.Fatalf("upload returned with %d active requests", active.Load())
	}
}

func TestMultipartWorkersRejectShortFileRead(t *testing.T) {
	bucket := newMultipartTestBucket(t, func(req *http.Request) (*http.Response, error) {
		t.Error("short file must not be uploaded with zero padding")
		return multipartSuccessResponse(req), nil
	})
	f := newMultipartTestFile(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := newMultipartRegressionClient().uploadMultipartParts(ctx, bucket, &UploadOSSParams{}, oss.InitiateMultipartUploadResult{Key: "file", UploadID: "upload"}, f, []oss.FileChunk{{Number: 1, Offset: 0, Size: 4}}, 1, time.Hour, nil)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected short file read error, got %v", err)
	}
}

func TestMultipartWorkersTokenRefreshFailureCancelsRequests(t *testing.T) {
	c := newMultipartRegressionClient()
	c.Client.SetTransport(multipartRegressionTransport(func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("injected token refresh failure")
	}))
	var active atomic.Int64
	bucket := newMultipartTestBucket(t, func(req *http.Request) (*http.Response, error) {
		active.Add(1)
		defer active.Add(-1)
		c.ossTokenMu.Lock()
		c.ossTokenExpiry = time.Now()
		c.ossTokenMu.Unlock()
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	f := newMultipartTestFile(t, 4)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := c.uploadMultipartParts(ctx, bucket, &UploadOSSParams{}, oss.InitiateMultipartUploadResult{Key: "file", UploadID: "upload"}, f, []oss.FileChunk{{Number: 1, Offset: 0, Size: 4}}, 1, 10*time.Millisecond, nil)
	if err == nil || !strings.Contains(err.Error(), "injected token refresh failure") {
		t.Fatalf("expected token refresh error, got %v", err)
	}
	if active.Load() != 0 {
		t.Fatalf("upload returned with %d active requests", active.Load())
	}
}

func TestMultipartTimeoutIncludesEndpointLookup(t *testing.T) {
	c := newMultipartRegressionClient()
	c.UseInternalUpload = true
	c.Client.SetTransport(multipartRegressionTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != ApiGetUploadEndpoint {
			t.Errorf("unexpected request: %s", req.URL)
		}
		<-req.Context().Done()
		return nil, req.Context().Err()
	}))
	err := c.UploadByMultipartWithProgress(nil, 0, nil, "0", nil, UploadMultipartWithTimeout(30*time.Millisecond))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected endpoint lookup deadline exceeded, got %v", err)
	}
}

func TestMultipartRotatedCredentialsSignRequestsConsistently(t *testing.T) {
	c := newMultipartRegressionClient()
	c.Client.SetTransport(multipartRegressionTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"AccessKeyID":"rotated-ak","AccessKeySecret":"rotated-sk","SecurityToken":"rotated-token","Expiration":"2030-01-01T00:00:00Z","StatusCode":"200"}`)), Request: req}, nil
	}))
	var requests int
	bucket := newMultipartTestBucket(t, func(req *http.Request) (*http.Response, error) {
		requests++
		ak, sk, token := "rotated-ak", "rotated-sk", "rotated-token"
		if requests == 1 {
			ak, sk, token = "test-ak", "test-sk", "test-token"
		}
		if got := req.Header.Get(OssSecurityTokenHeaderName); got != token {
			t.Errorf("security token=%q, want %q", got, token)
		}
		var keys []string
		values := make(map[string]string)
		for name, vals := range req.Header {
			name = strings.ToLower(name)
			if strings.HasPrefix(name, "x-oss-") {
				keys = append(keys, name)
				values[name] = vals[0]
			}
		}
		sort.Strings(keys)
		var headers string
		for _, name := range keys {
			headers += name + ":" + values[name] + "\n"
		}
		resource := "/test-bucket" + req.URL.EscapedPath()
		if query := req.URL.Query().Encode(); query != "" {
			resource += "?" + query
		}
		text := req.Method + "\n" + req.Header.Get("Content-MD5") + "\n" + req.Header.Get("Content-Type") + "\n" + req.Header.Get("Date") + "\n" + headers + resource
		mac := hmac.New(sha1.New, []byte(sk))
		_, _ = io.WriteString(mac, text)
		want := "OSS " + ak + ":" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
		if got := req.Header.Get("Authorization"); got != want {
			t.Errorf("request %d signed with mismatched credentials", requests)
		}
		if requests == 1 {
			c.ossTokenMu.Lock()
			c.ossTokenExpiry = time.Now()
			c.ossTokenMu.Unlock()
		}
		response := multipartSuccessResponse(req)
		if req.Method == http.MethodPost {
			response.Body = io.NopCloser(strings.NewReader(`{"state":true}`))
		}
		return response, nil
	}, oss.SetCredentialsProvider(&ossTokenProvider{client: c}))
	f := newMultipartTestFile(t, 8)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	params := &UploadOSSParams{}
	params.Callback.Callback = "{}"
	imur := oss.InitiateMultipartUploadResult{Key: "file", UploadID: "upload"}
	parts, err := c.uploadMultipartParts(ctx, bucket, params, imur, f, []oss.FileChunk{{Number: 1, Offset: 0, Size: 4}, {Number: 2, Offset: 4, Size: 4}}, 1, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bucket.CompleteMultipartUpload(imur, parts, append(OssOption(params, nil), oss.WithContext(ctx))...); err != nil {
		t.Fatal(err)
	}
	if requests != 3 {
		t.Fatalf("requests=%d, want two parts and completion", requests)
	}
}

func TestMultipartRejectsInvalidRefreshTime(t *testing.T) {
	for _, refreshTime := range []time.Duration{0, -time.Second} {
		err := New().UploadByMultipartWithProgress(nil, 0, nil, "0", nil, UploadMultipartWithTokenRefreshTime(refreshTime))
		if err == nil {
			t.Fatalf("expected invalid refresh time error for %s", refreshTime)
		}
	}
}

func TestOSSTokenConcurrentRefresh(t *testing.T) {
	const count = 8
	var entered sync.WaitGroup
	entered.Add(count)
	c := New()
	c.Client.SetTransport(multipartRegressionTransport(func(req *http.Request) (*http.Response, error) {
		entered.Done()
		entered.Wait()
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(validTokenBody)), Request: req}, nil
	}))
	results := make(chan error, count)
	for i := 0; i < count; i++ {
		go func() {
			_, err := c.GetOSSToken()
			results <- err
		}()
	}
	for i := 0; i < count; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

func TestOSSTokenRequestHonorsContext(t *testing.T) {
	c := New()
	c.Client.SetTransport(multipartRegressionTransport(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := c.getOSSTokenWithContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

func TestMultipartWorkersCancelAndJoinOnTimeout(t *testing.T) {
	var active atomic.Int64
	bucket := newMultipartTestBucket(t, func(req *http.Request) (*http.Response, error) {
		active.Add(1)
		defer active.Add(-1)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	f := newMultipartTestFile(t, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := newMultipartRegressionClient().uploadMultipartParts(ctx, bucket, &UploadOSSParams{}, oss.InitiateMultipartUploadResult{Key: "file", UploadID: "upload"}, f, []oss.FileChunk{{Number: 1, Offset: 0, Size: 4}}, 2, time.Hour, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if active.Load() != 0 {
		t.Fatalf("upload returned with %d active requests", active.Load())
	}
}

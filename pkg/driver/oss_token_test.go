package driver

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// ossTokenRoundTripper intercepts the gettoken.php request and returns a canned
// STS token, counting how many times the endpoint was hit.
type ossTokenRoundTripper struct {
	mu       sync.Mutex
	requests int
	body     string
}

func (rt *ossTokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.requests++
	rt.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(rt.body)),
		Request:    req,
	}, nil
}

func (rt *ossTokenRoundTripper) count() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.requests
}

func newTokenTestClient(rt *ossTokenRoundTripper) *Pan115Client {
	c := New()
	c.Client.SetTransport(rt)
	return c
}

const validTokenBody = `{"AccessKeyID":"AK","AccessKeySecret":"SK","SecurityToken":"ST","Expiration":"2030-01-01T00:00:00Z","StatusCode":"200"}`

func TestGetOSSTokenCachesUntilExpiry(t *testing.T) {
	rt := &ossTokenRoundTripper{body: validTokenBody}
	client := newTokenTestClient(rt)

	t1, err := client.GetOSSToken()
	if err != nil {
		t.Fatalf("first GetOSSToken failed: %v", err)
	}
	t2, err := client.GetOSSToken()
	if err != nil {
		t.Fatalf("second GetOSSToken failed: %v", err)
	}
	if t1 != t2 {
		t.Fatal("expected the cached token instance to be returned")
	}
	if got := rt.count(); got != 1 {
		t.Fatalf("expected 1 token request for two calls, got %d", got)
	}
}

func TestGetOSSTokenRefreshesWhenNearExpiry(t *testing.T) {
	rt := &ossTokenRoundTripper{body: validTokenBody}
	client := newTokenTestClient(rt)
	// Simulate a cached token that lies inside the refresh buffer: a stale
	// AccessKeyID plus an expiration only a minute away.
	client.ossToken = &UploadOSSTokenResp{AccessKeyID: "stale", StatusCode: "200"}
	client.ossTokenExpiry = time.Now().Add(time.Minute)

	tok, err := client.GetOSSToken()
	if err != nil {
		t.Fatalf("GetOSSToken failed: %v", err)
	}
	if tok.AccessKeyID != "AK" {
		t.Fatalf("expected a freshly fetched token, got AccessKeyID %q", tok.AccessKeyID)
	}
	if got := rt.count(); got != 1 {
		t.Fatalf("expected 1 token request, got %d", got)
	}
}

func TestGetOSSTokenZeroExpirationAlwaysRefetches(t *testing.T) {
	rt := &ossTokenRoundTripper{body: `{"AccessKeyID":"AK","AccessKeySecret":"SK","SecurityToken":"ST","StatusCode":"200"}`}
	client := newTokenTestClient(rt)

	if _, err := client.GetOSSToken(); err != nil {
		t.Fatalf("first GetOSSToken failed: %v", err)
	}
	if _, err := client.GetOSSToken(); err != nil {
		t.Fatalf("second GetOSSToken failed: %v", err)
	}
	if got := rt.count(); got != 2 {
		t.Fatalf("expected a re-fetch for zero Expiration, got %d requests", got)
	}
}

func TestImportCredentialInvalidatesOSSTokenCache(t *testing.T) {
	rt := &ossTokenRoundTripper{body: validTokenBody}
	client := newTokenTestClient(rt)

	crA := &Credential{}
	if err := crA.FromCookie("UID=1;CID=c1;SEID=s1;KID=k1"); err != nil {
		t.Fatalf("parse cookie A: %v", err)
	}
	client.ImportCredential(crA)

	if _, err := client.GetOSSToken(); err != nil {
		t.Fatalf("GetOSSToken after first import failed: %v", err)
	}

	crB := &Credential{}
	if err := crB.FromCookie("UID=2;CID=c2;SEID=s2;KID=k2"); err != nil {
		t.Fatalf("parse cookie B: %v", err)
	}
	client.ImportCredential(crB)

	if _, err := client.GetOSSToken(); err != nil {
		t.Fatalf("GetOSSToken after re-import failed: %v", err)
	}
	if got := rt.count(); got != 2 {
		t.Fatalf("expected re-import to drop the token cache, got %d requests", got)
	}
}
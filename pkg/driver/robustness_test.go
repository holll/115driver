package driver

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type robustnessTransport func(*http.Request) (*http.Response, error)

func (f robustnessTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestVersionsGetAppVersions(t *testing.T) {
	t.Run("no-empty-items", func(t *testing.T) {
		got := (Versions{"win": {"version_code": "1"}, "mac": {"version_code": "2"}}).GetAppVersions()
		require.ElementsMatch(t, []AppVersion{{AppName: "win", Version: "1"}, {AppName: "mac", Version: "2"}}, got)
	})
	t.Run("skip-non-string", func(t *testing.T) {
		versions := Versions{"valid": {"version_code": "3"}, "number": {"version_code": float64(4)}, "missing": {}, "null": nil, "bool": {"version_code": true}}
		require.NotPanics(t, func() {
			require.Equal(t, []AppVersion{{AppName: "valid", Version: "3"}}, versions.GetAppVersions())
		})
	})
	t.Run("empty", func(t *testing.T) {
		require.Empty(t, Versions(nil).GetAppVersions())
	})
}

func TestListWithLimitRejectsNonPositiveWithoutRequest(t *testing.T) {
	for _, limit := range []int64{0, -1} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			calls := 0
			c := New(WithClient(&http.Client{Transport: robustnessTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("不应发出请求")
			})}))
			files, err := c.ListWithLimit("0", limit)
			require.Error(t, err)
			require.Nil(t, files)
			require.Zero(t, calls)
		})
	}
}

func TestCookieCheckPreservesTransportError(t *testing.T) {
	want := errors.New("离线传输失败")
	calls := 0
	c := New(WithClient(&http.Client{Transport: robustnessTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, want
	})}))
	require.ErrorIs(t, c.CookieCheck(), want)
	require.Equal(t, 1, calls)
}

func TestCookieCheckState(t *testing.T) {
	for _, state := range []bool{true, false} {
		t.Run(fmt.Sprint(state), func(t *testing.T) {
			c := New(WithClient(&http.Client{Transport: robustnessTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"state":%t}`, state)))}, nil
			})}))
			if state {
				require.NoError(t, c.CookieCheck())
			} else {
				require.ErrorIs(t, c.CookieCheck(), ErrBadCookie)
			}
		})
	}
}

type robustnessBody struct {
	io.Reader
	closed bool
}

func (b *robustnessBody) Close() error {
	b.closed = true
	return nil
}

func TestDownloadInfoGetHTTPStatus(t *testing.T) {
	for _, status := range []int{200, 206, 204, 400, 403, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Test") != "preserved" {
					t.Error("下载请求丢失请求头")
				}
				w.WriteHeader(status)
				if status != 204 {
					_, _ = io.WriteString(w, "payload")
				}
			}))
			defer server.Close()
			info := DownloadInfo{Url: FileDownloadUrl{Url: server.URL}, Header: http.Header{"X-Test": {"preserved"}}}
			reader, err := info.Get()
			if status >= 200 && status < 300 {
				require.NoError(t, err)
				body, err := io.ReadAll(reader)
				require.NoError(t, err)
				if status != 204 {
					require.Equal(t, "payload", string(body))
				}
			} else {
				require.ErrorContains(t, err, fmt.Sprint(status))
				require.Nil(t, reader)
			}
		})
	}
}

func TestDownloadInfoGetClosesBody(t *testing.T) {
	for _, status := range []int{200, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			body := &robustnessBody{Reader: strings.NewReader("payload")}
			old := http.DefaultClient
			http.DefaultClient = &http.Client{Transport: robustnessTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Status: fmt.Sprint(status), Header: make(http.Header), Body: body}, nil
			})}
			t.Cleanup(func() { http.DefaultClient = old })
			info := DownloadInfo{Url: FileDownloadUrl{Url: "http://offline.invalid/file"}}
			_, _ = info.Get()
			require.True(t, body.closed)
		})
	}
}

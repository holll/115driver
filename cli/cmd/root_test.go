package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SheltonZhu/115driver/cli/internal/output"
	"github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/spf13/cobra"
)

type cliTestTransport func(*http.Request) (*http.Response, error)

func (f cliTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func cliTestResponse(body string) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

// 运行真实 Execute 错误输出路径，所有 HTTP 请求由离线替身接管。
func executeOfflineCLI(t *testing.T, transport cliTestTransport, args ...string) (int, string) {
	t.Helper()
	oldRoot, oldFactory := rootCmd, newCLIClient
	oldClient, oldPrinter := client, printer
	oldCookie, oldJSON, oldDebug := cookieFlag, jsonOutput, debugMode
	oldArgs, oldStdout := os.Args, os.Stdout
	t.Cleanup(func() {
		rootCmd, newCLIClient = oldRoot, oldFactory
		client, printer = oldClient, oldPrinter
		cookieFlag, jsonOutput, debugMode = oldCookie, oldJSON, oldDebug
		os.Args, os.Stdout = oldArgs, oldStdout
	})
	rootCmd = &cobra.Command{Use: "115driver", SilenceErrors: true, SilenceUsage: true, PersistentPreRunE: oldRoot.PersistentPreRunE}
	upload := &cobra.Command{Use: uploadCmd.Use, Args: uploadCmd.Args, RunE: uploadCmd.RunE}
	rootCmd.AddCommand(upload, &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { return nil }})
	rootCmd.SetArgs(args)
	newCLIClient = func(opts ...driver.Option) *driver.Pan115Client {
		c := driver.New(opts...)
		c.Client.SetTransport(transport)
		return c
	}
	cookieFlag, jsonOutput, debugMode = "UID=1_A1;CID=test;SEID=test;KID=test", true, false
	printer = nil
	os.Args = []string{"115driver"}
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	os.Stdout = f
	code := Execute()
	os.Stdout = oldStdout
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return code, string(b)
}

func decodeSingleCLIEnvelope(t *testing.T, text string) output.Envelope {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(text))
	var env output.Envelope
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("invalid JSON output %q: %v", text, err)
	}
	var extra interface{}
	if err := dec.Decode(&extra); err != io.EOF {
		t.Fatalf("expected exactly one JSON envelope, got extra=%v err=%v; output=%s", extra, err, text)
	}
	return env
}

func TestExecuteUploadPartialFailurePrintsOneJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "missing")
	calls := 0
	code, text := executeOfflineCLI(t, func(r *http.Request) (*http.Response, error) {
		calls++
		switch r.URL.Path {
		case "/files/add":
			return cliTestResponse(`{"state":true,"cid":"123"}`)
		default:
			if calls != 1 {
				t.Errorf("unexpected request: %s", r.URL)
			}
			return cliTestResponse(`{"state":true,"data":{"user_id":1}}`)
		}
	}, "upload", root, missing, "/")
	if code != output.ExitError {
		t.Fatalf("exit code=%d, want %d; output=%s", code, output.ExitError, text)
	}
	env := decodeSingleCLIEnvelope(t, text)
	if env.Success || env.Code != code || env.Error == "" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	data, ok := env.Data.(map[string]interface{})
	if !ok || data["remote_dir"] != "/" || data["files_uploaded"] != float64(0) || data["folders_created"] != float64(1) {
		t.Fatalf("missing partial results: %+v", env.Data)
	}
	if failures, ok := data["errors"].([]interface{}); !ok || len(failures) != 1 {
		t.Fatalf("unexpected partial failures: %+v", data)
	}
	if calls != 3 {
		t.Fatalf("expected authentication and two successful folder creations, got %d requests", calls)
	}
}

func TestExecuteGetUserErrorClassification(t *testing.T) {
	for _, tt := range []struct {
		name     string
		err      error
		body     string
		wantCode int
	}{
		{name: "DNS", err: fmt.Errorf("wrapped: %w", &net.DNSError{Err: "offline", Name: "example.invalid"}), wantCode: output.ExitNetwork},
		{name: "timeout", err: &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}, wantCode: output.ExitNetwork},
		{name: "URL", err: &url.Error{Op: "Get", URL: "https://example.invalid", Err: errors.New("offline")}, wantCode: output.ExitNetwork},
		{name: "invalid credential", body: `{"state":false,"errno":99,"error":"not logged in"}`, wantCode: output.ExitAuth},
		{name: "other API error", body: `{"state":false,"errno":123456,"error":"service error"}`, wantCode: output.ExitAuth},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, text := executeOfflineCLI(t, func(*http.Request) (*http.Response, error) {
				if tt.err != nil {
					return nil, tt.err
				}
				return cliTestResponse(tt.body)
			}, "probe")
			env := decodeSingleCLIEnvelope(t, text)
			if code != tt.wantCode || env.Code != code || env.Success {
				t.Fatalf("exit=%d envelope=%+v, want code %d", code, env, tt.wantCode)
			}
			if got := strings.Contains(env.Error, "re-authenticate"); got != (code == output.ExitAuth) {
				t.Fatalf("unexpected login advice: %q", env.Error)
			}
		})
	}
}

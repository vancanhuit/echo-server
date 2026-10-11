package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json/v2"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vancanhuit/echo-server/api"
)

func config(t *testing.T) Config {
	t.Helper()
	cfg, err := LoadConfig(nil, func(string) string { return "" }, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Environment, cfg.File = false, false
	return cfg
}

func request(cfg Config, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil))).ServeHTTP(w, r)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode %s: %v", w.Body, err)
	}
	return result
}

func TestEchoMethodsAndMetadata(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE", "PROPFIND", "CUSTOM"} {
		t.Run(method, func(t *testing.T) {
			w := request(config(t), method, "/one/two?x=1&x=2", `{"n":9007199254740993}`, map[string]string{"Content-Type": "application/json", "Cookie": "a=one%20two; b=2", "X-Test": "ok", "X-Forwarded-For": "203.0.113.1"})
			if w.Code != 200 {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			data := decode(t, w)
			h := data["http"].(map[string]any)
			if h["method"] != method || h["originalUrl"] != "/one/two?x=1&x=2" || h["baseUrl"] != "" || h["protocol"] != "http" {
				t.Fatalf("http: %v", h)
			}
			r := data["request"].(map[string]any)
			if r["params"].(map[string]any)["0"] != "/one/two" || r["cookies"].(map[string]any)["a"] != "one two" || r["headers"].(map[string]any)["x-test"] != "ok" {
				t.Fatalf("request: %v", r)
			}
			if !reflect.DeepEqual(r["query"].(map[string]any)["x"], []any{"1", "2"}) {
				t.Fatalf("query: %v", r["query"])
			}
			if !strings.Contains(w.Body.String(), "9007199254740993") {
				t.Fatal("JSON number lost precision")
			}
			if data["host"].(map[string]any)["ip"] == "203.0.113.1" {
				t.Fatal("trusted forwarded IP")
			}
			if _, exists := data["environment"]; exists {
				t.Fatal("environment leaked")
			}
		})
	}
}

func TestBodyParsers(t *testing.T) {
	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	if err := writer.WriteField("x", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, contentType, body string
		want                    any
		status                  int
	}{
		{"empty", "", "", map[string]any{}, 200},
		{"text", "text/plain", "hello", "hello", 200},
		{"jsonld", "application/ld+json", `{"x":"hello"}`, map[string]any{"x": "hello"}, 200},
		{"array", "application/json", `[1,2]`, []any{float64(1), float64(2)}, 200},
		{"form", "application/x-www-form-urlencoded", "x=hello&x=world", map[string]any{"x": []any{"hello", "world"}}, 200},
		{"multipart", writer.FormDataContentType(), multipartBody.String(), map[string]any{"x": "hello"}, 200},
		{"unknown", "application/octet-stream", "ignored", map[string]any{}, 200},
		{"broken json", "application/json", "{", nil, 400},
		{"scalar json", "application/json", "true", nil, 400},
		{"duplicate json", "application/json", `{"x":1,"x":2}`, nil, 400},
		{"bad form", "application/x-www-form-urlencoded", "x=%QQ", nil, 400},
		{"broken multipart", "multipart/form-data", "bad", nil, 400},
		{"oversized", "text/plain", strings.Repeat("x", maxBodyBytes+1), nil, 413},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := request(config(t), "GET", "/", test.body, map[string]string{"Content-Type": test.contentType})
			if w.Code != test.status {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if test.status == 200 {
				got := decode(t, w)["request"].(map[string]any)["body"]
				if !reflect.DeepEqual(got, test.want) {
					t.Fatalf("body %#v, want %#v", got, test.want)
				}
			}
		})
	}
	for _, size := range []int{10, maxBodyBytes + 1} {
		var compressed bytes.Buffer
		gz := gzip.NewWriter(&compressed)
		if _, err := gz.Write(bytes.Repeat([]byte("x"), size)); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		w := request(config(t), "POST", "/", compressed.String(), map[string]string{"Content-Type": "text/plain", "Content-Encoding": "gzip"})
		want := 200
		if size > maxBodyBytes {
			want = 413
		}
		if w.Code != want {
			t.Fatalf("gzip size %d: status %d", size, w.Code)
		}
	}
}

func TestResponseControls(t *testing.T) {
	for _, test := range []struct {
		name, target string
		headers      map[string]string
		status       int
		body         string
	}{
		{"body string", "/?echo_body=hello", nil, 200, `"hello"`},
		{"body json", "/?echo_body=" + url.QueryEscape(`{"ok":true}`), nil, 200, `{"ok":true}`},
		{"body null", "/?echo_body=null", nil, 200, `null`},
		{"body header wins", "/?echo_body=query", map[string]string{"X-Echo-Body": "header"}, 200, `"header"`},
		{"status query wins", "/?echo_body=no&echo_code=401", map[string]string{"X-Echo-Code": "500"}, 401, `"no"`},
		{"invalid query fallback", "/?echo_body=no&echo_code=999", map[string]string{"X-Echo-Code": "402"}, 402, `"no"`},
		{"invalid candidates ignored", "/?echo_body=no&echo_code=0200-200.401-404", nil, 404, `"no"`},
		{"204", "/?echo_body=no&echo_code=204", nil, 204, ""},
		{"205", "/?echo_body=no&echo_code=205", nil, 205, ""},
		{"304", "/?echo_body=no&echo_code=304", nil, 304, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := request(config(t), "GET", test.target, "", test.headers)
			if w.Code != test.status || w.Body.String() != test.body {
				t.Fatalf("got %d %s", w.Code, w.Body)
			}
		})
	}
	for range 30 {
		w := request(config(t), "GET", "/?echo_code=201-404-503", "", nil)
		if w.Code != 201 && w.Code != 404 && w.Code != 503 {
			t.Fatalf("unexpected random status %d", w.Code)
		}
		if w.Header().Get("X-Echo-Random-Status") != "201, 404, 503" {
			t.Fatal("missing candidates")
		}
	}
	w := request(config(t), "GET", "/?echo_header="+url.QueryEscape("One:query, Two:http://example.test:80"), "", map[string]string{"X-Echo-Header": "One:header"})
	if w.Header().Get("One") != "query" || w.Header().Get("Two") != "http://example.test:80" {
		t.Fatalf("headers: %v", w.Header())
	}
	for _, header := range []string{"Bad", "Bad Name:x", "X-Test:ok\r\nInjected:true", "Content-Length:1", "Transfer-Encoding:chunked"} {
		w := request(config(t), "GET", "/?echo_header="+url.QueryEscape(header), "", nil)
		if w.Code != 400 {
			t.Fatalf("accepted unsafe header %q", header)
		}
	}
}

func TestFeatureFlagsAndAliases(t *testing.T) {
	cfg := config(t)
	cfg.Cookies = false
	w := request(cfg, "GET", "/?echo_file=/&echo_env_body=HOME", "", map[string]string{"Cookie": "secret=value"})
	r := decode(t, w)["request"].(map[string]any)
	if !reflect.DeepEqual(r["cookies"], []any{}) {
		t.Fatal("cookies not disabled")
	}
	if _, exists := r["headers"].(map[string]any)["cookie"]; exists {
		t.Fatal("cookie header leaked")
	}
	cfg.Host, cfg.HTTP, cfg.Request, cfg.Header = false, false, false, false
	w = request(cfg, "GET", "/?echo_header=X-Test:hello", "", nil)
	if w.Body.String() != "{}" || w.Header().Get("X-Test") != "" {
		t.Fatalf("disabled features: %s %v", w.Body, w.Header())
	}
	cfg.BodyCommand = Command{Query: "body", Header: "X-Body"}
	w = request(cfg, "GET", "/?body=alias", "", nil)
	if w.Body.String() != `"alias"` {
		t.Fatalf("alias: %s", w.Body)
	}
	cfg.RandomStatusHeader = false
	w = request(cfg, "GET", "/?echo_code=201", "", nil)
	if w.Code != 201 || w.Header().Get("X-Echo-Random-Status") != "" {
		t.Fatal("status header not disabled")
	}
}

func TestEnvironmentAndFilePrecedence(t *testing.T) {
	t.Setenv("ECHO_TEST_VALUE", "environment")
	cfg := config(t)
	cfg.Environment, cfg.File = true, true
	dir := t.TempDir()
	path := filepath.Join(dir, "example.txt")
	if err := os.WriteFile(path, []byte("file contents"), 0600); err != nil {
		t.Fatal(err)
	}
	w := request(cfg, "GET", "/?echo_env_body=ECHO_TEST_VALUE&echo_body=body", "", nil)
	if w.Body.String() != `"environment"` {
		t.Fatal(w.Body.String())
	}
	w = request(cfg, "GET", "/?echo_env_body=ECHO_TEST_VALUE&echo_code=500&echo_file="+url.QueryEscape(path), "", nil)
	if w.Code != 200 || w.Body.String() != "file contents" {
		t.Fatalf("file: %d %s", w.Code, w.Body)
	}
	w = request(cfg, "GET", "/?echo_code=403&echo_file="+url.QueryEscape(dir), "", nil)
	if w.Code != 403 || w.Body.String() != `["example.txt"]` {
		t.Fatalf("directory: %d %s", w.Code, w.Body)
	}
	w = request(cfg, "GET", "/?echo_file="+url.QueryEscape(filepath.Join(dir, "missing")), "", nil)
	if w.Code != 404 || decode(t, w)["error"] != "File or Directory not found" {
		t.Fatalf("missing file: %d %s", w.Code, w.Body)
	}
	w = request(cfg, "GET", "/", "", nil)
	if decode(t, w)["environment"].(map[string]any)["ECHO_TEST_VALUE"] != "environment" {
		t.Fatal("environment not echoed")
	}
}

func TestDelayAndCancellation(t *testing.T) {
	cfg := config(t)
	start := time.Now()
	w := request(cfg, "GET", "/?echo_time=20", "", map[string]string{"X-Echo-Time": "invalid"})
	if w.Code != 200 || time.Since(start) < 20*time.Millisecond {
		t.Fatal("delay not applied")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := httptest.NewRequest("GET", "/?echo_time=60000", nil).WithContext(ctx)
	if err := delay(r, cfg); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
	for _, value := range []string{"NaN", "Inf", "-1", "60001", "invalid"} {
		r := httptest.NewRequest("GET", "/?echo_time="+value, nil).WithContext(ctx)
		if err := delay(r, cfg); err != nil {
			t.Fatalf("invalid delay %s was applied", value)
		}
	}
}

func TestOpenAPIHeadAndLogging(t *testing.T) {
	cfg := config(t)
	w := request(cfg, "GET", "/openapi.yaml", "", nil)
	if !bytes.Equal(w.Body.Bytes(), api.Spec) || !bytes.HasPrefix(api.Spec, []byte("openapi: 3.1.0\n")) || w.Header().Get("Content-Type") != "application/yaml" {
		t.Fatal("spec not served")
	}
	w = request(cfg, "HEAD", "/?echo_body=hello&echo_code=202", "", nil)
	if w.Code != 202 || w.Body.Len() != 0 {
		t.Fatal("HEAD returned a body")
	}
	var logs bytes.Buffer
	cfg.IgnorePing = true
	e := New(cfg, cfg.Logger(&logs))
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/ping", nil))
	if logs.Len() != 0 {
		t.Fatal("ping was logged")
	}
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/hello", nil))
	if !strings.Contains(logs.String(), "/hello") || !strings.Contains(logs.String(), `"echo"`) {
		t.Fatalf("log missing request: %s", &logs)
	}
}

func TestRealHTTPBodylessStatuses(t *testing.T) {
	srv := httptest.NewServer(New(config(t), slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer srv.Close()
	for _, status := range []string{"204", "205", "304"} {
		resp, err := srv.Client().Get(srv.URL + "/?echo_code=" + status)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if err != nil || len(body) != 0 {
			t.Fatalf("bodyless status %s: %q %v", status, body, err)
		}
	}
}

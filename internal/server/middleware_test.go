package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json/v2"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
)

func TestMiddlewareConfiguration(t *testing.T) {
	cfg := config(t)
	if cfg.Gzip || cfg.Secure || cfg.Timeout != 0 || len(cfg.CORSOrigins) != 0 || cfg.RateLimit != 0 {
		t.Fatalf("middleware enabled by default: %+v", cfg)
	}
	env := map[string]string{
		"MIDDLEWARE_GZIP": "true", "MIDDLEWARE_SECURE": "true", "MIDDLEWARE_TIMEOUT": "5s",
		"MIDDLEWARE_CORS_ORIGINS":   "https://example.com, http://localhost:3000",
		"MIDDLEWARE_RATELIMIT_RATE": "2.5", "MIDDLEWARE_RATELIMIT_BURST": "4",
	}
	cfg, err := LoadConfig([]string{"--middleware:timeout=2s"}, func(key string) string { return env[key] }, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Gzip || !cfg.Secure || cfg.Timeout != 2*time.Second || cfg.RateLimit != 2.5 || cfg.RateBurst != 4 || len(cfg.CORSOrigins) != 2 || cfg.CORSOrigins[1] != "http://localhost:3000" {
		t.Fatalf("middleware configuration: %+v", cfg)
	}
}

func TestRequestID(t *testing.T) {
	var previous string
	for _, id := range []string{"", "", "client-request-123"} {
		cfg := config(t)
		var logs bytes.Buffer
		e := New(cfg, cfg.Logger(&logs))
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{"))
		r.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		if id != "" {
			r.Header.Set(echo.HeaderXRequestID, id)
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		got := w.Header().Get(echo.HeaderXRequestID)
		if got == "" || got == previous || (id != "" && got != id) {
			t.Fatalf("request ID %q for input %q", got, id)
		}
		previous = got
		var record map[string]any
		if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		if record["request_id"] != got || record["status"] != float64(400) {
			t.Fatalf("uncorrelated error log: %v", record)
		}
	}
}

func TestContextTimeout(t *testing.T) {
	cfg := config(t)
	cfg.Timeout = 5 * time.Millisecond
	start := time.Now()
	w := request(cfg, "GET", "/?echo_time=60000", "", nil)
	if w.Code != 503 || decode(t, w)["message"] != "Service Unavailable" {
		t.Fatalf("deadline response: %d %s", w.Code, w.Body)
	}
	if elapsed := time.Since(start); elapsed < cfg.Timeout || elapsed > time.Second {
		t.Fatalf("deadline did not interrupt delay: %s", elapsed)
	}
	w = request(cfg, "GET", "/?echo_body=ok", "", nil)
	if w.Code != 200 || w.Body.String() != `"ok"` {
		t.Fatalf("fast request failed: %d %s", w.Code, w.Body)
	}
}

func TestGzipAndSecure(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := config(t)
		cfg.Gzip, cfg.Secure = enabled, enabled
		w := request(cfg, "GET", "/?echo_body=hello", "", map[string]string{"Accept-Encoding": "gzip", "X-Forwarded-Proto": "https"})
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		body := w.Body.Bytes()
		if enabled {
			if w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
				t.Fatalf("middleware headers: %v", w.Header())
			}
			gz, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			body, err = io.ReadAll(gz)
			if err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
		} else if w.Header().Get("Content-Encoding") != "" || w.Header().Get("X-Content-Type-Options") != "" {
			t.Fatalf("opt-in middleware enabled by default: %v", w.Header())
		}
		if string(body) != `"hello"` {
			t.Fatalf("wrong body: %q", body)
		}
		if w.Header().Get("Strict-Transport-Security") != "" {
			t.Fatal("unexpected HSTS")
		}
	}
	cfg := config(t)
	cfg.Gzip = true
	for _, target := range []string{"/?echo_code=204", "/?echo_code=205", "/?echo_code=304"} {
		w := request(cfg, "GET", target, "", map[string]string{"Accept-Encoding": "gzip"})
		if w.Body.Len() != 0 || w.Header().Get("Content-Encoding") != "" {
			t.Fatalf("gzip added a body to %s", target)
		}
	}
	w := request(cfg, "POST", "/", "{", map[string]string{"Accept-Encoding": "gzip", "Content-Type": "application/json"})
	if w.Code != 400 || decode(t, w)["message"] != "expected a JSON object or array" {
		t.Fatalf("gzip broke error handling: %d %s", w.Code, w.Body)
	}
}

func TestCORS(t *testing.T) {
	cfg := config(t)
	preflight := map[string]string{"Origin": "https://example.com", "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type,x-echo-body"}
	w := request(cfg, "OPTIONS", "/", "", preflight)
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS changed default OPTIONS")
	}
	cfg.CORSOrigins = []string{"https://example.com"}
	w = request(cfg, "OPTIONS", "/", "", preflight)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "https://example.com" || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "x-echo-body") {
		t.Fatalf("preflight: %d %v", w.Code, w.Header())
	}
	if w.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("credentials unexpectedly allowed")
	}
	w = request(cfg, "GET", "/", "", map[string]string{"Origin": "https://example.com.evil"})
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("untrusted origin allowed")
	}
	w = request(cfg, "OPTIONS", "/", "", nil)
	if w.Code != 200 {
		t.Fatal("non-preflight OPTIONS did not echo")
	}
}

func TestRateLimiterPeerIsolation(t *testing.T) {
	cfg := config(t)
	cfg.RateLimit, cfg.RateBurst = 0.01, 1
	e := New(cfg, cfg.Logger(io.Discard))
	for _, test := range []struct {
		peer, forwarded string
		status          int
	}{
		{"10.0.0.1:1234", "203.0.113.1", 200},
		{"10.0.0.1:4321", "203.0.113.2", 429},
		{"10.0.0.2:1234", "203.0.113.1", 200},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = test.peer
		r.Header.Set("X-Forwarded-For", test.forwarded)
		r.Header.Set("X-Real-IP", test.forwarded)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("peer %s: %d, want %d", test.peer, w.Code, test.status)
		}
		if test.status == 429 && (w.Header().Get("Retry-After") == "" || w.Header().Get(echo.HeaderXRequestID) == "") {
			t.Fatalf("rate limit headers: %v", w.Header())
		}
	}
}

func TestMultipartFilesRejected(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("name", "example"); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("file", "../../unexpected.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, "file contents"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	w := request(config(t), "POST", "/upload", body.String(), map[string]string{"Content-Type": writer.FormDataContentType()})
	if w.Code != 400 || decode(t, w)["message"] != "file uploads are not supported" {
		t.Fatalf("file upload accepted: %d %s", w.Code, w.Body)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("multipart temporary files leaked: %v", entries)
	}
}

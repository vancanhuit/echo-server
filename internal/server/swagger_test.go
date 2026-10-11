package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSwaggerUI(t *testing.T) {
	cfg := config(t)
	e := New(cfg, cfg.Logger(io.Discard))
	for _, test := range []struct {
		method, path string
		status       int
		contentType  string
	}{
		{"GET", "/swagger", 308, ""},
		{"HEAD", "/swagger", 308, ""},
		{"GET", "/swagger/", 200, "text/html"},
		{"HEAD", "/swagger/", 200, "text/html"},
		{"GET", "/swagger/swagger-ui.css", 200, "text/css"},
		{"GET", "/swagger/swagger-ui-bundle.js", 200, "javascript"},
		{"HEAD", "/swagger/swagger-ui-bundle.js", 200, "javascript"},
		{"GET", "/swagger/swagger-ui-standalone-preset.js", 200, "javascript"},
		{"GET", "/swagger/missing.js", 404, ""},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			r := httptest.NewRequest(test.method, test.path, nil).WithContext(t.Context())
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("status %d, want %d", w.Code, test.status)
			}
			if !strings.Contains(w.Header().Get("Content-Type"), test.contentType) {
				t.Fatalf("content type: %v", w.Header())
			}
			if test.status == 308 && w.Header().Get("Location") != "/swagger/" {
				t.Fatalf("redirect: %v", w.Header())
			}
			if test.method == http.MethodHead && w.Body.Len() != 0 {
				t.Fatal("HEAD returned a body")
			}
			if test.path == "/swagger/" && test.method == http.MethodGet {
				for _, value := range []string{"SwaggerUIBundle", "/openapi.yaml", "/swagger/swagger-ui-bundle.js", "validatorUrl: null"} {
					if !strings.Contains(w.Body.String(), value) {
						t.Fatalf("UI missing %q", value)
					}
				}
			}
			if w.Header().Get("X-Request-ID") == "" {
				t.Fatal("documentation bypassed request ID middleware")
			}
		})
	}
	for _, method := range []string{"POST", "OPTIONS", "CUSTOM"} {
		for _, path := range []string{"/swagger", "/swagger/", "/swagger/example"} {
			w := request(cfg, method, path, "hello", map[string]string{"Content-Type": "text/plain"})
			if w.Code != 200 || decode(t, w)["request"].(map[string]any)["body"] != "hello" {
				t.Fatalf("%s %s did not echo: %d %s", method, path, w.Code, w.Body)
			}
		}
	}
}

func TestSwaggerWithGzipAndSecure(t *testing.T) {
	cfg := config(t)
	cfg.Gzip, cfg.Secure = true, true
	srv := httptest.NewServer(New(cfg, cfg.Logger(io.Discard)))
	defer srv.Close()
	for _, path := range []string{"/swagger/", "/swagger/swagger-ui-bundle.js", "/swagger/swagger-ui.css", "/openapi.yaml"} {
		r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Accept-Encoding", "gzip")
		resp, err := srv.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		var reader io.Reader = resp.Body
		if resp.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(reader)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = gz.Close() }()
			reader = gz
		}
		body, err := io.ReadAll(reader)
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 || len(body) == 0 {
			t.Fatalf("%s: status %d, body length %d", path, resp.StatusCode, len(body))
		}
		if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
			t.Fatalf("%s was gzip-compressed twice", path)
		}
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("documentation bypassed Secure middleware")
		}
	}
}

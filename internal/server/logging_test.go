package server

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestRequestLoggingAndErrors(t *testing.T) {
	for _, test := range []struct {
		name, method, target, body string
		handler                    echo.HandlerFunc
		status                     int
		message                    string
		failed                     bool
	}{
		{name: "spec", method: "GET", target: "/openapi.yaml", status: 200},
		{name: "controlled status", method: "GET", target: "/?echo_code=503", status: 503},
		{name: "malformed JSON", method: "POST", target: "/", body: "{", status: 400, message: "expected a JSON object or array", failed: true},
		{name: "body limit", method: "POST", target: "/", body: strings.Repeat("x", maxBodyBytes+1), status: 413, message: "request body exceeds 1 MiB", failed: true},
		{name: "returned error", method: "GET", target: "/failure", handler: func(*echo.Context) error { return errors.New("private diagnostic") }, status: 500, message: "Internal Server Error", failed: true},
		{name: "wrapped HTTP error", method: "GET", target: "/failure", handler: func(*echo.Context) error {
			return errors.Join(errors.New("private diagnostic"), echo.NewHTTPError(409, "conflict"))
		}, status: 409, message: "conflict", failed: true},
		{name: "panic", method: "GET", target: "/failure", handler: func(*echo.Context) error { panic("private diagnostic") }, status: 500, message: "Internal Server Error", failed: true},
		{name: "HEAD error", method: "HEAD", target: "/failure", handler: func(*echo.Context) error { return errors.New("private diagnostic") }, status: 500, failed: true},
		{name: "committed response", method: "GET", target: "/failure", handler: func(c *echo.Context) error {
			if err := c.String(202, "already sent"); err != nil {
				return err
			}
			return errors.New("private diagnostic")
		}, status: 202, failed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config(t)
			var logs bytes.Buffer
			e := New(cfg, cfg.Logger(&logs))
			if test.handler != nil {
				e.Add(test.method, "/failure", test.handler)
			}
			r := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body)).WithContext(t.Context())
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("status %d, want %d: %s", w.Code, test.status, w.Body)
			}
			if test.message != "" {
				if got := decode(t, w)["message"]; got != test.message {
					t.Fatalf("message %v, want %q", got, test.message)
				}
				if w.Header().Get("Content-Type") != echo.MIMEApplicationJSON {
					t.Fatalf("error is not JSON: %v", w.Header())
				}
			}
			if strings.Contains(w.Body.String(), "private diagnostic") {
				t.Fatal("internal error leaked to client")
			}
			if test.method == http.MethodHead && w.Body.Len() != 0 {
				t.Fatal("HEAD error has a body")
			}
			if test.name == "committed response" && w.Body.String() != "already sent" {
				t.Fatalf("error handler rewrote committed response: %s", w.Body)
			}
			var record map[string]any
			// Unmarshal rejects multiple JSON values, catching duplicate request logs too.
			if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
				t.Fatalf("expected one JSON log: %v\n%s", err, &logs)
			}
			if record["status"] != float64(test.status) || record["method"] != test.method || record["uri"] != test.target {
				t.Fatalf("incorrect request log: %v", record)
			}
			if record["latency_ms"] == nil || record["response_size"] != float64(w.Body.Len()) {
				t.Fatalf("missing metrics: %v", record)
			}
			if test.failed {
				if record["level"] != "ERROR" || record["error"] == nil {
					t.Fatalf("failure not logged: %v", record)
				}
			} else if record["level"] != "INFO" {
				t.Fatalf("unexpected level: %v", record)
			}
		})
	}
}

func TestLogFormats(t *testing.T) {
	for _, format := range []string{"default", "line", "object"} {
		t.Run(format, func(t *testing.T) {
			cfg := config(t)
			cfg.LogFormat, cfg.Cookies = format, false
			var logs bytes.Buffer
			e := New(cfg, cfg.Logger(&logs))
			r := httptest.NewRequest("POST", "/?echo_time=5", strings.NewReader(`{"hello":"world"}`)).WithContext(t.Context())
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Cookie", "secret=value")
			e.ServeHTTP(httptest.NewRecorder(), r)
			var record map[string]any
			if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			_, hasEcho := record["echo"]
			if hasEcho != (format != "line") || (record["msg"] != "") != (format != "object") {
				t.Fatalf("wrong format: %v", record)
			}
			if record["latency_ms"].(float64) < 5 {
				t.Fatalf("delay missing from latency: %v", record)
			}
			if strings.Contains(logs.String(), "secret=value") {
				t.Fatal("disabled cookies leaked into logs")
			}
			if hasEcho {
				body := record["echo"].(map[string]any)["request"].(map[string]any)["body"]
				if body.(map[string]any)["hello"] != "world" {
					t.Fatalf("body not preserved in logs: %v", body)
				}
			}
		})
	}
}

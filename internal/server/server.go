package server

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/vancanhuit/echo-server/api"
	"golang.org/x/net/http/httpguts"
)

func New(cfg Config, logger *slog.Logger) *echo.Echo {
	e := echo.New()
	e.Logger = logger
	e.IPExtractor = echo.ExtractIPDirect()
	e.SchemeExtractor = echo.ExtractSchemeDirect()
	e.Use(middleware.RequestID(), requestLogger(cfg), middleware.Recover())
	if cfg.Secure {
		e.Use(middleware.Secure())
	}
	if len(cfg.CORSOrigins) != 0 {
		e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
			AllowOrigins:  cfg.CORSOrigins,
			ExposeHeaders: []string{echo.HeaderXRequestID, "X-Echo-Random-Status", "X-RateLimit-Limit", "X-RateLimit-Remaining", echo.HeaderRetryAfter},
		}))
	}
	if cfg.RateLimit > 0 {
		e.Use(middleware.RateLimiter(middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
			Rate: cfg.RateLimit, Burst: cfg.RateBurst,
		})))
	}
	if cfg.Timeout > 0 {
		e.Use(middleware.ContextTimeout(cfg.Timeout))
	}
	if cfg.Gzip {
		e.Use(middleware.Gzip())
	}
	e.GET("/openapi.yaml", func(c *echo.Context) error { return c.Blob(200, "application/yaml", api.Spec) })
	e.HEAD("/openapi.yaml", func(c *echo.Context) error { return c.NoContent(200) })
	handler := func(c *echo.Context) error { return handle(c, cfg) }
	e.Any("/*", handler)
	// Unknown HTTP methods should echo too, just as Express app.all('*') does.
	e.RouteNotFound("/*", handler)
	return e
}

func handle(c *echo.Context, cfg Config) error {
	r := c.Request()
	if err := r.Context().Err(); err != nil {
		return err
	}
	body, err := parseBody(r)
	if err := r.Context().Err(); err != nil {
		return err
	}
	if err != nil {
		return err
	}
	payload := snapshot(r, body, cfg)
	c.Set(echoLogKey, payload)
	if err := delay(r, cfg); err != nil {
		return err
	}
	status := statusCode(c, cfg)
	if cfg.Header {
		if err := responseHeaders(c, cfg.HeaderCommand.values(r)); err != nil {
			return err
		}
	}
	if cfg.File {
		if path := cfg.FileCommand.value(r); path != "" {
			return fileResponse(c, path, status)
		}
	}
	if cfg.Environment {
		if key := cfg.EnvCommand.value(r); key != "" {
			value, ok := os.LookupEnv(key)
			if !ok {
				return c.NoContent(status)
			}
			return respond(c, status, value)
		}
	}
	if value := cfg.BodyCommand.value(r); value != "" {
		if raw := jsontext.Value(value); raw.IsValid() {
			return respond(c, status, raw)
		}
		return respond(c, status, value)
	}
	return respond(c, status, payload)
}

func delay(r *http.Request, cfg Config) error {
	for _, value := range cfg.TimeCommand.values(r) {
		ms, err := strconv.ParseFloat(value, 64)
		if err != nil || !(ms >= float64(cfg.MinDelay)/float64(time.Millisecond) && ms <= float64(cfg.MaxDelay)/float64(time.Millisecond)) || ms == 0 {
			continue
		}
		timer := time.NewTimer(time.Duration(ms * float64(time.Millisecond)))
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil
		case <-r.Context().Done():
			return r.Context().Err()
		}
	}
	return nil
}

func statusCode(c *echo.Context, cfg Config) int {
	status := http.StatusOK
	for _, value := range cfg.CodeCommand.values(c.Request()) {
		var valid []string
		for part := range strings.SplitSeq(value, "-") {
			number := strings.TrimPrefix(part, "+")
			n, err := strconv.Atoi(number)
			if err == nil && n >= 200 && n <= 599 && strconv.Itoa(n) == number {
				valid = append(valid, part)
			}
		}
		if len(valid) > 0 {
			status, _ = strconv.Atoi(valid[rand.IntN(len(valid))])
			if cfg.RandomStatusHeader {
				c.Response().Header().Set("X-Echo-Random-Status", strings.Join(valid, ", "))
			}
		}
	}
	return status
}

func responseHeaders(c *echo.Context, values []string) error {
	for _, value := range values {
		if value == "" {
			continue
		}
		for item := range strings.SplitSeq(value, ", ") {
			name, value, found := strings.Cut(item, ":")
			name, value = strings.TrimSpace(name), strings.TrimSpace(value)
			if !found || !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
				return echo.NewHTTPError(400, "invalid response header")
			}
			// Framing belongs to net/http, never to untrusted response controls.
			switch strings.ToLower(name) {
			case "content-length", "transfer-encoding", "connection", "trailer", "upgrade", "keep-alive", "proxy-connection":
				return echo.NewHTTPError(400, "response framing headers cannot be overridden")
			}
			c.Response().Header().Set(name, value)
		}
	}
	return nil
}

func snapshot(r *http.Request, body any, cfg Config) api.EchoResponse {
	result := api.EchoResponse{}
	if cfg.Host {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		ip := r.RemoteAddr
		if h, _, err := net.SplitHostPort(ip); err == nil {
			ip = h
		}
		result.Host = &api.Host{Hostname: host, Ip: ip, Ips: []string{}}
	}
	if cfg.HTTP {
		result.Http = &api.HTTP{Method: r.Method, BaseUrl: "", OriginalUrl: r.URL.RequestURI(), Protocol: protocol(r)}
	}
	if cfg.Request {
		headers := map[string]any{"host": r.Host}
		for name, values := range r.Header {
			name = strings.ToLower(name)
			if name == "cookie" && !cfg.Cookies {
				continue
			}
			switch name {
			case "set-cookie":
				headers[name] = values
			case "cookie":
				headers[name] = strings.Join(values, "; ")
			default:
				headers[name] = strings.Join(values, ", ")
			}
		}
		var cookies any = []string{}
		if cfg.Cookies {
			values := map[string]any{}
			for _, cookie := range r.Cookies() {
				if _, exists := values[cookie.Name]; exists {
					continue
				}
				value, err := url.PathUnescape(cookie.Value)
				if err != nil {
					value = cookie.Value
				}
				if raw, ok := strings.CutPrefix(value, "j:"); ok && jsontext.Value(raw).IsValid() {
					values[cookie.Name] = jsontext.Value(raw)
				} else {
					values[cookie.Name] = value
				}
			}
			cookies = values
		}
		result.Request = &api.Request{Params: map[string]any{"0": r.URL.Path}, Query: flatten(r.URL.Query()), Cookies: cookies, Body: body, Headers: headers}
	}
	if cfg.Environment {
		env := map[string]string{}
		for _, entry := range os.Environ() {
			key, value, _ := strings.Cut(entry, "=")
			env[key] = value
		}
		result.Environment = &env
	}
	return result
}

func protocol(r *http.Request) api.HTTPProtocol {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func respond(c *echo.Context, status int, value any) error {
	if c.Request().Method == http.MethodHead || status == 204 || status == 205 || status == 304 {
		return c.NoContent(status)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if c.Response().Header().Get("Content-Type") == "" {
		c.Response().Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	return c.Blob(status, c.Response().Header().Get("Content-Type"), data)
}

func fileResponse(c *echo.Context, path string, status int) error {
	missing := func() error { return respond(c, 404, map[string]string{"error": "File or Directory not found"}) }
	info, err := os.Stat(path)
	if err != nil {
		return missing()
	}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return missing()
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		return respond(c, status, names)
	}
	if !info.Mode().IsRegular() {
		return missing()
	}
	file, err := os.Open(path)
	if err != nil {
		return missing()
	}
	defer func() { _ = file.Close() }()
	// Recheck the opened file so replacement with a special device isn't streamed.
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return missing()
	}
	if c.Request().Method == http.MethodHead {
		return c.NoContent(200)
	}
	return c.Stream(200, "text/plain; charset=utf-8", file)
}

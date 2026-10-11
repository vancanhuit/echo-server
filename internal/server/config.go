package server

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5/middleware"
	"golang.org/x/net/http/httpguts"
)

type Command struct {
	Query  string
	Header string
}

type Config struct {
	Port                                                                          int
	Host, HTTP, Request, Cookies, Environment, File, Header                       bool
	MinDelay, MaxDelay                                                            time.Duration
	RandomStatusHeader                                                            bool
	BodyCommand, EnvCommand, CodeCommand, HeaderCommand, TimeCommand, FileCommand Command
	LogApp, LogLevel, LogFormat                                                   string
	IgnorePing                                                                    bool
	Gzip, Secure                                                                  bool
	Timeout                                                                       time.Duration
	CORSOrigins                                                                   []string
	RateLimit                                                                     float64
	RateBurst                                                                     int
}

// LoadConfig accepts the upstream colon-separated CLI flags; CLI overrides environment.
func LoadConfig(args []string, getenv func(string) string, output io.Writer) (Config, error) {
	c := Config{}
	fs := flag.NewFlagSet("echo-server", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.IntVar(&c.Port, "port", 80, "HTTP listen port")
	for _, item := range []struct {
		name  string
		value *bool
	}{
		{"host", &c.Host}, {"http", &c.HTTP}, {"request", &c.Request},
		{"cookies", &c.Cookies}, {"environment", &c.Environment}, {"file", &c.File}, {"header", &c.Header},
	} {
		fs.BoolVar(item.value, "enable:"+item.name, true, "enable "+item.name)
	}
	var minMS, maxMS int64
	fs.Int64Var(&minMS, "controls:times:min", 0, "minimum delay in milliseconds")
	fs.Int64Var(&maxMS, "controls:times:max", 60000, "maximum delay in milliseconds")
	fs.BoolVar(&c.RandomStatusHeader, "commands:httpCode:headerResponse", true, "emit status candidates")
	for _, item := range []struct {
		name, suffix string
		command      *Command
	}{
		{"httpBody", "body", &c.BodyCommand}, {"httpEnvBody", "env_body", &c.EnvCommand},
		{"httpCode", "code", &c.CodeCommand}, {"httpHeaders", "header", &c.HeaderCommand},
		{"time", "time", &c.TimeCommand}, {"file", "file", &c.FileCommand},
	} {
		fs.StringVar(&item.command.Query, "commands:"+item.name+":query", "echo_"+item.suffix, "query command name")
		fs.StringVar(&item.command.Header, "commands:"+item.name+":header", "x-echo-"+strings.ReplaceAll(item.suffix, "_", "-"), "header command name")
	}
	fs.StringVar(&c.LogApp, "logs:app", "echo-server", "application name in logs")
	fs.StringVar(&c.LogLevel, "logs:level", "debug", "trace, debug, info, warn, error, or fatal")
	fs.StringVar(&c.LogFormat, "logs:format", "default", "default, line, or object")
	fs.BoolVar(&c.IgnorePing, "logs:ignore:ping", false, "omit /ping request logs")
	fs.BoolVar(&c.Gzip, "middleware:gzip", false, "enable gzip response compression")
	fs.BoolVar(&c.Secure, "middleware:secure", false, "enable Echo security headers")
	fs.DurationVar(&c.Timeout, "middleware:timeout", 0, "cooperative request timeout (e.g. 5s); 0 disables")
	var corsOrigins string
	fs.StringVar(&corsOrigins, "middleware:cors:origins", "", "comma-separated allowed origins; empty disables CORS")
	fs.Float64Var(&c.RateLimit, "middleware:rateLimit:rate", 0, "requests per second per peer IP; 0 disables rate limiting")
	fs.IntVar(&c.RateBurst, "middleware:rateLimit:burst", 1, "maximum immediate requests per peer IP")
	var envErr error
	fs.VisitAll(func(f *flag.Flag) {
		key := strings.ToUpper(strings.ReplaceAll(f.Name, ":", "_"))
		if value := getenv(key); value != "" {
			if err := fs.Set(f.Name, value); err != nil && envErr == nil {
				envErr = fmt.Errorf("%s: %w", key, err)
			}
		}
	})
	if envErr != nil {
		return c, envErr
	}
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if fs.NArg() != 0 {
		return c, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if c.Port < 1 || c.Port > 65535 {
		return c, fmt.Errorf("port must be between 1 and 65535")
	}
	if c.Timeout < 0 {
		return c, fmt.Errorf("middleware timeout must not be negative")
	}
	if c.RateLimit < 0 || math.IsNaN(c.RateLimit) || math.IsInf(c.RateLimit, 0) || c.RateBurst < 1 {
		return c, fmt.Errorf("rate limit must be finite and nonnegative, with burst at least 1")
	}
	if corsOrigins != "" {
		for origin := range strings.SplitSeq(corsOrigins, ",") {
			c.CORSOrigins = append(c.CORSOrigins, strings.TrimSpace(origin))
		}
		if _, err := (middleware.CORSConfig{AllowOrigins: c.CORSOrigins}).ToMiddleware(); err != nil {
			return c, fmt.Errorf("CORS origins: %w", err)
		}
	}
	if minMS < 0 || maxMS < minMS || maxMS > int64((1<<63-1)/time.Millisecond) {
		return c, fmt.Errorf("delay bounds must satisfy 0 <= min <= max and fit a time.Duration")
	}
	c.MinDelay, c.MaxDelay = time.Duration(minMS)*time.Millisecond, time.Duration(maxMS)*time.Millisecond
	for _, cmd := range []Command{c.BodyCommand, c.EnvCommand, c.CodeCommand, c.HeaderCommand, c.TimeCommand, c.FileCommand} {
		if cmd.Query == "" || !httpguts.ValidHeaderFieldName(cmd.Header) {
			return c, fmt.Errorf("command query and header names must be valid and nonempty")
		}
	}
	if _, err := c.level(); err != nil {
		return c, err
	}
	switch c.LogFormat {
	case "default", "line", "object":
	default:
		return c, fmt.Errorf("invalid log format %q", c.LogFormat)
	}
	if enabled, _ := strconv.ParseBool(getenv("LOGS_SEQ_ENABLED")); enabled {
		return c, fmt.Errorf("direct Seq transport is not supported; collect JSON stdout with your log shipper")
	}
	return c, nil
}

func (c Config) level() (slog.Level, error) {
	switch c.LogLevel {
	case "trace":
		return slog.LevelDebug - 4, nil
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	case "fatal":
		return slog.LevelError + 4, nil
	default:
		return 0, fmt.Errorf("invalid log level %q", c.LogLevel)
	}
}

func (c Config) Logger(w io.Writer) *slog.Logger {
	level, _ := c.level()
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})).With("name", c.LogApp)
}

func (cmd Command) values(r *http.Request) []string {
	return []string{r.Header.Get(cmd.Header), r.URL.Query().Get(cmd.Query)}
}

func (cmd Command) value(r *http.Request) string {
	for _, value := range cmd.values(r) {
		if value != "" {
			return value
		}
	}
	return ""
}

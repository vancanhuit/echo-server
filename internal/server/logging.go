package server

import (
	"fmt"
	"log/slog"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

const echoLogKey = "echo-server.request"

func requestLogger(cfg Config) echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		Skipper: func(c *echo.Context) bool {
			return cfg.IgnorePing && c.Request().URL.RequestURI() == "/ping"
		},
		HandleError:     true,
		LogMethod:       true,
		LogURI:          true,
		LogStatus:       true,
		LogLatency:      true,
		LogResponseSize: true,
		LogRequestID:    true,
		LogValuesFunc: func(c *echo.Context, v middleware.RequestLoggerValues) error {
			attrs := []any{"request_id", v.RequestID, "method", v.Method, "uri", v.URI, "status", v.Status,
				"latency_ms", float64(v.Latency.Microseconds()) / 1000, "response_size", v.ResponseSize}
			if payload := c.Get(echoLogKey); cfg.LogFormat != "line" && payload != nil {
				attrs = append(attrs, "echo", payload)
			}
			message := ""
			if cfg.LogFormat != "object" {
				message = fmt.Sprintf("[%s] - %s://%s%s", v.Method, protocol(c.Request()), c.Request().Host, v.URI)
			}
			level := slog.LevelInfo
			if v.Error != nil {
				level = slog.LevelError
				attrs = append(attrs, "error", v.Error)
			}
			c.Logger().Log(c.Request().Context(), level, message, attrs...)
			return nil
		},
	})
}

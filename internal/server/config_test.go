package server

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestConfiguration(t *testing.T) {
	cfg, err := LoadConfig(nil, func(string) string { return "" }, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 80 || !cfg.Environment || !cfg.File || cfg.MaxDelay != time.Minute || cfg.BodyCommand.Query != "echo_body" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	env := map[string]string{"PORT": "9090", "ENABLE_COOKIES": "false", "COMMANDS_HTTPBODY_QUERY": "body", "COMMANDS_HTTPCODE_HEADERRESPONSE": "false"}
	cfg, err = LoadConfig([]string{"--port", "8081"}, func(key string) string { return env[key] }, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8081 || cfg.Cookies || cfg.RandomStatusHeader || cfg.BodyCommand.Query != "body" {
		t.Fatalf("precedence/overrides: %+v", cfg)
	}
	for _, args := range [][]string{
		{"--middleware:timeout=-1s"}, {"--middleware:timeout=invalid"},
		{"--middleware:rateLimit:rate=NaN"}, {"--middleware:rateLimit:rate=Inf"}, {"--middleware:rateLimit:rate=-1"},
		{"--middleware:rateLimit:burst=0"}, {"--middleware:rateLimit:burst=-1"},
		{"--middleware:cors:origins=https://example.com/path"}, {"--middleware:cors:origins=invalid"},
		{"--middleware:cors:origins=https://example.com,"},
		{"--port=0"}, {"--port=65536"}, {"--controls:times:min=-1"},
		{"--controls:times:min=10", "--controls:times:max=5"}, {"--controls:times:max=9223372036854775807"},
		{"--commands:httpBody:header=bad header"}, {"--commands:time:query="},
		{"--logs:level=nope"}, {"--logs:format=nope"}, {"--enable:file=perhaps"}, {"unexpected"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := LoadConfig(args, func(string) string { return "" }, io.Discard); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
	if _, err := LoadConfig(nil, func(key string) string {
		if key == "PORT" {
			return "invalid"
		}
		return ""
	}, io.Discard); err == nil {
		t.Fatal("expected invalid environment error")
	}
}

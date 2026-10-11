package server

import (
	"compress/gzip"
	"compress/zlib"
	"encoding/json/jsontext"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v5"
)

const maxBodyBytes = 1 << 20

func parseBody(r *http.Request) (any, error) {
	var reader io.Reader = r.Body
	var compressed io.ReadCloser
	var err error
	switch strings.ToLower(r.Header.Get("Content-Encoding")) {
	case "", "identity":
	case "gzip":
		compressed, err = gzip.NewReader(reader)
	case "deflate":
		compressed, err = zlib.NewReader(reader)
	default:
		return nil, echo.NewHTTPError(415, "unsupported content encoding")
	}
	if err != nil {
		return nil, echo.NewHTTPError(400, "invalid compressed body")
	}
	if compressed != nil {
		defer func() { _ = compressed.Close() }()
		reader = compressed
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxBodyBytes+1))
	if err != nil {
		return nil, echo.NewHTTPError(400, "cannot read request body")
	}
	if len(data) > maxBodyBytes {
		return nil, echo.NewHTTPError(413, "request body exceeds 1 MiB")
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil && r.Header.Get("Content-Type") != "" {
		return nil, echo.NewHTTPError(400, "invalid content type")
	}
	switch mediaType {
	case "application/json", "application/ld+json":
		value := jsontext.Value(data)
		trimmed := strings.TrimSpace(string(data))
		if !value.IsValid() || (trimmed[0] != '{' && trimmed[0] != '[') {
			return nil, echo.NewHTTPError(400, "expected a JSON object or array")
		}
		return value, nil
	case "text/plain":
		return string(data), nil
	case "application/x-www-form-urlencoded":
		values, err := url.ParseQuery(string(data))
		if err != nil {
			return nil, echo.NewHTTPError(400, "invalid form body")
		}
		return flatten(values), nil
	case "multipart/form-data":
		if params["boundary"] == "" {
			return nil, echo.NewHTTPError(400, "missing multipart boundary")
		}
		form, err := multipart.NewReader(strings.NewReader(string(data)), params["boundary"]).ReadForm(maxBodyBytes)
		if err != nil {
			return nil, echo.NewHTTPError(400, "invalid multipart body")
		}
		defer func() { _ = form.RemoveAll() }()
		if len(form.File) != 0 {
			return nil, echo.NewHTTPError(400, "file uploads are not supported")
		}
		return flatten(form.Value), nil
	default:
		return map[string]any{}, nil
	}
}

func flatten(values url.Values) map[string]any {
	result := make(map[string]any, len(values))
	for key, values := range values {
		if len(values) == 1 {
			result[key] = values[0]
		} else {
			result[key] = values
		}
	}
	return result
}

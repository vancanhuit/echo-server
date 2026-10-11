// Package api contains the embedded OpenAPI specification and generated models.
package api

import _ "embed"

//go:generate oapi-codegen --config oapi-codegen.yaml -o models.gen.go openapi.yaml

//go:embed openapi.yaml
var Spec []byte

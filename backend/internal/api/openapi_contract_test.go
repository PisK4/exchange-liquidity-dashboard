package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAPIResponsesHaveSchemas(t *testing.T) {
	spec := readOpenAPISpec(t, filepath.Join("..", "..", "docs", "openapi.json"))
	components, ok := spec["components"].(map[string]any)
	if !ok {
		t.Fatalf("components missing")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatalf("components.schemas missing")
	}
	for _, name := range []string{
		"HealthResponse",
		"SymbolsResponse",
		"CoverageResponse",
		"DashboardMeta",
		"LiquiditySnapshot",
		"QualitySnapshot",
		"ShareSnapshot",
		"Top30Snapshot",
		"CollectionStatusResponse",
		"RuntimeConfig",
	} {
		if _, ok := schemas[name]; !ok {
			t.Fatalf("components.schemas.%s missing", name)
		}
	}

	paths := spec["paths"].(map[string]any)
	for path, rawPath := range paths {
		pathSpec := rawPath.(map[string]any)
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			operation, ok := pathSpec[method].(map[string]any)
			if !ok {
				continue
			}
			response := operation["responses"].(map[string]any)["200"].(map[string]any)
			content, ok := response["content"].(map[string]any)
			if !ok {
				t.Fatalf("%s %s 200 response missing content", method, path)
			}
			media, ok := content["application/json"].(map[string]any)
			if !ok {
				t.Fatalf("%s %s 200 response missing application/json content", method, path)
			}
			schema, ok := media["schema"].(map[string]any)
			if !ok || len(schema) == 0 {
				t.Fatalf("%s %s 200 response missing schema", method, path)
			}
		}
	}
}

func TestSwaggerMirrorsOpenAPI(t *testing.T) {
	openapi, err := os.ReadFile(filepath.Join("..", "..", "docs", "openapi.json"))
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	swagger, err := os.ReadFile(filepath.Join("..", "..", "docs", "swagger.json"))
	if err != nil {
		t.Fatalf("read swagger: %v", err)
	}
	if string(openapi) != string(swagger) {
		t.Fatalf("swagger.json must mirror openapi.json; run make swagger")
	}
}

func readOpenAPISpec(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return spec
}

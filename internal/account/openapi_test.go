package account

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

const specFile = "../../api/openapi.json"

// TestOpenAPISpecIsCurrent keeps api/openapi.json, which the web app's
// types are generated from, in step with the handlers. Regenerate with
// UPDATE_OPENAPI=1 go test ./internal/account.
func TestOpenAPISpecIsCurrent(t *testing.T) {
	api, _ := NewAPI(nil, nil)
	got, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if os.Getenv("UPDATE_OPENAPI") != "" {
		if err := os.MkdirAll("../../api", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(specFile, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(specFile)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("api/openapi.json is out of date (%v); run UPDATE_OPENAPI=1 go test ./internal/account", err)
	}
}

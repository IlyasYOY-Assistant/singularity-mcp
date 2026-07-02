package singularity

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/IlyasYOY/singularity-mcp/internal/config"
	"github.com/IlyasYOY/singularity-mcp/openapi"
)

func TestLiveReadOnlyListProjects(t *testing.T) {
	if os.Getenv("SINGULARITY_LIVE") != "1" {
		t.Skip("set SINGULARITY_LIVE=1 to run live read-only tests")
	}
	token := os.Getenv("SINGULARITY_TOKEN")
	if token == "" {
		t.Fatal("SINGULARITY_TOKEN is required for live tests")
	}
	baseURL := os.Getenv("SINGULARITY_BASE_URL")
	if baseURL == "" {
		baseURL = config.DefaultBaseURL
	}

	catalog, err := NewCatalog(openapi.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	op, ok := catalog.Operation("singularity_projects", "list")
	if !ok {
		t.Fatal("projects list operation missing")
	}
	client, err := NewAPIClient(baseURL, token, config.DefaultTimeout)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := client.Call(context.Background(), op, map[string]any{"maxCount": float64(1)})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatalf("invalid JSON: %s", raw)
	}
}

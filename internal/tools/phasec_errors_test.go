package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IlyasYOY/singularity-mcp/internal/singularity"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestStableClientErrorsCrossMCPBoundary(t *testing.T) {
	tests := []struct {
		name       string
		clientOpts []singularity.APIClientOption
		args       map[string]any
		handler    http.HandlerFunc
		want       []string
	}{
		{
			name:       "response too large",
			clientOpts: []singularity.APIClientOption{singularity.WithMaxResponseBytes(16)},
			args:       map[string]any{"operation": "list"},
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"projects":[]}` + strings.Repeat(" ", 20)))
			},
			want: []string{`"type":"response_too_large"`, `"message":`, `"limit":16`},
		},
		{
			name:       "pagination stalled",
			clientOpts: []singularity.APIClientOption{singularity.WithPaginationLimits(3, 3000)},
			args:       map[string]any{"operation": "list", "all": true},
			handler: func(w http.ResponseWriter, r *http.Request) {
				items := make([]map[string]any, 1000)
				for i := range items {
					items[i] = map[string]any{"id": i}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"projects": items})
			},
			want: []string{`"type":"pagination_stalled"`, `"message":`, `"offset":1000`},
		},
		{
			name:       "exhausted transient GET",
			clientOpts: []singularity.APIClientOption{singularity.WithSleeper(func(context.Context, time.Duration) error { return nil })},
			args:       map[string]any{"operation": "list"},
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`private body`))
			},
			want: []string{`"type":"api_error"`, `"message":`, `"status":503`, `"method":"GET"`, `"path":"/v2/project"`, `"retriable":true`, `"attempts":3`, `"retryAfter":2`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := testCatalog(t)
			api := httptest.NewServer(tt.handler)
			defer api.Close()
			apiClient, err := singularity.NewAPIClient(api.URL, "secret-token", time.Second, tt.clientOpts...)
			if err != nil {
				t.Fatal(err)
			}
			srv := NewServerWithOptions(apiClient, catalog, "test", Options{RequireWriteApproval: false})
			mcpClient, err := client.NewInProcessClient(srv)
			if err != nil {
				t.Fatal(err)
			}
			defer mcpClient.Close()
			startClient(t, mcpClient)
			req := mcp.CallToolRequest{}
			req.Params.Name = "singularity_projects"
			req.Params.Arguments = tt.args
			result, err := mcpClient.CallTool(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError {
				t.Fatal("expected IsError=true")
			}
			text := resultText(result)
			var payload map[string]any
			if err := json.Unmarshal([]byte(text), &payload); err != nil {
				t.Fatalf("not stable JSON: %v: %s", err, text)
			}
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %s: %s", want, text)
				}
			}
			for _, leaked := range []string{"secret-token", "private body"} {
				if strings.Contains(text, leaked) {
					t.Fatalf("leaked %q: %s", leaked, text)
				}
			}
		})
	}
}

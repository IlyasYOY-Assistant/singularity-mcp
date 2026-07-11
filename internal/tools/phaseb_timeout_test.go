package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestAPIOperationTimeoutCancelsRequestAndReturnsStructuredError(t *testing.T) {
	catalog := testCatalog(t)
	cancelled := make(chan struct{})
	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer api.Close()
	srv := NewServerWithOptions(testClient(t, api.URL), catalog, "test", Options{RequireWriteApproval: false, OperationTimeout: 25 * time.Millisecond})
	c, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	startClient(t, c)
	req := mcp.CallToolRequest{}
	req.Params.Name = "singularity_projects"
	req.Params.Arguments = map[string]any{"operation": "list"}
	outer, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	result, err := c.CallTool(outer, req)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) >= time.Second {
		t.Fatal("operation was not bounded by operation timeout")
	}
	if !result.IsError || !strings.Contains(resultText(result), `"type":"operation_timeout"`) || !strings.Contains(resultText(result), `"timeoutMs":25`) {
		t.Fatalf("result=%#v text=%s", result, resultText(result))
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream request context was not cancelled")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestAPIOperationTimeoutStopsPaginationWithoutExtraRequest(t *testing.T) {
	catalog := testCatalog(t)
	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if call == 1 {
			items := make([]map[string]any, 1000)
			for i := range items {
				items[i] = map[string]any{"id": i}
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"projects": items}); err != nil {
				t.Errorf("encode: %v", err)
			}
			return
		}
		<-r.Context().Done()
	}))
	defer api.Close()
	srv := NewServerWithOptions(testClient(t, api.URL), catalog, "test", Options{RequireWriteApproval: false, OperationTimeout: 25 * time.Millisecond})
	c, _ := client.NewInProcessClient(srv)
	defer c.Close()
	startClient(t, c)
	req := mcp.CallToolRequest{}
	req.Params.Name = "singularity_projects"
	req.Params.Arguments = map[string]any{"operation": "list", "all": true}
	result, err := c.CallTool(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(resultText(result), `"type":"operation_timeout"`) {
		t.Fatalf("result=%s", resultText(result))
	}
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 2 {
		t.Fatalf("calls=%d, want exactly two and no post-timeout request", calls.Load())
	}
}

func TestAPIOperationTimeoutStartsAfterWriteApproval(t *testing.T) {
	catalog := testCatalog(t)
	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"id":"P-1"}`))
	}))
	defer api.Close()
	srv := NewServerWithOptions(testClient(t, api.URL), catalog, "test", Options{
		RequireWriteApproval: true,
		ApprovalTimeout:      time.Second,
		OperationTimeout:     25 * time.Millisecond,
	})
	h := &delayedApprovalHandler{delay: 50 * time.Millisecond}
	c := newInProcessClientWithElicitation(t, srv, h)
	defer c.Close()
	startClient(t, c)
	req := mcp.CallToolRequest{}
	req.Params.Name = "singularity_projects"
	req.Params.Arguments = map[string]any{"operation": "create", "body": map[string]any{"title": "x"}}
	result, err := c.CallTool(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || calls.Load() != 1 {
		t.Fatalf("result=%s calls=%d", resultText(result), calls.Load())
	}
}

type delayedApprovalHandler struct{ delay time.Duration }

func (h *delayedApprovalHandler) Elicit(ctx context.Context, request mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
	select {
	case <-time.After(h.delay):
		return &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionAccept, Content: map[string]any{"approved": true}}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

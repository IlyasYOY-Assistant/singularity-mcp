package singularity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestResponseSizeLimit(t *testing.T) {
	catalog := testCatalog(t)
	op, _ := catalog.Operation("singularity_projects", "list")
	for _, tc := range []struct {
		name    string
		extra   int
		wantErr bool
	}{
		{"exact limit", 0, false}, {"one byte over", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const limit = 64
			body := []byte(`{"projects":[]}`)
			body = append(body[:len(body)-1], []byte(strings.Repeat(" ", limit-len(body)+tc.extra))...)
			body = append(body, '}')
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
			defer srv.Close()
			client, err := NewAPIClient(srv.URL, "token", time.Second, WithMaxResponseBytes(limit))
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Call(context.Background(), op, nil)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				structured := StructuredError(err)
				if !strings.Contains(structured, `"type":"response_too_large"`) || !strings.Contains(structured, `"limit":64`) || strings.Contains(structured, string(body)) || strings.Contains(structured, "invalid JSON") {
					t.Fatalf("structured=%s", structured)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResponseSizeLimitMaxInt64DoesNotOverflow(t *testing.T) {
	catalog := testCatalog(t)
	op, _ := catalog.Operation("singularity_projects", "list")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"P-1"}]}`))
	}))
	defer srv.Close()
	client, err := NewAPIClient(srv.URL, "token", time.Second, WithMaxResponseBytes(int64(1<<63-1)))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := client.Call(context.Background(), op, nil)
	if err != nil || !strings.Contains(string(raw), `"id":"P-1"`) {
		t.Fatalf("raw=%s err=%v", raw, err)
	}
}

func TestMaxItemsHardBoundWhenUpstreamIgnoresMaxCount(t *testing.T) {
	catalog := testCatalog(t)
	projects, _ := catalog.Operation("singularity_projects", "list")
	tasks, _ := catalog.Operation("singularity_tasks", "search")
	for _, tc := range []struct {
		name      string
		op        *Operation
		args      map[string]any
		listField string
	}{
		{"list", projects, map[string]any{"all": true}, "projects"},
		{"search", tasks, map[string]any{"query": "match"}, "tasks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("maxCount"); got != "2" {
					t.Fatalf("maxCount=%q", got)
				}
				items := []map[string]any{{"id": "1", "title": "match"}, {"id": "2", "title": "match"}, {"id": "3", "title": "match"}}
				_ = json.NewEncoder(w).Encode(map[string]any{tc.listField: items})
			}))
			defer srv.Close()
			client, _ := NewAPIClient(srv.URL, "token", time.Second, WithPaginationLimits(10, 2))
			raw, err := client.Call(context.Background(), tc.op, tc.args)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			items := got[tc.listField].([]any)
			pagination := got["pagination"].(map[string]any)
			if len(items) != 2 || pagination["nextOffset"] != float64(2) {
				t.Fatalf("result=%s", raw)
			}
			if tc.name == "search" && got["query"].(map[string]any)["truncated"] != true {
				t.Fatalf("search not marked truncated: %s", raw)
			}
		})
	}
}

func TestBoundedPageIterator(t *testing.T) {
	catalog := testCatalog(t)
	op, _ := catalog.Operation("singularity_projects", "list")
	var queries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query())
		items := make([]map[string]any, PageSize)
		for i := range items {
			items[i] = map[string]any{"id": "P-" + string(rune('A'+i%26)) + string(rune('A'+i/26))}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": items})
	}))
	defer srv.Close()
	client, _ := NewAPIClient(srv.URL, "token", time.Second, WithPaginationLimits(1, 2*PageSize))
	raw, err := client.Call(context.Background(), op, map[string]any{"all": true, "offset": float64(5)})
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0].Get("offset") != "5" || queries[0].Get("maxCount") != "1000" {
		t.Fatalf("queries=%v", queries)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	p := got["pagination"].(map[string]any)
	if p["reason"] != "max_pages" || p["nextOffset"] != float64(1005) || p["scannedPages"] != float64(1) || p["scannedItems"] != float64(PageSize) || p["truncated"] != true {
		t.Fatalf("pagination=%#v", p)
	}
}

func TestBoundedPageIteratorDetectsRepeatedPage(t *testing.T) {
	catalog := testCatalog(t)
	op, _ := catalog.Operation("singularity_projects", "list")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items := make([]map[string]any, PageSize)
		for i := range items {
			items[i] = map[string]any{"id": i}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": items})
	}))
	defer srv.Close()
	client, _ := NewAPIClient(srv.URL, "token", time.Second, WithPaginationLimits(10, 3*PageSize))
	_, err := client.Call(context.Background(), op, map[string]any{"all": true})
	if err == nil || !strings.Contains(StructuredError(err), `"type":"pagination_stalled"`) {
		t.Fatalf("err=%v structured=%s", err, StructuredError(err))
	}
}

func TestBoundedPageIteratorHonorsCancellation(t *testing.T) {
	catalog := testCatalog(t)
	op, _ := catalog.Operation("singularity_projects", "list")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client, _ := NewAPIClient("https://api.example", "token", time.Second, WithPaginationLimits(10, 10))
	_, err := client.Call(ctx, op, map[string]any{"all": true})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("err=%v", err)
	}
}

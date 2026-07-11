package singularity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestSearchStopsImmediatelyAfterLimitPlusOneMatches(t *testing.T) {
	catalog := testCatalog(t)
	op, _ := catalog.Operation("singularity_tasks", "search")
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		items := make([]map[string]any, PageSize)
		for i := range items {
			items[i] = map[string]any{"id": fmt.Sprintf("T-%d-%d", call, i), "title": "other"}
		}
		if call == 1 {
			items[10]["title"] = "match"
		} else if call == 2 {
			items[20]["title"] = "match"
			items[21]["title"] = "match"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tasks": items})
	}))
	defer srv.Close()
	client, _ := NewAPIClient(srv.URL, "token", time.Second, WithPaginationLimits(10, 10*PageSize))
	raw, err := client.Call(context.Background(), op, map[string]any{"query": "match", "limit": float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || got["count"] != float64(2) {
		t.Fatalf("calls=%d result=%s", calls.Load(), raw)
	}
	query := got["query"].(map[string]any)
	pagination := got["pagination"].(map[string]any)
	if query["truncated"] != true || pagination["scannedPages"] != float64(2) || pagination["scannedItems"] != float64(1022) || pagination["morePagesPossible"] != true || pagination["nextOffset"] != float64(1021) {
		t.Fatalf("metadata=%s", raw)
	}
}

func TestSearchAllFalseHonorsPageSizeAndMarksBoundedResultsTruncated(t *testing.T) {
	catalog := testCatalog(t)
	op, _ := catalog.Operation("singularity_tasks", "search")
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.URL.Query().Get("maxCount"); got != "7" {
			t.Fatalf("maxCount=%q", got)
		}
		items := make([]map[string]any, 7)
		for i := range items {
			items[i] = map[string]any{"id": "T-" + strconv.Itoa(i), "title": "other"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tasks": items})
	}))
	defer srv.Close()
	client := testClient(t, srv.URL)
	raw, err := client.Call(context.Background(), op, map[string]any{"query": "missing", "all": false, "maxCount": float64(7)})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	query := got["query"].(map[string]any)
	pagination := got["pagination"].(map[string]any)
	if calls.Load() != 1 || query["truncated"] != true || pagination["scannedItems"] != float64(7) || pagination["nextOffset"] != float64(7) {
		t.Fatalf("calls=%d result=%s", calls.Load(), raw)
	}
}

func TestSearchAllFalseReportsFullPageContinuation(t *testing.T) {
	catalog := testCatalog(t)
	op, _ := catalog.Operation("singularity_tasks", "search")
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		items := make([]map[string]any, PageSize)
		for i := range items {
			items[i] = map[string]any{"id": "T-" + strconv.Itoa(i), "title": "other"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tasks": items})
	}))
	defer srv.Close()
	client := testClient(t, srv.URL)
	raw, err := client.Call(context.Background(), op, map[string]any{"query": "missing", "all": false})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	pagination := got["pagination"].(map[string]any)
	if calls.Load() != 1 || pagination["scannedPages"] != float64(1) || pagination["scannedItems"] != float64(PageSize) || pagination["morePagesPossible"] != true || pagination["nextOffset"] != float64(PageSize) {
		t.Fatalf("calls=%d result=%s", calls.Load(), raw)
	}
}

func TestSearchCriteriaClassificationIncludesZeroValuesAndRejectsControls(t *testing.T) {
	catalog := testCatalog(t)
	tasks, _ := catalog.Operation("singularity_tasks", "search")
	projects, _ := catalog.Operation("singularity_projects", "search")
	for _, tc := range []struct {
		name string
		op   *Operation
		args map[string]any
		ok   bool
	}{
		{"offset only", tasks, map[string]any{"offset": float64(1)}, false},
		{"maxCount only", tasks, map[string]any{"maxCount": float64(1)}, false},
		{"fields only", tasks, map[string]any{"fields": []any{"title"}}, false},
		{"all only", tasks, map[string]any{"all": false}, false},
		{"compact only", tasks, map[string]any{"compact": false}, false},
		{"expansion only", tasks, map[string]any{"includeArchived": true}, false},
		{"checked zero", tasks, map[string]any{"checked": float64(0)}, true},
		{"priority zero", tasks, map[string]any{"priority": float64(0)}, true},
		{"task project filter", tasks, map[string]any{"projectId": "P-1"}, true},
		{"project notebook false", projects, map[string]any{"isNotebook": false}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseSearchOptions(tc.op, tc.args)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected criteria error")
			}
		})
	}
}

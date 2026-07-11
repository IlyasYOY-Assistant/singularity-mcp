package singularity

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGETRetriesTransientStatusesAndSucceedsOnThirdAttempt(t *testing.T) {
	catalog := testCatalog(t)
	op, _ := catalog.Operation("singularity_projects", "list")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"projects":[]}`))
	}))
	defer server.Close()

	var waits []time.Duration
	client, _ := NewAPIClient(server.URL, "token", time.Second, WithSleeper(func(ctx context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}))
	if _, err := client.Call(context.Background(), op, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || len(waits) != 2 {
		t.Fatalf("calls=%d waits=%v", calls.Load(), waits)
	}
	for _, wait := range waits {
		if wait <= 0 || wait > 2*time.Second {
			t.Fatalf("unbounded wait %s", wait)
		}
	}
}

func TestGETRetryAfterIntegerAndHTTPDate(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		header string
		want   time.Duration
	}{
		{name: "integer seconds", header: "3", want: 3 * time.Second},
		{name: "HTTP date", header: now.Add(4 * time.Second).Format(http.TimeFormat), want: 4 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog := testCatalog(t)
			op, _ := catalog.Operation("singularity_projects", "list")
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					w.Header().Set("Retry-After", tc.header)
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				_, _ = w.Write([]byte(`{"projects":[]}`))
			}))
			defer server.Close()
			var got time.Duration
			client, _ := NewAPIClient(server.URL, "token", time.Second,
				WithClock(func() time.Time { return now }),
				WithSleeper(func(ctx context.Context, delay time.Duration) error { got = delay; return nil }))
			if _, err := client.Call(context.Background(), op, nil); err != nil {
				t.Fatal(err)
			}
			if calls != 2 || got != tc.want {
				t.Fatalf("calls=%d wait=%s want=%s", calls, got, tc.want)
			}
		})
	}
}

func TestRetryAfterOverflowFallsBackToBoundedBackoff(t *testing.T) {
	catalog := testCatalog(t)
	op, _ := catalog.Operation("singularity_projects", "list")
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "9223372036854775807")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"projects":[]}`))
	}))
	defer srv.Close()
	var wait time.Duration
	client, _ := NewAPIClient(srv.URL, "token", time.Second, WithSleeper(func(ctx context.Context, delay time.Duration) error {
		wait = delay
		return nil
	}))
	if _, err := client.Call(context.Background(), op, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || wait <= 0 || wait > 2*time.Second {
		t.Fatalf("calls=%d wait=%s", calls, wait)
	}
	now := time.Now()
	past := now.Add(-time.Hour).UTC().Format(http.TimeFormat)
	if _, ok := parseRetryAfter(past, now); ok {
		t.Fatal("past Retry-After date must be rejected")
	}
}

func TestGETRetryExhaustionHasStableMetadata(t *testing.T) {
	catalog := testCatalog(t)
	op, _ := catalog.Operation("singularity_projects", "list")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"token":"token","body":"private"}`))
	}))
	defer server.Close()
	client, _ := NewAPIClient(server.URL, "token", time.Second, WithSleeper(func(context.Context, time.Duration) error { return nil }))
	_, err := client.Call(context.Background(), op, nil)
	if err == nil {
		t.Fatal("expected exhaustion")
	}
	if calls != 3 {
		t.Fatalf("calls=%d", calls)
	}
	got := StructuredError(err)
	for _, want := range []string{`"type":"api_error"`, `"status":503`, `"method":"GET"`, `"path":"/v2/project"`, `"message":`, `"retriable":true`, `"attempts":3`, `"retryAfter":7`} {
		if !strings.Contains(got, want) {
			t.Fatalf("structured error missing %s: %s", want, got)
		}
	}
	for _, secret := range []string{"token", "private"} {
		if strings.Contains(got, secret) {
			t.Fatalf("structured error leaked %q: %s", secret, got)
		}
	}
}

func TestNonRetriableStatusesAndWritesAreAttemptedOnce(t *testing.T) {
	catalog := testCatalog(t)
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			op, _ := catalog.Operation("singularity_projects", "list")
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(status) }))
			defer server.Close()
			client, _ := NewAPIClient(server.URL, "token", time.Second, WithSleeper(func(context.Context, time.Duration) error { t.Fatal("unexpected sleep"); return nil }))
			_, _ = client.Call(context.Background(), op, nil)
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
	for _, operation := range []string{"create", "update", "delete"} {
		t.Run(operation, func(t *testing.T) {
			op, _ := catalog.Operation("singularity_projects", operation)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusServiceUnavailable) }))
			defer server.Close()
			client, _ := NewAPIClient(server.URL, "token", time.Second, WithSleeper(func(context.Context, time.Duration) error { t.Fatal("unexpected sleep"); return nil }))
			args := map[string]any{}
			switch operation {
			case "create":
				args["body"] = map[string]any{"title": "x"}
			case "update":
				args["id"] = "P-1"
				args["body"] = map[string]any{"title": "x"}
			case "delete":
				args["id"] = "P-1"
				args["confirm"] = true
			}
			_, _ = client.Call(context.Background(), op, args)
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}

func TestRetryBackoffCancellationReturnsImmediately(t *testing.T) {
	catalog := testCatalog(t)
	op, _ := catalog.Operation("singularity_projects", "list")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	client, _ := NewAPIClient(server.URL, "token", time.Second, WithSleeper(func(ctx context.Context, delay time.Duration) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}))
	started := time.Now()
	_, err := client.Call(ctx, op, nil)
	if !errors.Is(err, context.Canceled) || time.Since(started) > 250*time.Millisecond {
		t.Fatalf("err=%v elapsed=%s", err, time.Since(started))
	}
}

func TestPageRetryDoesNotDuplicateSuccessfulPages(t *testing.T) {
	catalog := testCatalog(t)
	op, _ := catalog.Operation("singularity_projects", "list")
	var offsets []string
	secondAttempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := r.URL.Query().Get("offset")
		offsets = append(offsets, offset)
		if offset == "1000" {
			secondAttempts++
			if secondAttempts == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(`{"projects":[{"id":"last"}]}`))
			return
		}
		items := make([]string, PageSize)
		for i := range items {
			items[i] = `{"id":"p` + strings.Repeat("x", i%3) + `"}`
		}
		_, _ = io.WriteString(w, `{"projects":[`+strings.Join(items, ",")+`]}`)
	}))
	defer server.Close()
	client, _ := NewAPIClient(server.URL, "token", time.Second, WithSleeper(func(context.Context, time.Duration) error { return nil }))
	raw, err := client.Call(context.Background(), op, map[string]any{"all": true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(offsets, ",") != "0,1000,1000" {
		t.Fatalf("offsets=%v", offsets)
	}
	if strings.Count(string(raw), `"id":"last"`) != 1 {
		t.Fatalf("duplicated page: %s", raw)
	}
}

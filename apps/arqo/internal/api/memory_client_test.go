package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestPolarisMemoryClientSearchAppliesRewriteAndLimit(t *testing.T) {
	var gotQuery string
	var gotLimit string
	client := &PolarisMemoryClient{
		baseURL: "http://polaris.test",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotQuery = req.URL.Query().Get("q")
			gotLimit = req.URL.Query().Get("limit")
			body := `{"entries":[{"user_id":"u1","session_id":"s1","task_id":"t1","summary":"a"}]}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})},
		rewriteMode:  "trim",
		rankMode:     "none",
		defaultLimit: 3,
		strict:       true,
	}
	entries, err := client.Search("u1", "s1", "  hello   world  ", 0)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got=%d", len(entries))
	}
	if gotQuery != "hello world" {
		t.Fatalf("unexpected rewritten query: %q", gotQuery)
	}
	if gotLimit != "3" {
		t.Fatalf("unexpected limit: %q", gotLimit)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestPolarisMemoryClientRankingModes(t *testing.T) {
	baseEntries := []MemoryEntry{
		{TaskID: "t1", Summary: "long long summary"},
		{TaskID: "t2", Summary: "mid"},
		{TaskID: "t3", Summary: "x"},
	}
	tests := []struct {
		name    string
		mode    string
		wantIDs []string
	}{
		{name: "none", mode: "none", wantIDs: []string{"t1", "t2", "t3"}},
		{name: "short_first", mode: "short_first", wantIDs: []string{"t3", "t2", "t1"}},
		{name: "long_first", mode: "long_first", wantIDs: []string{"t1", "t2", "t3"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &PolarisMemoryClient{rankMode: tt.mode}
			got := c.rankEntries(baseEntries)
			gotIDs := make([]string, 0, len(got))
			for _, item := range got {
				gotIDs = append(gotIDs, item.TaskID)
			}
			if !reflect.DeepEqual(gotIDs, tt.wantIDs) {
				t.Fatalf("unexpected ranking result: got=%v want=%v", gotIDs, tt.wantIDs)
			}
		})
	}
}

func TestPolarisMemoryClientFallbackStrict(t *testing.T) {
	client := &PolarisMemoryClient{
		baseURL:      "http://127.0.0.1:1",
		client:       &http.Client{},
		rewriteMode:  "none",
		rankMode:     "none",
		defaultLimit: 5,
		strict:       false,
	}
	entries, err := client.Search("u1", "", "q", 1)
	if err != nil {
		t.Fatalf("non-strict mode should not return error, got=%v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty entries in non-strict mode, got=%d", len(entries))
	}

	client.strict = true
	_, err = client.Search("u1", "", "q", 1)
	if err == nil {
		t.Fatal("strict mode should return error when upstream is unreachable")
	}
}

func TestNewPolarisMemoryClientFromEnvConfig(t *testing.T) {
	env := map[string]string{
		"ARQO_POLARIS_URL":            "http://polaris.local",
		"ARQO_POLARIS_TIMEOUT_MS":     "2200",
		"ARQO_MEMORY_QUERY_REWRITE":   "trim",
		"ARQO_MEMORY_HIT_RANK":        "short_first",
		"ARQO_MEMORY_HIT_LIMIT":       "9",
		"ARQO_MEMORY_FALLBACK_STRICT": "true",
		"ARQO_MEMORY_HINT_ENABLED":    "true",
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	for _, k := range []string{"ARQO_POLARIS_URL", "ARQO_POLARIS_TIMEOUT_MS", "ARQO_MEMORY_QUERY_REWRITE", "ARQO_MEMORY_HIT_RANK", "ARQO_MEMORY_HIT_LIMIT", "ARQO_MEMORY_FALLBACK_STRICT", "ARQO_MEMORY_HINT_ENABLED"} {
		if _, ok := env[k]; !ok {
			t.Setenv(k, "")
		}
	}

	client := NewPolarisMemoryClientFromEnv()
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.rewriteMode != "trim" || client.rankMode != "short_first" || client.defaultLimit != 9 || !client.strict {
		t.Fatalf("unexpected client config: %+v", client)
	}
	if !client.hintEnabled {
		t.Fatalf("expected hintEnabled=true, got=%v", client.hintEnabled)
	}
	if client.client == nil {
		t.Fatal("expected non-nil http client")
	}
	if got := int(client.client.Timeout.Milliseconds()); got != 2200 {
		t.Fatalf("unexpected timeout: got=%d", got)
	}
}

func TestPolarisMemoryClientSearchByHint(t *testing.T) {
	var path string
	var body string
	client := &PolarisMemoryClient{
		baseURL: "http://polaris.test",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			path = req.URL.Path
			raw, _ := io.ReadAll(req.Body)
			body = string(raw)
			resp := `{"entries":[{"user_id":"u1","session_id":"s1","task_id":"t1","summary":"x"}]}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(resp)),
				Header:     make(http.Header),
			}, nil
		})},
		hintEnabled:  true,
		defaultLimit: 4,
		rankMode:     "none",
		rewriteMode:  "trim",
		strict:       true,
	}
	entries, err := client.SearchByHint("u1", "s1", "dependency graph", 0)
	if err != nil {
		t.Fatalf("SearchByHint failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got=%d", len(entries))
	}
	if path != "/memory/search_by_hint" {
		t.Fatalf("unexpected path: %s", path)
	}
	if !strings.Contains(body, "GRAPH_TRAVERSAL") {
		t.Fatalf("expected GRAPH_TRAVERSAL strategy in body, got=%s", body)
	}
}

func TestInferHintStrategy(t *testing.T) {
	if got := inferHintStrategy("relation between services"); got != "GRAPH_TRAVERSAL" {
		t.Fatalf("unexpected strategy for relation query: %s", got)
	}
	if got := inferHintStrategy("show task t100 details"); got != "KV_POINT_GET" {
		t.Fatalf("unexpected strategy for task query: %s", got)
	}
	if got := inferHintStrategy("summarize recent status"); got != "NONE" {
		t.Fatalf("unexpected strategy for default query: %s", got)
	}
}

func TestParseHelpers(t *testing.T) {
	_ = os.Setenv("ARQO_TEST_POSITIVE_INT", "-10")
	if got := parsePositiveIntEnv("ARQO_TEST_POSITIVE_INT", 7); got != 7 {
		t.Fatalf("expected fallback for invalid positive int, got=%d", got)
	}
	_ = os.Setenv("ARQO_TEST_ENUM", "unknown")
	if got := parseEnumEnv("ARQO_TEST_ENUM", "none", []string{"none", "trim"}); got != "none" {
		t.Fatalf("expected enum fallback, got=%s", got)
	}

	_ = os.Setenv("ARQO_TEST_POSITIVE_INT", "11")
	if got := parsePositiveIntEnv("ARQO_TEST_POSITIVE_INT", 7); got != 11 {
		t.Fatalf("expected parsed int=11, got=%d", got)
	}
	_ = os.Setenv("ARQO_TEST_ENUM", "trim")
	if got := parseEnumEnv("ARQO_TEST_ENUM", "none", []string{"none", "trim"}); got != "trim" {
		t.Fatalf("expected enum trim, got=%s", got)
	}

	_ = os.Unsetenv("ARQO_TEST_POSITIVE_INT")
	_ = os.Unsetenv("ARQO_TEST_ENUM")
	if got := parsePositiveIntEnv("ARQO_TEST_POSITIVE_INT", 5); got != 5 {
		t.Fatalf("expected fallback when env missing, got=%d", got)
	}
	if got := parseEnumEnv("ARQO_TEST_ENUM", "none", []string{"none", "trim"}); got != "none" {
		t.Fatalf("expected fallback when env missing, got=%s", got)
	}
}

func ExamplePolarisMemoryClient_rewriteQuery() {
	client := &PolarisMemoryClient{rewriteMode: "trim"}
	fmt.Println(client.rewriteQuery("  a   b  c "))
	// Output: a b c
}

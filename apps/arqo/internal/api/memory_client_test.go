package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"aurora/apps/arqo/internal/scheduler"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestMem3MemoryClientUsesVersionedIngestAndSearch(t *testing.T) {
	var paths []string
	var bodies []string
	client := &Mem3MemoryClient{
		baseURL: "http://mem3.test",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(req.Body)
			paths = append(paths, req.URL.Path)
			bodies = append(bodies, string(raw))
			status := http.StatusAccepted
			body := `{"version":"1.0","accepted":true}`
			if req.URL.Path == "/v1/memory/search" {
				status = http.StatusOK
				body = `{"version":"1.0","working_memory":{"recent_outputs":[],"latest_summary":{"summary":"","summary_version":0,"through_sequence":-1}},"retrieval":{"strategy":"NONE","items":[]},"consistency":{"latest_ingested_sequence":0,"summary_through_sequence":0,"summary_pending":false}}`
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})},
		strict: true,
	}

	scope := Mem3Scope{TenantID: "tenant_1", AgentID: "agent_1", UserID: "u1", SessionID: "s1", DAGID: "d1"}
	err := client.Ingest(context.Background(), Mem3IngestRequest{
		Version: "1.0", IdempotencyKey: "dag-context:d1", Kind: "DAG_CONTEXT", Scope: scope,
		Payload: Mem3DAGContext{RawQuery: "query", IntentSlot: Mem3IntentSlot{MacroIntent: "TEST", Entities: []string{}, ActionVerbs: []string{}}},
	})
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	_, err = client.Search(context.Background(), Mem3SearchRequest{
		Version: "1.0", Scope: scope, RecentLimit: 5, MemHint: scheduler.DefaultMemHint(),
		CurrentTask: Mem3CurrentTask{TaskID: "t1", NodeType: "skill", ParentTaskIDs: []string{}, MemHintSourceTaskIDs: []string{}},
	})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(paths) != 2 || paths[0] != "/v1/memory/ingest" || paths[1] != "/v1/memory/search" {
		t.Fatalf("unexpected paths: %v", paths)
	}
	if !strings.Contains(bodies[0], `"kind":"DAG_CONTEXT"`) || !strings.Contains(bodies[1], `"current_task"`) {
		t.Fatalf("unexpected request bodies: %v", bodies)
	}
}

func TestNewMem3MemoryClientFromEnv(t *testing.T) {
	t.Setenv("ARQO_MEM3_URL", "http://mem3.local")
	t.Setenv("ARQO_MEM3_TIMEOUT_MS", "2200")
	t.Setenv("ARQO_MEMORY_FALLBACK_STRICT", "true")
	client := NewMem3MemoryClientFromEnv()
	if client == nil || client.baseURL != "http://mem3.local" || !client.strict {
		t.Fatalf("unexpected client: %+v", client)
	}
	if got := client.client.Timeout.Milliseconds(); got != 2200 {
		t.Fatalf("unexpected timeout: %d", got)
	}
}

func TestMem3MemoryClientNonStrictFallback(t *testing.T) {
	client := &Mem3MemoryClient{
		baseURL: "http://127.0.0.1:1",
		client:  &http.Client{},
		strict:  false,
	}
	if err := client.Ingest(context.Background(), Mem3IngestRequest{}); err != nil {
		t.Fatalf("non-strict mode should degrade without error: %v", err)
	}
}

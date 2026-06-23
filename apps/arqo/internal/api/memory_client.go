package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"aurora/apps/arqo/internal/scheduler"
)

type Mem3Scope struct {
	TenantID  string `json:"tenant_id"`
	AgentID   string `json:"agent_id"`
	UserID    string `json:"user_id,omitempty"`
	SessionID string `json:"session_id"`
	DAGID     string `json:"dag_id"`
}

type Mem3IntentSlot struct {
	MacroIntent     string   `json:"macro_intent"`
	Entities        []string `json:"entities"`
	TemporalContext string   `json:"temporal_context,omitempty"`
	ActionVerbs     []string `json:"action_verbs"`
	ExtractionHint  string   `json:"extraction_hint,omitempty"`
}

type Mem3DAGContext struct {
	RawQuery   string         `json:"raw_query"`
	IntentSlot Mem3IntentSlot `json:"intent_slot"`
	ObservedAt time.Time      `json:"observed_at"`
}

type Mem3TaskOutput struct {
	TaskID        string    `json:"task_id"`
	ParentTaskIDs []string  `json:"parent_task_ids,omitempty"`
	Sequence      int64     `json:"sequence"`
	NodeType      string    `json:"node_type"`
	SkillName     string    `json:"skill_name,omitempty"`
	Output        any       `json:"output"`
	SkillSummary  string    `json:"skill_summary,omitempty"`
	CompletedAt   time.Time `json:"completed_at"`
}

type Mem3IngestRequest struct {
	Version        string    `json:"version"`
	IdempotencyKey string    `json:"idempotency_key"`
	Kind           string    `json:"kind"`
	Scope          Mem3Scope `json:"scope"`
	Payload        any       `json:"payload"`
}

type Mem3CurrentTask struct {
	TaskID               string   `json:"task_id"`
	Sequence             int64    `json:"sequence"`
	NodeType             string   `json:"node_type"`
	ParentTaskIDs        []string `json:"parent_task_ids"`
	MemHintSourceTaskIDs []string `json:"mem_hint_source_task_ids"`
}

type Mem3SearchRequest struct {
	Version     string            `json:"version"`
	Scope       Mem3Scope         `json:"scope"`
	CurrentTask Mem3CurrentTask   `json:"current_task"`
	RecentLimit int               `json:"recent_limit"`
	MemHint     scheduler.MemHint `json:"mem_hint"`
}

type Mem3MemoryItem struct {
	TaskID   string `json:"task_id,omitempty"`
	Sequence int64  `json:"sequence,omitempty"`
	NodeType string `json:"node_type,omitempty"`
	Output   any    `json:"output,omitempty"`
	Summary  string `json:"summary,omitempty"`
}

type Mem3Summary struct {
	Summary         string `json:"summary"`
	SummaryVersion  int64  `json:"summary_version"`
	ThroughSequence int64  `json:"through_sequence"`
}

type Mem3SearchResponse struct {
	Version       string `json:"version"`
	WorkingMemory struct {
		RecentOutputs []Mem3MemoryItem `json:"recent_outputs"`
		LatestSummary Mem3Summary      `json:"latest_summary"`
	} `json:"working_memory"`
	Retrieval struct {
		Strategy string           `json:"strategy"`
		Items    []map[string]any `json:"items"`
	} `json:"retrieval"`
	Consistency struct {
		LatestIngestedSequence int64 `json:"latest_ingested_sequence"`
		SummaryThroughSequence int64 `json:"summary_through_sequence"`
		SummaryPending         bool  `json:"summary_pending"`
	} `json:"consistency"`
}

type MemoryClient interface {
	Ingest(context.Context, Mem3IngestRequest) error
	Search(context.Context, Mem3SearchRequest) (Mem3SearchResponse, error)
}

type Mem3MemoryClient struct {
	baseURL string
	client  *http.Client
	strict  bool
}

func NewMem3MemoryClientFromEnv() *Mem3MemoryClient {
	baseURL := strings.TrimSpace(os.Getenv("ARQO_MEM3_URL"))
	if baseURL == "" {
		return nil
	}
	timeoutMS := parsePositiveIntEnv("ARQO_MEM3_TIMEOUT_MS", 1500)
	return &Mem3MemoryClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: time.Duration(timeoutMS) * time.Millisecond},
		strict:  strings.EqualFold(strings.TrimSpace(os.Getenv("ARQO_MEMORY_FALLBACK_STRICT")), "true"),
	}
}

func (c *Mem3MemoryClient) Ingest(ctx context.Context, payload Mem3IngestRequest) error {
	if c == nil || c.baseURL == "" {
		return nil
	}
	return c.postJSON(ctx, "/v1/memory/ingest", payload, http.StatusAccepted, nil)
}

func (c *Mem3MemoryClient) Search(ctx context.Context, payload Mem3SearchRequest) (Mem3SearchResponse, error) {
	var out Mem3SearchResponse
	if c == nil || c.baseURL == "" {
		return out, nil
	}
	err := c.postJSON(ctx, "/v1/memory/search", payload, http.StatusOK, &out)
	return out, err
}

func (c *Mem3MemoryClient) postJSON(ctx context.Context, path string, payload any, expectedStatus int, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		if c.strict {
			return err
		}
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != expectedStatus {
		err = fmt.Errorf("mem3 %s status=%d", path, resp.StatusCode)
		if c.strict {
			return err
		}
		return nil
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			if c.strict {
				return err
			}
		}
	}
	return nil
}

func parsePositiveIntEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

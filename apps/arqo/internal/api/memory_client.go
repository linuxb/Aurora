package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type MemoryEntry struct {
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id"`
	Summary   string `json:"summary"`
}

type MemorySearcher interface {
	Search(userID, sessionID, query string, limit int) ([]MemoryEntry, error)
	SearchByHint(userID, sessionID, query string, limit int) ([]MemoryEntry, error)
}

type PolarisMemoryClient struct {
	baseURL      string
	client       *http.Client
	rewriteMode  string
	rankMode     string
	defaultLimit int
	strict       bool
	hintEnabled  bool
}

func NewPolarisMemoryClientFromEnv() *PolarisMemoryClient {
	baseURL := strings.TrimSpace(os.Getenv("ARQO_POLARIS_URL"))
	if baseURL == "" {
		return nil
	}
	timeoutMS := parsePositiveIntEnv("ARQO_POLARIS_TIMEOUT_MS", 1500)
	return &PolarisMemoryClient{
		baseURL:      strings.TrimRight(baseURL, "/"),
		client:       &http.Client{Timeout: time.Duration(timeoutMS) * time.Millisecond},
		rewriteMode:  parseEnumEnv("ARQO_MEMORY_QUERY_REWRITE", "none", []string{"none", "trim"}),
		rankMode:     parseEnumEnv("ARQO_MEMORY_HIT_RANK", "none", []string{"none", "short_first", "long_first"}),
		defaultLimit: parsePositiveIntEnv("ARQO_MEMORY_HIT_LIMIT", 5),
		strict:       strings.EqualFold(strings.TrimSpace(os.Getenv("ARQO_MEMORY_FALLBACK_STRICT")), "true"),
		hintEnabled:  strings.EqualFold(strings.TrimSpace(os.Getenv("ARQO_MEMORY_HINT_ENABLED")), "true"),
	}
}

func (c *PolarisMemoryClient) Search(userID, sessionID, query string, limit int) ([]MemoryEntry, error) {
	if c == nil || c.baseURL == "" {
		return nil, nil
	}

	effectiveLimit := limit
	if effectiveLimit <= 0 {
		effectiveLimit = c.defaultLimit
	}
	effectiveQuery := c.rewriteQuery(query)

	values := url.Values{}
	values.Set("user_id", userID)
	if sessionID != "" {
		values.Set("session_id", sessionID)
	}
	if effectiveQuery != "" {
		values.Set("q", effectiveQuery)
	}
	if effectiveLimit > 0 {
		values.Set("limit", strconv.Itoa(effectiveLimit))
	}
	reqURL := fmt.Sprintf("%s/memory/search?%s", c.baseURL, values.Encode())
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		if c.strict {
			return nil, err
		}
		return nil, nil
	}

	resp, err := c.client.Do(req)
	if err != nil {
		if c.strict {
			return nil, err
		}
		return nil, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err = fmt.Errorf("polaris search status=%d", resp.StatusCode)
		if c.strict {
			return nil, err
		}
		return nil, nil
	}

	var payload struct {
		Entries []MemoryEntry `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		if c.strict {
			return nil, err
		}
		return nil, nil
	}
	return c.rankEntries(payload.Entries), nil
}

func (c *PolarisMemoryClient) SearchByHint(userID, sessionID, query string, limit int) ([]MemoryEntry, error) {
	if c == nil || c.baseURL == "" || !c.hintEnabled {
		return nil, nil
	}
	effectiveLimit := limit
	if effectiveLimit <= 0 {
		effectiveLimit = c.defaultLimit
	}
	effectiveQuery := c.rewriteQuery(query)
	payload := map[string]any{
		"user_id":    userID,
		"session_id": sessionID,
		"limit":      effectiveLimit,
		"mem_hint": map[string]any{
			"strategy":       inferHintStrategy(effectiveQuery),
			"semantic_query": effectiveQuery,
		},
	}
	body, _ := json.Marshal(payload)
	reqURL := fmt.Sprintf("%s/memory/search_by_hint", c.baseURL)
	req, err := http.NewRequest(http.MethodPost, reqURL, strings.NewReader(string(body)))
	if err != nil {
		if c.strict {
			return nil, err
		}
		return nil, nil
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		if c.strict {
			return nil, err
		}
		return nil, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err = fmt.Errorf("polaris hint search status=%d", resp.StatusCode)
		if c.strict {
			return nil, err
		}
		return nil, nil
	}
	var out struct {
		Entries []MemoryEntry `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		if c.strict {
			return nil, err
		}
		return nil, nil
	}
	return c.rankEntries(out.Entries), nil
}

func (c *PolarisMemoryClient) rewriteQuery(query string) string {
	trimmed := strings.TrimSpace(query)
	switch c.rewriteMode {
	case "trim":
		return strings.Join(strings.Fields(trimmed), " ")
	default:
		return query
	}
}

func (c *PolarisMemoryClient) rankEntries(entries []MemoryEntry) []MemoryEntry {
	out := make([]MemoryEntry, len(entries))
	copy(out, entries)
	switch c.rankMode {
	case "short_first":
		sort.SliceStable(out, func(i, j int) bool {
			return len(out[i].Summary) < len(out[j].Summary)
		})
	case "long_first":
		sort.SliceStable(out, func(i, j int) bool {
			return len(out[i].Summary) > len(out[j].Summary)
		})
	}
	return out
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

func parseEnumEnv(key, fallback string, allowed []string) string {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	for _, candidate := range allowed {
		if raw == candidate {
			return candidate
		}
	}
	return fallback
}

func inferHintStrategy(query string) string {
	lower := strings.ToLower(strings.TrimSpace(query))
	if strings.Contains(lower, "relation") || strings.Contains(lower, "dependency") || strings.Contains(lower, "impact") {
		return "GRAPH_TRAVERSAL"
	}
	if strings.Contains(lower, "task ") || strings.Contains(lower, "step ") {
		return "KV_POINT_GET"
	}
	return "NONE"
}

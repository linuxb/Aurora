package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
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
}

type PolarisMemoryClient struct {
	baseURL string
	client  *http.Client
}

func NewPolarisMemoryClientFromEnv() *PolarisMemoryClient {
	baseURL := strings.TrimSpace(os.Getenv("ARQO_POLARIS_URL"))
	if baseURL == "" {
		return nil
	}
	return &PolarisMemoryClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{
			Timeout: 1500 * time.Millisecond,
		},
	}
}

func (c *PolarisMemoryClient) Search(userID, sessionID, query string, limit int) ([]MemoryEntry, error) {
	if c == nil || c.baseURL == "" {
		return nil, nil
	}
	values := url.Values{}
	values.Set("user_id", userID)
	if sessionID != "" {
		values.Set("session_id", sessionID)
	}
	if query != "" {
		values.Set("q", query)
	}
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	reqURL := fmt.Sprintf("%s/memory/search?%s", c.baseURL, values.Encode())
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("polaris search status=%d", resp.StatusCode)
	}
	var payload struct {
		Entries []MemoryEntry `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Entries, nil
}

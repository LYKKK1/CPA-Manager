package codexinspect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestInspectAutoToggleNeverDeletesInvalidAccounts(t *testing.T) {
	var disabledNames []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer management-key" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{
				{"name": "invalid.json", "type": "codex", "authIndex": "invalid", "disabled": false},
				{"name": "quota.json", "type": "codex", "authIndex": "quota", "disabled": false},
				{"name": "restored.json", "type": "codex", "authIndex": "restored", "disabled": true},
			}})
		case "/v0/management/api-call":
			var req struct {
				AuthIndex string `json:"authIndex"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			statusCode := http.StatusOK
			usedPercent := 10.0
			if req.AuthIndex == "invalid" {
				statusCode = http.StatusUnauthorized
			}
			if req.AuthIndex == "quota" {
				usedPercent = 100.0
			}
			body, _ := json.Marshal(map[string]any{"rate_limit": rateLimitPayload(usedPercent)})
			_ = json.NewEncoder(w).Encode(map[string]any{"status_code": statusCode, "body": string(body)})
		case "/v0/management/auth-files/status":
			var req struct {
				Name     string `json:"name"`
				Disabled bool   `json:"disabled"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			disabledNames = append(disabledNames, req.Name)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "disabled": req.Disabled})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)

	result, err := Inspect(context.Background(), RuntimeConfig{BaseURL: upstream.URL, ManagementKey: "management-key"}, Settings{Timeout: 5 * time.Second}, ScheduleConfig{AutoToggle: true})
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(disabledNames) != 2 {
		t.Fatalf("status changes = %#v, want quota/restored", disabledNames)
	}
	if result.Summary.DeleteCount != 1 || result.Summary.SkippedDeletes != 1 {
		t.Fatalf("delete summary = %#v", result.Summary)
	}
	if result.Summary.AutoDisabled != 1 || result.Summary.AutoEnabled != 1 {
		t.Fatalf("toggle summary = %#v", result.Summary)
	}
}

func rateLimitPayload(weeklyUsed float64) map[string]any {
	return map[string]any{
		"primary_window": map[string]any{
			"limit_window_seconds": weekWindowSeconds,
			"used_percent":         weeklyUsed,
		},
		"secondary_window": map[string]any{
			"limit_window_seconds": fiveHourWindowSeconds,
			"used_percent":         0,
		},
	}
}

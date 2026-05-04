package codexinspect

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const codexUsageURL = "https://chatgpt.com/backend-api/wham/usage"

const defaultUserAgent = "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal"

const (
	fiveHourWindowSeconds = 18000
	weekWindowSeconds     = 604800
)

func DefaultSettings() Settings {
	return Settings{
		TargetType:           "codex",
		Workers:              4,
		DelaySeconds:         0,
		Timeout:              15 * time.Second,
		UserAgent:            defaultUserAgent,
		UsedPercentThreshold: 100,
	}
}

type service struct {
	client *http.Client
}

func Inspect(ctx context.Context, runtime RuntimeConfig, settings Settings, schedule ScheduleConfig) (RunResult, error) {
	settings = normalizeSettings(settings)
	startedAt := time.Now().UnixMilli()
	result := RunResult{Settings: settings, Schedule: schedule, StartedAt: startedAt}
	client := &http.Client{Timeout: settings.Timeout}
	svc := service{client: client}

	files, err := svc.listAuthFiles(ctx, runtime)
	if err != nil {
		result.FinishedAt = time.Now().UnixMilli()
		result.Error = err.Error()
		return result, err
	}

	accounts := make([]AuthFile, 0, len(files))
	for _, file := range files {
		if normalizeProvider(file) == settings.TargetType {
			accounts = append(accounts, file)
		}
	}

	items := inspectAccounts(ctx, svc, runtime, settings, accounts)
	result.Results = sortResults(items)
	result.Summary = buildSummary(len(files), len(accounts), result.Results, nil)
	if schedule.AutoToggle {
		outcomes := svc.executeAutoToggle(ctx, runtime, result.Results)
		result.Outcomes = outcomes
		result.Summary = buildSummary(len(files), len(accounts), result.Results, outcomes)
	}
	result.FinishedAt = time.Now().UnixMilli()
	return result, nil
}

func normalizeSettings(settings Settings) Settings {
	defaults := DefaultSettings()
	if strings.TrimSpace(settings.TargetType) == "" {
		settings.TargetType = defaults.TargetType
	}
	settings.TargetType = strings.ToLower(strings.TrimSpace(settings.TargetType))
	if settings.Workers <= 0 {
		settings.Workers = defaults.Workers
	}
	if settings.DelaySeconds < 0 {
		settings.DelaySeconds = 0
	}
	if settings.Timeout <= 0 {
		settings.Timeout = defaults.Timeout
	}
	if strings.TrimSpace(settings.UserAgent) == "" {
		settings.UserAgent = defaults.UserAgent
	}
	if settings.UsedPercentThreshold <= 0 || settings.UsedPercentThreshold > 100 {
		settings.UsedPercentThreshold = defaults.UsedPercentThreshold
	}
	return settings
}

func inspectAccounts(ctx context.Context, svc service, runtime RuntimeConfig, settings Settings, accounts []AuthFile) []ResultItem {
	if len(accounts) == 0 {
		return nil
	}
	workers := settings.Workers
	if workers > len(accounts) {
		workers = len(accounts)
	}
	results := make([]ResultItem, len(accounts))
	jobs := make(chan int)
	var wg sync.WaitGroup

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				results[index] = svc.inspectSingleAccount(ctx, runtime, settings, accounts[index])
			}
		}()
	}

	for index := range accounts {
		if ctx.Err() != nil {
			break
		}
		jobs <- index
		if settings.DelaySeconds > 0 && index < len(accounts)-1 {
			select {
			case <-ctx.Done():
				break
			case <-time.After(time.Duration(settings.DelaySeconds) * time.Second):
			}
		}
	}
	close(jobs)
	wg.Wait()
	return results
}

func (s service) listAuthFiles(ctx context.Context, runtime RuntimeConfig) ([]AuthFile, error) {
	var response struct {
		Files []map[string]any `json:"files"`
	}
	if err := s.managementJSON(ctx, runtime, http.MethodGet, "/auth-files", nil, &response); err != nil {
		return nil, err
	}
	files := make([]AuthFile, 0, len(response.Files))
	for _, raw := range response.Files {
		files = append(files, authFileFromRaw(raw))
	}
	return files, nil
}

func (s service) inspectSingleAccount(ctx context.Context, runtime RuntimeConfig, settings Settings, account AuthFile) ResultItem {
	authIndex := readAuthIndex(account)
	base := ResultItem{
		FileName:       account.Name,
		DisplayAccount: displayAccount(account),
		AuthIndex:      authIndex,
		AccountID:      resolveAccountID(account.Raw),
		Provider:       normalizeProvider(account),
		Disabled:       account.Disabled,
	}
	if authIndex == "" {
		base.Action = ActionKeep
		base.ActionReason = "缺少 auth_index，保留账号"
		base.Error = "缺少 auth_index"
		return base
	}

	body, statusCode, err := s.apiCall(ctx, runtime, authIndex, base.AccountID, settings.UserAgent)
	if err != nil {
		base.Action = ActionKeep
		base.ActionReason = "探测异常，保留账号"
		base.Error = err.Error()
		return base
	}
	base.StatusCode = &statusCode
	payload := parseUsagePayload(body)
	rateLimit := readRateLimit(payload)
	usedPercent := deriveUsedPercent(rateLimit)
	base.UsedPercent = usedPercent
	bodyLower := strings.ToLower(string(body))
	isQuota := statusCode == http.StatusPaymentRequired || strings.Contains(bodyLower, "quota exhausted") || strings.Contains(bodyLower, "limit reached") || strings.Contains(bodyLower, "payment_required") || isRateLimitReached(rateLimit) || (usedPercent != nil && *usedPercent >= settings.UsedPercentThreshold)
	decision := resolveAction(account.Disabled, statusCode, rateLimit, usedPercent, isQuota, settings.UsedPercentThreshold)
	base.Action = decision.Action
	base.ActionReason = decision.Reason
	base.IsQuota = decision.IsQuota
	if decision.UsedPercent != nil {
		base.UsedPercent = decision.UsedPercent
	}
	return base
}

func (s service) apiCall(ctx context.Context, runtime RuntimeConfig, authIndex string, accountID string, userAgent string) ([]byte, int, error) {
	payload := map[string]any{
		"authIndex": authIndex,
		"method":    http.MethodGet,
		"url":       codexUsageURL,
		"header": map[string]string{
			"Authorization": "Bearer $TOKEN$",
			"Content-Type":  "application/json",
			"User-Agent":    userAgent,
		},
	}
	if accountID != "" {
		payload["header"].(map[string]string)["Chatgpt-Account-Id"] = accountID
	}
	var response struct {
		StatusCode    *int            `json:"status_code"`
		StatusCodeAlt *int            `json:"statusCode"`
		Body          json.RawMessage `json:"body"`
	}
	if err := s.managementJSON(ctx, runtime, http.MethodPost, "/api-call", payload, &response); err != nil {
		return nil, 0, err
	}
	statusCode := 0
	if response.StatusCode != nil {
		statusCode = *response.StatusCode
	} else if response.StatusCodeAlt != nil {
		statusCode = *response.StatusCodeAlt
	} else {
		return response.Body, 0, errors.New("响应缺少 status_code")
	}
	return response.Body, statusCode, nil
}

func (s service) executeAutoToggle(ctx context.Context, runtime RuntimeConfig, results []ResultItem) []ExecutionOutcome {
	outcomes := make([]ExecutionOutcome, 0)
	for _, item := range results {
		if item.Action != ActionDisable && item.Action != ActionEnable {
			continue
		}
		disabled := item.Action == ActionDisable
		err := s.managementJSON(ctx, runtime, http.MethodPatch, "/auth-files/status", map[string]any{"name": item.FileName, "disabled": disabled}, nil)
		outcome := ExecutionOutcome{Action: item.Action, FileName: item.FileName, DisplayAccount: item.DisplayAccount, Success: err == nil}
		if err != nil {
			outcome.Error = err.Error()
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}

func (s service) managementJSON(ctx context.Context, runtime RuntimeConfig, method string, path string, payload any, target any) error {
	if strings.TrimSpace(runtime.BaseURL) == "" || strings.TrimSpace(runtime.ManagementKey) == "" {
		return errors.New("usage service is not configured")
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(runtime.BaseURL, "/")+"/v0/management"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+runtime.ManagementKey)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("management API %s %s failed: %s %s", method, path, res.Status, strings.TrimSpace(string(data)))
	}
	if target == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, target)
}

type decision struct {
	Action      string
	Reason      string
	UsedPercent *float64
	IsQuota     bool
}

func resolveAction(disabled bool, statusCode int, rateLimit map[string]any, usedPercent *float64, isQuota bool, threshold float64) decision {
	fiveHour, weekly := pickClassifiedWindows(rateLimit)
	weeklyUsed := getUsedPercent(weekly)
	if weekly != nil && weeklyUsed != nil {
		fiveHourUsed := getUsedPercent(fiveHour)
		weeklyOver := *weeklyUsed >= threshold
		fiveHourOver := fiveHourUsed != nil && *fiveHourUsed >= threshold
		if statusCode == http.StatusUnauthorized {
			return decision{Action: ActionDelete, Reason: "接口返回 401，建议删除失效账号", UsedPercent: weeklyUsed}
		}
		if weeklyOver {
			if disabled {
				return decision{Action: ActionKeep, Reason: "周额度达到阈值，但账号已禁用", UsedPercent: weeklyUsed, IsQuota: true}
			}
			return decision{Action: ActionDisable, Reason: "周额度达到阈值，建议禁用账号", UsedPercent: weeklyUsed, IsQuota: true}
		}
		if disabled {
			reason := "周额度仍可用，建议立即启用账号"
			if fiveHourOver {
				reason = "5 小时额度达到阈值，但周额度仍可用，建议立即启用账号"
			}
			return decision{Action: ActionEnable, Reason: reason, UsedPercent: weeklyUsed}
		}
		if fiveHourOver {
			return decision{Action: ActionKeep, Reason: "5 小时额度达到阈值，但周额度仍可用，暂不禁用账号", UsedPercent: weeklyUsed}
		}
		return decision{Action: ActionKeep, Reason: "周额度仍可用，无需处理", UsedPercent: weeklyUsed}
	}
	if statusCode == http.StatusUnauthorized {
		return decision{Action: ActionDelete, Reason: "接口返回 401，建议删除失效账号", UsedPercent: usedPercent}
	}
	if isQuota {
		if disabled {
			return decision{Action: ActionKeep, Reason: "额度达到阈值，但账号已禁用", UsedPercent: usedPercent, IsQuota: true}
		}
		return decision{Action: ActionDisable, Reason: "额度达到阈值，建议禁用账号", UsedPercent: usedPercent, IsQuota: true}
	}
	if disabled {
		return decision{Action: ActionEnable, Reason: "额度可用，建议启用账号", UsedPercent: usedPercent}
	}
	return decision{Action: ActionKeep, Reason: "额度可用，无需处理", UsedPercent: usedPercent}
}

func buildSummary(totalFiles int, probeSetCount int, results []ResultItem, outcomes []ExecutionOutcome) Summary {
	summary := Summary{TotalFiles: totalFiles, ProbeSetCount: probeSetCount, SampledCount: len(results)}
	for _, item := range results {
		switch item.Action {
		case ActionDelete:
			summary.DeleteCount++
			summary.SkippedDeletes++
		case ActionDisable:
			summary.DisableCount++
		case ActionEnable:
			summary.EnableCount++
		default:
			summary.KeepCount++
		}
	}
	for _, outcome := range outcomes {
		if !outcome.Success {
			summary.AutoFailed++
			continue
		}
		if outcome.Action == ActionDisable {
			summary.AutoDisabled++
		}
		if outcome.Action == ActionEnable {
			summary.AutoEnabled++
		}
	}
	if summary.SkippedDeletes > 0 {
		summary.Messages = append(summary.Messages, "检测到失效账号，仅记录建议删除，不自动删除")
	}
	return summary
}

func authFileFromRaw(raw map[string]any) AuthFile {
	return AuthFile{
		Name:      readString(raw["name"]),
		Type:      readString(raw["type"]),
		Provider:  readString(raw["provider"]),
		AuthIndex: firstValue(raw, "authIndex", "auth_index"),
		Disabled:  readBool(firstValue(raw, "disabled", "unavailable")),
		Status:    readString(raw["status"]),
		Raw:       raw,
	}
}

func normalizeProvider(file AuthFile) string {
	provider := readString(file.Provider)
	if provider == "" {
		provider = readString(file.Type)
	}
	return strings.ToLower(provider)
}

func readAuthIndex(file AuthFile) string {
	return readString(file.AuthIndex)
}

func displayAccount(file AuthFile) string {
	for _, key := range []string{"displayAccount", "display_account", "account", "email", "name"} {
		if value := readString(file.Raw[key]); value != "" {
			return value
		}
	}
	return file.Name
}

func sortResults(items []ResultItem) []ResultItem {
	sorted := append([]ResultItem(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Action != sorted[j].Action {
			return actionRank(sorted[i].Action) < actionRank(sorted[j].Action)
		}
		return sorted[i].FileName < sorted[j].FileName
	})
	return sorted
}

func actionRank(action string) int {
	switch action {
	case ActionDelete:
		return 0
	case ActionDisable:
		return 1
	case ActionEnable:
		return 2
	default:
		return 3
	}
}

func parseUsagePayload(body []byte) map[string]any {
	if len(body) == 0 {
		return nil
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	if text, ok := payload.(string); ok {
		var nested map[string]any
		if err := json.Unmarshal([]byte(text), &nested); err == nil {
			return nested
		}
		return nil
	}
	if record, ok := payload.(map[string]any); ok {
		return record
	}
	return nil
}

func readRateLimit(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	return readRecord(firstValue(payload, "rate_limit", "rateLimit"))
}

func deriveUsedPercent(rateLimit map[string]any) *float64 {
	for _, window := range getLimitWindows(rateLimit) {
		if used := getUsedPercent(window); used != nil {
			return used
		}
	}
	return nil
}

func isRateLimitReached(rateLimit map[string]any) bool {
	return readBool(firstValue(rateLimit, "limit_reached", "limitReached"))
}

func pickClassifiedWindows(rateLimit map[string]any) (map[string]any, map[string]any) {
	var fiveHour map[string]any
	var weekly map[string]any
	for _, window := range getLimitWindows(rateLimit) {
		seconds := readFloat(firstValue(window, "limit_window_seconds", "limitWindowSeconds"))
		if seconds == nil {
			continue
		}
		if int(*seconds) == fiveHourWindowSeconds {
			fiveHour = window
		}
		if int(*seconds) == weekWindowSeconds {
			weekly = window
		}
	}
	return fiveHour, weekly
}

func getLimitWindows(rateLimit map[string]any) []map[string]any {
	if rateLimit == nil {
		return nil
	}
	windows := make([]map[string]any, 0, 2)
	for _, key := range []string{"primary_window", "primaryWindow", "secondary_window", "secondaryWindow"} {
		if record := readRecord(rateLimit[key]); record != nil {
			windows = append(windows, record)
		}
	}
	return windows
}

func getUsedPercent(window map[string]any) *float64 {
	return readFloat(firstValue(window, "used_percent", "usedPercent"))
}

func resolveAccountID(raw map[string]any) string {
	for _, key := range []string{"chatgpt_account_id", "chatgptAccountId", "account_id", "accountId"} {
		if value := readString(raw[key]); value != "" {
			return value
		}
	}
	for _, key := range []string{"metadata", "attributes"} {
		if record := readRecord(raw[key]); record != nil {
			if value := resolveAccountID(record); value != "" {
				return value
			}
		}
	}
	for _, key := range []string{"id_token", "idToken"} {
		if value := readString(raw[key]); value != "" {
			if accountID := readAccountIDFromJWT(value); accountID != "" {
				return accountID
			}
		}
	}
	return ""
}

func readAccountIDFromJWT(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	return readString(firstValue(payload, "chatgpt_account_id", "chatgptAccountId", "account_id", "accountId"))
}

func firstValue(record map[string]any, keys ...string) any {
	if record == nil {
		return nil
	}
	for _, key := range keys {
		if value, ok := record[key]; ok {
			return value
		}
	}
	return nil
}

func readRecord(value any) map[string]any {
	if record, ok := value.(map[string]any); ok {
		return record
	}
	return nil
}

func readString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func readBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on", "disabled":
			return true
		}
	}
	return false
}

func readFloat(value any) *float64 {
	switch typed := value.(type) {
	case float64:
		return &typed
	case int:
		value := float64(typed)
		return &value
	case json.Number:
		if parsed, err := typed.Float64(); err == nil {
			return &parsed
		}
	case string:
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
			return &parsed
		}
	}
	return nil
}

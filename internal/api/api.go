package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"orchids-api/internal/clerk"
	"orchids-api/internal/client"
	"orchids-api/internal/keeper"
	"orchids-api/internal/logger"
	"orchids-api/internal/store"
)

type API struct {
	store  *store.Store
	keeper *keeper.AccountKeeper
	logger *logger.RequestLogger
}

type ExportData struct {
	Version  int             `json:"version"`
	ExportAt time.Time       `json:"export_at"`
	Accounts []store.Account `json:"accounts"`
}

type ImportResult struct {
	Total    int `json:"total"`
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
}

func New(s *store.Store) *API {
	return &API{store: s}
}

func NewWithKeeper(s *store.Store, k *keeper.AccountKeeper) *API {
	return &API{store: s, keeper: k}
}

func NewWithKeeperAndLogger(s *store.Store, k *keeper.AccountKeeper, l *logger.RequestLogger) *API {
	return &API{store: s, keeper: k, logger: l}
}

func (a *API) GetLogger() *logger.RequestLogger {
	return a.logger
}

func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/accounts", a.HandleAccounts)
	mux.HandleFunc("/api/accounts/", a.HandleAccountByID)
}

func (a *API) HandleAccounts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		accounts, err := a.store.ListAccounts()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(accounts)

	case http.MethodPost:
		var acc store.Account
		if err := json.NewDecoder(r.Body).Decode(&acc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if acc.ClientCookie != "" && acc.SessionID == "" {
			info, err := clerk.FetchAccountInfo(acc.ClientCookie)
			if err != nil {
				log.Printf("Failed to fetch account info: %v", err)
				http.Error(w, "Failed to fetch account info: "+err.Error(), http.StatusBadRequest)
				return
			}
			acc.SessionID = info.SessionID
			acc.ClientUat = info.ClientUat
			acc.ProjectID = info.ProjectID
			acc.UserID = info.UserID
			acc.Email = info.Email
		}

		if err := a.store.CreateAccount(&acc); err != nil {
			log.Printf("Failed to create account: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(acc)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) HandleAccountByID(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// 检查是否是 refresh 请求
	path := strings.TrimPrefix(r.URL.Path, "/api/accounts/")
	if strings.HasSuffix(path, "/refresh") {
		a.handleRefreshAccount(w, r)
		return
	}

	// 检查是否是 test 请求（激活测试）
	if strings.HasSuffix(path, "/test") {
		a.handleTestAccount(w, r)
		return
	}

	idStr := path
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		acc, err := a.store.GetAccount(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(acc)

	case http.MethodPut:
		existing, err := a.store.GetAccount(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		var acc store.Account
		if err := json.NewDecoder(r.Body).Decode(&acc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		acc.ID = id

		if acc.SessionID == "" {
			acc.SessionID = existing.SessionID
		}
		if acc.ClientUat == "" {
			acc.ClientUat = existing.ClientUat
		}
		if acc.ProjectID == "" {
			acc.ProjectID = existing.ProjectID
		}
		if acc.UserID == "" {
			acc.UserID = existing.UserID
		}
		if acc.Email == "" {
			acc.Email = existing.Email
		}

		if err := a.store.UpdateAccount(&acc); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(acc)

	case http.MethodDelete:
		if err := a.store.DeleteAccount(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) HandleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	accounts, err := a.store.ListAccounts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	exportData := ExportData{
		Version:  1,
		ExportAt: time.Now(),
		Accounts: make([]store.Account, len(accounts)),
	}
	for i, acc := range accounts {
		exportData.Accounts[i] = *acc
		exportData.Accounts[i].ID = 0
		exportData.Accounts[i].RequestCount = 0
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=accounts_export.json")
	json.NewEncoder(w).Encode(exportData)
}

func (a *API) HandleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var exportData ExportData
	if err := json.NewDecoder(r.Body).Decode(&exportData); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	result := ImportResult{Total: len(exportData.Accounts)}

	for _, acc := range exportData.Accounts {
		acc.ID = 0
		acc.RequestCount = 0
		if err := a.store.CreateAccount(&acc); err != nil {
			log.Printf("Failed to import account %s: %v", acc.Name, err)
			result.Skipped++
		} else {
			result.Imported++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleAccountsHealth 账号健康检查 API
func (a *API) HandleAccountsHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	if a.keeper == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "keeper not initialized",
			"healthy": 0,
			"total":   0,
		})
		return
	}

	statuses := a.keeper.GetStatus()
	healthy, total := a.keeper.GetHealthyCount()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"healthy":  healthy,
		"total":    total,
		"accounts": statuses,
	})
}

// handleRefreshAccount 手动刷新单个账号
func (a *API) handleRefreshAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/accounts/")
	idStr := strings.TrimSuffix(path, "/refresh")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	if a.keeper == nil {
		http.Error(w, "Keeper not initialized", http.StatusInternalServerError)
		return
	}

	if err := a.keeper.RefreshAccountByID(id); err != nil {
		http.Error(w, "Refresh failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Account refreshed successfully",
	})
}

// handleTestAccount 测试单个账号是否可用（发送 hi 请求）
func (a *API) handleTestAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/accounts/")
	idStr := strings.TrimSuffix(path, "/test")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	// 获取账号
	acc, err := a.store.GetAccount(id)
	if err != nil {
		http.Error(w, "Account not found: "+err.Error(), http.StatusNotFound)
		return
	}

	startTime := time.Now()
	log.Printf("[TestAccount] 开始测试账号 %s (%s)", acc.Name, acc.Email)

	// 创建客户端并发送测试请求
	apiClient := client.NewFromAccount(acc)

	var testResult struct {
		Success  bool   `json:"success"`
		Message  string `json:"message"`
		Duration int64  `json:"duration_ms"`
		Response string `json:"response,omitempty"`
	}

	// 使用 context 设置超时
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var responseText strings.Builder

	err = apiClient.SendRequest(ctx, "hi", []interface{}{}, "claude-sonnet-4-5", func(msg client.SSEMessage) {
		if msg.Type == "model" && msg.Event != nil {
			if evtType, ok := msg.Event["type"].(string); ok {
				if evtType == "text-delta" {
					if delta, ok := msg.Event["delta"].(string); ok {
						responseText.WriteString(delta)
					}
				}
			}
		}
	}, nil)

	duration := time.Since(startTime).Milliseconds()

	if err != nil {
		testResult.Success = false
		testResult.Message = fmt.Sprintf("请求失败: %v", err)
		testResult.Duration = duration
		log.Printf("[TestAccount] 账号 %s 测试失败: %v, 耗时=%dms", acc.Name, err, duration)

		// 记录到日志系统
		if a.logger != nil {
			a.logger.LogRequest(fmt.Sprintf("test-%d", acc.ID), acc.ID, acc.Name,
				fmt.Sprintf("激活测试失败: %v", err), duration, false)
		}
	} else {
		testResult.Success = true
		testResult.Message = "账号激活成功"
		testResult.Duration = duration
		testResult.Response = responseText.String()
		log.Printf("[TestAccount] 账号 %s 测试成功, 耗时=%dms, 响应=%s", acc.Name, duration, responseText.String())

		// 标记账号为活跃
		if a.keeper != nil {
			a.keeper.MarkAccountActive(acc.ID)
		}

		// 记录到日志系统
		if a.logger != nil {
			a.logger.LogRequest(fmt.Sprintf("test-%d", acc.ID), acc.ID, acc.Name,
				fmt.Sprintf("激活测试成功, 响应: %s", responseText.String()), duration, true)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(testResult)
}

// HandleRefreshAll 一键刷新所有账号
func (a *API) HandleRefreshAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if a.keeper == nil {
		http.Error(w, "Keeper not initialized", http.StatusInternalServerError)
		return
	}

	// 异步执行刷新，避免超时
	go func() {
		a.keeper.RefreshAll()
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Refresh started for all accounts",
	})
}

// HandleBatchDelete 批量删除账号
func (a *API) HandleBatchDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.IDs) == 0 {
		http.Error(w, "No IDs provided", http.StatusBadRequest)
		return
	}

	deleted := 0
	failed := 0
	for _, id := range req.IDs {
		if err := a.store.DeleteAccount(id); err != nil {
			log.Printf("Failed to delete account %d: %v", id, err)
			failed++
		} else {
			deleted++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"deleted": deleted,
		"failed":  failed,
	})
}

// HandleLogs 获取历史日志
func (a *API) HandleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if a.logger == nil {
		http.Error(w, "Logger not initialized", http.StatusInternalServerError)
		return
	}

	// 获取 limit 参数
	limit := 100
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	logs := a.logger.GetLogs(limit)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"logs":  logs,
		"total": len(logs),
	})
}

// HandleLogsSSE 实时日志 SSE 流
func (a *API) HandleLogsSSE(w http.ResponseWriter, r *http.Request) {
	if a.logger == nil {
		http.Error(w, "Logger not initialized", http.StatusInternalServerError)
		return
	}

	// 设置 SSE 响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// 订阅日志
	id, ch := a.logger.Subscribe()
	if ch == nil {
		http.Error(w, "Too many listeners", http.StatusServiceUnavailable)
		return
	}
	defer a.logger.Unsubscribe(id)

	log.Printf("[LogsSSE] 客户端连接, listener_id=%d", id)

	// 发送连接成功消息
	fmt.Fprintf(w, "event: connected\ndata: {\"listener_id\":%d}\n\n", id)
	flusher.Flush()

	// 监听日志和客户端断开
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			log.Printf("[LogsSSE] 客户端断开, listener_id=%d", id)
			return
		case entry, ok := <-ch:
			if !ok {
				return
			}
			data := entry.ToJSON()
			fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// HandleLogsStats 获取日志统计信息
func (a *API) HandleLogsStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if a.logger == nil {
		http.Error(w, "Logger not initialized", http.StatusInternalServerError)
		return
	}

	total, listeners := a.logger.Stats()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_logs": total,
		"listeners":  listeners,
	})
}

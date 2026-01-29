package logger

import (
	"encoding/json"
	"sync"
	"time"
)

// LogEntry 日志条目
type LogEntry struct {
	ID        int64     `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`     // info, error, warn
	RequestID string    `json:"request_id,omitempty"`
	AccountID int64     `json:"account_id,omitempty"`
	Account   string    `json:"account,omitempty"`
	Message   string    `json:"message"`
	Duration  int64     `json:"duration_ms,omitempty"`
	Success   bool      `json:"success,omitempty"`
}

// RequestLogger 请求日志收集器
type RequestLogger struct {
	mu        sync.RWMutex
	logs      []LogEntry
	maxSize   int
	nextID    int64
	listeners map[int64]chan LogEntry
	listenerMu sync.Mutex
	nextListenerID int64
}

const (
	DefaultMaxSize = 200 // 最多保留 200 条日志
	MaxListeners   = 10  // 最多 10 个监听者
)

// New 创建日志收集器
func New() *RequestLogger {
	return &RequestLogger{
		logs:      make([]LogEntry, 0, DefaultMaxSize),
		maxSize:   DefaultMaxSize,
		listeners: make(map[int64]chan LogEntry),
	}
}

// Log 记录日志
func (l *RequestLogger) Log(entry LogEntry) {
	l.mu.Lock()
	l.nextID++
	entry.ID = l.nextID
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	// 环形缓冲区
	if len(l.logs) >= l.maxSize {
		l.logs = l.logs[1:]
	}
	l.logs = append(l.logs, entry)
	l.mu.Unlock()

	// 非阻塞推送给监听者
	l.listenerMu.Lock()
	for _, ch := range l.listeners {
		select {
		case ch <- entry:
		default:
			// channel 满了就跳过，不阻塞
		}
	}
	l.listenerMu.Unlock()
}

// LogRequest 记录请求日志（简化接口）
func (l *RequestLogger) LogRequest(requestID string, accountID int64, accountName, message string, durationMs int64, success bool) {
	level := "info"
	if !success {
		level = "error"
	}
	l.Log(LogEntry{
		Level:     level,
		RequestID: requestID,
		AccountID: accountID,
		Account:   accountName,
		Message:   message,
		Duration:  durationMs,
		Success:   success,
	})
}

// LogInfo 记录信息日志
func (l *RequestLogger) LogInfo(message string) {
	l.Log(LogEntry{
		Level:   "info",
		Message: message,
	})
}

// LogError 记录错误日志
func (l *RequestLogger) LogError(requestID, message string) {
	l.Log(LogEntry{
		Level:     "error",
		RequestID: requestID,
		Message:   message,
	})
}

// GetLogs 获取最近的日志
func (l *RequestLogger) GetLogs(limit int) []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if limit <= 0 || limit > len(l.logs) {
		limit = len(l.logs)
	}

	start := len(l.logs) - limit
	if start < 0 {
		start = 0
	}

	result := make([]LogEntry, limit)
	copy(result, l.logs[start:])
	return result
}

// Subscribe 订阅实时日志
func (l *RequestLogger) Subscribe() (int64, <-chan LogEntry) {
	l.listenerMu.Lock()
	defer l.listenerMu.Unlock()

	// 限制最大监听者数量
	if len(l.listeners) >= MaxListeners {
		return 0, nil
	}

	l.nextListenerID++
	id := l.nextListenerID
	ch := make(chan LogEntry, 50) // 缓冲区
	l.listeners[id] = ch
	return id, ch
}

// Unsubscribe 取消订阅
func (l *RequestLogger) Unsubscribe(id int64) {
	l.listenerMu.Lock()
	defer l.listenerMu.Unlock()

	if ch, ok := l.listeners[id]; ok {
		close(ch)
		delete(l.listeners, id)
	}
}

// ToJSON 将日志条目转换为 JSON
func (e *LogEntry) ToJSON() string {
	data, _ := json.Marshal(e)
	return string(data)
}

// Stats 获取统计信息
func (l *RequestLogger) Stats() (total int, listeners int) {
	l.mu.RLock()
	total = len(l.logs)
	l.mu.RUnlock()

	l.listenerMu.Lock()
	listeners = len(l.listeners)
	l.listenerMu.Unlock()
	return
}

package debug

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// LogEntry 日志条目
type LogEntry struct {
	Type      string
	Filename  string
	Content   interface{}
	Timestamp time.Time
}

// Logger 异步调试日志记录器
type Logger struct {
	enabled   bool
	dir       string
	rawFile   *os.File
	outFile   *os.File
	mu        sync.Mutex
	startTime time.Time
	logChan   chan LogEntry
	wg        sync.WaitGroup
	closed    bool
}

const logChannelBuffer = 1000

// New 创建新的异步调试日志记录器
func New(enabled bool) *Logger {
	if !enabled {
		return &Logger{enabled: false}
	}

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	dir := filepath.Join("debug-logs", timestamp)
	os.MkdirAll(dir, 0755)

	l := &Logger{
		enabled:   true,
		dir:       dir,
		startTime: time.Now(),
		logChan:   make(chan LogEntry, logChannelBuffer),
	}

	l.wg.Add(1)
	go l.asyncWriter()

	return l
}

// asyncWriter 异步日志写入 goroutine
func (l *Logger) asyncWriter() {
	defer l.wg.Done()

	for entry := range l.logChan {
		l.processEntry(entry)
	}
}

// processEntry 处理单个日志条目
func (l *Logger) processEntry(entry LogEntry) {
	switch entry.Type {
	case "json":
		l.writeJSONSync(entry.Filename, entry.Content)
	case "file":
		l.writeFileSync(entry.Filename, entry.Content.(string))
	case "upstream_sse":
		l.writeUpstreamSSESync(entry.Content.(string))
	case "output_sse":
		data := entry.Content.([]string)
		l.writeOutputSSESync(data[0], data[1])
	}
}

// CleanupAllLogs 清理所有调试日志（启动时调用）
func CleanupAllLogs() {
	os.RemoveAll("debug-logs")
	os.MkdirAll("debug-logs", 0755)
}

// Dir 返回日志目录
func (l *Logger) Dir() string {
	if !l.enabled {
		return ""
	}
	return l.dir
}

// enqueue 将日志条目加入队列（非阻塞）
func (l *Logger) enqueue(entry LogEntry) {
	select {
	case l.logChan <- entry:
	default:
	}
}

// LogIncomingRequest 记录 1. 进入的 Claude API 请求
func (l *Logger) LogIncomingRequest(req interface{}) {
	if !l.enabled || l.closed {
		return
	}
	l.enqueue(LogEntry{
		Type:      "json",
		Filename:  "1_claude_request.json",
		Content:   req,
		Timestamp: time.Now(),
	})
}

// LogConvertedPrompt 记录 2. 转换后的 prompt
func (l *Logger) LogConvertedPrompt(prompt string) {
	if !l.enabled || l.closed {
		return
	}
	l.enqueue(LogEntry{
		Type:      "file",
		Filename:  "2_converted_prompt.md",
		Content:   prompt,
		Timestamp: time.Now(),
	})
}

// LogUpstreamRequest 记录 3. 发送给上游的请求
func (l *Logger) LogUpstreamRequest(url string, headers map[string]string, body interface{}) {
	if !l.enabled || l.closed {
		return
	}

	data := map[string]interface{}{
		"url":     url,
		"headers": headers,
		"body":    body,
	}
	l.enqueue(LogEntry{
		Type:      "json",
		Filename:  "3_upstream_request.json",
		Content:   data,
		Timestamp: time.Now(),
	})
}

// LogUpstreamSSE 记录 4. 上游返回的原始 SSE（追加写入）
func (l *Logger) LogUpstreamSSE(eventType string, data string) {
	if !l.enabled || l.closed {
		return
	}

	elapsed := time.Since(l.startTime).Milliseconds()
	line := fmt.Sprintf("[%dms] %s: %s\n", elapsed, eventType, data)
	l.enqueue(LogEntry{
		Type:      "upstream_sse",
		Content:   line,
		Timestamp: time.Now(),
	})
}

// LogOutputSSE 记录 5. 转换给客户端的 SSE（追加写入）
func (l *Logger) LogOutputSSE(event string, data string) {
	if !l.enabled || l.closed {
		return
	}

	elapsed := time.Since(l.startTime).Milliseconds()
	line := fmt.Sprintf("[%dms] event: %s\ndata: %s\n\n", elapsed, event, data)
	l.enqueue(LogEntry{
		Type:      "output_sse",
		Content:   []string{event, line},
		Timestamp: time.Now(),
	})
}

// LogSummary 记录请求摘要
func (l *Logger) LogSummary(inputTokens, outputTokens int, duration time.Duration, stopReason string) {
	if !l.enabled || l.closed {
		return
	}

	summary := map[string]interface{}{
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
		"total_tokens":  inputTokens + outputTokens,
		"duration_ms":   duration.Milliseconds(),
		"stop_reason":   stopReason,
	}
	l.enqueue(LogEntry{
		Type:      "json",
		Filename:  "6_summary.json",
		Content:   summary,
		Timestamp: time.Now(),
	})
}

// Close 关闭日志记录器，等待所有日志写入完成
func (l *Logger) Close() {
	if !l.enabled || l.closed {
		return
	}

	l.closed = true
	close(l.logChan)
	l.wg.Wait()

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.rawFile != nil {
		l.rawFile.Close()
		l.rawFile = nil
	}
	if l.outFile != nil {
		l.outFile.Close()
		l.outFile = nil
	}
}

func (l *Logger) writeJSONSync(filename string, data interface{}) {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(filepath.Join(l.dir, filename), jsonData, 0644)
}

func (l *Logger) writeFileSync(filename string, content string) {
	os.WriteFile(filepath.Join(l.dir, filename), []byte(content), 0644)
}

func (l *Logger) writeUpstreamSSESync(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.rawFile == nil {
		f, err := os.OpenFile(filepath.Join(l.dir, "4_upstream_sse.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return
		}
		l.rawFile = f
	}

	l.rawFile.WriteString(line)
}

func (l *Logger) writeOutputSSESync(event string, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.outFile == nil {
		f, err := os.OpenFile(filepath.Join(l.dir, "5_client_sse.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return
		}
		l.outFile = f
	}

	l.outFile.WriteString(line)
}

func cleanupOldDirs(basePath string, maxKeep int) {
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return
	}

	var dirs []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e)
		}
	}

	if len(dirs) <= maxKeep {
		return
	}

	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i].Name() > dirs[j].Name()
	})

	for i := maxKeep; i < len(dirs); i++ {
		os.RemoveAll(filepath.Join(basePath, dirs[i].Name()))
	}
}

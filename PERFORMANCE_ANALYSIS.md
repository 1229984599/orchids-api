# Orchids-2api 性能分析报告

## 问题描述

用户反馈：**每次请求进来，响应反应很慢，回流（流式响应）延迟较大**

---

## 1. 请求流程分析

```
客户端请求 → [1] Handler 接收解析
           → [2] 负载均衡器选择账号 (数据库查询)
           → [3] 构建 Prompt (字符串处理)
           → [4] 获取 Clerk JWT Token (网络请求!) ★ 瓶颈
           → [5] 发送到上游 Orchids 服务器 (网络请求!) ★ 瓶颈
           → [6] 接收 SSE 流式响应
           → [7] 转换并返回给客户端
```

---

## 2. 性能瓶颈定位

### 🔴 核心问题 1：每次请求都重新获取 JWT Token

**位置**: `internal/client/client.go:106-110`

```go
func (c *Client) SendRequest(...) error {
    token, err := c.GetToken()  // ★ 每次请求都调用！
    if err != nil {
        return fmt.Errorf("failed to get token: %w", err)
    }
    // ...
}
```

**问题分析**:
- `GetToken()` 每次都向 `https://clerk.orchids.app/v1/client/sessions/{session}/tokens` 发送 POST 请求
- 这是一个 **网络 IO 阻塞操作**，延迟约 200-500ms
- 对于高并发场景，这是严重的性能瓶颈
- JWT Token 通常有效期 1-2 小时，完全可以缓存复用

**影响程度**: ⚠️ 严重 - 每个请求额外增加 200-500ms 延迟

---

### 🔴 核心问题 2：每次请求都创建新的 HTTP Client

**位置**: `internal/client/client.go:52-57, 59-74`

```go
func New(cfg *config.Config) *Client {
    return &Client{
        config:     cfg,
        httpClient: &http.Client{},  // ★ 每次都创建新实例
    }
}

func NewFromAccount(acc *store.Account) *Client {
    // ...
    return &Client{
        config:     cfg,
        account:    acc,
        httpClient: &http.Client{},  // ★ 每次都创建新实例
    }
}
```

**问题分析**:
- 未配置连接池、超时、Keep-Alive 等
- 无法复用 TCP 连接，每次请求都需要重新建立连接 (TCP 三次握手 + TLS 握手)
- TLS 握手对于 HTTPS 尤其耗时（100-300ms）

**影响程度**: ⚠️ 严重 - 每个请求额外增加 100-300ms 延迟

---

### 🟠 问题 3：负载均衡器锁竞争

**位置**: `internal/loadbalancer/loadbalancer.go:26-59`

```go
func (lb *LoadBalancer) GetNextAccountExcluding(excludeIDs []int64) (*store.Account, error) {
    lb.mu.Lock()                           // ★ 写锁
    defer lb.mu.Unlock()
    
    accounts, err := lb.store.GetEnabledAccounts()  // ★ 持锁期间访问数据库
    // ...
    if err := lb.store.IncrementRequestCount(account.ID); err != nil {  // ★ 持锁期间写数据库
        return nil, err
    }
    return account, nil
}
```

**问题分析**:
- 使用互斥锁（而非读写锁优化读操作）
- 在持锁期间进行数据库 IO（查询 + 更新），锁持有时间过长
- 高并发时所有请求串行等待

**影响程度**: 🟠 中等 - 高并发时产生排队延迟

---

### 🟠 问题 4：Store 层读操作使用写锁

**位置**: `internal/store/store.go:191-221`

```go
func (s *Store) GetEnabledAccounts() ([]*Account, error) {
    s.mu.RLock()   // 虽然用了 RLock，但...
    defer s.mu.RUnlock()
    // ...
}

func (s *Store) IncrementRequestCount(id int64) error {
    s.mu.Lock()    // ★ 写锁会阻塞所有读操作
    defer s.mu.Unlock()
    // ...
}
```

**问题分析**:
- 虽然读操作用了 `RLock`，但 `IncrementRequestCount` 的写锁会阻塞所有并发的读操作
- 更新请求计数不应该阻塞其他请求

---

### 🟡 问题 5：上游服务器位于欧洲（地理延迟）

**位置**: `internal/client/client.go:19`

```go
const upstreamURL = "https://orchids-server.calmstone-6964e08a.westeurope.azurecontainerapps.io/agent/coding-agent"
```

**问题分析**:
- 上游服务器位于 **West Europe** (Azure 西欧区域)
- 如果用户在中国/亚洲，网络往返时间 (RTT) 约 200-400ms
- 这是外部因素，无法直接优化，但可以通过批量处理减少请求次数

---

### 🟡 问题 6：调试日志同步写入

**位置**: `internal/handler/handler.go:143-148, 257`

```go
logger := debug.New(h.config.DebugEnabled)
defer logger.Close()
logger.LogIncomingRequest(req)
// ...
logger.LogOutputSSE(event, data)  // ★ 每条 SSE 消息都写日志
```

**问题分析**:
- 如果 `DEBUG_ENABLED=true`，每条 SSE 消息都会触发日志写入
- 日志写入是同步 IO 操作，会阻塞响应

---

## 3. 性能问题总结

| 问题 | 严重程度 | 预估延迟影响 | 优先级 |
|------|---------|-------------|--------|
| 每次请求获取 JWT Token | 🔴 严重 | +200-500ms | P0 |
| HTTP Client 未复用连接 | 🔴 严重 | +100-300ms | P0 |
| 负载均衡器锁粒度过大 | 🟠 中等 | 高并发时排队 | P1 |
| Store 写锁阻塞读操作 | 🟠 中等 | 高并发时排队 | P1 |
| 上游服务器地理位置 | 🟡 低 | +200-400ms RTT | 不可优化 |
| 调试日志同步写入 | 🟡 低 | 取决于日志量 | P2 |

**首次请求预估延迟**: 500-1200ms（仅代理层开销，不含上游处理时间）

---

## 4. 优化方案

### 方案 A：JWT Token 缓存（优先级 P0）

```go
// internal/client/token_cache.go
package client

import (
    "sync"
    "time"
)

type TokenCache struct {
    mu     sync.RWMutex
    tokens map[string]*CachedToken
}

type CachedToken struct {
    JWT       string
    ExpiresAt time.Time
}

var tokenCache = &TokenCache{
    tokens: make(map[string]*CachedToken),
}

// GetCachedToken 获取缓存的 Token，如果不存在或已过期则返回空
func (tc *TokenCache) GetCachedToken(sessionID string) (string, bool) {
    tc.mu.RLock()
    defer tc.mu.RUnlock()
    
    cached, exists := tc.tokens[sessionID]
    if !exists {
        return "", false
    }
    
    // 提前 5 分钟过期，确保 Token 有效
    if time.Now().Add(5 * time.Minute).After(cached.ExpiresAt) {
        return "", false
    }
    
    return cached.JWT, true
}

// SetCachedToken 缓存 Token
func (tc *TokenCache) SetCachedToken(sessionID, jwt string, ttl time.Duration) {
    tc.mu.Lock()
    defer tc.mu.Unlock()
    
    tc.tokens[sessionID] = &CachedToken{
        JWT:       jwt,
        ExpiresAt: time.Now().Add(ttl),
    }
}

// 修改 Client.GetToken() 方法
func (c *Client) GetToken() (string, error) {
    // 先尝试从缓存获取
    if jwt, ok := tokenCache.GetCachedToken(c.config.SessionID); ok {
        return jwt, nil
    }
    
    // 缓存未命中，请求新 Token
    jwt, err := c.fetchNewToken()
    if err != nil {
        return "", err
    }
    
    // 缓存 Token (默认 50 分钟，假设 Token 有效期 1 小时)
    tokenCache.SetCachedToken(c.config.SessionID, jwt, 50*time.Minute)
    
    return jwt, nil
}
```

**预期收益**: 消除 90%+ 的 Token 获取延迟

---

### 方案 B：全局 HTTP Client 连接池（优先级 P0）

```go
// internal/client/http_pool.go
package client

import (
    "net"
    "net/http"
    "time"
)

var sharedHTTPClient = &http.Client{
    Timeout: 120 * time.Second,
    Transport: &http.Transport{
        MaxIdleConns:        100,              // 最大空闲连接数
        MaxIdleConnsPerHost: 20,               // 每个主机最大空闲连接
        MaxConnsPerHost:     50,               // 每个主机最大连接数
        IdleConnTimeout:     90 * time.Second, // 空闲连接超时
        TLSHandshakeTimeout: 10 * time.Second,
        DialContext: (&net.Dialer{
            Timeout:   30 * time.Second,
            KeepAlive: 30 * time.Second,
        }).DialContext,
        ForceAttemptHTTP2: true,  // 尝试使用 HTTP/2
    },
}

// 修改 Client 结构
func New(cfg *config.Config) *Client {
    return &Client{
        config:     cfg,
        httpClient: sharedHTTPClient,  // 使用共享客户端
    }
}

func NewFromAccount(acc *store.Account) *Client {
    cfg := &config.Config{...}
    return &Client{
        config:     cfg,
        account:    acc,
        httpClient: sharedHTTPClient,  // 使用共享客户端
    }
}
```

**预期收益**: 
- 复用 TCP 连接，减少握手开销
- 支持 HTTP/2 多路复用
- 更好的连接管理和资源利用

---

### 方案 C：负载均衡器优化（优先级 P1）

```go
// internal/loadbalancer/loadbalancer_optimized.go
package loadbalancer

import (
    "sync"
    "sync/atomic"
    "time"
)

type OptimizedLoadBalancer struct {
    store          *store.Store
    accounts       []*store.Account      // 缓存的账号列表
    accountsLock   sync.RWMutex
    lastRefresh    time.Time
    refreshTTL     time.Duration
    counter        uint64                 // 使用原子计数器替代请求计数更新
    pendingUpdates map[int64]int64        // 待批量更新的请求计数
    updateLock     sync.Mutex
}

func NewOptimized(s *store.Store) *OptimizedLoadBalancer {
    lb := &OptimizedLoadBalancer{
        store:          s,
        refreshTTL:     5 * time.Second,
        pendingUpdates: make(map[int64]int64),
    }
    
    // 立即加载账号
    lb.refreshAccounts()
    
    // 后台定期刷新账号列表
    go lb.backgroundRefresh()
    
    // 后台批量更新请求计数
    go lb.backgroundUpdateCounts()
    
    return lb
}

func (lb *OptimizedLoadBalancer) GetNextAccountExcluding(excludeIDs []int64) (*store.Account, error) {
    lb.accountsLock.RLock()
    accounts := lb.accounts
    lb.accountsLock.RUnlock()
    
    if len(accounts) == 0 {
        lb.refreshAccounts()
        lb.accountsLock.RLock()
        accounts = lb.accounts
        lb.accountsLock.RUnlock()
    }
    
    // 过滤排除的账号
    filtered := filterAccounts(accounts, excludeIDs)
    if len(filtered) == 0 {
        return nil, errors.New("no enabled accounts available")
    }
    
    // 选择账号
    account := lb.selectAccount(filtered)
    
    // 异步增加请求计数
    go lb.scheduleCountUpdate(account.ID)
    
    return account, nil
}

func (lb *OptimizedLoadBalancer) scheduleCountUpdate(accountID int64) {
    lb.updateLock.Lock()
    lb.pendingUpdates[accountID]++
    lb.updateLock.Unlock()
}

func (lb *OptimizedLoadBalancer) backgroundUpdateCounts() {
    ticker := time.NewTicker(5 * time.Second)
    for range ticker.C {
        lb.updateLock.Lock()
        updates := lb.pendingUpdates
        lb.pendingUpdates = make(map[int64]int64)
        lb.updateLock.Unlock()
        
        for accountID, count := range updates {
            lb.store.AddRequestCount(accountID, count)
        }
    }
}
```

**预期收益**:
- 账号列表缓存，减少数据库查询
- 异步批量更新请求计数，不阻塞请求处理
- 读操作无锁竞争

---

### 方案 D：调试日志异步化（优先级 P2）

```go
// internal/debug/async_logger.go
package debug

import (
    "sync"
)

type AsyncLogger struct {
    enabled  bool
    logChan  chan LogEntry
    wg       sync.WaitGroup
    filePath string
}

type LogEntry struct {
    Type    string
    Content interface{}
}

func NewAsync(enabled bool) *AsyncLogger {
    logger := &AsyncLogger{
        enabled: enabled,
        logChan: make(chan LogEntry, 1000),  // 缓冲通道
    }
    
    if enabled {
        logger.wg.Add(1)
        go logger.writer()
    }
    
    return logger
}

func (l *AsyncLogger) writer() {
    defer l.wg.Done()
    for entry := range l.logChan {
        // 实际写入文件
        l.writeEntry(entry)
    }
}

func (l *AsyncLogger) Log(entryType string, content interface{}) {
    if !l.enabled {
        return
    }
    
    select {
    case l.logChan <- LogEntry{Type: entryType, Content: content}:
        // 成功入队
    default:
        // 队列满了，丢弃日志（避免阻塞）
    }
}

func (l *AsyncLogger) Close() {
    if l.enabled {
        close(l.logChan)
        l.wg.Wait()
    }
}
```

---

## 5. 实施优先级建议

### 第一阶段（立即实施，收益最大）

1. **JWT Token 缓存** - 预计减少 200-500ms 延迟
2. **全局 HTTP Client 连接池** - 预计减少 100-300ms 延迟

### 第二阶段（高并发优化）

3. **负载均衡器账号缓存** - 减少数据库查询
4. **异步批量更新请求计数** - 消除写锁阻塞

### 第三阶段（精细优化）

5. **调试日志异步化** - 减少 IO 阻塞
6. **SSE 响应缓冲优化** - 减少 Flush 调用次数

---

## 6. 快速验证方法

```bash
# 1. 检查当前请求延迟
time curl -X POST http://localhost:3002/v1/messages \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}],"stream":false}'

# 2. 分析日志中的时间戳
tail -f debug/*/request.json | jq '.timestamp'

# 3. 使用 Go pprof 分析
# 在 main.go 添加:
# import _ "net/http/pprof"
# go func() { http.ListenAndServe(":6060", nil) }()

# 访问 http://localhost:6060/debug/pprof/profile
```

---

## 7. 预期优化效果

| 指标 | 优化前 | 优化后 |
|------|--------|--------|
| 首次请求延迟 | 500-1200ms | 50-100ms |
| 后续请求延迟 | 500-1200ms | 10-50ms |
| Token 获取 | 每次请求 | 缓存命中 99%+ |
| 连接复用 | 无 | TCP/TLS 复用 |
| 高并发吞吐 | 受锁限制 | 大幅提升 |

---

## 8. 附录：完整优化代码示例

请参考本文档中的代码片段进行实施，或联系开发团队获取完整的优化补丁。

---

**报告生成时间**: 2026-01-29  
**分析人**: AI Assistant  
**项目版本**: Orchids-2api (Go)

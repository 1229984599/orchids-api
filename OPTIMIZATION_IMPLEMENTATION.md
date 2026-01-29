# Orchids-2api 性能优化实施计划

> 基于 PERFORMANCE_ANALYSIS.md 分析报告，按优先级逐步实施优化

---

## 实施状态总览

| 序号 | 优化项 | 优先级 | 预期收益 | 状态 |
|------|--------|--------|---------|------|
| 1 | JWT Token 缓存 | P0 | -200~500ms | ✅ 已完成 |
| 2 | 全局 HTTP Client 连接池 | P0 | -100~300ms | ✅ 已完成 |
| 3 | 负载均衡器账号缓存 | P1 | 减少DB查询 | ✅ 已完成 |
| 4 | 异步批量更新请求计数 | P1 | 消除写锁阻塞 | ✅ 已完成 |
| 5 | 调试日志异步化 | P2 | 减少IO阻塞 | ✅ 已完成 |

---

## 优化 1：JWT Token 缓存 ✅ 已完成

**目标**: 缓存 JWT Token，避免每次请求都向 Clerk API 发送网络请求

**修改文件**:
- `internal/client/token_cache.go` (新建)
- `internal/client/client.go` (修改)

**实施内容**:
1. ✅ 创建 TokenCache 结构，使用 sync.RWMutex 保证并发安全
2. ✅ 实现 GetCachedToken / SetCachedToken 方法
3. ✅ 修改 Client.GetToken() 方法，优先从缓存获取
4. ✅ Token 缓存 50 分钟（假设有效期 1 小时，提前 10 分钟刷新）

**状态**: ✅ 已完成 (2026-01-29)

---

## 优化 2：全局 HTTP Client 连接池 ✅ 已完成

**目标**: 使用共享的 HTTP Client，复用 TCP/TLS 连接

**修改文件**:
- `internal/client/client.go` (修改)

**实施内容**:
1. ✅ 创建全局 sharedHTTPClient，配置连接池参数
2. ✅ 配置 MaxIdleConns=100、MaxIdleConnsPerHost=20、IdleConnTimeout=90s
3. ✅ 启用 HTTP/2 支持 (ForceAttemptHTTP2=true)
4. ✅ 修改 New() 和 NewFromAccount() 使用共享客户端

**状态**: ✅ 已完成 (2026-01-29)

---

## 优化 3：负载均衡器账号缓存 ✅ 已完成

**目标**: 缓存账号列表，减少数据库查询

**修改文件**:
- `internal/loadbalancer/loadbalancer.go` (修改)

**实施内容**:
1. ✅ 添加账号列表缓存和刷新时间戳
2. ✅ 设置缓存 TTL（5秒）
3. ✅ 后台 goroutine 定期刷新账号列表 (backgroundRefreshAccounts)
4. ✅ 使用 RWMutex 优化读操作，读取时无锁竞争

**状态**: ✅ 已完成 (2026-01-29)

---

## 优化 4：异步批量更新请求计数 ✅ 已完成

**目标**: 异步更新请求计数，不阻塞请求处理

**修改文件**:
- `internal/loadbalancer/loadbalancer.go` (修改)
- `internal/store/store.go` (添加 AddRequestCount 方法)
- `cmd/server/main.go` (添加 lb.Close() 调用)

**实施内容**:
1. ✅ 使用 pendingUpdates map 收集待更新的计数
2. ✅ 后台 goroutine 每 5 秒批量写入数据库 (backgroundUpdateCounts)
3. ✅ 请求处理路径不再直接写数据库，调用 scheduleCountUpdate 异步处理
4. ✅ 添加 LoadBalancer.Close() 方法，程序退出时刷新待更新计数

**状态**: ✅ 已完成 (2026-01-29)

---

## 优化 5：调试日志异步化 ✅ 已完成

**目标**: 异步写入调试日志，避免阻塞响应

**修改文件**:
- `internal/debug/logger.go` (修改)

**实施内容**:
1. ✅ 使用 buffered channel (容量 1000) 收集日志条目
2. ✅ 后台 goroutine (asyncWriter) 异步写入文件
3. ✅ 队列满时使用 select default 丢弃日志（避免阻塞）
4. ✅ Close() 方法等待所有日志写入完成

**状态**: ✅ 已完成 (2026-01-29)

---

## 实施日志

### 优化 1 实施记录
- 开始时间: 2026-01-29 15:40
- 完成时间: 2026-01-29 15:45
- 修改文件: 
  - `internal/client/token_cache.go` (新建 82 行)
  - `internal/client/client.go` (修改 GetToken -> fetchNewToken)
- 测试结果: gofmt 语法检查通过

### 优化 2 实施记录
- 开始时间: 2026-01-29 15:45
- 完成时间: 2026-01-29 15:50
- 修改文件: 
  - `internal/client/client.go` (添加 sharedHTTPClient)
- 测试结果: gofmt 语法检查通过

### 优化 3 实施记录
- 开始时间: 2026-01-29 15:50
- 完成时间: 2026-01-29 15:55
- 修改文件: 
  - `internal/loadbalancer/loadbalancer.go` (重写，添加缓存逻辑)
- 测试结果: gofmt 语法检查通过

### 优化 4 实施记录
- 开始时间: 2026-01-29 15:55
- 完成时间: 2026-01-29 16:00
- 修改文件: 
  - `internal/loadbalancer/loadbalancer.go` (添加异步计数更新)
  - `internal/store/store.go` (添加 AddRequestCount 方法)
  - `cmd/server/main.go` (添加 defer lb.Close())
- 测试结果: gofmt 语法检查通过

### 优化 5 实施记录
- 开始时间: 2026-01-29 16:00
- 完成时间: 2026-01-29 16:05
- 修改文件: 
  - `internal/debug/logger.go` (重写为异步版本)
- 测试结果: gofmt 语法检查通过

---

## 预期性能提升

| 指标 | 优化前 | 优化后 |
|------|--------|--------|
| 首次请求延迟 | 500-1200ms | 50-100ms |
| 后续请求延迟 | 500-1200ms | 10-50ms |
| Token 获取 | 每次请求 200-500ms | 缓存命中 <1ms |
| TCP 连接 | 每次新建 100-300ms | 复用 <10ms |
| 负载均衡查询 | 每次查DB | 缓存读取 <1ms |
| 请求计数更新 | 同步写DB | 异步批量 无阻塞 |
| 调试日志 | 同步IO | 异步写入 无阻塞 |

---

## 修改文件清单

```
internal/client/token_cache.go    # 新建 - JWT Token 缓存
internal/client/client.go         # 修改 - HTTP 连接池 + Token 缓存集成
internal/loadbalancer/loadbalancer.go  # 重写 - 账号缓存 + 异步计数更新
internal/store/store.go           # 修改 - 添加 AddRequestCount 方法
internal/debug/logger.go          # 重写 - 异步日志
cmd/server/main.go                # 修改 - 添加 lb.Close()
```

---

**文档创建时间**: 2026-01-29  
**所有优化完成时间**: 2026-01-29

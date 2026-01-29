# Orchids-2api 优化计划

> 创建时间：2026-01-29
> 状态：已完成

---

## 问题诊断

根据服务器日志分析，当前请求慢的主要原因：

| 问题 | 耗时影响 | 日志证据 |
|------|---------|----------|
| 账号 Session 过期（401 Signed out） | 每个过期账号重试约 1 秒 | `Error: failed to get token: token request failed with status 401` |
| 无定时刷新机制 | 账号长时间不用会过期 | 多个账号连续 401 |
| Token 缓存无预热 | 首次请求必须等待 | `[TokenCache] 缓存未命中` |
| 无重试限制 | 可能无限重试 | 单请求耗时 8+ 秒 |

**关键理解**：401 错误不是账号失效，而是 Session 过期需要刷新。应通过定时保活避免过期。

---

## 优化任务列表

### P0：立即修复（解决慢的核心问题）

- [x] **P0-1：账号定时保活机制** ✅ 已完成
  - 后台定时刷新所有启用账号的 Session
  - 刷新间隔：每 30 分钟（Token 有效期约 50 分钟）
  - 刷新失败记录错误但不禁用账号
  - 启动时立即预热所有账号
  - 文件：`internal/keeper/keeper.go`（新建）

- [x] **P0-2：重试次数限制 + 指数退避** ✅ 已完成
  - 最大重试 3 次
  - 每次重试间隔指数增长（100ms, 200ms, 400ms）
  - 超过限制返回明确错误
  - 文件：`internal/handler/handler.go`

- [x] **P0-3：Token 缓存 Singleflight 去重** ✅ 已完成
  - 使用 singleflight 避免并发重复刷新
  - 多个请求使用同一账号时只刷新一次
  - 文件：`internal/client/token_cache.go`

### P1：性能优化

- [x] **P1-1：账号缓存 TTL 调整** ✅ 已完成
  - 账号缓存 TTL 从 5 秒改为 30 秒
  - 计数更新间隔从 5 秒改为 10 秒
  - 减少日志噪音
  - 文件：`internal/loadbalancer/loadbalancer.go`

- [x] **P1-2：加权选择算法优化** ✅ 已完成
  - 使用前缀和 + 二分查找
  - 时间复杂度从 O(n) 优化到 O(log n)
  - 文件：`internal/loadbalancer/loadbalancer.go`

- [x] **P1-3：Token 缓存大小限制 + 后台清理** ✅ 已完成
  - 限制最大缓存条目数（1000）
  - 后台定期清理过期 Token（每 5 分钟）
  - 避免内存泄漏
  - 文件：`internal/client/token_cache.go`

### P2：增强功能

- [x] **P2-1：账号健康检查 API** ✅ 已完成
  - 新增 `GET /api/accounts/health` 端点
  - 返回账号状态（active/refreshing/error）
  - 显示最后刷新时间和下次刷新时间
  - 文件：`internal/api/api.go`

- [x] **P2-2：请求日志增强** ✅ 已完成
  - 添加请求 ID
  - 结构化日志格式
  - 记录重试次数和耗时分解
  - 文件：`internal/handler/handler.go`

- [x] **P2-3：手动刷新账号 API** ✅ 已完成
  - 新增 `POST /api/accounts/{id}/refresh` 端点
  - 支持手动触发单个账号刷新
  - 文件：`internal/api/api.go`

- [x] **P2-4：成功/失败计数追踪** ✅ 已完成
  - 数据库新增 success_count 和 failure_count 字段
  - 请求成功/失败时异步更新计数
  - 管理面板显示成功/失败统计
  - 文件：`internal/store/store.go`, `internal/loadbalancer/loadbalancer.go`, `internal/handler/handler.go`, `web/static/index.html`

---

## 完成记录

| 任务 | 完成时间 | 备注 |
|------|---------|------|
| P0-1：账号定时保活机制 | 2026-01-29 | 新增 internal/keeper/keeper.go |
| P0-2：重试次数限制 + 指数退避 | 2026-01-29 | 最大重试 3 次，指数退避 |
| P0-3：Token 缓存 Singleflight 去重 | 2026-01-29 | 避免并发重复刷新 |
| P1-1：账号缓存 TTL 调整 | 2026-01-29 | 5s→30s，减少日志噪音 |
| P1-2：加权选择算法优化 | 2026-01-29 | O(n)→O(log n) 二分查找 |
| P1-3：Token 缓存大小限制 + 后台清理 | 2026-01-29 | 最大 1000 条，每 5 分钟清理 |
| P2-1：账号健康检查 API | 2026-01-29 | GET /api/accounts/health |
| P2-2：请求日志增强 | 2026-01-29 | 添加请求 ID 和重试次数 |
| P2-3：手动刷新账号 API | 2026-01-29 | POST /api/accounts/{id}/refresh |
| P2-4：成功/失败计数追踪 | 2026-01-29 | 数据库+面板显示 |

---

## 技术细节

### P0-1 账号保活机制设计

```
┌─────────────────────────────────────────────────────────────┐
│                      AccountKeeper                          │
├─────────────────────────────────────────────────────────────┤
│  启动时：                                                    │
│    1. 获取所有启用账号                                        │
│    2. 并发刷新所有账号 Session（预热）                         │
│    3. 更新 Token 缓存                                        │
│                                                             │
│  定时任务（每 30 分钟）：                                     │
│    1. 获取所有启用账号                                        │
│    2. 逐个刷新 Session（避免并发过高）                         │
│    3. 刷新成功：更新 Token 缓存 + 记录时间                     │
│    4. 刷新失败：记录错误日志，下次重试                         │
└─────────────────────────────────────────────────────────────┘
```

**核心代码结构**：

```go
// internal/keeper/keeper.go
package keeper

type AccountKeeper struct {
    store       *store.Store
    tokenCache  *client.TokenCache
    refreshInterval time.Duration
    stopCh      chan struct{}

    mu          sync.RWMutex
    lastRefresh map[int64]time.Time  // accountID -> 最后刷新时间
    lastError   map[int64]error      // accountID -> 最后错误
}

const (
    DefaultRefreshInterval = 30 * time.Minute
    StartupConcurrency     = 5  // 启动时并发刷新数
)

func New(store *store.Store, tokenCache *client.TokenCache) *AccountKeeper

func (k *AccountKeeper) Start()           // 启动保活任务
func (k *AccountKeeper) Stop()            // 停止保活任务
func (k *AccountKeeper) WarmUp()          // 预热所有账号
func (k *AccountKeeper) RefreshAccount(id int64) error  // 刷新单个账号
func (k *AccountKeeper) GetStatus() map[int64]AccountStatus  // 获取状态
```

### P0-2 重试配置

```go
const (
    MaxRetryCount    = 3
    BaseRetryDelay   = 100 * time.Millisecond
)

// 退避计算
backoff := time.Duration(1<<retryCount) * BaseRetryDelay
```

### P0-3 Singleflight 模式

```go
import "golang.org/x/sync/singleflight"

type TokenCache struct {
    mu     sync.RWMutex
    tokens map[string]*CachedToken
    group  singleflight.Group
}

func (tc *TokenCache) GetOrFetch(key string, fetch func() (string, error)) (string, error) {
    if token := tc.Get(key); token != "" {
        return token, nil
    }
    result, err, _ := tc.group.Do(key, func() (interface{}, error) {
        jwt, err := fetch()
        if err == nil {
            tc.Set(key, jwt)
        }
        return jwt, err
    })
    if err != nil {
        return "", err
    }
    return result.(string), nil
}
```

---

## 刷新流程

```
请求进入
    ↓
从 TokenCache 获取 Token
    ↓
┌─ 命中 ──→ 直接使用（快速路径）
│
└─ 未命中
      ↓
   Singleflight 去重
      ↓
   调用 clerk.FetchAccountInfo(cookie)
      ↓
   ┌─ 成功 ──→ 缓存 Token，继续请求
   │
   └─ 失败（401）
         ↓
      切换账号重试（最多 3 次）
         ↓
      ┌─ 成功 ──→ 返回结果
      │
      └─ 全部失败 ──→ 返回错误
```

---

## 保活流程

```
服务启动
    ↓
AccountKeeper.Start()
    ↓
WarmUp()（并发预热所有账号）
    ↓
启动定时器（每 30 分钟）
    ↓
定时刷新所有启用账号
    ↓
更新 TokenCache + 记录状态
```

---

## 参考项目

- CLIProxyAPIPlus：`/Users/lvzhentao/gitee-learn/CPAP-mine/CLIProxyAPIPlus`
  - `internal/auth/orchids/session_cache.go` - Session 缓存 + Singleflight
  - `internal/auth/kiro/cooldown.go` - 冷却管理
  - 最小刷新间隔防止频繁刷新

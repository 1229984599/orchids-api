package client

import (
	"log"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// 缓存配置
const (
	MaxCacheSize        = 1000              // 最大缓存条目数
	CacheCleanupInterval = 5 * time.Minute  // 后台清理间隔
)

// CachedToken 缓存的 Token 信息
type CachedToken struct {
	JWT       string
	ExpiresAt time.Time
}

// TokenCache JWT Token 缓存管理器
type TokenCache struct {
	mu     sync.RWMutex
	tokens map[string]*CachedToken
	group  singleflight.Group // Singleflight 去重
}

// 全局 Token 缓存实例
var tokenCache = &TokenCache{
	tokens: make(map[string]*CachedToken),
}

// 确保清理任务只启动一次
var cleanupOnce sync.Once

// startCleanup 启动后台清理任务
func (tc *TokenCache) startCleanup() {
	cleanupOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(CacheCleanupInterval)
			defer ticker.Stop()
			for range ticker.C {
				tc.cleanupExpired()
			}
		}()
		log.Println("[TokenCache] 后台清理任务已启动")
	})
}

// cleanupExpired 清理过期的 Token
func (tc *TokenCache) cleanupExpired() {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	now := time.Now()
	expired := 0
	for sessionID, cached := range tc.tokens {
		if now.After(cached.ExpiresAt) {
			delete(tc.tokens, sessionID)
			expired++
		}
	}

	if expired > 0 {
		log.Printf("[TokenCache] 已清理 %d 个过期 Token，剩余 %d 个", expired, len(tc.tokens))
	}
}

// evictOldest 淘汰最旧的 Token（当缓存满时）
func (tc *TokenCache) evictOldest() {
	// 找到最早过期的 Token
	var oldestKey string
	var oldestTime time.Time

	for sessionID, cached := range tc.tokens {
		if oldestKey == "" || cached.ExpiresAt.Before(oldestTime) {
			oldestKey = sessionID
			oldestTime = cached.ExpiresAt
		}
	}

	if oldestKey != "" {
		delete(tc.tokens, oldestKey)
		log.Printf("[TokenCache] 缓存已满，淘汰最旧的 Token: %s...", oldestKey[:16])
	}
}

// GetCachedToken 获取缓存的 Token
// 如果 Token 不存在或即将过期（提前 5 分钟），返回空和 false
func (tc *TokenCache) GetCachedToken(sessionID string) (string, bool) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	cached, exists := tc.tokens[sessionID]
	if !exists {
		return "", false
	}

	// 提前 5 分钟过期，确保返回的 Token 仍然有效
	if time.Now().Add(5 * time.Minute).After(cached.ExpiresAt) {
		return "", false
	}

	return cached.JWT, true
}

// SetCachedToken 缓存 Token
func (tc *TokenCache) SetCachedToken(sessionID, jwt string, ttl time.Duration) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	// 检查是否超过最大缓存大小
	if len(tc.tokens) >= MaxCacheSize {
		tc.evictOldest()
	}

	tc.tokens[sessionID] = &CachedToken{
		JWT:       jwt,
		ExpiresAt: time.Now().Add(ttl),
	}

	// 确保清理任务已启动
	tc.startCleanup()
}

// ClearToken 清除指定 session 的缓存 Token（用于 Token 失效时）
func (tc *TokenCache) ClearToken(sessionID string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	delete(tc.tokens, sessionID)
}

// ClearAllTokens 清除所有缓存的 Token
func (tc *TokenCache) ClearAllTokens() {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	tc.tokens = make(map[string]*CachedToken)
}

// Stats 返回缓存统计信息
func (tc *TokenCache) Stats() (total int, valid int) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	total = len(tc.tokens)
	now := time.Now().Add(5 * time.Minute)
	for _, cached := range tc.tokens {
		if now.Before(cached.ExpiresAt) {
			valid++
		}
	}
	return
}

// GetOrFetch 获取 Token，如果缓存未命中则调用 fetch 函数获取
// 使用 Singleflight 确保同一 sessionID 只会并发获取一次
func (tc *TokenCache) GetOrFetch(sessionID string, fetch func() (string, error)) (string, error) {
	// 1. 先尝试从缓存获取
	if jwt, ok := tc.GetCachedToken(sessionID); ok {
		return jwt, nil
	}

	// 2. 使用 Singleflight 去重，确保同一 sessionID 只获取一次
	result, err, _ := tc.group.Do(sessionID, func() (interface{}, error) {
		// 再次检查缓存（双重检查）
		if jwt, ok := tc.GetCachedToken(sessionID); ok {
			return jwt, nil
		}

		// 调用 fetch 获取新 Token
		jwt, err := fetch()
		if err != nil {
			return "", err
		}

		// 缓存 Token（50 分钟有效期）
		tc.SetCachedToken(sessionID, jwt, 50*time.Minute)
		return jwt, nil
	})

	if err != nil {
		return "", err
	}
	return result.(string), nil
}

// GetGlobalCache 获取全局缓存实例
func GetGlobalCache() *TokenCache {
	return tokenCache
}

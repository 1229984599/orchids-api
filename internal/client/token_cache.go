package client

import (
	"sync"
	"time"
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
}

// 全局 Token 缓存实例
var tokenCache = &TokenCache{
	tokens: make(map[string]*CachedToken),
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

	tc.tokens[sessionID] = &CachedToken{
		JWT:       jwt,
		ExpiresAt: time.Now().Add(ttl),
	}
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

// TokenCacheStats 返回缓存统计信息
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

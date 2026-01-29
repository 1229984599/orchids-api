package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"time"

	"orchids-api/internal/config"
	"orchids-api/internal/debug"
	"orchids-api/internal/store"
)

const upstreamURL = "https://orchids-server.calmstone-6964e08a.westeurope.azurecontainerapps.io/agent/coding-agent"

// Token 缓存有效期（50 分钟，假设 Clerk Token 有效期 1 小时）
const tokenCacheTTL = 50 * time.Minute

// sharedHTTPClient 全局共享的 HTTP 客户端，复用 TCP/TLS 连接
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
		ForceAttemptHTTP2: true, // 尝试使用 HTTP/2
	},
}

type Client struct {
	config     *config.Config
	account    *store.Account
	httpClient *http.Client
}

type TokenResponse struct {
	JWT string `json:"jwt"`
}

type AgentRequest struct {
	Prompt        string        `json:"prompt"`
	ChatHistory   []interface{} `json:"chatHistory"`
	ProjectID     string        `json:"projectId"`
	CurrentPage   interface{}   `json:"currentPage"`
	AgentMode     string        `json:"agentMode"`
	Mode          string        `json:"mode"`
	GitRepoUrl    string        `json:"gitRepoUrl"`
	Email         string        `json:"email"`
	ChatSessionID int           `json:"chatSessionId"`
	UserID        string        `json:"userId"`
	APIVersion    int           `json:"apiVersion"`
	Model         string        `json:"model,omitempty"`
}

type SSEMessage struct {
	Type  string                 `json:"type"`
	Event map[string]interface{} `json:"event,omitempty"`
	Raw   map[string]interface{} `json:"-"`
}

func New(cfg *config.Config) *Client {
	return &Client{
		config:     cfg,
		httpClient: sharedHTTPClient, // 使用全局共享客户端
	}
}

func NewFromAccount(acc *store.Account) *Client {
	cfg := &config.Config{
		SessionID:    acc.SessionID,
		ClientCookie: acc.ClientCookie,
		ClientUat:    acc.ClientUat,
		ProjectID:    acc.ProjectID,
		UserID:       acc.UserID,
		AgentMode:    acc.AgentMode,
		Email:        acc.Email,
	}
	return &Client{
		config:     cfg,
		account:    acc,
		httpClient: sharedHTTPClient, // 使用全局共享客户端
	}
}

// GetToken 获取 JWT Token（优先从缓存获取，使用 Singleflight 去重）
func (c *Client) GetToken() (string, error) {
	return tokenCache.GetOrFetch(c.config.SessionID, func() (string, error) {
		log.Printf("[TokenCache] 缓存未命中，获取新Token: session=%s", c.config.SessionID[:16]+"...")
		jwt, err := c.fetchNewToken()
		if err != nil {
			return "", err
		}
		log.Printf("[TokenCache] Token已缓存: session=%s, TTL=%v", c.config.SessionID[:16]+"...", tokenCacheTTL)
		return jwt, nil
	})
}

// fetchNewToken 从 Clerk API 获取新的 JWT Token
func (c *Client) fetchNewToken() (string, error) {
	url := fmt.Sprintf("https://clerk.orchids.app/v1/client/sessions/%s/tokens?__clerk_api_version=2025-11-10&_clerk_js_version=5.117.0", c.config.SessionID)

	req, err := http.NewRequest("POST", url, strings.NewReader("organization_id="))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", c.config.GetCookies())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		// Token 获取失败，清除可能的旧缓存
		tokenCache.ClearToken(c.config.SessionID)
		return "", fmt.Errorf("token request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}

	return tokenResp.JWT, nil
}

// InvalidateToken 使指定 session 的 Token 缓存失效（用于 Token 过期时调用）
func (c *Client) InvalidateToken() {
	tokenCache.ClearToken(c.config.SessionID)
	log.Printf("[TokenCache] Token已失效: session=%s", c.config.SessionID[:16]+"...")
}

func (c *Client) SendRequest(ctx context.Context, prompt string, chatHistory []interface{}, model string, onMessage func(SSEMessage), logger *debug.Logger) error {
	token, err := c.GetToken()
	if err != nil {
		return fmt.Errorf("failed to get token: %w", err)
	}

	payload := AgentRequest{
		Prompt:        prompt,
		ChatHistory:   chatHistory,
		ProjectID:     c.config.ProjectID,
		CurrentPage:   map[string]interface{}{},
		AgentMode:     c.config.AgentMode,
		Mode:          "agent",
		GitRepoUrl:    "",
		Email:         c.config.Email,
		ChatSessionID: rand.IntN(90000000) + 10000000,
		UserID:        c.config.UserID,
		APIVersion:    2,
		Model:         model,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", upstreamURL, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Orchids-Api-Version", "2")

	// 记录上游请求
	if logger != nil {
		headers := map[string]string{
			"Accept":                "text/event-stream",
			"Authorization":         "Bearer [REDACTED]",
			"Content-Type":          "application/json",
			"X-Orchids-Api-Version": "2",
		}
		logger.LogUpstreamRequest(upstreamURL, headers, payload)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 如果返回 401，可能是 Token 过期，清除缓存
	if resp.StatusCode == http.StatusUnauthorized {
		c.InvalidateToken()
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upstream request failed with status %d (token invalidated): %s", resp.StatusCode, string(body))
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upstream request failed with status %d: %s", resp.StatusCode, string(body))
	}

	reader := bufio.NewReader(resp.Body)
	var buffer strings.Builder

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		buffer.WriteString(line)

		if line == "\n" {
			eventData := buffer.String()
			buffer.Reset()

			lines := strings.Split(eventData, "\n")
			for _, l := range lines {
				if strings.HasPrefix(l, "data: ") {
					rawData := strings.TrimPrefix(l, "data: ")

					var msg map[string]interface{}
					if err := json.Unmarshal([]byte(rawData), &msg); err != nil {
						continue
					}

					msgType, _ := msg["type"].(string)

					// 记录上游 SSE
					if logger != nil {
						logger.LogUpstreamSSE(msgType, rawData)
					}

					// 只处理 "model" 类型的事件
					if msgType != "model" {
						continue
					}

					sseMsg := SSEMessage{
						Type: msgType,
						Raw:  msg,
					}

					if event, ok := msg["event"].(map[string]interface{}); ok {
						sseMsg.Event = event
					}

					onMessage(sseMsg)
				}
			}
		}
	}

	return nil
}

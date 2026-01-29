package clerk

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type ClientResponse struct {
	Response struct {
		ID                  string `json:"id"`
		LastActiveSessionID string `json:"last_active_session_id"`
		Sessions            []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			User   struct {
				ID             string `json:"id"`
				EmailAddresses []struct {
					EmailAddress string `json:"email_address"`
				} `json:"email_addresses"`
			} `json:"user"`
			LastActiveToken struct {
				JWT string `json:"jwt"`
			} `json:"last_active_token"`
		} `json:"sessions"`
	} `json:"response"`
}

type AccountInfo struct {
	SessionID    string
	ClientCookie string
	ClientUat    string
	ProjectID    string
	UserID       string
	Email        string
	JWT          string
}

func FetchAccountInfo(clientCookie string) (*AccountInfo, error) {
	url := "https://clerk.orchids.app/v1/client?__clerk_api_version=2025-11-10&_clerk_js_version=5.117.0"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Orchids/0.0.57 Chrome/138.0.7204.251 Electron/37.10.3 Safari/537.36")
	req.Header.Set("Accept-Language", "zh-CN")
	req.AddCookie(&http.Cookie{Name: "__client", Value: clientCookie})

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch client info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var clientResp ClientResponse
	if err := json.NewDecoder(resp.Body).Decode(&clientResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(clientResp.Response.Sessions) == 0 {
		return nil, fmt.Errorf("no active sessions found")
	}

	session := clientResp.Response.Sessions[0]
	if len(session.User.EmailAddresses) == 0 {
		return nil, fmt.Errorf("no email address found")
	}

	return &AccountInfo{
		SessionID:    clientResp.Response.LastActiveSessionID,
		ClientCookie: clientCookie,
		ClientUat:    fmt.Sprintf("%d", time.Now().Unix()),
		ProjectID:    "280b7bae-cd29-41e4-a0a6-7f603c43b607",
		UserID:       session.User.ID,
		Email:        session.User.EmailAddresses[0].EmailAddress,
		JWT:          session.LastActiveToken.JWT,
	}, nil
}

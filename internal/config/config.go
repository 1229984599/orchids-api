package config

import (
	"os"
)

type Config struct {
	Port         string
	DebugEnabled bool
	SessionID    string
	ClientCookie string
	ClientUat    string
	ProjectID    string
	UserID       string
	AgentMode    string
	Email        string
	AdminUser    string
	AdminPass    string
	AdminPath    string
}

func Load() *Config {
	return &Config{
		Port:         getEnv("PORT", "3002"),
		DebugEnabled: getEnv("DEBUG_ENABLED", "true") == "true",
		SessionID:    getEnv("SESSION_ID", "sess_38BUxtHf7iMY9B3A1mbM85favDX"),
		ClientCookie: getEnv("CLIENT_COOKIE", "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJpZCI6ImNsaWVudF8zN0VrNWZtNk0zcXUwS0VBRlVNMmhxeE92ZVYiLCJyb3RhdGluZ190b2tlbiI6Ink2aTF4eHdvY3JzbHJmbTZsdzFtYmY3cnl4dGcwdXIxYWlhbnZiMTkifQ.U5Zt0RwA4oeFWc7b66qsHdw2Q-kV8p1rnsw7Lo6CwPqZfFGG25IEaqty3s7tZrQcY7yzNikqFHlxU7s1j-rRcopZqDEWtBRBHdrB9wDGJzXw7cA1DgL5nfDWHUN50pNut3Ol0MFZE0Lm_plXrlzUifJ9CLooBIzRxcIBGn8W2h-KNCZ5fXMMlJUx6j_Q0YfrVctJYekhtgdH0_5EDFbjEmAsMesaiNuYZ2UbMH1o0LsrPrv0hxEZt_eI3HBEvmkCrtYEL02tTFwVUb08Y5Kme9Oq1E7QO1qOz28OKl7kN_aqv6XFxe75MyBUtBF_9W2gcYi0jORF2m3x1XvmB1RV5A"),
		ClientUat:    getEnv("CLIENT_UAT", "1768272707"),
		ProjectID:    getEnv("PROJECT_ID", "280b7bae-cd29-41e4-a0a6-7f603c43b607"),
		UserID:       getEnv("USER_ID", "user_38BUxvjgpzOuZwztspEW9ZYroXs"),
		AgentMode:    getEnv("AGENT_MODE", "claude-opus-4.5"),
		Email:        getEnv("EMAIL", "crushla4@swsdz.com"),
		AdminUser:    getEnv("ADMIN_USER", "admin"),
		AdminPass:    getEnv("ADMIN_PASS", "admin123"),
		AdminPath:    getEnv("ADMIN_PATH", "/admin"),
	}
}

func (c *Config) GetCookies() string {
	return "__client=" + c.ClientCookie + "; __client_uat=" + c.ClientUat
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

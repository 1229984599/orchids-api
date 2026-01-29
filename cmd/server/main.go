package main

import (
	"bufio"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"orchids-api/internal/api"
	"orchids-api/internal/config"
	"orchids-api/internal/debug"
	"orchids-api/internal/handler"
	"orchids-api/internal/keeper"
	"orchids-api/internal/loadbalancer"
	"orchids-api/internal/logger"
	"orchids-api/internal/middleware"
	"orchids-api/internal/store"
	"orchids-api/web"
)

func loadEnv() {
	file, err := os.Open(".env")
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			value = strings.Trim(value, "\"'")
			os.Setenv(key, value)
		}
	}
}

func main() {
	loadEnv()

	cfg := config.Load()

	// 启动时清理所有调试日志
	if cfg.DebugEnabled {
		debug.CleanupAllLogs()
		log.Println("已清理调试日志目录")
	}

	dataDir := filepath.Join(".", "data")
	os.MkdirAll(dataDir, 0755)
	dbPath := filepath.Join(dataDir, "orchids.db")

	s, err := store.New(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer s.Close()

	lb := loadbalancer.New(s)
	defer lb.Close() // 确保程序退出时关闭负载均衡器，刷新待更新的计数

	// 启动账号保活服务
	accountKeeper := keeper.New(s)
	accountKeeper.Start()
	defer accountKeeper.Stop()

	// 创建请求日志收集器
	requestLogger := logger.New()
	log.Println("请求日志系统已初始化")

	apiHandler := api.NewWithKeeperAndLogger(s, accountKeeper, requestLogger)
	h := handler.NewWithAll(cfg, lb, accountKeeper, requestLogger)

	mux := http.NewServeMux()

	mux.HandleFunc("/v1/messages", h.HandleMessages)

	mux.HandleFunc("/api/accounts", middleware.BasicAuth(cfg.AdminUser, cfg.AdminPass, apiHandler.HandleAccounts))
	mux.HandleFunc("/api/accounts/", middleware.BasicAuth(cfg.AdminUser, cfg.AdminPass, apiHandler.HandleAccountByID))
	mux.HandleFunc("/api/accounts/health", middleware.BasicAuth(cfg.AdminUser, cfg.AdminPass, apiHandler.HandleAccountsHealth))
	mux.HandleFunc("/api/refresh-all", middleware.BasicAuth(cfg.AdminUser, cfg.AdminPass, apiHandler.HandleRefreshAll))
	mux.HandleFunc("/api/batch-delete", middleware.BasicAuth(cfg.AdminUser, cfg.AdminPass, apiHandler.HandleBatchDelete))
	mux.HandleFunc("/api/export", middleware.BasicAuth(cfg.AdminUser, cfg.AdminPass, apiHandler.HandleExport))
	mux.HandleFunc("/api/import", middleware.BasicAuth(cfg.AdminUser, cfg.AdminPass, apiHandler.HandleImport))

	// 日志相关 API
	mux.HandleFunc("/api/logs", middleware.BasicAuth(cfg.AdminUser, cfg.AdminPass, apiHandler.HandleLogs))
	mux.HandleFunc("/api/logs/stream", middleware.BasicAuth(cfg.AdminUser, cfg.AdminPass, apiHandler.HandleLogsSSE))
	mux.HandleFunc("/api/logs/stats", middleware.BasicAuth(cfg.AdminUser, cfg.AdminPass, apiHandler.HandleLogsStats))

	mux.HandleFunc(cfg.AdminPath+"/", middleware.BasicAuthHandler(cfg.AdminUser, cfg.AdminPass, http.StripPrefix(cfg.AdminPath, web.StaticHandler())))

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	log.Printf("Server running on port %s", cfg.Port)
	log.Printf("Admin UI: http://localhost:%s%s", cfg.Port, cfg.AdminPath)
	if err := http.ListenAndServe(":"+cfg.Port, mux); err != nil {
		log.Fatal(err)
	}
}

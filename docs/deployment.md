# 部署指南

## Docker 构建

### 使用构建脚本

```bash
./build.sh
```

### 手动构建

```bash
docker build --platform linux/amd64 -f ./Dockerfile -t opus-api:latest .
```

## 运行容器

### 加载镜像

```bash
docker load -i opus-api.tar
```

### 使用 docker-compose

```bash
docker compose up -d
```

### 查看日志

```bash
docker compose logs -f opus-api
```

## 本地开发

### 安装依赖

```bash
go mod download
```

### 运行服务

```bash
go run ./cmd/server/main.go
```

## 测试

### 运行测试

```bash
go test ./...
```

### 现有测试

- `internal/tiktoken/tokenizer_test.go`
  - Token 估算测试
  - 文本 Token 测试
  - CJK 字符检测测试

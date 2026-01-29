package prompt

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ImageSource 表示图片来源
type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// CacheControl 缓存控制
type CacheControl struct {
	Type string `json:"type"`
}

// ContentBlock 表示消息内容中的一个块
type ContentBlock struct {
	Type   string       `json:"type"`
	Text   string       `json:"text,omitempty"`
	Source *ImageSource `json:"source,omitempty"`

	// tool_use 字段
	ID    string      `json:"id,omitempty"`
	Name  string      `json:"name,omitempty"`
	Input interface{} `json:"input,omitempty"`

	// tool_result 字段
	ToolUseID    string        `json:"tool_use_id,omitempty"`
	Content      interface{}   `json:"content,omitempty"`
	IsError      bool          `json:"is_error,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// MessageContent 联合类型
type MessageContent struct {
	Text   string
	Blocks []ContentBlock
}

func (mc *MessageContent) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		mc.Text = text
		mc.Blocks = nil
		return nil
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(data, &blocks); err == nil {
		mc.Text = ""
		mc.Blocks = blocks
		return nil
	}

	return fmt.Errorf("content must be string or array of content blocks")
}

func (mc MessageContent) MarshalJSON() ([]byte, error) {
	if mc.Blocks != nil {
		return json.Marshal(mc.Blocks)
	}
	return json.Marshal(mc.Text)
}

func (mc *MessageContent) IsString() bool {
	return mc.Blocks == nil
}

func (mc *MessageContent) GetText() string {
	return mc.Text
}

func (mc *MessageContent) GetBlocks() []ContentBlock {
	return mc.Blocks
}

// Message 消息结构
type Message struct {
	Role    string         `json:"role"`
	Content MessageContent `json:"content"`
}

// SystemItem 系统提示词项
type SystemItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ClaudeAPIRequest Claude API 请求结构
type ClaudeAPIRequest struct {
	Model    string        `json:"model"`
	Messages []Message     `json:"messages"`
	System   []SystemItem  `json:"system"`
	Tools    []interface{} `json:"tools"`
	Stream   bool          `json:"stream"`
}

// 系统预设提示词
const systemPreset = `你是 AI 编程助手，通过代理服务与用户交互。

## 对话历史结构
- <turn index="N" role="user|assistant"> 包含每轮对话
- <tool_use id="..." name="..."> 表示工具调用
- <tool_result tool_use_id="..."> 表示工具执行结果

## 规则
1. 仅依赖当前工具和历史上下文
2. 用户在本地环境工作
3. 回复简洁专业`

// FormatMessagesAsMarkdown 将 Claude messages 转换为结构化的对话历史
func FormatMessagesAsMarkdown(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}

	var parts []string

	// 排除最后一条 user 消息（它会单独作为当前请求）
	historyMessages := messages
	if len(messages) > 0 && messages[len(messages)-1].Role == "user" {
		historyMessages = messages[:len(messages)-1]
	}

	turnIndex := 1
	for _, msg := range historyMessages {
		switch msg.Role {
		case "user":
			userContent := formatUserMessage(msg.Content)
			if userContent != "" {
				parts = append(parts, fmt.Sprintf("<turn index=\"%d\" role=\"user\">\n%s\n</turn>", turnIndex, userContent))
				turnIndex++
			}
		case "assistant":
			assistantContent := formatAssistantMessage(msg.Content)
			if assistantContent != "" {
				parts = append(parts, fmt.Sprintf("<turn index=\"%d\" role=\"assistant\">\n%s\n</turn>", turnIndex, assistantContent))
				turnIndex++
			}
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, "\n\n")
}

// formatUserMessage 格式化用户消息
func formatUserMessage(content MessageContent) string {
	var parts []string

	if content.IsString() {
		text := strings.TrimSpace(content.GetText())
		if text != "" {
			parts = append(parts, text)
		}
		return strings.Join(parts, "\n")
	}

	for _, block := range content.GetBlocks() {
		switch block.Type {
		case "text":
			text := strings.TrimSpace(block.Text)
			if text != "" {
				parts = append(parts, text)
			}
		case "image":
			if block.Source != nil {
				parts = append(parts, fmt.Sprintf("[Image: %s]", block.Source.MediaType))
			}
		case "tool_result":
			resultStr := formatToolResultContent(block.Content)
			errorAttr := ""
			if block.IsError {
				errorAttr = ` is_error="true"`
			}
			parts = append(parts, fmt.Sprintf("<tool_result tool_use_id=\"%s\"%s>\n%s\n</tool_result>", block.ToolUseID, errorAttr, resultStr))
		}
	}

	return strings.Join(parts, "\n")
}

// formatAssistantMessage 格式化 assistant 消息
func formatAssistantMessage(content MessageContent) string {
	var parts []string

	if content.IsString() {
		text := strings.TrimSpace(content.GetText())
		if text != "" {
			parts = append(parts, text)
		}
		return strings.Join(parts, "\n")
	}

	for _, block := range content.GetBlocks() {
		switch block.Type {
		case "text":
			text := strings.TrimSpace(block.Text)
			if text != "" {
				parts = append(parts, text)
			}
		case "thinking":
			// 跳过 thinking 内容，不放入历史
			continue
		case "tool_use":
			// 使用简洁的 JSON 格式表示工具调用
			inputJSON, _ := json.Marshal(block.Input)
			parts = append(parts, fmt.Sprintf("<tool_use id=\"%s\" name=\"%s\">\n%s\n</tool_use>", block.ID, block.Name, string(inputJSON)))
		}
	}

	return strings.Join(parts, "\n")
}

// formatToolResultContent 格式化工具结果内容
func formatToolResultContent(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, item := range v {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if text, ok := itemMap["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
		jsonBytes, _ := json.Marshal(v)
		return string(jsonBytes)
	default:
		jsonBytes, _ := json.Marshal(v)
		return string(jsonBytes)
	}
}

// BuildPromptV2 构建优化的 prompt
func BuildPromptV2(req ClaudeAPIRequest) string {
	var sections []string

	// 1. 原始系统提示词（来自客户端）
	var clientSystem []string
	for _, s := range req.System {
		if s.Type == "text" && s.Text != "" {
			clientSystem = append(clientSystem, s.Text)
		}
	}
	if len(clientSystem) > 0 {
		sections = append(sections, fmt.Sprintf("<client_system>\n%s\n</client_system>", strings.Join(clientSystem, "\n\n")))
	}

	// 2. 代理系统预设
	sections = append(sections, fmt.Sprintf("<proxy_instructions>\n%s\n</proxy_instructions>", systemPreset))

	// 3. 可用工具列表
	if len(req.Tools) > 0 {
		var toolNames []string
		for _, t := range req.Tools {
			if tm, ok := t.(map[string]interface{}); ok {
				if name, ok := tm["name"].(string); ok {
					toolNames = append(toolNames, name)
				}
			}
		}
		if len(toolNames) > 0 {
			sections = append(sections, fmt.Sprintf("<available_tools>\n%s\n</available_tools>", strings.Join(toolNames, ", ")))
		}
	}

	// 4. 对话历史
	history := FormatMessagesAsMarkdown(req.Messages)
	if history != "" {
		sections = append(sections, fmt.Sprintf("<conversation_history>\n%s\n</conversation_history>", history))
	}

	// 5. 当前用户请求
	var currentRequest string
	if len(req.Messages) > 0 {
		lastMsg := req.Messages[len(req.Messages)-1]
		if lastMsg.Role == "user" {
			currentRequest = formatUserMessage(lastMsg.Content)
		}
	}
	if strings.TrimSpace(currentRequest) == "" {
		currentRequest = "继续"
	}

	sections = append(sections, fmt.Sprintf("<user_request>\n%s\n</user_request>", currentRequest))

	return strings.Join(sections, "\n\n")
}

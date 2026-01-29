package tiktoken

import (
	"unicode"
)

// EstimateTokens 估算文本的 token 数量
// 使用近似算法：
// - 英文单词约 0.75 token/word
// - 中文字符约 1.5 token/char
// - 数字和特殊字符单独计算
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}

	tokens := 0
	inWord := false
	wordLength := 0

	for _, r := range text {
		// ASCII 字符（英文、数字、标点）
		if r < 128 {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				if !inWord {
					inWord = true
					wordLength = 0
				}
				wordLength += 4
			} else {
				// 标点符号和空格
				if inWord {
					// 单词结束，计算这个单词的 tokens
					tokens += estimateWordTokens(wordLength)
					inWord = false
				}
				// 空格通常不占 token，其他符号占 1 token
				if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
					tokens++
				}
			}
		} else {
			// 非 ASCII 字符（主要是中文等亚洲语言）
			if inWord {
				tokens += estimateWordTokens(wordLength)
				inWord = false
			}
			// 中文等复杂字符通常约 1.5 tokens
			tokens += 10
		}
	}

	// 处理最后一个单词
	if inWord {
		tokens += estimateWordTokens(wordLength)
	}

	return tokens
}

// estimateWordTokens 估算英文单词的 token 数量
// 常见子词分词规则：
// - 1-4 字符的词通常约 1 token
// - 5-8 字符的词通常约 1-2 tokens
// - 9-12 字符的词通常约 2 tokens
// - 更长的词约每 4 字符 1 token
func estimateWordTokens(length int) int {
	if length <= 4 {
		return 1
	}
	if length <= 8 {
		return 1 + (length-4)/4
	}
	if length <= 12 {
		return 2 + (length-8)/5
	}
	return 3 + (length-12)/4
}

// EstimateInputTokens 估算输入 prompt 的 token 数量
func EstimateInputTokens(prompt string) int {
	return EstimateTokens(prompt)
}

// EstimateOutputTokens 估算输出文本的 token 数量
func EstimateOutputTokens(text string) int {
	return EstimateTokens(text)
}

// EstimateChineseTokens 专门估算中文文本的 token 数量
// 中文平均每个字符约 1.5 tokens
func EstimateChineseTokens(text string) int {
	count := 0
	for _, r := range text {
		// 检查是否是中文字符（CJK 统一表意文字）
		if r >= 0x4E00 && r <= 0x9FFF {
			count++
		}
	}
	return int(float64(count) * 1.5)
}

// EstimateTextTokens 简单估算：每个字符算 4 个 token
func EstimateTextTokens(text string) int {
	count := 0
	for range text {
		count++
	}
	return count / 3
}

// EstimateMessagesTokens 估算消息列表的 token 数量
// 考虑消息格式和角色标记的开销
func EstimateMessagesTokens(messages []map[string]interface{}) int {
	tokens := 0

	for _, msg := range messages {
		// 角色标记约 3 tokens
		tokens += 3

		// 消息分隔符约 4 tokens
		tokens += 4

		// 内容
		if content, ok := msg["content"].(string); ok {
			tokens += EstimateTextTokens(content)
		}
	}

	// 整体格式开销约 3 tokens
	tokens += 3

	return tokens
}

// IsCJK 判断是否是中日韩字符
func IsCJK(r rune) bool {
	// CJK 统一表意文字
	if r >= 0x4E00 && r <= 0x9FFF {
		return true
	}
	// CJK 扩展 A
	if r >= 0x3400 && r <= 0x4DBF {
		return true
	}
	// CJK 扩展 B-F
	if r >= 0x20000 && r <= 0x2EBEF {
		return true
	}
	// 平假名
	if r >= 0x3040 && r <= 0x309F {
		return true
	}
	// 片假名
	if r >= 0x30A0 && r <= 0x30FF {
		return true
	}
	// 韩文
	if r >= 0xAC00 && r <= 0xD7A3 {
		return true
	}
	// 标点符号
	if r >= 0x3000 && r <= 0x303F {
		return true
	}
	return false
}

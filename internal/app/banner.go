package app

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Karlsk/go-hify/internal/platform/config"
)

// printBanner 打印启动 banner 到 stdout——给人看的「框形信息块」，
// 区别于 logStartup 的 slog 结构化记录（给日志系统检索）。内容脱敏：
// providers 只列名字、绝不记 Key（CLAUDE.md §密钥 / Go 日志规范第 20 条）。
func printBanner(cfg *config.Config) {
	const keyCol = len("providers") // 信息行 key 列宽，对齐到最长 key（providers=9）

	type kv struct{ k, v string }
	rows := []kv{
		{"version", version},
		{"listen", ":" + cfg.Server.Port},
		{"log", fmt.Sprintf("%s (%s)", cfg.Logging.Level, cfg.Logging.Format)},
		{"budget", fmt.Sprintf("$%s/day · %d rpm", formatCents(cfg.Budget.DailyBudgetUSDCents), cfg.Budget.UserRPM)},
		{"providers", providerList(cfg)},
	}

	lines := []string{"  Hify  ·  AI Agent Platform"}
	for _, r := range rows {
		lines = append(lines, "  "+r.k+strings.Repeat(" ", keyCol-len(r.k))+"  "+r.v)
	}

	innerW := 0
	for _, l := range lines {
		if w := utf8.RuneCountInString(l); w > innerW {
			innerW = w
		}
	}
	innerW += 2 // 右侧留白

	border := strings.Repeat("─", innerW)
	fmt.Println()
	fmt.Println("┌" + border + "┐")
	fmt.Println("│" + padRight(lines[0], innerW) + "│")
	fmt.Println("├" + border + "┤")
	for _, l := range lines[1:] {
		fmt.Println("│" + padRight(l, innerW) + "│")
	}
	fmt.Println("└" + border + "┘")
	fmt.Println()
}

// padRight 用空格把 s 右补到 width（按 rune 计宽，兼容非 ASCII）。
func padRight(s string, width int) string {
	pad := width - utf8.RuneCountInString(s)
	if pad < 0 {
		pad = 0
	}
	return s + strings.Repeat(" ", pad)
}

// providerList 列出已配置的 provider 名（openai/claude/gemini/ollama）；全空返回 none。
func providerList(cfg *config.Config) string {
	var ps []string
	if cfg.LLM.OpenAIKey != "" {
		ps = append(ps, "openai")
	}
	if cfg.LLM.ClaudeKey != "" {
		ps = append(ps, "claude")
	}
	if cfg.LLM.GeminiKey != "" {
		ps = append(ps, "gemini")
	}
	if cfg.LLM.OllamaBaseURL != "" {
		ps = append(ps, "ollama")
	}
	if len(ps) == 0 {
		return "none"
	}
	return strings.Join(ps, ", ")
}

// formatCents 把美分格式化为美元字符串：1000→"10"，1500→"15.00"，2050→"20.50"。
func formatCents(c int64) string {
	dollars, cents := c/100, c%100
	if cents == 0 {
		return strconv.FormatInt(dollars, 10)
	}
	return fmt.Sprintf("%d.%02d", dollars, cents)
}

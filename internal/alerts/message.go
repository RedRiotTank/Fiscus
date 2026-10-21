package alerts

import (
	"fmt"
	"html"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// formatTelegram renders an alert with the small HTML subset supported by the
// Telegram Bot API.
func formatTelegram(alert domain.Alert) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>%s: %s</b>\n", strings.ToUpper(string(alert.Severity)), html.EscapeString(alert.Title))
	b.WriteString(html.EscapeString(alert.Message))
	fmt.Fprintf(&b, "\n\nRule: <code>%s</code>", html.EscapeString(alert.Rule))
	if alert.AccountID != "" {
		fmt.Fprintf(&b, "\nAccount: <code>%s</code>", html.EscapeString(alert.AccountID))
	}
	return b.String()
}

// formatDiscord renders a plain-text alert for a Discord webhook message.
func formatDiscord(alert domain.Alert) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s\n%s", strings.ToUpper(string(alert.Severity)), alert.Title, alert.Message)
	fmt.Fprintf(&b, "\nRule: %s", alert.Rule)
	if alert.AccountID != "" {
		fmt.Fprintf(&b, " | Account: %s", alert.AccountID)
	}
	return b.String()
}

func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 1 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-1]) + "…"
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

package presenters

import "strings"

func FirstNonEmptyLine(content string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func FlattenContent(content string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(content, "\n", " ")), " ")
}

func TruncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}

	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}

	return string(runes[:max-3]) + "..."
}

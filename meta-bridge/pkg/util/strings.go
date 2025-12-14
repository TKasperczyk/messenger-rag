package util

// Truncate safely truncates a string to max runes (not bytes) to preserve UTF-8.
// If the string is longer than max, it appends "..." to indicate truncation.
func Truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

// TruncateExact safely truncates a string to exactly max runes without ellipsis.
// Useful for database field length limits.
func TruncateExact(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

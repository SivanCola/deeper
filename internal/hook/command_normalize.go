package hook

import (
	"strings"
)

func parseSimpleHookCommandFields(command string) ([]string, []bool, [][2]int, bool) {
	// A newline is a shell command separator, not argument whitespace; leave
	// multi-command strings alone like other compound commands.
	if strings.ContainsAny(command, "\n\r") {
		return nil, nil, nil, false
	}
	s := strings.TrimSpace(command)
	fields := []string{}
	repaired := []bool{}
	spans := [][2]int{}
	for i := 0; i < len(s); {
		for i < len(s) && isShellWhitespace(s[i]) {
			i++
		}
		if i >= len(s) {
			break
		}
		var b strings.Builder
		fieldStarted := false
		fieldRepaired := false
		var quote byte
		escapedQuote := false
		for i < len(s) {
			c := s[i]
			if quote == 0 {
				if isShellWhitespace(c) {
					break
				}
				if isShellControl(c) {
					return nil, nil, nil, false
				}
				if n := escapedShellQuoteLen(s, i); n > 0 {
					quote = '"'
					escapedQuote = true
					fieldStarted = true
					fieldRepaired = true
					spans = append(spans, [2]int{i, i + n})
					i += n
					continue
				}
				if c == '"' || c == '\'' {
					quote = c
					escapedQuote = false
					fieldStarted = true
					i++
					continue
				}
				b.WriteByte(c)
				fieldStarted = true
				i++
				continue
			}
			if escapedQuote {
				if n := escapedShellQuoteLen(s, i); n > 0 {
					quote = 0
					escapedQuote = false
					fieldRepaired = true
					spans = append(spans, [2]int{i, i + n})
					i += n
					continue
				}
				b.WriteByte(c)
				i++
				continue
			}
			if c == quote {
				quote = 0
				i++
				continue
			}
			if quote == '"' && c == '\\' && i+1 < len(s) && isDoubleQuoteEscapedByte(s[i+1]) {
				b.WriteByte(s[i+1])
				i += 2
				continue
			}
			b.WriteByte(c)
			i++
		}
		if quote != 0 {
			return nil, nil, nil, false
		}
		if !fieldStarted {
			return nil, nil, nil, false
		}
		fields = append(fields, b.String())
		repaired = append(repaired, fieldRepaired)
		for i < len(s) && isShellWhitespace(s[i]) {
			i++
		}
	}
	return fields, repaired, spans, len(fields) > 0
}

func escapedShellQuoteLen(s string, i int) int {
	j := i
	for j < len(s) && s[j] == '\\' {
		j++
	}
	if j == i || j >= len(s) || s[j] != '"' {
		return 0
	}
	return j - i + 1
}

func isShellWhitespace(c byte) bool {
	return c == ' ' || c == '\t'
}

func isShellControl(c byte) bool {
	switch c {
	case '&', '|', ';', '<', '>':
		return true
	default:
		return false
	}
}

func isDoubleQuoteEscapedByte(c byte) bool {
	switch c {
	case '"', '\\', '$', '`', '\n':
		return true
	default:
		return false
	}
}

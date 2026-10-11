package config

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
)

func isOfficialDeepSeekOpenAIEndpoint(raw string) bool {
	path, ok := deepSeekOpenAIEndpointPath(raw)
	return ok && (path == "" || path == "/v1")
}

func deepSeekOpenAIEndpointPath(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "https") ||
		!strings.EqualFold(u.Hostname(), "api.deepseek.com") || u.Port() != "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	return strings.TrimRight(u.EscapedPath(), "/"), true
}

type providerTOMLBlock struct {
	start int
	end   int
}

func providerTOMLBlocks(lines []string) []providerTOMLBlock {
	headerLines := make([]int, 0)
	providerStarts := make([]int, 0)
	state := tomlOutside
	for i, line := range lines {
		if state != tomlOutside {
			state = advanceTOMLStringState(state, line)
			continue
		}
		if tomlSectionHeader(line) != "" {
			headerLines = append(headerLines, i)
			if isProviderArrayTableHeader(line) {
				providerStarts = append(providerStarts, i)
			}
		}
		state = advanceTOMLStringState(tomlOutside, line)
	}
	out := make([]providerTOMLBlock, 0, len(providerStarts))
	for _, start := range providerStarts {
		end := len(lines)
		for _, header := range headerLines {
			if header > start {
				end = header
				break
			}
		}
		out = append(out, providerTOMLBlock{start: start, end: end})
	}
	return out
}

type providerTOMLInlineBlock struct {
	chatEndpoints            [][2]int
	start, end               int
	kindStart, kindEnd       int
	baseURLStart, baseURLEnd int
	fields                   map[string]providerTOMLInlineField
	segments                 [][2]int
}

type providerTOMLInlineField struct {
	valueStart, valueEnd int
	segment              int
}

type tomlReplacement struct {
	start, end int
	value      string
}

// providerTOMLInlineBlocks locates providers declared as an inline TOML array
// while preserving byte offsets so migration can edit only two scalar values.
// The parser is deliberately lexical: BurntSushi/toml validates the document,
// while this scan handles nested arrays/tables and quoted delimiters without
// re-rendering comments or unknown fields.
func providerTOMLInlineBlocks(raw string) ([]providerTOMLInlineBlock, error) {
	arrayStart, arrayEnd, err := providerTOMLInlineArrayRange(raw)
	if err != nil {
		return nil, err
	}
	return collectProviderTOMLInlineBlocks(raw, arrayStart, arrayEnd)
}

func providerTOMLInlineArrayRange(raw string) (int, int, error) {
	arrayStart, arrayEnd := -1, -1
	section := ""
	state := tomlOutside
	for _, span := range tomlLineSpans(raw) {
		if state != tomlOutside {
			state = advanceTOMLStringState(state, span.text)
			continue
		}
		if header := tomlSectionHeader(span.text); header != "" {
			section = header
			state = advanceTOMLStringState(tomlOutside, span.text)
			continue
		}
		if section != "" {
			state = advanceTOMLStringState(tomlOutside, span.text)
			continue
		}
		line := strings.TrimRight(span.text, "\r\n")
		nextState := advanceTOMLStringState(tomlOutside, line)
		key, _, ok := tomlKeyValue(line)
		if !ok || strings.Trim(key, `"'`) != "providers" {
			state = nextState
			continue
		}
		equals := strings.IndexByte(line, '=')
		valueStart := span.start + equals + 1
		for valueStart < len(raw) && (raw[valueStart] == ' ' || raw[valueStart] == '\t' || raw[valueStart] == '\r' || raw[valueStart] == '\n') {
			valueStart++
		}
		if valueStart >= len(raw) || raw[valueStart] != '[' {
			state = nextState
			continue
		}
		valueEnd, err := scanTOMLDelimitedValue(raw, valueStart, '[', ']')
		if err != nil {
			return -1, -1, err
		}
		arrayStart, arrayEnd = valueStart, valueEnd
		break
	}
	if arrayStart < 0 {
		return -1, -1, fmt.Errorf("providers inline array not found")
	}
	return arrayStart, arrayEnd, nil
}

func collectProviderTOMLInlineBlocks(raw string, arrayStart, arrayEnd int) ([]providerTOMLInlineBlock, error) {
	var tables []providerTOMLInlineBlock
	stack := make([]byte, 0, 4)
	tableStart := -1
	var scanErr error
	err := scanTOMLOutsideStrings(raw, arrayStart, arrayEnd+1, func(pos int, ch byte) bool {
		if scanErr != nil {
			return false
		}
		switch ch {
		case '[', '{':
			stack = append(stack, ch)
			if ch == '{' && len(stack) == 2 && stack[0] == '[' {
				tableStart = pos
			}
		case ']', '}':
			if len(stack) == 0 || (ch == ']' && stack[len(stack)-1] != '[') || (ch == '}' && stack[len(stack)-1] != '{') {
				scanErr = fmt.Errorf("invalid providers inline array nesting")
				return false
			}
			if ch == '}' && len(stack) == 2 && tableStart >= 0 {
				block, err := parseProviderTOMLInlineBlock(raw, tableStart, pos)
				if err != nil {
					scanErr = err
					return false
				}
				tables = append(tables, block)
				tableStart = -1
			}
			stack = stack[:len(stack)-1]
		}
		return true
	})
	if scanErr != nil {
		return nil, scanErr
	}
	if err != nil {
		return nil, err
	}
	if len(stack) != 0 || len(tables) == 0 {
		return nil, fmt.Errorf("providers inline array contains no provider tables")
	}
	return tables, nil
}

func parseProviderTOMLInlineBlock(raw string, start, end int) (providerTOMLInlineBlock, error) {
	block := providerTOMLInlineBlock{
		start: start, end: end, kindStart: -1, baseURLStart: -1,
		fields: make(map[string]providerTOMLInlineField),
	}
	segmentStart := start + 1
	depth := 0
	var segments [][2]int
	var scanErr error
	err := scanTOMLOutsideStrings(raw, start+1, end, func(pos int, ch byte) bool {
		if scanErr != nil {
			return false
		}
		switch ch {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth < 0 {
				scanErr = fmt.Errorf("invalid inline provider table nesting")
				return false
			}
		case ',':
			if depth == 0 {
				segments = append(segments, [2]int{segmentStart, pos})
				segmentStart = pos + 1
			}
		}
		return true
	})
	if scanErr != nil {
		return block, scanErr
	}
	if err != nil {
		return block, err
	}
	segments = append(segments, [2]int{segmentStart, end})
	block.segments = append(block.segments, segments...)
	for segmentIndex, segment := range segments {
		start, end := trimTOMLWhitespace(raw, segment[0], segment[1])
		if start >= end {
			continue
		}
		equals, err := findTOMLAssignmentEquals(raw, start, end)
		if err != nil {
			return block, err
		}
		if equals < 0 {
			return block, fmt.Errorf("inline provider table contains a value without a key")
		}
		key := strings.Trim(strings.TrimSpace(raw[start:equals]), `"'`)
		valueStart, valueEnd := trimTOMLWhitespace(raw, equals+1, end)
		if comment := tomlInlineCommentIndex(raw[valueStart:valueEnd]); comment >= 0 {
			valueEnd = valueStart + comment
			valueStart, valueEnd = trimTOMLWhitespace(raw, valueStart, valueEnd)
		}
		block.fields[key] = providerTOMLInlineField{valueStart: valueStart, valueEnd: valueEnd, segment: segmentIndex}
		switch key {
		case "request_url", "chat_url":
			// Empty overrides are equivalent to omission and stay empty.
			if raw[valueStart:valueEnd] != `""` && raw[valueStart:valueEnd] != `''` {
				block.chatEndpoints = append(block.chatEndpoints, [2]int{valueStart, valueEnd})
			}
		case "kind":
			block.kindStart, block.kindEnd = valueStart, valueEnd
		case "base_url":
			block.baseURLStart, block.baseURLEnd = valueStart, valueEnd
		}
	}
	return block, nil
}

func scanTOMLDelimitedValue(raw string, start int, open, close byte) (int, error) {
	depth := 0
	end := -1
	var scanErr error
	err := scanTOMLOutsideStrings(raw, start, len(raw), func(pos int, ch byte) bool {
		switch ch {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				end = pos
				return false
			}
			if depth < 0 {
				scanErr = fmt.Errorf("invalid TOML array nesting")
				return false
			}
		}
		return true
	})
	if scanErr != nil {
		return -1, scanErr
	}
	if err != nil {
		return -1, err
	}
	if end < 0 {
		return -1, fmt.Errorf("unterminated TOML inline array")
	}
	return end, nil
}

// scanTOMLOutsideStrings visits structural bytes outside TOML strings and
// comments. It is used only after BurntSushi/toml has validated the document.
func scanTOMLOutsideStrings(raw string, start, end int, visit func(int, byte) bool) error {
	const (
		outside = iota
		basic
		literal
		multilineBasic
		multilineLiteral
	)
	state, escaped := outside, false
	for i := start; i < end; {
		ch := raw[i]
		switch state {
		case basic:
			if escaped {
				escaped = false
				i++
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				state = outside
			}
			i++
		case literal:
			if ch == '\'' {
				state = outside
			}
			i++
		case multilineBasic:
			if escaped {
				escaped = false
				i++
				continue
			}
			if ch == '\\' {
				escaped = true
				i++
				continue
			}
			if strings.HasPrefix(raw[i:], `"""`) {
				state = outside
				i += 3
				continue
			}
			i++
		case multilineLiteral:
			if strings.HasPrefix(raw[i:], "'''") {
				state = outside
				i += 3
				continue
			}
			i++
		default:
			if ch == '#' {
				for i < end && raw[i] != '\n' {
					i++
				}
				continue
			}
			if ch == '"' {
				run := 1
				for i+run < end && raw[i+run] == '"' {
					run++
				}
				if run >= 3 {
					state = multilineBasic
					i += 3
				} else {
					state = basic
					i++
				}
				continue
			}
			if ch == '\'' {
				run := 1
				for i+run < end && raw[i+run] == '\'' {
					run++
				}
				if run >= 3 {
					state = multilineLiteral
					i += 3
				} else {
					state = literal
					i++
				}
				continue
			}
			if visit != nil && !visit(i, ch) {
				return nil
			}
			i++
		}
	}
	if state != outside {
		return fmt.Errorf("unterminated TOML string")
	}
	return nil
}

func trimTOMLWhitespace(raw string, start, end int) (int, int) {
	for start < end && strings.ContainsRune(" \t\r\n", rune(raw[start])) {
		start++
	}
	for end > start && strings.ContainsRune(" \t\r\n", rune(raw[end-1])) {
		end--
	}
	return start, end
}

func findTOMLAssignmentEquals(raw string, start, end int) (int, error) {
	var found = -1
	depth := 0
	err := scanTOMLOutsideStrings(raw, start, end, func(pos int, ch byte) bool {
		switch ch {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		case '=':
			if depth == 0 {
				found = pos
				return false
			}
		}
		return true
	})
	return found, err
}

func applyTOMLReplacements(raw string, replacements []tomlReplacement) string {
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start < replacements[j].start })
	for _, r := range slices.Backward(replacements) {
		raw = raw[:r.start] + r.value + raw[r.end:]
	}
	return raw
}

func isProviderArrayTableHeader(line string) bool {
	trimmed := strings.TrimSpace(line)
	if comment := tomlInlineCommentIndex(trimmed); comment >= 0 {
		trimmed = strings.TrimSpace(trimmed[:comment])
	}
	if !strings.HasPrefix(trimmed, "[[") || !strings.HasSuffix(trimmed, "]]") {
		return false
	}
	key := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
	switch {
	case key == "providers", key == "'providers'":
		return true
	case len(key) >= 2 && key[0] == '"' && key[len(key)-1] == '"':
		decoded, err := strconv.Unquote(key)
		return err == nil && decoded == "providers"
	default:
		return false
	}
}

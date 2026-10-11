package config

import (
	"strconv"
	"strings"
)

func replaceTOMLStringAssignment(line, value string) string {
	return replaceTOMLScalarAssignment(line, strconv.Quote(value))
}

func replaceTOMLScalarAssignment(line, encoded string) string {
	carriageReturn := strings.HasSuffix(line, "\r")
	line = strings.TrimSuffix(line, "\r")
	equals, err := findTOMLAssignmentEquals(line, 0, len(line))
	if err != nil {
		equals = strings.IndexByte(line, '=')
	}
	if equals < 0 {
		return line
	}
	rhs := line[equals+1:]
	leadingLen := len(rhs) - len(strings.TrimLeft(rhs, " \t"))
	leading := rhs[:leadingLen]
	suffix := ""
	if comment := tomlInlineCommentIndex(rhs); comment >= 0 {
		spaceStart := comment
		for spaceStart > 0 && (rhs[spaceStart-1] == ' ' || rhs[spaceStart-1] == '\t') {
			spaceStart--
		}
		suffix = rhs[spaceStart:]
	}
	next := line[:equals+1] + leading + encoded + suffix
	if carriageReturn {
		next += "\r"
	}
	return next
}

func tomlInlineCommentIndex(value string) int {
	inBasic, inLiteral, escaped := false, false, false
	for i := range len(value) {
		ch := value[i]
		if inBasic {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inBasic = false
			}
			continue
		}
		if inLiteral {
			if ch == '\'' {
				inLiteral = false
			}
			continue
		}
		switch ch {
		case '"':
			inBasic = true
		case '\'':
			inLiteral = true
		case '#':
			return i
		}
	}
	return -1
}

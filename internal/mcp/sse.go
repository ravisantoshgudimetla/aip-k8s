package mcp

import (
	"bytes"
	"fmt"
	"strings"
)

// ExtractSSEDataLine extracts the first "data: " line from an SSE response body.
// Returns an error if no data line is found or the body is empty.
func ExtractSSEDataLine(body []byte) (string, error) {
	if len(body) == 0 {
		return "", fmt.Errorf("SSE response body is empty")
	}
	lines := bytes.SplitSeq(body, []byte("\n"))
	for line := range lines {
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		after, ok := strings.CutPrefix(string(line), "data: ")
		if ok {
			return after, nil
		}
	}
	return "", fmt.Errorf("SSE response missing data line in body:\n%s", string(body))
}

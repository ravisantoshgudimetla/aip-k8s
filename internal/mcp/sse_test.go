package mcp

import (
	"testing"
)

func TestExtractSSEDataLine(t *testing.T) {
	tests := []struct {
		name    string
		body    []byte
		want    string
		wantErr bool
	}{
		{
			name: "standard SSE with event and data",
			body: []byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n"),
			want: `{"jsonrpc":"2.0","id":1,"result":{}}`,
		},
		{
			name: "CRLF line endings",
			body: []byte("event: message\r\ndata: {\"result\":\"ok\"}\r\n\r\n"),
			want: `{"result":"ok"}`,
		},
		{
			name: "data line only",
			body: []byte("data: hello\n"),
			want: "hello",
		},
		{
			name: "first data line returned when multiple present",
			body: []byte("data: first\ndata: second\n"),
			want: "first",
		},
		{
			name:    "empty body",
			body:    []byte{},
			wantErr: true,
		},
		{
			name:    "no data line",
			body:    []byte("event: message\nid: 1\n"),
			wantErr: true,
		},
		{
			name:    "data prefix without space not matched",
			body:    []byte("data:nospace\n"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractSSEDataLine(tc.body)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

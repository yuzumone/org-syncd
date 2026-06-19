package mcpserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestReadMessageUsesNewlineDelimitedJSON(t *testing.T) {
	input := "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{}}\n"
	got, err := readMessage(bufio.NewReader(strings.NewReader(input)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != strings.TrimSpace(input) {
		t.Fatalf("message = %q", got)
	}
}

func TestWriteMessageUsesNewlineDelimitedJSON(t *testing.T) {
	var out bytes.Buffer
	if err := writeMessage(&out, response{JSONRPC: "2.0", ID: json.RawMessage("1"), Result: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Content-Length") {
		t.Fatalf("unexpected header framing: %q", out.String())
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("response is not newline terminated: %q", out.String())
	}
	var got response
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &got); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
}

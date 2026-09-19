package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type mcpResp struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// mcpSession feeds the given JSON-RPC lines to the server and returns the
// responses in order (notifications and garbage produce none).
func mcpSession(t *testing.T, origWD string, lines ...string) []mcpResp {
	t.Helper()
	var out strings.Builder
	if err := mcpServe(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out, origWD); err != nil {
		t.Fatal(err)
	}
	var resps []mcpResp
	for _, ln := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if ln == "" {
			continue
		}
		var r mcpResp
		if err := json.Unmarshal([]byte(ln), &r); err != nil {
			t.Fatalf("bad response line %q: %v", ln, err)
		}
		resps = append(resps, r)
	}
	return resps
}

func respResult(t *testing.T, r mcpResp, into any) {
	t.Helper()
	if r.Error != nil {
		t.Fatalf("unexpected rpc error: %+v", r.Error)
	}
	if err := json.Unmarshal(r.Result, into); err != nil {
		t.Fatal(err)
	}
}

func toolText(t *testing.T, r mcpResp) (string, bool) {
	t.Helper()
	var res struct {
		Content []mcpContent `json:"content"`
		IsError bool         `json:"isError"`
	}
	respResult(t, r, &res)
	if len(res.Content) == 0 || res.Content[0].Type != "text" {
		t.Fatalf("no text content: %s", r.Result)
	}
	return res.Content[0].Text, res.IsError
}

func TestMCPSession(t *testing.T) {
	t.Chdir(t.TempDir())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	// Repository: #1 empty, #2 with a.txt, then an uncommitted edit.
	if _, err := run(t, "save", "base"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, "a.txt", "one\n")
	if _, err := run(t, "save"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, "a.txt", "two\n")

	resps := mcpSession(t, wd,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`not json at all`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"snapshot","arguments":{"tag":"before risky"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"diff","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"restore","arguments":{"ref":"@2"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"log","arguments":{"all":true}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"frobnicate"}`,
		`{"jsonrpc":"2.0","id":8,"method":"ping"}`,
	)
	if len(resps) != 8 {
		t.Fatalf("want 8 responses (notification and garbage dropped), got %d: %+v", len(resps), resps)
	}

	// initialize: echoes the client's dialect, identifies itself.
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	respResult(t, resps[0], &init)
	if init.ProtocolVersion != "2025-03-26" || init.ServerInfo.Name != "vrs" || init.ServerInfo.Version != Version {
		t.Fatalf("initialize: %+v", init)
	}

	// tools/list: the four agent tools.
	var list struct {
		Tools []mcpToolDef `json:"tools"`
	}
	respResult(t, resps[1], &list)
	want := map[string]bool{}
	for _, tool := range list.Tools {
		want[tool.Name] = true
	}
	for _, name := range []string{"snapshot", "restore", "diff", "log"} {
		if !want[name] {
			t.Fatalf("tools/list missing %q: %+v", name, want)
		}
	}

	// snapshot: hidden checkpoint #3 with the tag.
	text, isErr := toolText(t, resps[2])
	if isErr || !strings.Contains(text, "checkpoint #3") || !strings.Contains(text, "before risky") {
		t.Fatalf("snapshot: isErr=%v text=%q", isErr, text)
	}

	// diff: the uncommitted edit against the position (#2).
	text, isErr = toolText(t, resps[3])
	if isErr || !strings.Contains(text, "modified  a.txt") {
		t.Fatalf("diff: isErr=%v text=%q", isErr, text)
	}

	// restore @2: working copy rewound to the last save, nothing lost.
	text, isErr = toolText(t, resps[4])
	if isErr || !strings.Contains(text, "working copy now at #2") {
		t.Fatalf("restore: isErr=%v text=%q", isErr, text)
	}
	if b, _ := os.ReadFile("a.txt"); string(b) != "one\n" {
		t.Fatalf("a.txt after restore: %q", b)
	}

	// log --all: the checkpoint is visible and tagged.
	text, isErr = toolText(t, resps[5])
	if isErr || !strings.Contains(text, "[capture]") || !strings.Contains(text, "before risky") {
		t.Fatalf("log: isErr=%v text=%q", isErr, text)
	}

	// unknown method → protocol error; ping → empty result.
	if resps[6].Error == nil || resps[6].Error.Code != -32601 {
		t.Fatalf("unknown method: %+v", resps[6].Error)
	}
	var ping map[string]any
	respResult(t, resps[7], &ping)
}

func TestMCPToolErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	wd, _ := os.Getwd()

	// Not a repository.
	resps := mcpSession(t, wd,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"snapshot","arguments":{}}}`,
	)
	text, isErr := toolText(t, resps[0])
	if !isErr || !strings.Contains(text, "not a vrs repository") {
		t.Fatalf("snapshot outside repo: isErr=%v text=%q", isErr, text)
	}

	// Unknown tool.
	resps = mcpSession(t, wd,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"frob","arguments":{}}}`,
	)
	if _, isErr = toolText(t, resps[0]); !isErr {
		t.Fatal("unknown tool should be a tool error")
	}

	// Repo exists; bad ref is a tool error, not a protocol error.
	if _, err := run(t, "save", "base"); err != nil {
		t.Fatal(err)
	}
	resps = mcpSession(t, wd,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"restore","arguments":{"ref":"@99"}}}`,
	)
	text, isErr = toolText(t, resps[0])
	if !isErr || !strings.Contains(text, "no snapshot #99") {
		t.Fatalf("restore @99: isErr=%v text=%q", isErr, text)
	}
}

func TestMCPPathArgument(t *testing.T) {
	// Server spawned somewhere random; the tools carry the repo path.
	nowhere := t.TempDir()
	repo := t.TempDir()
	t.Chdir(repo)
	writeFile(t, "a.txt", "one\n")
	if _, err := run(t, "save", "base"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, "a.txt", "two\n")
	t.Chdir(nowhere)

	resps := mcpSession(t, nowhere,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"snapshot","arguments":{"path":`+jsonStr(repo)+`}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"diff","arguments":{"path":`+jsonStr(repo)+`}}}`,
	)
	text, isErr := toolText(t, resps[0])
	if isErr || !strings.Contains(text, "checkpoint #2") {
		t.Fatalf("snapshot via path: isErr=%v text=%q", isErr, text)
	}
	text, isErr = toolText(t, resps[1])
	if isErr || !strings.Contains(text, "modified  a.txt") {
		t.Fatalf("diff via path: isErr=%v text=%q", isErr, text)
	}

	// Bad path → tool error.
	resps = mcpSession(t, nowhere,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"diff","arguments":{"path":"/definitely/not/here"}}}`,
	)
	if _, isErr = toolText(t, resps[0]); !isErr {
		t.Fatal("bad path should be a tool error")
	}
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

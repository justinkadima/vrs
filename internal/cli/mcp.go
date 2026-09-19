package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// runMCP runs the Model Context Protocol server over stdio. MCP clients
// (AI coding agents) spawn this command as a child process and exchange
// newline-delimited JSON-RPC messages: the client owns the lifecycle, so
// this is not a daemon — it lives exactly as long as the agent session.
//
// Tools exposed: snapshot, restore, diff, log. Every tool accepts an
// optional absolute "path" to the repository, because the server's own
// working directory is whatever the client happened to spawn it in.
func runMCP(args []string, out, errW io.Writer) error {
	if len(args) > 0 {
		fmt.Fprintln(errW, "usage: vrs mcp")
		return ErrUsage
	}
	origWD, err := os.Getwd()
	if err != nil {
		return err
	}
	return mcpServe(os.Stdin, out, origWD)
}

// --- protocol plumbing ------------------------------------------------------

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// mcpServe reads newline-delimited JSON-RPC requests from r and writes
// responses to w, one per line, until r closes.
func mcpServe(r io.Reader, w io.Writer, origWD string) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	enc := json.NewEncoder(w)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req mcpRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue // can't address an unparseable request; drop it
		}
		if len(req.ID) == 0 {
			continue // notification (initialized, cancelled, …): no reply
		}
		result, protoErr := mcpDispatch(req.Method, req.Params, origWD)
		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
		if protoErr != nil {
			resp = rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: protoErr}
		}
		if err := enc.Encode(&resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

func mcpDispatch(method string, params json.RawMessage, origWD string) (any, *rpcError) {
	switch method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-06-18"
		}
		return map[string]any{
			"protocolVersion": p.ProtocolVersion, // speak the client's dialect
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "vrs", "version": Version},
		}, nil

	case "ping":
		return map[string]any{}, nil

	case "tools/list":
		return map[string]any{"tools": mcpTools()}, nil

	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
		}
		text, err := mcpTool(p.Name, p.Arguments, origWD)
		if err != nil {
			// Tool execution errors are results, not protocol errors (per MCP).
			return map[string]any{
				"content": []mcpContent{{Type: "text", Text: err.Error()}},
				"isError": true,
			}, nil
		}
		return map[string]any{
			"content": []mcpContent{{Type: "text", Text: text}},
		}, nil

	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
	}
}

// --- tools ------------------------------------------------------------------

type mcpToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func mcpTools() []mcpToolDef {
	str := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
	obj := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required}
	}
	pathProp := str("absolute path to the repository (default: the directory the server started in)")
	return []mcpToolDef{
		{
			Name: "snapshot",
			Description: "Create a checkpoint of the entire working tree. Cheap (deduplicated) and idempotent: " +
				"if nothing changed since the last snapshot, the id of the snapshot that already records this state is returned. " +
				"Use before risky steps; restore later with the restore tool.",
			InputSchema: obj(map[string]any{
				"tag":  str("short label for the checkpoint, e.g. 'before npm install'"),
				"path": pathProp,
			}),
		},
		{
			Name: "restore",
			Description: "Restore the working tree to a snapshot (rewind). The current state is captured first, " +
				"so nothing is ever lost. Saving from a restored position starts a new line; all old snapshots are preserved.",
			InputSchema: obj(map[string]any{
				"ref":  str("snapshot to restore: @N (e.g. @5), @-N (N saves back from the tip), @2h (newest at least 2h old). Omit for the newest save."),
				"path": pathProp,
			}),
		},
		{
			Name:        "diff",
			Description: "List added/modified/deleted files in the working tree vs the snapshot it sits on (or vs a given ref).",
			InputSchema: obj(map[string]any{
				"ref":  str("optional snapshot to compare against (@N, @-N, @2h); default: the current position"),
				"path": pathProp,
			}),
		},
		{
			Name:        "log",
			Description: "Show the numbered snapshot timeline. Snapshot ids work as refs in restore/diff. Checkpoints (agent/undo captures) appear only with all=true.",
			InputSchema: obj(map[string]any{
				"all":   map[string]any{"type": "boolean", "description": "include hidden checkpoints"},
				"limit": map[string]any{"type": "integer", "description": "show at most N snapshots"},
				"path":  pathProp,
			}),
		},
	}
}

// mcpTool executes one tool call and returns the text result.
func mcpTool(name string, args map[string]any, origWD string) (string, error) {
	// Resolve the repository directory: explicit path arg wins, else the
	// directory the server was spawned in.
	if path, _ := args["path"].(string); path != "" {
		if err := os.Chdir(path); err != nil {
			return "", fmt.Errorf("path: %w", err)
		}
	} else if err := os.Chdir(origWD); err != nil {
		return "", err
	}

	switch name {
	case "snapshot":
		tag, _ := args["tag"].(string)
		rc, err := openRepo()
		if err != nil {
			return "", err
		}
		defer rc.st.Close()
		id, created, changed, err := checkpoint(rc, tag)
		if err != nil {
			return "", err
		}
		if !created {
			return fmt.Sprintf("unchanged — state already recorded as #%d (restore with ref @%d)", id, id), nil
		}
		if tag != "" {
			return fmt.Sprintf("checkpoint #%d — %d file(s) changed (tag: %s)", id, changed, tag), nil
		}
		return fmt.Sprintf("checkpoint #%d — %d file(s) changed", id, changed), nil

	case "restore":
		argv := []string{"goto"}
		if ref, _ := args["ref"].(string); ref != "" {
			argv = append(argv, normalizeRef(ref))
		}
		return cliText(argv...)

	case "diff":
		argv := []string{"diff"}
		if ref, _ := args["ref"].(string); ref != "" {
			argv = append(argv, normalizeRef(ref))
		}
		return cliText(argv...)

	case "log":
		argv := []string{"log"}
		if all, _ := args["all"].(bool); all {
			argv = append(argv, "--all")
		}
		if limit, ok := args["limit"].(float64); ok && limit > 0 {
			argv = append(argv, "-n", strconv.Itoa(int(limit)))
		}
		return cliText(argv...)

	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

// normalizeRef accepts "@5", "5", "-2", "2h" — anything without a leading @
// is treated as a snapshot reference and prefixed.
func normalizeRef(ref string) string {
	if !strings.HasPrefix(ref, "@") {
		return "@" + ref
	}
	return ref
}

// cliText runs an in-process CLI command and returns its combined output.
func cliText(argv ...string) (string, error) {
	var out, errB strings.Builder
	if err := Run(argv, &out, &errB); err != nil {
		msg := strings.TrimSpace(errB.String())
		if msg != "" {
			return "", fmt.Errorf("%s (%v)", msg, err)
		}
		return "", err
	}
	text := strings.TrimSpace(out.String() + errB.String())
	if text == "" {
		return "(no output)", nil
	}
	return text, nil
}

// Package mcptest contains only offline subprocess fixtures. Call Run explicitly
// from a TestMCPProcessHelper or an opt-in smoke binary, never production assembly.
package mcptest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/netty-linux/daimon/internal/mcp"
	"io"
	"os"
	"strings"
)

func Run() bool {
	mode := os.Getenv("MCP_FIXTURE")
	if mode == "" {
		return false
	}
	if trace := os.Getenv("MCP_TRACE"); trace != "" {
		_ = os.WriteFile(trace, []byte(fmt.Sprintf("%d", os.Getpid())), 0600)
	}
	send := func(value any) { raw, _ := json.Marshal(value); fmt.Println(string(raw)) }
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 2*mcp.MaxMessageBytes)
	initialized, notified := false, false
	for scanner.Scan() {
		var r struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &r) != nil {
			return true
		}
		if r.Method == "notifications/initialized" {
			if !initialized {
				return true
			}
			notified = true
			continue
		}
		if r.Method == "notifications/cancelled" {
			continue
		}
		result := any(map[string]any{})
		switch r.Method {
		case "initialize":
			if mode == "flood" {
				for i := 0; i < 10000; i++ {
					send(map[string]any{"jsonrpc": "2.0", "id": i + 1, "method": "ping"})
				}
				_, _ = io.Copy(io.Discard, os.Stdin)
				return true
			}
			if mode == "malformed" {
				fmt.Println("not-json")
				return true
			}
			if mode == "wrong-id" {
				send(map[string]any{"jsonrpc": "2.0", "id": 999, "result": map[string]any{}})
				continue
			}
			if mode == "huge" {
				fmt.Println(strings.Repeat("x", mcp.MaxMessageBytes+1))
				return true
			}
			if mode == "exit" {
				return true
			}
			if mode == "hang-init" {
				_, _ = io.Copy(io.Discard, os.Stdin)
				return true
			}
			if mode == "stderr" {
				_, _ = os.Stderr.Write([]byte(strings.Repeat("secret-stderr", 200000)))
			}
			var params struct {
				Version      string         `json:"protocolVersion"`
				Capabilities map[string]any `json:"capabilities"`
			}
			if json.Unmarshal(r.Params, &params) != nil || params.Version != mcp.ProtocolVersion || len(params.Capabilities) != 0 {
				return true
			}
			version := mcp.ProtocolVersion
			if mode == "version" {
				version = "unsupported"
			}
			initialized = true
			result = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "fixture", "version": "1"}}
		case "tools/list":
			if !notified {
				return true
			}
			name := "lookup"
			schema := any(map[string]any{"type": "object", "properties": map[string]any{"query": map[string]string{"type": "string"}}})
			if mode == "invalid-schema" {
				schema = nil
			}
			if mode == "name" {
				name = "BAD.tool"
			}
			list := []any{map[string]any{"name": name, "description": "Offline lookup", "inputSchema": schema}, map[string]any{"name": "mutate", "description": "Unapproved write", "inputSchema": map[string]string{"type": "object"}}, map[string]any{"name": "unknown", "inputSchema": map[string]string{"type": "object"}}}
			if mode == "duplicate" {
				list = append(list, list[0])
			}
			result = map[string]any{"tools": list}
			if mode == "pagination" {
				var params map[string]any
				_ = json.Unmarshal(r.Params, &params)
				if params["cursor"] == nil {
					result = map[string]any{"tools": list[:1], "nextCursor": "page2"}
				} else {
					result = map[string]any{"tools": list[1:]}
				}
			}
		case "tools/call":
			if !notified {
				return true
			}
			if trace := os.Getenv("MCP_TRACE"); trace != "" {
				f, e := os.OpenFile(trace, os.O_APPEND|os.O_WRONLY, 0600)
				if e == nil {
					_, _ = f.WriteString("\ncall")
					_ = f.Close()
				}
			}
			if mode == "crash" {
				return true
			}
			if mode == "timeout" {
				_, _ = io.Copy(io.Discard, os.Stdin)
				return true
			}
			if mode == "malformed-call" {
				fmt.Println("bad-call")
				return true
			}
			if mode == "rpc-error" {
				send(map[string]any{"jsonrpc": "2.0", "id": r.ID, "error": map[string]any{"code": -32602, "message": "secret-remote"}})
				continue
			}
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if json.Unmarshal(r.Params, &params) != nil || params.Name != "lookup" {
				return true
			}
			text := string(params.Arguments)
			if mode == "secrets" {
				text = fmt.Sprintf("provider=%s arg=%s", os.Getenv("DAIMON_API_KEY"), os.Args[len(os.Args)-1])
			}
			if mode == "large-result" {
				text = strings.Repeat("x", mcp.MaxResultBytes+1)
			}
			kind := "text"
			if mode == "image" {
				kind = "image"
			}
			result = map[string]any{"content": []any{map[string]string{"type": kind, "text": text}}, "isError": mode == "tool-error"}
		default:
			return true
		}
		send(map[string]any{"jsonrpc": "2.0", "id": r.ID, "result": result})
	}
	return true
}

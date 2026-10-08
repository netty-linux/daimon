// Package cuatest is an offline test double, never production composition.
// It implements real stdio initialization/discovery/calls without desktop access.
package cuatest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

func Run() {
	executable, _ := os.Executable()
	modeBytes, _ := os.ReadFile(executable + ".mode")
	mode := strings.TrimSpace(string(modeBytes))
	trace := func(text string) {
		f, e := os.OpenFile(executable+".trace", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e == nil {
			_, _ = fmt.Fprintln(f, text)
			_ = f.Close()
		}
	}
	trace("started")
	defer trace("closed")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	send := func(id json.RawMessage, result any) {
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		fmt.Println(string(raw))
	}
	initialized, notified := false, false
	for scanner.Scan() {
		var r struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &r) != nil {
			return
		}
		switch r.Method {
		case "initialize":
			if mode == "startup-failed" {
				return
			}
			var p struct {
				Version string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(r.Params, &p)
			if p.Version != "2025-06-18" {
				return
			}
			initialized = true
			send(r.ID, map[string]any{"protocolVersion": p.Version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "offline-cua", "version": "1"}})
		case "notifications/initialized":
			if !initialized {
				return
			}
			notified = true
		case "notifications/cancelled":
			return
		case "tools/list":
			if !notified {
				return
			}
			list := []any{}
			for _, name := range []string{"list_apps", "list_windows", "get_window_state", "get_accessibility_tree", "click", "type_text", "bring_to_front", "launch_app", "kill_app", "get_desktop_state", "future_untrusted"} {
				schema := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": true}
				if mode == "invalid-discovery" {
					schema["type"] = "array"
				}
				list = append(list, map[string]any{"name": name, "description": "Offline CUA fixture", "inputSchema": schema})
			}
			send(r.ID, map[string]any{"tools": list})
		case "tools/call":
			modeBytes, _ = os.ReadFile(executable + ".mode")
			mode = strings.TrimSpace(string(modeBytes))
			trace("call")
			if mode == "crash" {
				return
			}
			if mode == "timeout" {
				_, _ = io.Copy(io.Discard, os.Stdin)
				return
			}
			var p struct {
				Name string         `json:"name"`
				Args map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(r.Params, &p)
			if os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("GROQ_API_KEY") != "" || os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("OPENROUTER_API_KEY") != "" || os.Getenv("DAIMON_API_KEY") != "" || os.Getenv("CUA_DRIVER_DANGEROUSLY_BYPASS_APPROVALS") != "" {
				return
			}
			if p.Name == "get_window_state" && p.Args["include_screenshot"] != false {
				return
			}
			content := []any{map[string]any{"type": "text", "text": "controlled textual observation"}}
			if mode == "image" {
				content = []any{map[string]any{"type": "image", "mimeType": "image/png", "data": "SCREENSHOT-SECRET"}}
			}
			if mode == "multi-text-image" {
				content = []any{map[string]any{"type": "text", "text": "summary"}, map[string]any{"type": "text", "text": `{"screenshot":{"base64":"SCREENSHOT-SECRET"}}`}}
			}
			if mode == "text-image" {
				content = []any{map[string]any{"type": "text", "text": `{"get_desktop_state":{"base64":"SCREENSHOT-SECRET"}}`}}
			}
			send(r.ID, map[string]any{"content": content})
		default:
			return
		}
	}
}

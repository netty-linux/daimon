package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/memory"
	"github.com/netty-linux/daimon/internal/sandbox"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"unicode/utf8"
)

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		failure(w, 415, "unsupported_media_type")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var big *http.MaxBytesError
		if errors.As(err, &big) {
			failure(w, 413, "body_limit")
		} else {
			failure(w, 400, "invalid_request")
		}
		return false
	}
	// Enforce exact schema spelling in addition to DisallowUnknownFields (which
	// otherwise matches case-insensitively). Reject duplicate keys/null/extra JSON.
	keys := map[string]bool{}
	t := reflect.TypeOf(dst).Elem()
	for i := 0; i < t.NumField(); i++ {
		keys[strings.Split(t.Field(i).Tag.Get("json"), ",")[0]] = true
	}
	if !utf8.Valid(data) || !strictObject(data, keys) {
		failure(w, 400, "invalid_request")
		return false
	}
	if input, ok := dst.(*memory.Input); ok {
		value, err := memory.DecodeInput(data)
		if err != nil {
			failure(w, 400, "invalid_memory")
			return false
		}
		*input = value
		return true
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil || d.Decode(new(any)) != io.EOF {
		failure(w, 400, "invalid_request")
		return false
	}
	return true
}
func strictObject(data []byte, keys map[string]bool) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		tok, err := d.Token()
		key, ok := tok.(string)
		if err != nil || !ok || !keys[key] || seen[key] {
			return false
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
		// Current request schemas contain only scalars and an array of strings.
		var scalar any
		if json.Unmarshal(value, &scalar) != nil {
			return false
		}
		switch v := scalar.(type) {
		case map[string]any:
			if key == "sandbox_profile" {
				if _, err := sandbox.DecodeProfile(value); err != nil {
					return false
				}
				continue
			}
			if key != "computer_profile" {
				return false
			}
			if _, err := computer.DecodeProfile(value); err != nil {
				return false
			}
		case string:
		case []any:
			for _, item := range v {
				if _, ok := item.(string); !ok {
					return false
				}
			}
		default:
			return false
		}
	}
	tok, err = d.Token()
	return err == nil && tok == json.Delim('}') && d.Decode(new(any)) == io.EOF
}
func respond(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil || len(data)+1 > MaxResponseBytes {
		failure(w, 500, "internal_error")
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}
func failure(w http.ResponseWriter, status int, code string) {
	// Both fields originate only from controlled literals/classifiers.
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": strings.ReplaceAll(code, "_", " ")}})
}

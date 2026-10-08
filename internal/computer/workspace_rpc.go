package computer

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"strings"
)

// WorkspaceRPC is a private control-plane capability, not a model Tool or HTTP
// proxy. It reuses the exact local/Fleet endpoint and credential/DNS boundaries.
func (c *CUAMedia) WorkspaceRPC(ctx context.Context, service, method string, p []byte) ([][]byte, error) {
	allowed := service == "FilesystemService" && (method == "Stat" || method == "ListDir" || method == "MakeDir" || method == "ReadFile" || method == "BeginUpload" || method == "UploadChunk" || method == "CommitUpload" || method == "AbortUpload") || service == "ProcessService" && method == "StartProcess"
	if !allowed || len(p) > 128*1024 {
		return nil, ErrArguments
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	body := make([]byte, 5+len(p))
	binary.BigEndian.PutUint32(body[1:], uint32(len(p)))
	copy(body[5:], p)
	u := *c.base
	u.Path = strings.TrimSuffix(u.Path, "/") + "/cua.env.v1." + service + "/" + method
	req, e := http.NewRequestWithContext(ctx, "POST", u.String(), bytes.NewReader(body))
	if e != nil {
		return nil, ErrMedia
	}
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	req.Header.Set("X-Grpc-Web", "1")
	for k, v := range c.headers {
		req.Header[k] = append([]string{}, v...)
	}
	if c.token != "" {
		req.Header.Set("X-Cua-Env-Authorization", "Bearer "+c.token)
	}
	res, e := c.client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrMedia
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/grpc-web") {
		return nil, ErrMedia
	}
	data, e := io.ReadAll(io.LimitReader(res.Body, 256*1024+1))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if e != nil || len(data) > 256*1024 {
		return nil, ErrMedia
	}
	messages := [][]byte{}
	status := res.Header.Get("Grpc-Status")
	trailer := false
	for len(data) > 0 {
		if len(data) < 5 || len(messages) > 256 || trailer {
			return nil, ErrMedia
		}
		n := uint64(binary.BigEndian.Uint32(data[1:]))
		if n > uint64(len(data)-5) {
			return nil, ErrMedia
		}
		part := data[5 : 5+n]
		flag := data[0]
		data = data[5+n:]
		switch flag {
		case 0:
			messages = append(messages, append([]byte{}, part...))
		case 128:
			trailer = true
			for _, line := range strings.Split(string(part), "\r\n") {
				if strings.HasPrefix(strings.ToLower(line), "grpc-status:") {
					status = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
				}
			}
		default:
			return nil, ErrMedia
		}
	}
	if status != "0" || len(messages) == 0 {
		return nil, ErrMedia
	}
	return messages, nil
}

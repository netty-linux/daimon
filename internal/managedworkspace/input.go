package managedworkspace

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/netty-linux/daimon/internal/workspaceplan"
)

func ReadPlan(ctx context.Context, name string) ([]byte, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	i, err := os.Lstat(name)
	if err != nil || !sourceRegular(i) {
		return nil, workspaceplan.ErrInvalid
	}
	limit := workspaceplan.DefaultLimits().PlanBytes
	if i.Size() > int64(limit) {
		return nil, workspaceplan.ErrLimit
	}
	r, err := os.OpenRoot(filepath.Dir(name))
	if err != nil {
		return nil, workspaceplan.ErrInvalid
	}
	defer r.Close()
	f, err := openRead(r, filepath.Base(name), false)
	if err != nil {
		return nil, workspaceplan.ErrInvalid
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !sourceRegular(opened) || !os.SameFile(i, opened) {
		return nil, workspaceplan.ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, workspaceplan.ErrInvalid
	}
	if len(data) > limit {
		return nil, workspaceplan.ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

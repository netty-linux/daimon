package sandbox

import "context"

// Keeps an existing ownership journal visible when the operator disables CUA.
// It performs no runtime operation and cannot provision a Computer.
type UnavailableBackend struct{}

func (UnavailableBackend) Create(context.Context, CreateRequest) (Remote, error) {
	return Remote{}, errorOf(NotConfigured)
}
func (UnavailableBackend) Get(context.Context, string) (Remote, error) {
	return Remote{}, errorOf(NotConfigured)
}
func (UnavailableBackend) Delete(context.Context, string) error { return errorOf(NotConfigured) }
func (UnavailableBackend) Close(context.Context) error          { return nil }
func (UnavailableBackend) Probe(context.Context) (RuntimeInfo, error) {
	return RuntimeInfo{Backend: CUALocal, Runtime: "gvisor", Reason: NotConfigured}, nil
}

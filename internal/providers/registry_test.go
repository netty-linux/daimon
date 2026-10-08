package providers

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/netty-linux/daimon/internal/model"
)

func TestRegistry(t *testing.T) {
	var r Registry
	f := CompatibleFactory(OpenAI)
	if err := r.Register(GroqFactory()); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(f); err != nil {
		t.Fatal(err)
	}
	f.ID = "changed"
	f.Build = nil
	got, err := r.Get(OpenAI)
	if err != nil || got.ID != OpenAI || got.Build == nil {
		t.Fatalf("lookup: %v", err)
	}
	if err := r.Register(CompatibleFactory(OpenAI)); !errors.Is(err, ErrDuplicate) {
		t.Fatal(err)
	}
	if _, err := r.Get("missing"); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	ids := r.IDs()
	if !reflect.DeepEqual(ids, []ID{Groq, OpenAI}) {
		t.Fatal(ids)
	}
	ids[0] = "mutated"
	if r.IDs()[0] != Groq {
		t.Fatal("IDs alias registry")
	}
	var nilRegistry *Registry
	if _, err := nilRegistry.Get(OpenAI); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	if err := nilRegistry.Register(got); !errors.Is(err, ErrInvalidFactory) {
		t.Fatal(err)
	}
}

func TestInvalidRegistryInput(t *testing.T) {
	for _, id := range []ID{"", " ", "OpenAI", "a/b", "a\n", "0bad", "é", ID(strings.Repeat("a", 65))} {
		t.Run(string(id), func(t *testing.T) {
			var r Registry
			f := CompatibleFactory(id)
			for _, err := range []error{r.Register(f), func() error { _, err := r.Get(id); return err }(), func() error { _, err := f.New(Config{}); return err }()} {
				var typed *RegistryError
				if !errors.Is(err, ErrInvalidID) || !errors.As(err, &typed) {
					t.Fatal(err)
				}
			}
			if len(r.IDs()) != 0 {
				t.Fatal("invalid registration changed registry")
			}
		})
	}
	var r Registry
	if err := r.Register(Factory{ID: OpenAI}); !errors.Is(err, ErrInvalidFactory) {
		t.Fatal(err)
	}
	if _, err := (Factory{ID: OpenAI}).New(Config{}); !errors.Is(err, ErrInvalidFactory) {
		t.Fatal(err)
	}
}

func TestFactoryErrorPropagation(t *testing.T) {
	var r Registry
	called := false
	f := Factory{ID: "test", Build: func(Config) (model.Model, error) { called = true; return nil, context.Canceled }}
	if err := r.Register(f); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get("test")
	if err != nil || called {
		t.Fatal("registration or lookup constructed model")
	}
	if _, err := got.New(Config{}); err != context.Canceled || !called {
		t.Fatal(err)
	}
}

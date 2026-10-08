package providers

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateIDWithoutResolution(t *testing.T) {
	for _, id := range []ID{OpenAI, Groq, "not-installed-1", ID(strings.Repeat("a", 64))} {
		if err := ValidateID(id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []ID{"", " ", "Bad", "../bad", ID(strings.Repeat("a", 65))} {
		err := ValidateID(id)
		var typed *RegistryError
		if !errors.Is(err, ErrInvalidID) || !errors.As(err, &typed) {
			t.Fatal(err)
		}
	}
}

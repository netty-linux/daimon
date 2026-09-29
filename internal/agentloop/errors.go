package agentloop

import "fmt"

// InvalidConfigError é retornado quando a configuração inicial é inválida.
type InvalidConfigError struct {
	Err error
}

func (e InvalidConfigError) Error() string {
	if e.Err == nil {
		return "invalid configuration"
	}
	return fmt.Sprintf("invalid configuration: %v", e.Err)
}

func (e InvalidConfigError) Unwrap() error {
	return e.Err
}

// InvalidResponseError é retornado quando o modelo retorna uma resposta inválida.
type InvalidResponseError struct {
	Err error
}

func (e InvalidResponseError) Error() string {
	if e.Err == nil {
		return "invalid response from model"
	}
	return fmt.Sprintf("invalid response: %v", e.Err)
}

func (e InvalidResponseError) Unwrap() error {
	return e.Err
}

// MaxStepsError é retornado quando MaxSteps é atingido.
// Mantido para compatibilidade, mas novo código deve usar LimitError.
type MaxStepsError struct {
	Max int
}

func (e MaxStepsError) Error() string {
	return fmt.Sprintf("max steps reached (%d)", e.Max)
}

// ModelError é retornado quando o modelo falha.
type ModelError struct {
	Err error
}

func (e ModelError) Error() string {
	if e.Err == nil {
		return "model error"
	}
	return fmt.Sprintf("model error: %v", e.Err)
}

func (e ModelError) Unwrap() error {
	return e.Err
}

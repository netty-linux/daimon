package agentloop

// Event representa um evento emitido pelo agent loop.
type Event struct {
	Type string
	Data map[string]interface{}
}

// eventEmitter permite emitir eventos de forma controlada.
type eventEmitter interface {
	emit(Event)
}

// defaultEmitter é o emissor padrão (no-op por enquanto).
type defaultEmitter struct{}

func (defaultEmitter) emit(e Event) {
	// Por enquanto, eventos são apenas para observabilidade futura
	// Em produção, isso pode enviar para um sistema de logs ou tracing
}

var emitter eventEmitter = defaultEmitter{}

// emitEvent emite um evento para observabilidade.
// Eventos não devem conter:
//   - prompts ou respostas completas
//   - argumentos ou resultados de ferramentas
//   - nomes de arquivos ou IDs fornecidos pelo modelo
//   - mensagens de erro detalhadas
//   - secrets ou chain-of-thought
func emitEvent(e Event) {
	emitter.emit(e)
}

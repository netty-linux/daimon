package agentloop

import (
	"encoding/json"

	"github.com/netty-linux/daimon/internal/model"
)

// messageBytes mede o tamanho em bytes de uma Message.
// Inclui Content, ToolCallID, e para tool calls: IDs, nomes, argumentos JSON.
// Conta bytes usando len() sobre UTF-8, não runes.
func messageBytes(msg model.Message) int {
	total := len(msg.Content) + len(msg.ToolCallID)

	for _, tc := range msg.ToolCalls {
		total += len(tc.ID) + len(tc.Name)
		if tc.Arguments != nil {
			// Argumentos já são []byte ou string; assumimos que são JSON válido
			if argStr, ok := tc.Arguments.(string); ok {
				total += len(argStr)
			} else if argBytes, ok := tc.Arguments.([]byte); ok {
				total += len(argBytes)
			} else {
				// Fallback: marshal
				if b, err := json.Marshal(tc.Arguments); err == nil {
					total += len(b)
				}
			}
		}
	}

	return total
}

// toolCallArgumentsBytes mede o tamanho total dos argumentos de tool calls.
func toolCallArgumentsBytes(calls []model.ToolCall) int {
	total := 0
	for _, tc := range calls {
		if tc.Arguments != nil {
			if argStr, ok := tc.Arguments.(string); ok {
				total += len(argStr)
			} else if argBytes, ok := tc.Arguments.([]byte); ok {
				total += len(argBytes)
			} else {
				if b, err := json.Marshal(tc.Arguments); err == nil {
					total += len(b)
				}
			}
		}
	}
	return total
}

// historyBytes mede o tamanho total de um histórico de mensagens.
func historyBytes(history []model.Message) int {
	total := 0
	for _, msg := range history {
		total += messageBytes(msg)
	}
	return total
}

// canFitInHistory verifica se adicionar uma mensagem excederia os limites.
func canFitInHistory(history []model.Message, newMsg model.Message, maxMessages, maxBytes int) bool {
	if len(history)+1 > maxMessages {
		return false
	}
	currentBytes := historyBytes(history)
	newBytes := messageBytes(newMsg)
	return currentBytes+newBytes <= maxBytes
}

// reserveHistoryForResults verifica se há espaço para reservar recibos de tool results.
// Cada resultado é considerado até maxResultBytes.
func reserveHistoryForResults(history []model.Message, assistantMsg model.Message, toolCallsCount, maxMessages, maxBytes, maxResultBytes int) bool {
	// Mensagem assistant com tool calls
	assistantBytes := messageBytes(assistantMsg)

	// Espaço para cada resultado (conservador: maxResultBytes cada)
	resultsSpace := toolCallsCount * maxResultBytes

	// Mensagens totais necessárias: 1 (assistant) + toolCallsCount (results)
	totalMessagesNeeded := len(history) + 1 + toolCallsCount
	totalBytesNeeded := historyBytes(history) + assistantBytes + resultsSpace

	return totalMessagesNeeded <= maxMessages && totalBytesNeeded <= maxBytes
}

package agentloop

import "github.com/netty-linux/daimon/internal/model"

const maxInt = int(^uint(0) >> 1)

// Sums saturate, and capacity checks subtract from the remaining budget so
// overflow can never make an oversized batch look small enough to execute.
func sizeSum(a, b int) int {
	if b > maxInt-a {
		return maxInt
	}
	return a + b
}
func messageBytes(msg model.Message) int {
	total := sizeSum(len(msg.Content), len(msg.ToolCallID))
	for _, call := range msg.ToolCalls {
		total = sizeSum(total, len(call.ID))
		total = sizeSum(total, len(call.Name))
		total = sizeSum(total, len(call.Arguments))
	}
	return total
}
func historyBytes(history []model.Message) int {
	total := 0
	for _, msg := range history {
		total = sizeSum(total, messageBytes(msg))
	}
	return total
}

// historyLimit checks one new message plus reserved receipts for calls.
func historyLimit(history []model.Message, next model.Message, calls []model.ToolCall, b Budget) error {
	count := sizeSum(sizeSum(len(history), 1), len(calls))
	if len(history) >= b.MaxHistoryMessages || len(calls) > b.MaxHistoryMessages-len(history)-1 {
		return LimitError{Kind: LimitMaxHistoryMessages, Limit: int64(b.MaxHistoryMessages), Actual: int64(count)}
	}
	used := historyBytes(history)
	remaining := b.MaxHistoryBytes
	var exceeded bool
	for _, n := range []int{used, messageBytes(next)} {
		if n > remaining {
			exceeded = true
			break
		}
		remaining -= n
	}
	actual := sizeSum(used, messageBytes(next))
	// Correlation IDs occupy bytes in both the assistant call and its receipt.
	for _, call := range calls {
		actual = sizeSum(sizeSum(actual, len(call.ID)), b.MaxToolResultBytes)
		if !exceeded {
			if len(call.ID) > remaining {
				exceeded = true
				continue
			}
			remaining -= len(call.ID)
			if b.MaxToolResultBytes > remaining {
				exceeded = true
				continue
			}
			remaining -= b.MaxToolResultBytes
		}
	}
	if exceeded {
		return LimitError{Kind: LimitMaxHistoryBytes, Limit: int64(b.MaxHistoryBytes), Actual: int64(actual)}
	}
	return nil
}

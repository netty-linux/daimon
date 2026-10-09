package sessions

import (
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/threads"
)

// This private pre-admission read never runs under the Manager mutex and never
// changes normal resolution failures. Unknown identity blocks scheduled turns.
func (m *Manager) admissionBot(id threads.ID) (bot bots.ID) {
	defer func() {
		if recover() != nil {
			bot = ""
		}
	}()
	t, e := m.deps.Threads.Get(id)
	if e != nil || threads.Validate(t) != nil || t.ID != id {
		return ""
	}
	return t.BotID
}

// ScheduledStatus reports Bot-wide admission without private configuration.
func (m *Manager) ScheduledStatus(bot bots.ID) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := Status("")
	for _, id := range m.active {
		s := m.sessions[id]
		if s == nil {
			status = Running
			continue
		}
		if s.admissionBot == "" || s.admissionBot == bot {
			if s.snapshot.Status == WaitingApproval {
				return WaitingApproval
			}
			status = Running
		}
	}
	return status
}

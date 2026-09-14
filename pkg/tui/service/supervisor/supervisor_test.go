package supervisor

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/tui/service/tabstate"
)

func newTestSupervisor(ids []string, activeID string) *Supervisor {
	s := &Supervisor{
		runners:      make(map[string]*SessionRunner),
		programReady: make(chan struct{}),
	}
	for _, id := range ids {
		s.runners[id] = &SessionRunner{ID: id, State: tabstate.New(id, "")}
		s.order = append(s.order, id)
	}
	s.activeID = activeID
	return s
}

func TestCloseSession_FocusesPreviousTab(t *testing.T) {
	t.Parallel()
	// Tabs: [A, B, C], active=C. Close C → expect B.
	s := newTestSupervisor([]string{"A", "B", "C"}, "C")

	next := s.CloseSession("C")

	assert.Equal(t, "B", next)
	assert.Equal(t, "B", s.activeID)
	assert.Equal(t, []string{"A", "B"}, s.order)
}

func TestCloseSession_FocusesPreviousTab_Middle(t *testing.T) {
	t.Parallel()
	// Tabs: [A, B, C], active=B. Close B → expect A.
	s := newTestSupervisor([]string{"A", "B", "C"}, "B")

	next := s.CloseSession("B")

	assert.Equal(t, "A", next)
	assert.Equal(t, "A", s.activeID)
	assert.Equal(t, []string{"A", "C"}, s.order)
}

func TestCloseSession_FirstTab_FocusesNewFirst(t *testing.T) {
	t.Parallel()
	// Tabs: [A, B, C], active=A. Close A → expect B (new first).
	s := newTestSupervisor([]string{"A", "B", "C"}, "A")

	next := s.CloseSession("A")

	assert.Equal(t, "B", next)
	assert.Equal(t, "B", s.activeID)
	assert.Equal(t, []string{"B", "C"}, s.order)
}

func TestCloseSession_LastRemaining(t *testing.T) {
	t.Parallel()
	// Tabs: [A], active=A. Close A → expect "".
	s := newTestSupervisor([]string{"A"}, "A")

	next := s.CloseSession("A")

	assert.Empty(t, next)
	assert.Empty(t, s.activeID)
	assert.Empty(t, s.order)
}

func TestCloseSession_InactiveTab(t *testing.T) {
	t.Parallel()
	// Tabs: [A, B, C], active=A. Close C → active stays A.
	s := newTestSupervisor([]string{"A", "B", "C"}, "A")

	next := s.CloseSession("C")

	assert.Equal(t, "A", next)
	assert.Equal(t, "A", s.activeID)
	assert.Equal(t, []string{"A", "B"}, s.order)
}

func TestCloseSession_NonExistent(t *testing.T) {
	t.Parallel()
	s := newTestSupervisor([]string{"A", "B"}, "A")

	next := s.CloseSession("Z")

	assert.Equal(t, "A", next)
	assert.Equal(t, []string{"A", "B"}, s.order)
}

func TestCloseSession_TwoTabs_CloseSecond(t *testing.T) {
	t.Parallel()
	// Tabs: [A, B], active=B. Close B → expect A.
	s := newTestSupervisor([]string{"A", "B"}, "B")

	next := s.CloseSession("B")

	assert.Equal(t, "A", next)
	assert.Equal(t, "A", s.activeID)
	assert.Equal(t, []string{"A"}, s.order)
}

func TestRuntimeEventUpdatesOwningTab(t *testing.T) {
	t.Parallel()
	s := newTestSupervisor([]string{"A", "B"}, "A")
	active := &runtime.ElicitationRequestEvent{ElicitationID: "active"}
	background := &runtime.ElicitationRequestEvent{ElicitationID: "background"}
	s.handleRuntimeEvent("A", active)
	s.handleRuntimeEvent("B", background)
	s.handleRuntimeEvent("gone", background)
	tabs, activeIdx := s.GetTabs()
	assert.Equal(t, 0, activeIdx)
	assert.False(t, tabs[0].NeedsAttention)
	assert.True(t, tabs[1].NeedsAttention)
	assert.Nil(t, s.GetRunner("A").State.Consume())
	assert.Same(t, background, s.GetRunner("B").State.Consume())
	s.SwitchTo("B")
	tabs, activeIdx = s.GetTabs()
	assert.Equal(t, 1, activeIdx)
	assert.False(t, tabs[1].NeedsAttention)
}

func TestRestoredConversationKeepsTabRoutingIdentity(t *testing.T) {
	t.Parallel()
	s := newTestSupervisor([]string{"tab"}, "tab")
	s.GetRunner("tab").State.ReplaceSession("restored")
	s.handleRuntimeEvent("tab", &runtime.StreamStartedEvent{SessionID: "restored"})
	tabs, _ := s.GetTabs()
	assert.Equal(t, "tab", tabs[0].SessionID)
	assert.True(t, tabs[0].IsRunning)
	s.handleRuntimeEvent("tab", &runtime.StreamStoppedEvent{SessionID: "tab"})
	tabs, _ = s.GetTabs()
	assert.True(t, tabs[0].IsRunning)
	s.handleRuntimeEvent("tab", &runtime.StreamStoppedEvent{SessionID: "restored"})
	tabs, _ = s.GetTabs()
	assert.False(t, tabs[0].IsRunning)
}

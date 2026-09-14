// Package tabstate holds the state shared by a tab and its runtime subscription.
package tabstate

import (
	"slices"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/docker/docker-agent/pkg/runtime"
)

// State is shared with the supervisor, which applies events before routing them to the UI.
// Its methods are safe to call from either the subscription or the UI goroutine.
type State struct {
	mu             sync.Mutex
	tabID          string
	title          string
	running        bool
	needsAttention bool
	pending        []tea.Msg
}

func New(tabID, title string) *State { return &State{tabID: tabID, title: title} }

// Snapshot returns one consistent set of tab-bar properties.
func (s *State) Snapshot() (title string, running, needsAttention bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.title, s.running, s.needsAttention
}

func (s *State) SetTitle(title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.title = title
}

func (s *State) Acknowledge() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.needsAttention = false
}

// Prepend restores an already-seen dialog without raising a new attention indicator.
func (s *State) Prepend(event tea.Msg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append([]tea.Msg{event}, s.pending...)
}

func (s *State) Consume() tea.Msg {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	event := s.pending[0]
	s.pending[0] = nil
	s.pending = s.pending[1:]
	return event
}

// Apply updates status at event arrival. The caller supplies visibility under its routing lock.
func (s *State) Apply(msg tea.Msg, active bool) (changed, bell bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch ev := msg.(type) {
	case *runtime.StreamStartedEvent:
		if !isTopLevelStream(s.tabID, ev.SessionID) {
			return false, false
		}
		s.running = true
		s.retainDetachedElicitations()
	case *runtime.StreamStoppedEvent:
		if !isTopLevelStream(s.tabID, ev.SessionID) {
			return false, false
		}
		s.running = false
		s.retainDetachedElicitations()
	case *runtime.SessionTitleEvent:
		s.title = ev.Title
	case *runtime.ToolCallConfirmationEvent, *runtime.MaxIterationsReachedEvent, *runtime.ElicitationRequestEvent:
		if active {
			return false, false
		}
		s.pending = append(s.pending, msg)
		s.needsAttention = true
		return true, true
	default:
		return false, false
	}
	return true, false
}

// Detached jobs outlive the foreground turn; their unanswered prompts remain live.
func (s *State) retainDetachedElicitations() {
	s.pending = slices.DeleteFunc(s.pending, func(msg tea.Msg) bool {
		ev, ok := msg.(*runtime.ElicitationRequestEvent)
		return !ok || isTopLevelStream(s.tabID, ev.SessionID)
	})
	s.needsAttention = len(s.pending) > 0
}

// Older emitters omit the session ID for top-level events.
func isTopLevelStream(sessionID, eventSessionID string) bool {
	return eventSessionID == "" || eventSessionID == sessionID
}

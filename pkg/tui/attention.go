package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/tui/core"
	"github.com/docker/docker-agent/pkg/tui/dialog"
	"github.com/docker/docker-agent/pkg/tui/service"
)

// stashedDialog holds a background dialog instance that was on screen when
// the user navigated away from a tab, paired with the runtime event that
// caused it to open. The event is used as an identity check on return: if
// the tab's pending event no longer matches, the agent
// has superseded the prompt and we discard the stash in favour of building
// a fresh dialog from the new event.
type stashedDialog struct {
	dialog dialog.Dialog
	event  tea.Msg
}

// replayPendingEvent checks if a session has pending attention events (e.g.
// tool confirmation, max iterations, elicitation) that were received while
// the tab was inactive. Every queued event is replayed, in arrival order, so
// concurrent attention events (e.g. two background-job elicitations) all
// reopen as stacked dialogs instead of only the most recent one (#3584). Each
// event was already processed by the chat page (updating the message list),
// but the dialog command was discarded for inactive sessions.
//
// If a stashed dialog instance is available for this session and its
// associated event still matches the first pending one, the same instance is
// re-opened so any in-progress input survives the round trip (issue #2770).
// Otherwise the stash is discarded and a fresh dialog is built.
func (m *appModel) replayPendingEvent(sessionID string) tea.Cmd {
	tab := m.tabs[sessionID]
	if tab == nil {
		return nil
	}
	if tab.sessionState == nil || tab.state == nil {
		tab.stashedDialog = nil
		return nil
	}

	var cmds []tea.Cmd
	for first := true; ; first = false {
		pendingEvent := tab.state.Consume()
		if pendingEvent == nil {
			if first {
				// No pending event at all: any stash is stale (e.g. the agent finished).
				tab.stashedDialog = nil
			}
			break
		}

		// Only the first (oldest) event can match a stashed live dialog
		// instance: the stash holds exactly the one dialog that was on
		// screen when the user left the tab.
		if first {
			if stash := tab.stashedDialog; stash != nil {
				tab.stashedDialog = nil
				if stash.event == pendingEvent && stash.dialog != nil {
					cmds = append(cmds, core.CmdHandler(dialog.OpenDialogMsg{
						Model:            stash.dialog,
						OriginatingEvent: pendingEvent,
					}))
					continue
				}
			}
		}

		if cmd := m.dialogCmdForPendingEvent(pendingEvent, tab.sessionState); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	if len(cmds) == 0 {
		return nil
	}
	// tea.Sequence (not tea.Batch) is required for deterministic ordering:
	// tea.Batch runs commands concurrently with no ordering guarantee on
	// which resulting Msg reaches Update first, so two concurrent attention
	// events (e.g. two background-job elicitations queued while this tab was
	// inactive) could stack in a random order on every replay even though
	// they were popped off the FIFO queue above in arrival order (#3584
	// should-fix: FIFO replay). tea.Sequence guarantees each OpenDialogMsg is
	// delivered to Update in the order the commands were built, so the
	// dialog stack's bottom-to-top order matches arrival order every time.
	return tea.Sequence(cmds...)
}

// dialogCmdForPendingEvent builds the OpenDialogMsg command for a single
// replayed attention event. Shared by every event replayPendingEvent pops off
// the queue after the first (stash-eligible) one.
func (m *appModel) dialogCmdForPendingEvent(pendingEvent tea.Msg, sessionState *service.SessionState) tea.Cmd {
	switch ev := pendingEvent.(type) {
	case *runtime.ToolCallConfirmationEvent:
		return core.CmdHandler(dialog.OpenDialogMsg{
			Model:            dialog.NewToolConfirmationDialog(m.ar, ev, sessionState),
			OriginatingEvent: ev,
		})

	case *runtime.MaxIterationsReachedEvent:
		return core.CmdHandler(dialog.OpenDialogMsg{
			Model:            dialog.NewMaxIterationsDialog(ev.MaxIterations, m.application),
			OriginatingEvent: ev,
		})

	case *runtime.ElicitationRequestEvent:
		return m.replayElicitationEvent(ev)
	}

	return nil
}

// replayElicitationEvent opens the appropriate elicitation dialog for a pending event.
func (m *appModel) replayElicitationEvent(ev *runtime.ElicitationRequestEvent) tea.Cmd {
	// Check if this is an OAuth flow
	if ev.Meta != nil {
		if elicitationType, ok := ev.Meta["docker-agent/type"].(string); ok && elicitationType == "oauth_flow" {
			var serverURL string
			if url, ok := ev.Meta["docker-agent/server_url"].(string); ok {
				serverURL = url
			}
			return core.CmdHandler(dialog.OpenDialogMsg{
				Model:            dialog.NewOAuthAuthorizationDialog(m.ctx(), serverURL, m.application, ev.ElicitationID),
				OriginatingEvent: ev,
			})
		}
	}

	switch ev.Mode {
	case "url":
		return core.CmdHandler(dialog.OpenDialogMsg{
			Model:            dialog.NewURLElicitationDialog(m.ctx(), ev.Message, ev.URL, ev.ElicitationID),
			OriginatingEvent: ev,
		})
	default:
		return core.CmdHandler(dialog.OpenDialogMsg{
			Model:            dialog.NewElicitationDialog(ev.Message, ev.Schema, ev.Meta, ev.ElicitationID),
			OriginatingEvent: ev,
		})
	}
}

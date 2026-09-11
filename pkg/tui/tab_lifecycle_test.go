package tui

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/docker/docker-agent/pkg/app"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/tui/commands"
	"github.com/docker/docker-agent/pkg/tui/components/editor"
	"github.com/docker/docker-agent/pkg/tui/components/notification"
	"github.com/docker/docker-agent/pkg/tui/core"
	"github.com/docker/docker-agent/pkg/tui/dialog"
	"github.com/docker/docker-agent/pkg/tui/messages"
	"github.com/docker/docker-agent/pkg/tui/page/chat"
	"github.com/docker/docker-agent/pkg/tui/service"
	"github.com/docker/docker-agent/pkg/tui/service/supervisor"
)

func newTabLifecycleModel(t *testing.T) *appModel {
	t.Helper()
	m := newSpawnTestModel(t, &spySpawner{})
	delete(m.tabs, "test")
	m.initSessionComponents(m.supervisor.ActiveID(), m.application, m.application.Session())
	t.Cleanup(func() {
		m.cleanupAll()
		m.cleanupManagedResources()
	})
	return m
}

func TestSwitchTabPreservesComponentsAndDraft(t *testing.T) {
	t.Parallel()
	m := newTabLifecycleModel(t)
	firstID := m.supervisor.ActiveID()
	firstPage, firstEditor, firstState := m.chatPage, m.editor, m.sessionState
	firstEditor.SetValue("unfinished draft")

	_, _ = m.handleSpawnSession("/second")
	secondID := m.supervisor.ActiveID()
	secondPage, secondEditor := m.chatPage, m.editor
	secondEditor.SetValue("second draft")

	_, _ = m.handleSwitchTab(firstID)
	assert.Same(t, firstPage, m.chatPage)
	assert.Same(t, firstEditor, m.editor)
	assert.Same(t, firstState, m.sessionState)
	assert.Equal(t, "unfinished draft", m.editor.Value())

	_, _ = m.handleSwitchTab(secondID)
	assert.Same(t, secondPage, m.chatPage)
	assert.Same(t, secondEditor, m.editor)
	assert.Equal(t, "second draft", m.editor.Value())
}

func TestSwitchTabFailureLeavesDialogAndComponents(t *testing.T) {
	t.Parallel()
	m := newTabLifecycleModel(t)
	id := m.supervisor.ActiveID()
	page, ed, state := m.chatPage, m.editor, m.sessionState
	event := &messages.SendMsg{}
	prompt := &stubDialog{id: "draft"}
	_, _ = m.dialogMgr.Update(dialog.OpenDialogMsg{Model: prompt, OriginatingEvent: event})

	_, cmd := m.handleSwitchTab("missing")

	assert.Equal(t, id, m.supervisor.ActiveID())
	assert.NotContains(t, m.tabs, "missing")
	assert.Same(t, page, m.chatPage)
	assert.Same(t, ed, m.editor)
	assert.Same(t, state, m.sessionState)
	assert.Same(t, prompt, m.dialogMgr.TopDialog())
	assert.Nil(t, m.tabs[id].stashedDialog)
	assert.Nil(t, m.supervisor.ConsumePendingEvent(id))
	assert.True(t, hasMsg[notification.ShowMsg](collectMsgs(cmd)))
}

func TestSwitchTabBuildsComponentsBeforeReplacingActiveUI(t *testing.T) {
	t.Parallel()
	m := newTabLifecycleModel(t)
	outgoingApp, outgoingPage, outgoingEditor := m.application, m.chatPage, m.editor
	calls := 0
	m.buildCommandCategories = func(_ context.Context, model tea.Model) []commands.Category {
		calls++
		assert.Same(t, outgoingApp, core.Resolve[*app.App](model))
		assert.Same(t, outgoingPage, core.Resolve[chat.Page](model))
		assert.Same(t, outgoingEditor, core.Resolve[editor.Editor](model))
		return nil
	}

	_, _ = m.handleSpawnSession("/second")

	assert.Equal(t, 2, calls)
	assert.NotSame(t, outgoingPage, m.chatPage)
	assert.NotSame(t, outgoingEditor, m.editor)
}

func TestSwitchTabFailedRestoreConsumesPendingState(t *testing.T) {
	t.Parallel()
	m := newTabLifecycleModel(t)
	outgoingPage := m.chatPage
	sess := session.New()
	application := app.New(t.Context(), storeRuntime{store: session.NewInMemorySessionStore()}, sess)
	id := m.supervisor.AddSession(t.Context(), application, sess, "/second", nil)
	m.ensureTab(id).pendingRestore = new("missing")
	m.ensureTab(id).pendingSidebarCollapsed = new(true)
	m.buildCommandCategories = func(_ context.Context, model tea.Model) []commands.Category {
		assert.Same(t, application, core.Resolve[*app.App](model))
		assert.Same(t, outgoingPage, core.Resolve[chat.Page](model))
		return nil
	}

	_, _ = m.handleSwitchTab(id)

	assert.Nil(t, m.tabs[id].pendingRestore)
	assert.Nil(t, m.tabs[id].pendingSidebarCollapsed)
	assert.Same(t, application, m.application)
	assert.Same(t, m.tabs[id].chatPage, m.chatPage)
	assert.True(t, m.chatPage.GetSidebarSettings().Collapsed)
	assert.Nil(t, m.applySidebarCollapsed(id))
}

func TestCloseInactiveTabRemovesAllUIState(t *testing.T) {
	t.Parallel()
	m := newTabLifecycleModel(t)
	closedID := m.supervisor.ActiveID()
	closedEditor := &mockEditor{}
	m.ensureTab(closedID).editor = closedEditor
	m.ensureTab(closedID).pendingRestore = new("saved")
	m.ensureTab(closedID).pendingSidebarCollapsed = new(true)
	m.ensureTab(closedID).stashedDialog = &stashedDialog{dialog: &stubDialog{id: "stashed"}}
	_, _ = m.handleSpawnSession("/second")
	page, ed, state := m.chatPage, m.editor, m.sessionState

	_, _ = m.handleCloseTab(closedID)

	assert.True(t, closedEditor.cleanupCalled)
	assert.NotContains(t, m.tabs, closedID)
	assert.Same(t, page, m.chatPage)
	assert.Same(t, ed, m.editor)
	assert.Same(t, state, m.sessionState)
}

func TestCloseLastTabKeepsUIUntilReplacement(t *testing.T) {
	for _, name := range []string{"spawn", "spawn failure"} {
		t.Run(name, func(t *testing.T) {
			fail := name == "spawn failure"
			t.Parallel()
			m := newTabLifecycleModel(t)
			oldID := m.supervisor.ActiveID()
			oldPage, oldEditor := m.chatPage, m.editor
			if fail {
				m.supervisor.Shutdown()
				m.supervisor = supervisor.New(func(context.Context, string) (*app.App, *session.Session, func(), error) {
					return nil, nil, nil, errors.New("spawn failed")
				})
				m.supervisor.AddSession(t.Context(), m.application, m.application.Session(), "/initial", nil)
			}

			_, cmd := m.handleCloseTab(oldID)

			assert.NotContains(t, m.tabs, oldID)
			if fail {
				assert.Zero(t, m.supervisor.Count())
				assert.Same(t, oldPage, m.chatPage)
				assert.Same(t, oldEditor, m.editor)
				assert.True(t, hasMsg[notification.ShowMsg](collectMsgs(cmd)))
			} else {
				assert.Equal(t, 1, m.supervisor.Count())
				assert.NotSame(t, oldPage, m.chatPage)
				assert.NotSame(t, oldEditor, m.editor)
			}
		})
	}
}

func TestTabPersistedSessionLookup(t *testing.T) {
	t.Parallel()
	m := newTabLifecycleModel(t)
	id := m.supervisor.ActiveID()
	assert.Equal(t, m.application.Session().ID, m.persistedSessionID(id))
	assert.Equal(t, id, m.findTabByPersistedID(m.application.Session().ID))

	for _, savedID := range []string{"saved-session", ""} {
		m.ensureTab(id).pendingRestore = new(savedID)
		assert.Equal(t, savedID, m.persistedSessionID(id))
		assert.Equal(t, id, m.findTabByPersistedID(savedID))
	}
	m.tabs[id].pendingRestore = nil

	loaded := session.New()
	m.application.ReplaceSession(t.Context(), loaded)
	assert.Equal(t, id, m.supervisor.ActiveID(), "loading a conversation must not change the routing key")
	assert.Equal(t, loaded.ID, m.persistedSessionID(id))
	assert.Equal(t, id, m.findTabByPersistedID(loaded.ID))
	assert.Equal(t, "missing", m.persistedSessionID("missing"))
	assert.Empty(t, m.findTabByPersistedID("missing"))
}

func TestRoutedEventDoesNotInitializeUnloadedTab(t *testing.T) {
	t.Parallel()
	m := newTabLifecycleModel(t)
	id, err := m.supervisor.SpawnSession(t.Context(), "/second")
	require.NoError(t, err)
	m.ensureTab(id).pendingRestore = new("saved")
	m.ensureTab(id).sessionState = service.NewSessionState(session.New())

	_, cmd := m.handleRoutedMsg(messages.RoutedMsg{SessionID: id, Inner: messages.SendMsg{Content: "hidden"}})

	assert.Nil(t, cmd)
	assert.Nil(t, m.tabs[id].chatPage)
	assert.Equal(t, "saved", *m.tabs[id].pendingRestore)
}

func TestSwitchTabRestoresSavedSession(t *testing.T) {
	t.Parallel()
	m := newTabLifecycleModel(t)
	store := session.NewInMemorySessionStore()
	saved := session.New(session.WithWorkingDir("/second"))
	saved.AddMessage(session.UserMessage("saved conversation"))
	require.NoError(t, store.AddSession(t.Context(), saved))
	placeholder := session.New(session.WithWorkingDir("/second"))
	application := app.New(t.Context(), storeRuntime{store: store}, placeholder)
	id := m.supervisor.AddSession(t.Context(), application, placeholder, "/second", nil)
	tab := m.ensureTab(id)
	tab.pendingRestore = new(saved.ID)
	tab.pendingSidebarCollapsed = new(true)

	_, _ = m.handleSwitchTab(id)

	assert.Same(t, tab, m.tabs[id])
	assert.Equal(t, id, m.supervisor.ActiveID())
	assert.Equal(t, saved.ID, m.application.Session().ID)
	assert.Equal(t, saved.ID, m.persistedSessionID(id))
	assert.Nil(t, tab.pendingRestore)
	assert.Nil(t, tab.pendingSidebarCollapsed)
	assert.Same(t, tab.chatPage, m.chatPage)
	assert.Same(t, tab.editor, m.editor)
	assert.True(t, m.chatPage.GetSidebarSettings().Collapsed)
	assert.Contains(t, m.chatPage.View(), "saved conversation")
}

func TestInitRestoresPendingTab(t *testing.T) {
	for _, background := range []bool{false, true} {
		name := "initial tab"
		if background {
			name = "background tab"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := newTabLifecycleModel(t)
			store := session.NewInMemorySessionStore()
			saved := session.New(session.WithWorkingDir("/initial"))
			saved.AddMessage(session.UserMessage("startup conversation"))
			require.NoError(t, store.AddSession(t.Context(), saved))
			id := m.supervisor.ActiveID()
			placeholder := m.application.Session()
			application := app.New(t.Context(), storeRuntime{store: store}, placeholder)
			if background {
				placeholder = session.New()
				application = app.New(t.Context(), storeRuntime{store: store}, placeholder)
				id = m.supervisor.AddSession(t.Context(), application, placeholder, "/initial", nil)
				m.pendingActiveTab = id
			} else {
				m.supervisor.ReplaceRunnerApp(t.Context(), id, application, "/initial", nil)
				m.application = application
			}
			tab := m.ensureTab(id)
			tab.pendingRestore = new(saved.ID)
			tab.pendingSidebarCollapsed = new(true)

			_ = m.init()

			assert.Same(t, tab, m.tabs[id])
			assert.Equal(t, id, m.supervisor.ActiveID())
			assert.Empty(t, m.pendingActiveTab)
			assert.Nil(t, tab.pendingRestore)
			assert.Nil(t, tab.pendingSidebarCollapsed)
			assert.Equal(t, saved.ID, m.application.Session().ID)
			assert.Contains(t, m.chatPage.View(), "startup conversation")
		})
	}
}

func TestSettingsAndCleanupSkipUninitializedTabs(t *testing.T) {
	setupSettingsConfigTest(t)
	m := newApplySettingsModel(t)
	tab := m.ensureTab("restored")
	tab.pendingRestore = new("saved")

	_, _ = m.handleApplySettings(messages.ApplySettingsMsg{Preferences: defaultTestPreferences()})
	m.cleanupAll()
	m.cleanupManagedResources()

	assert.Same(t, tab, m.tabs["restored"])
	assert.Equal(t, "saved", *tab.pendingRestore)
	assert.Nil(t, tab.chatPage)
	assert.Nil(t, tab.editor)
}

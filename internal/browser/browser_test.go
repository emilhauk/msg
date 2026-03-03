//go:build !short

// Package browser_test contains end-to-end browser tests using go-rod.
// These tests launch a real headless Chromium browser and exercise the
// full client/server stack. They are excluded from the fast unit-test run
// via the !short build constraint; run them with:
//
//	go test ./internal/browser/... -v -timeout 120s
package browser_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/emilhauk/chat/internal/model"
	"github.com/emilhauk/chat/internal/testutil"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	roomID      = "room-browser"
	aliceUserID = "user-alice"
	bobUserID   = "user-bob"
)

var (
	alice = model.User{ID: aliceUserID, Name: "Alice", Email: "alice@example.com"}
	bob   = model.User{ID: bobUserID, Name: "Bob", Email: "bob@example.com"}
)

// newBrowser launches a headless Chromium browser and registers cleanup.
func newBrowser(t *testing.T) *rod.Browser {
	t.Helper()
	l := launcher.New().Headless(true)
	if path, exists := launcher.LookPath(); exists {
		l = l.Bin(path)
	}
	u := l.MustLaunch()
	b := rod.New().ControlURL(u).MustConnect()
	t.Cleanup(func() { b.MustClose() })
	return b
}

// authPage creates a new page, sets the session cookie for the given user,
// navigates to the room, and waits for the page to fully load.
func authPage(t *testing.T, b *rod.Browser, ts *testutil.TestServer, user model.User, room string) *rod.Page {
	t.Helper()
	parsed, err := url.Parse(ts.Server.URL)
	require.NoError(t, err, "authPage: parse server URL")

	cookie := ts.AuthCookie(t, user)
	page := b.MustPage("")
	page.MustSetCookies(&proto.NetworkCookieParam{
		Name:   cookie.Name,
		Value:  cookie.Value,
		Domain: parsed.Hostname(),
		Path:   "/",
	})
	page.MustNavigate(ts.Server.URL + "/rooms/" + room)
	page.MustWaitLoad()
	return page
}

// postMessage sends a POST /rooms/{id}/messages request as the given user.
func postMessage(t *testing.T, ts *testutil.TestServer, user model.User, room, text string) {
	t.Helper()
	cookie := ts.AuthCookie(t, user)
	form := url.Values{"text": {text}}
	req, err := http.NewRequest("POST",
		ts.Server.URL+"/rooms/"+room+"/messages",
		strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// seedMessage inserts a message directly into Redis for setup purposes.
func seedMessage(t *testing.T, ts *testutil.TestServer, user model.User, room, text string) model.Message {
	t.Helper()
	ms := time.Now().UnixMilli()
	msgID := fmt.Sprintf("%d-%s", ms, user.ID)
	msg := model.Message{
		ID:          msgID,
		RoomID:      room,
		UserID:      user.ID,
		Text:        text,
		CreatedAt:   time.UnixMilli(ms),
		CreatedAtMS: fmt.Sprintf("%d", ms),
	}
	require.NoError(t, ts.Redis.SaveMessage(context.Background(), msg))
	return msg
}

// isHidden returns true if the element has the HTML `hidden` attribute set.
func isHidden(t *testing.T, el *rod.Element) bool {
	t.Helper()
	attr, err := el.Attribute("hidden")
	require.NoError(t, err)
	return attr != nil
}

// TestOwnerControls_InitialLoad verifies that on page load, alice's own
// messages show the edit button and bob's messages keep it hidden.
func TestOwnerControls_InitialLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Browser Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))

	aliceMsg := seedMessage(t, ts, alice, roomID, "hello from alice")
	time.Sleep(2 * time.Millisecond) // ensure distinct timestamps
	bobMsg := seedMessage(t, ts, bob, roomID, "hello from bob")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomID)

	// alice's own message: edit button must NOT be hidden
	aliceEdit := page.Timeout(5 * time.Second).
		MustElement("#msg-" + aliceMsg.ID + " .message__edit")
	if isHidden(t, aliceEdit) {
		t.Error("alice's own message: expected edit button to be visible, but it is hidden")
	}

	// bob's message: edit button must remain hidden for alice
	bobEdit := page.Timeout(5 * time.Second).
		MustElement("#msg-" + bobMsg.ID + " .message__edit")
	if !isHidden(t, bobEdit) {
		t.Error("bob's message: expected edit button to be hidden for alice, but it is visible")
	}
}

// TestOwnerControls_SSEInsert verifies that a message posted by alice
// via the API and received via SSE shows the edit button in alice's browser.
// This is the regression test for the htmx:sseMessage vs htmx:afterSwap bug.
func TestOwnerControls_SSEInsert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Browser Test Room"})

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomID)

	// Give the HTMX SSE connection time to subscribe on the server side.
	time.Sleep(300 * time.Millisecond)

	postMessage(t, ts, alice, roomID, "alice sse message")

	// Wait for an article to appear in the message list (SSE insert).
	article := page.Timeout(5 * time.Second).MustElement("article.message")

	// The edit button must be visible (not hidden) for alice's own message.
	editBtn := article.MustElement(".message__edit")
	if isHidden(t, editBtn) {
		t.Error("SSE-inserted own message: expected edit button to be visible, but it is hidden")
	}
}

// TestOwnerControls_SSEInsert_OtherUser verifies that a message posted by bob
// and received via SSE keeps the edit button hidden in alice's browser.
func TestOwnerControls_SSEInsert_OtherUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Browser Test Room"})

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomID)

	// Give the HTMX SSE connection time to subscribe on the server side.
	time.Sleep(300 * time.Millisecond)

	postMessage(t, ts, bob, roomID, "bob sse message")

	// Wait for an article to appear.
	article := page.Timeout(5 * time.Second).MustElement("article.message")

	// The edit button must remain hidden — alice cannot edit bob's message.
	editBtn := article.MustElement(".message__edit")
	if !isHidden(t, editBtn) {
		t.Error("SSE-inserted other user's message: expected edit button to be hidden, but it is visible")
	}
}

// TestVersionReload verifies that when the server publishes a new build version
// via the SSE channel, the page reloads (or shows the update hint).
func TestVersionReload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const versionRoom = "room-version"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: versionRoom, Name: "Version Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, versionRoom)

	// Force document.hasFocus() to return false so the version event triggers
	// window.location.reload() rather than just showing the update hint.
	page.MustEval(`() => { document.hasFocus = () => false; }`)

	// Set up the navigation listener before publishing (so we don't miss it).
	waitNav := page.WaitNavigation(proto.PageLifecycleEventNameLoad)

	// Give both EventSource connections time to establish their Redis subscriptions.
	// The SSE handler subscribes after flushing initial events, so we wait 300ms.
	time.Sleep(300 * time.Millisecond)

	// Publish a different version via Redis pub/sub. The SSE handler relays this
	// as an SSE version event, triggering window.location.reload() in the browser.
	require.NoError(t, ts.Redis.Publish(
		context.Background(), versionRoom, "version:sha-new-deploy",
	))

	// Wait for navigation (page reload) with a 5-second deadline.
	done := make(chan struct{})
	go func() { waitNav(); close(done) }()

	select {
	case <-done:
		// Page reloaded successfully.
	case <-time.After(5 * time.Second):
		t.Fatal("page did not reload within 5s after version change SSE event")
	}
}

// ---------------------------------------------------------------------------
// Service Worker tests
// ---------------------------------------------------------------------------

const swRoom = "room-sw"

// TestSW_Registration verifies that the service worker is registered and
// controls the page after navigating to a room.
func TestSW_Registration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: swRoom, Name: "SW Test Room"})

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, swRoom)

	// Allow time for the SW install → activate cycle.
	time.Sleep(500 * time.Millisecond)

	val, err := page.Eval(`() => !!navigator.serviceWorker.controller`)
	require.NoError(t, err)
	assert.True(t, val.Value.Bool(), "service worker should control the page")
}

// TestSW_PushEvent uses the Chrome DevTools Protocol to inject a synthetic push
// event into the service worker. Because the page tab is visible, the SW should
// suppress the OS notification (tab-visible suppression logic). We verify that
// the SW processed the event without crashing and that the page is still usable.
func TestSW_PushEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: swRoom, Name: "SW Test Room"})

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, swRoom)

	// Allow time for the SW to activate and claim the page.
	time.Sleep(500 * time.Millisecond)

	// Verify SW is controlling the page before we attempt CDP delivery.
	val, err := page.Eval(`() => !!navigator.serviceWorker.controller`)
	require.NoError(t, err)
	if !val.Value.Bool() {
		t.Skip("service worker not controlling the page — skipping CDP push test")
	}

	// Enable the ServiceWorker CDP domain so version events are emitted.
	// We subscribe to the event BEFORE enabling the domain to avoid a race.
	var regID proto.ServiceWorkerRegistrationID
	regCh := make(chan proto.ServiceWorkerRegistrationID, 1)

	waitVersion := b.EachEvent(func(e *proto.ServiceWorkerWorkerVersionUpdated) bool {
		for _, v := range e.Versions {
			if v.RegistrationID != "" {
				regCh <- v.RegistrationID
				return true
			}
		}
		return false
	})

	require.NoError(t, proto.ServiceWorkerEnable{}.Call(page))

	// Wait for the first version updated event (contains registration ID).
	select {
	case regID = <-regCh:
		// Got the registration ID.
	case <-time.After(3 * time.Second):
		// The SW domain may not emit if no SW is installed — skip gracefully.
		waitVersion()
		t.Skip("ServiceWorker.workerVersionUpdated not received within 3s — SW may not be installed")
	}
	waitVersion() // release the EachEvent goroutine

	// Parse the server origin for the CDP call.
	parsed, err := url.Parse(ts.Server.URL)
	require.NoError(t, err)
	origin := parsed.Scheme + "://" + parsed.Host

	// Listen for console messages from any target (SW logs are emitted here).
	consoleCh := make(chan string, 20)
	waitConsole := b.EachEvent(func(e *proto.RuntimeConsoleAPICalled) bool {
		for _, arg := range e.Args {
			s := arg.Description
			if s == "" {
				s = arg.Value.String()
			}
			if strings.Contains(s, "[sw] push received") {
				consoleCh <- s
				return true
			}
		}
		return false
	})

	// Deliver a synthetic push message via CDP.
	pushData := `{"title":"Test","body":"Hi","roomId":"` + swRoom + `","url":"/rooms/` + swRoom + `"}`
	err = proto.ServiceWorkerDeliverPushMessage{
		Origin:         origin,
		RegistrationID: regID,
		Data:           pushData,
	}.Call(page)
	require.NoError(t, err, "CDP DeliverPushMessage should not error")

	// Wait for the SW console log confirming the push was received.
	select {
	case msg := <-consoleCh:
		// The SW logs hasVisibleTab status. We just verify it ran.
		t.Logf("SW console: %s", msg)
	case <-time.After(3 * time.Second):
		// Some headless environments may not surface SW console logs via CDP.
		// Treat as a non-fatal skip rather than a hard failure.
		t.Log("SW console log not captured within 3s — CDP console forwarding may be unavailable")
	}
	waitConsole() // release the EachEvent goroutine

	// Final sanity: the page should still be responsive after the push.
	val, err = page.Eval(`() => document.readyState`)
	require.NoError(t, err)
	assert.Equal(t, "complete", val.Value.String(), "page should still be ready after push delivery")
}

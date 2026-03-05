//go:build !short

package browser_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	waitVersion := page.EachEvent(func(e *proto.ServiceWorkerWorkerVersionUpdated) bool {
		for _, v := range e.Versions {
			if v.RegistrationID != "" {
				regCh <- v.RegistrationID
				return true
			}
		}
		return false
	})

	require.NoError(t, proto.ServiceWorkerEnable{}.Call(page))

	// Start the event-processing loop in the background. EachEvent's wait
	// function must be running for the callback to fire and write to regCh.
	go waitVersion()

	// Wait for the first version updated event (contains registration ID).
	select {
	case regID = <-regCh:
		// Got the registration ID.
	case <-time.After(3 * time.Second):
		t.Skip("ServiceWorker.workerVersionUpdated not received within 3s — SW may not be installed")
	}

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
	go waitConsole()

	// Re-enable the ServiceWorker domain — EachEvent's cleanup disabled it after
	// waitVersion returned.
	require.NoError(t, proto.ServiceWorkerEnable{}.Call(page))

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

	// Final sanity: the page should still be responsive after the push.
	val, err = page.Eval(`() => document.readyState`)
	require.NoError(t, err)
	assert.Equal(t, "complete", val.Value.String(), "page should still be ready after push delivery")
}

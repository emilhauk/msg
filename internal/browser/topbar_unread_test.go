//go:build !short

package browser_test

import (
	"context"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTopbarUnread_AppearsOnMobile verifies that the mobile top-bar unread
// indicator appears when another room receives a message, shows the correct
// room name and count, and links directly to the unread room.
func TestTopbarUnread_AppearsOnMobile(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}

	const (
		viewedRoom = "topbar-viewed"
		otherRoom  = "topbar-other"
	)

	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: viewedRoom, Name: "Viewed Room"})
	ts.SeedRoom(t, model.Room{ID: otherRoom, Name: "Other Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, viewedRoom, alice.ID)
	ts.GrantAccess(t, viewedRoom, bob.ID)
	ts.GrantAccess(t, otherRoom, alice.ID)
	ts.GrantAccess(t, otherRoom, bob.ID)

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, viewedRoom)

	// Set mobile viewport so the indicator is visible.
	require.NoError(t, proto.EmulationSetDeviceMetricsOverride{
		Width:             375,
		Height:            812,
		DeviceScaleFactor: 1,
		Mobile:            true,
	}.Call(page))

	// Give SSE connections time to register.
	time.Sleep(500 * time.Millisecond)

	// Indicator should be hidden initially (no unreads).
	hidden := page.MustEval(`() => document.getElementById('topbar-unread').hidden`).Bool()
	assert.True(t, hidden, "topbar-unread should be hidden initially")

	// Bob posts a message in the other room.
	postMessage(t, ts, bob, otherRoom, "hello from other room")

	// Wait for the unread SSE event + JS update.
	time.Sleep(800 * time.Millisecond)

	// Indicator should now be visible.
	hidden = page.MustEval(`() => document.getElementById('topbar-unread').hidden`).Bool()
	assert.False(t, hidden, "topbar-unread should be visible after unread event")

	// Check room name and count.
	roomText := page.MustEval(`() => document.getElementById('topbar-unread-room').textContent`).Str()
	assert.Equal(t, "# Other Room", roomText, "should show other room name")

	countText := page.MustEval(`() => document.getElementById('topbar-unread-count').textContent`).Str()
	assert.Equal(t, "1", countText, "should show count of 1")

	moreText := page.MustEval(`() => document.getElementById('topbar-unread-more').textContent`).Str()
	assert.Equal(t, "", moreText, "should show no +N with only one unread room")

	// The indicator should link directly to the unread room.
	href := page.MustEval(`() => document.getElementById('topbar-unread').href`).Str()
	assert.Contains(t, href, "/rooms/"+otherRoom, "indicator href should point to the unread room")
}

// TestTopbarUnread_MultipleRooms verifies the +N suffix when multiple rooms
// have unread messages.
func TestTopbarUnread_MultipleRooms(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}

	const (
		viewedRoom = "topbar-multi-viewed"
		roomA      = "topbar-multi-a"
		roomB      = "topbar-multi-b"
	)

	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: viewedRoom, Name: "Main"})
	ts.SeedRoom(t, model.Room{ID: roomA, Name: "Room A"})
	ts.SeedRoom(t, model.Room{ID: roomB, Name: "Room B"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	for _, rid := range []string{viewedRoom, roomA, roomB} {
		ts.GrantAccess(t, rid, alice.ID)
		ts.GrantAccess(t, rid, bob.ID)
	}

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, viewedRoom)

	require.NoError(t, proto.EmulationSetDeviceMetricsOverride{
		Width:             375,
		Height:            812,
		DeviceScaleFactor: 1,
		Mobile:            true,
	}.Call(page))

	time.Sleep(500 * time.Millisecond)

	// Bob posts 3 messages in Room A and 1 in Room B.
	for i := 0; i < 3; i++ {
		postMessage(t, ts, bob, roomA, "msg a")
		time.Sleep(50 * time.Millisecond)
	}
	postMessage(t, ts, bob, roomB, "msg b")

	time.Sleep(800 * time.Millisecond)

	// The indicator should show Room A (highest count = 3) with +1 for Room B.
	roomText := page.MustEval(`() => document.getElementById('topbar-unread-room').textContent`).Str()
	assert.Equal(t, "# Room A", roomText, "should show room with highest unread count")

	countText := page.MustEval(`() => document.getElementById('topbar-unread-count').textContent`).Str()
	assert.Equal(t, "3", countText, "should show count of 3")

	moreText := page.MustEval(`() => document.getElementById('topbar-unread-more').textContent`).Str()
	assert.Equal(t, "+1", moreText, "should show +1 for one additional unread room")
}

// TestTopbarUnread_ClearsOnBadgeClick verifies that the indicator updates when
// a sidebar badge is cleared by clicking a room link.
func TestTopbarUnread_ClearsOnBadgeClick(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}

	const (
		viewedRoom = "topbar-clear-viewed"
		otherRoom  = "topbar-clear-other"
	)

	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: viewedRoom, Name: "Current"})
	ts.SeedRoom(t, model.Room{ID: otherRoom, Name: "Unread"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, viewedRoom, alice.ID)
	ts.GrantAccess(t, viewedRoom, bob.ID)
	ts.GrantAccess(t, otherRoom, alice.ID)
	ts.GrantAccess(t, otherRoom, bob.ID)

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, viewedRoom)

	require.NoError(t, proto.EmulationSetDeviceMetricsOverride{
		Width:             375,
		Height:            812,
		DeviceScaleFactor: 1,
		Mobile:            true,
	}.Call(page))

	time.Sleep(500 * time.Millisecond)

	// Trigger an unread event.
	postMessage(t, ts, bob, otherRoom, "hi")
	time.Sleep(800 * time.Millisecond)

	// Indicator should be visible.
	hidden := page.MustEval(`() => document.getElementById('topbar-unread').hidden`).Bool()
	assert.False(t, hidden, "topbar-unread should be visible")

	// Click the sidebar link for the other room — this clears the badge.
	// We prevent actual navigation to stay on the same page for assertion.
	page.MustEval(`() => {
		const link = document.querySelector('.room-sidebar__link[data-room-id="topbar-clear-other"]');
		link.addEventListener('click', e => e.preventDefault(), { once: true });
		link.click();
	}`)
	time.Sleep(200 * time.Millisecond)

	// Indicator should now be hidden.
	hidden = page.MustEval(`() => document.getElementById('topbar-unread').hidden`).Bool()
	assert.True(t, hidden, "topbar-unread should be hidden after clearing badge")
}

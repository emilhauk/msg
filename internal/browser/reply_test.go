//go:build !short

package browser_test

import (
	"context"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/go-rod/rod"
	"github.com/stretchr/testify/require"
)

func isVisible(t *testing.T, page *rod.Page, sel string) bool {
	t.Helper()
	return page.MustEval(`(sel) => { const el = document.querySelector(sel); return !!el && el.offsetParent !== null; }`, sel).Bool()
}

// TestReply_StripHiddenUntilReplyAndClearsOnCancel guards against CSS
// display rules defeating the hidden attribute on the reply strip.
func TestReply_StripHiddenUntilReplyAndClearsOnCancel(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Browser Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	msg := seedMessageAt(t, ts, alice, roomID, "quote me", time.Now())

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomID)
	page.Timeout(5 * time.Second).MustElement("#msg-" + msg.ID)

	require.False(t, isVisible(t, page, "#reply-strip"), "strip visible before any reply")

	page.MustEval(`(id) => window.__startReply(id)`, msg.ID)
	require.True(t, isVisible(t, page, "#reply-strip"), "strip hidden after starting reply")
	require.Equal(t, msg.ID, page.MustElement("#reply-to-input").MustProperty("value").String())

	page.MustElement("[data-reply-cancel]").MustClick()
	require.False(t, isVisible(t, page, "#reply-strip"), "strip visible after cancel")
	require.Equal(t, "", page.MustElement("#reply-to-input").MustProperty("value").String())
}

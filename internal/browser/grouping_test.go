//go:build !short

package browser_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/stretchr/testify/require"
)

// seedMessageAt inserts a message with a specific timestamp.
func seedMessageAt(t *testing.T, ts *testutil.TestServer, user model.User, room, text string, at time.Time) model.Message {
	t.Helper()
	ms := at.UnixMilli()
	msgID := fmt.Sprintf("%d-%s", ms, user.ID)
	msg := model.Message{
		ID:          msgID,
		RoomID:      room,
		UserID:      user.ID,
		Text:        text,
		CreatedAt:   at,
		CreatedAtMS: fmt.Sprintf("%d", ms),
	}
	require.NoError(t, ts.Redis.SaveMessage(context.Background(), msg))
	return msg
}

// TestGrouping_ConsecutiveSameAuthor verifies that consecutive messages from
// the same author within 5 minutes get the message--continuation class.
func TestGrouping_ConsecutiveSameAuthor(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Browser Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	now := time.Now()
	msg1 := seedMessageAt(t, ts, alice, roomID, "first message", now)
	msg2 := seedMessageAt(t, ts, alice, roomID, "second message", now.Add(30*time.Second))
	msg3 := seedMessageAt(t, ts, alice, roomID, "third message", now.Add(60*time.Second))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomID)

	// First message: NOT a continuation (no predecessor)
	el1 := page.Timeout(5 * time.Second).MustElement("#msg-" + msg1.ID)
	cls1, err := el1.Attribute("class")
	require.NoError(t, err)
	require.NotContains(t, *cls1, "message--continuation", "first message should not be a continuation")

	// Second and third: ARE continuations
	el2 := page.MustElement("#msg-" + msg2.ID)
	cls2, err := el2.Attribute("class")
	require.NoError(t, err)
	require.Contains(t, *cls2, "message--continuation", "second message should be a continuation")

	el3 := page.MustElement("#msg-" + msg3.ID)
	cls3, err := el3.Attribute("class")
	require.NoError(t, err)
	require.Contains(t, *cls3, "message--continuation", "third message should be a continuation")
}

// TestGrouping_TimeGapBreaksGroup verifies that a >5 minute gap between
// messages from the same author breaks the group.
func TestGrouping_TimeGapBreaksGroup(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Browser Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	now := time.Now()
	msg1 := seedMessageAt(t, ts, alice, roomID, "before gap", now)
	msg2 := seedMessageAt(t, ts, alice, roomID, "after gap", now.Add(6*time.Minute))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomID)

	el1 := page.Timeout(5 * time.Second).MustElement("#msg-" + msg1.ID)
	cls1, err := el1.Attribute("class")
	require.NoError(t, err)
	require.NotContains(t, *cls1, "message--continuation")

	el2 := page.MustElement("#msg-" + msg2.ID)
	cls2, err := el2.Attribute("class")
	require.NoError(t, err)
	require.NotContains(t, *cls2, "message--continuation", "message after >5min gap should not be a continuation")
}

// TestGrouping_DifferentAuthorBreaksGroup verifies that a message from a
// different author breaks the grouping.
func TestGrouping_DifferentAuthorBreaksGroup(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Browser Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, roomID, bob.ID)

	now := time.Now()
	seedMessageAt(t, ts, alice, roomID, "alice says hi", now)
	msg2 := seedMessageAt(t, ts, bob, roomID, "bob says hi", now.Add(10*time.Second))
	msg3 := seedMessageAt(t, ts, alice, roomID, "alice again", now.Add(20*time.Second))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomID)

	el2 := page.Timeout(5 * time.Second).MustElement("#msg-" + msg2.ID)
	cls2, err := el2.Attribute("class")
	require.NoError(t, err)
	require.NotContains(t, *cls2, "message--continuation", "different author should not be a continuation")

	el3 := page.MustElement("#msg-" + msg3.ID)
	cls3, err := el3.Attribute("class")
	require.NoError(t, err)
	require.NotContains(t, *cls3, "message--continuation", "author change should break grouping")
}

// TestGrouping_SSEInsert verifies that a new SSE message from the same author
// gets the continuation class applied.
func TestGrouping_SSEInsert(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Browser Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	// Seed one message so there's something to group with.
	seedMessageAt(t, ts, alice, roomID, "initial message", time.Now())

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomID)

	// Wait for SSE to connect.
	time.Sleep(300 * time.Millisecond)

	// Post a second message via API → delivered via SSE.
	postMessage(t, ts, alice, roomID, "sse follow-up")

	// Wait for a second article to appear.
	page.Timeout(5 * time.Second).MustEval(`() => {
		return new Promise((resolve) => {
			const check = () => {
				const articles = document.querySelectorAll('#message-list-content > article.message');
				if (articles.length >= 2) return resolve(true);
				setTimeout(check, 50);
			};
			check();
		});
	}`)

	// The second article should be a continuation.
	articles := page.MustElements("#message-list-content > article.message")
	require.GreaterOrEqual(t, len(articles), 2)
	last := articles[len(articles)-1]
	cls, err := last.Attribute("class")
	require.NoError(t, err)
	require.Contains(t, *cls, "message--continuation", "SSE-inserted message from same author should be a continuation")
}

// TestGrouping_DeleteRegroups verifies that deleting a message causes the
// next message to be re-evaluated for grouping.
func TestGrouping_DeleteRegroups(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Browser Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, roomID, bob.ID)

	now := time.Now()
	msg1 := seedMessageAt(t, ts, alice, roomID, "alice first", now)
	msg2 := seedMessageAt(t, ts, bob, roomID, "bob middle", now.Add(10*time.Second))
	msg3 := seedMessageAt(t, ts, alice, roomID, "alice third", now.Add(20*time.Second))

	b := newBrowser(t)
	// Log in as bob so we can delete bob's message.
	page := authPage(t, b, ts, bob, roomID)

	// Verify msg3 is NOT a continuation (different author before it).
	el3 := page.Timeout(5 * time.Second).MustElement("#msg-" + msg3.ID)
	cls3, err := el3.Attribute("class")
	require.NoError(t, err)
	require.NotContains(t, *cls3, "message--continuation", "msg3 should not be continuation before delete")

	// Wait for SSE.
	time.Sleep(300 * time.Millisecond)

	// Delete bob's middle message via API.
	cookie := ts.AuthCookie(t, bob)
	req, err := http.NewRequest("DELETE",
		ts.Server.URL+"/rooms/"+roomID+"/messages/"+msg2.ID, nil)
	require.NoError(t, err)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Wait for delete SSE event and re-grouping.
	page.Timeout(5 * time.Second).MustEval(`(msgId) => {
		return new Promise((resolve) => {
			const check = () => {
				if (!document.getElementById('msg-' + msgId)) return resolve(true);
				setTimeout(check, 50);
			};
			check();
		});
	}`, msg2.ID)

	// Now msg3 should be a continuation of msg1 (same author, <5min).
	el3After := page.MustElement("#msg-" + msg3.ID)
	cls3After, err := el3After.Attribute("class")
	require.NoError(t, err)

	// Also verify msg1 is still not a continuation.
	el1 := page.MustElement("#msg-" + msg1.ID)
	cls1, err := el1.Attribute("class")
	require.NoError(t, err)
	require.NotContains(t, *cls1, "message--continuation", "first message should never be a continuation")
	require.Contains(t, *cls3After, "message--continuation", "msg3 should become continuation after middle message deleted")
}

package redis_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/emilhauk/chat/internal/model"
	redisclient "github.com/emilhauk/chat/internal/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newClient(t *testing.T) (*redisclient.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc, err := redisclient.New("redis://" + mr.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { rc.Close() })
	return rc, mr
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

func TestSetGetSession_RoundTrip(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()

	user := model.User{ID: "u1", Name: "Alice", Email: "alice@example.com", AvatarURL: "https://example.com/a.png"}
	require.NoError(t, rc.SetSession(ctx, "token1", user))

	got, err := rc.GetSession(ctx, "token1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, user.ID, got.ID)
	assert.Equal(t, user.Name, got.Name)
	assert.Equal(t, user.AvatarURL, got.AvatarURL)
}

func TestGetSession_Missing(t *testing.T) {
	rc, _ := newClient(t)
	got, err := rc.GetSession(context.Background(), "nonexistent-token")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSessionTTL_FastForward(t *testing.T) {
	rc, mr := newClient(t)
	ctx := context.Background()

	user := model.User{ID: "u2", Name: "Bob"}
	require.NoError(t, rc.SetSession(ctx, "token2", user))

	// Advance time past the 90-day session TTL.
	mr.FastForward(91 * 24 * time.Hour)

	got, err := rc.GetSession(ctx, "token2")
	require.NoError(t, err)
	assert.Nil(t, got, "session should have expired after 91 days")
}

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

func TestMessagePagination(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()
	roomID := "test-room"

	// Seed 5 messages with increasing timestamps.
	base := time.Now().UnixMilli()
	var msgs []model.Message
	for i := 0; i < 5; i++ {
		ms := base + int64(i*1000)
		msg := model.Message{
			ID:          fmt.Sprintf("%d-user1", ms),
			RoomID:      roomID,
			UserID:      "user1",
			Text:        fmt.Sprintf("msg %d", i),
			CreatedAt:   time.UnixMilli(ms),
			CreatedAtMS: fmt.Sprintf("%d", ms),
		}
		require.NoError(t, rc.SaveMessage(ctx, msg))
		msgs = append(msgs, msg)
	}

	t.Run("GetLatestMessages", func(t *testing.T) {
		got, err := rc.GetLatestMessages(ctx, roomID, 10)
		require.NoError(t, err)
		assert.Len(t, got, 5)
		// Should be oldest-first (ascending).
		assert.Equal(t, msgs[0].ID, got[0].ID)
		assert.Equal(t, msgs[4].ID, got[4].ID)
	})

	t.Run("GetMessagesBefore", func(t *testing.T) {
		// Before msg[3]: should return msgs[0..2].
		got, err := rc.GetMessagesBefore(ctx, roomID, msgs[3].CreatedAt.UnixMilli(), 10)
		require.NoError(t, err)
		assert.Len(t, got, 3)
		assert.Equal(t, msgs[0].ID, got[0].ID)
		assert.Equal(t, msgs[2].ID, got[2].ID)
	})

	t.Run("GetMessagesAfter", func(t *testing.T) {
		// After msg[1]: should return msgs[2..4].
		got, err := rc.GetMessagesAfter(ctx, roomID, msgs[1].CreatedAt.UnixMilli(), 10)
		require.NoError(t, err)
		assert.Len(t, got, 3)
		assert.Equal(t, msgs[2].ID, got[0].ID)
		assert.Equal(t, msgs[4].ID, got[2].ID)
	})

	t.Run("GetLatestMessages_Limit", func(t *testing.T) {
		got, err := rc.GetLatestMessages(ctx, roomID, 2)
		require.NoError(t, err)
		assert.Len(t, got, 2)
		// Should return the two newest messages.
		assert.Equal(t, msgs[3].ID, got[0].ID)
		assert.Equal(t, msgs[4].ID, got[1].ID)
	})
}

// ---------------------------------------------------------------------------
// Reactions
// ---------------------------------------------------------------------------

func TestReactionToggle(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()

	roomID := "rr"
	ms := time.Now().UnixMilli()
	msg := model.Message{
		ID:          fmt.Sprintf("%d-user1", ms),
		RoomID:      roomID,
		UserID:      "user1",
		Text:        "react!",
		CreatedAt:   time.UnixMilli(ms),
		CreatedAtMS: fmt.Sprintf("%d", ms),
	}
	require.NoError(t, rc.SaveMessage(ctx, msg))

	t.Run("add reaction", func(t *testing.T) {
		counts, err := rc.ToggleReaction(ctx, msg.ID, "👍", "user1")
		require.NoError(t, err)
		assert.Equal(t, 1, counts["👍"])
	})

	t.Run("second user adds same reaction", func(t *testing.T) {
		counts, err := rc.ToggleReaction(ctx, msg.ID, "👍", "user2")
		require.NoError(t, err)
		assert.Equal(t, 2, counts["👍"])
	})

	t.Run("first user removes reaction", func(t *testing.T) {
		counts, err := rc.ToggleReaction(ctx, msg.ID, "👍", "user1")
		require.NoError(t, err)
		assert.Equal(t, 1, counts["👍"])
	})

	t.Run("last user removes reaction - count drops to zero", func(t *testing.T) {
		counts, err := rc.ToggleReaction(ctx, msg.ID, "👍", "user2")
		require.NoError(t, err)
		// Count of 0 should be omitted from the map.
		_, exists := counts["👍"]
		assert.False(t, exists, "emoji key should be removed when count reaches 0")
	})
}

// ---------------------------------------------------------------------------
// Push subscriptions
// ---------------------------------------------------------------------------

func TestPushSubscription_CRUD(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()
	userID := "push-user"
	endpoint := "https://push.example.com/sub/abc123"
	subJSON := `{"endpoint":"` + endpoint + `","keys":{"p256dh":"abc","auth":"def"}}`

	// Save subscription.
	require.NoError(t, rc.SavePushSubscription(ctx, userID, endpoint, subJSON))

	// GetAll should have it.
	subs, err := rc.GetAllPushSubscriptions(ctx, userID)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	assert.Equal(t, subJSON, subs[endpoint])

	// Delete subscription.
	require.NoError(t, rc.DeletePushSubscription(ctx, userID, endpoint))

	// GetAll should now be empty.
	subs, err = rc.GetAllPushSubscriptions(ctx, userID)
	require.NoError(t, err)
	assert.Empty(t, subs)
}

// ---------------------------------------------------------------------------
// Mute / DND
// ---------------------------------------------------------------------------

func TestMute_Timed(t *testing.T) {
	rc, mr := newClient(t)
	ctx := context.Background()
	userID := "mute-timed"

	require.NoError(t, rc.SetMute(ctx, userID, time.Hour))

	muted, err := rc.IsMuted(ctx, userID)
	require.NoError(t, err)
	assert.True(t, muted, "should be muted immediately after SetMute")

	// Advance time past the 1-hour TTL; key expires in miniredis.
	mr.FastForward(2 * time.Hour)

	muted, err = rc.IsMuted(ctx, userID)
	require.NoError(t, err)
	assert.False(t, muted, "should not be muted after TTL expires")
}

func TestMute_Forever(t *testing.T) {
	rc, mr := newClient(t)
	ctx := context.Background()
	userID := "mute-forever"

	require.NoError(t, rc.SetMute(ctx, userID, 0)) // 0 = indefinite

	muted, err := rc.IsMuted(ctx, userID)
	require.NoError(t, err)
	assert.True(t, muted)

	// Advancing time must not expire an indefinite mute.
	mr.FastForward(1000 * time.Hour)

	muted, err = rc.IsMuted(ctx, userID)
	require.NoError(t, err)
	assert.True(t, muted, "indefinite mute should persist after FastForward")
}

func TestMute_Clear(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()
	userID := "mute-clear"

	require.NoError(t, rc.SetMute(ctx, userID, time.Hour))
	require.NoError(t, rc.ClearMute(ctx, userID))

	muted, err := rc.IsMuted(ctx, userID)
	require.NoError(t, err)
	assert.False(t, muted, "mute should be cleared")
}

func TestGetMuteUntil_NotMuted(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()

	until, isMuted, err := rc.GetMuteUntil(ctx, "no-mute-user")
	require.NoError(t, err)
	assert.False(t, isMuted)
	assert.True(t, until.IsZero())
}

func TestGetMuteUntil_Forever(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()
	userID := "mute-until-forever"

	require.NoError(t, rc.SetMute(ctx, userID, 0))

	until, isMuted, err := rc.GetMuteUntil(ctx, userID)
	require.NoError(t, err)
	assert.True(t, isMuted)
	assert.Equal(t, 9999, until.Year(), "sentinel year for indefinite mute should be 9999")
}

func TestGetMuteUntil_Timed(t *testing.T) {
	rc, mr := newClient(t)
	ctx := context.Background()
	userID := "mute-until-timed"

	require.NoError(t, rc.SetMute(ctx, userID, time.Hour))

	until, isMuted, err := rc.GetMuteUntil(ctx, userID)
	require.NoError(t, err)
	assert.True(t, isMuted)
	assert.False(t, until.IsZero(), "until should be a non-zero time")
	assert.True(t, until.After(time.Now()), "until should be in the future")

	// After TTL expiry the key is gone — GetMuteUntil should report not muted.
	mr.FastForward(2 * time.Hour)

	_, isMuted, err = rc.GetMuteUntil(ctx, userID)
	require.NoError(t, err)
	assert.False(t, isMuted, "should not be muted after TTL expires")
}

func TestReactionToggle_ReactedByMe(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()

	ms := time.Now().UnixMilli()
	msg := model.Message{
		ID:          fmt.Sprintf("%d-u", ms),
		RoomID:      "rm",
		UserID:      "u",
		Text:        "x",
		CreatedAt:   time.UnixMilli(ms),
		CreatedAtMS: fmt.Sprintf("%d", ms),
	}
	require.NoError(t, rc.SaveMessage(ctx, msg))

	_, err := rc.ToggleReaction(ctx, msg.ID, "❤️", "user1")
	require.NoError(t, err)

	reactions, err := rc.GetReactions(ctx, msg.ID, "user1")
	require.NoError(t, err)
	require.Len(t, reactions, 1)
	assert.True(t, reactions[0].ReactedByMe, "user1 should see ReactedByMe=true")

	reactions2, err := rc.GetReactions(ctx, msg.ID, "user2")
	require.NoError(t, err)
	require.Len(t, reactions2, 1)
	assert.False(t, reactions2[0].ReactedByMe, "user2 should see ReactedByMe=false")
}

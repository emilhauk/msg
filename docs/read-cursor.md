# Read Cursor & Unread Tracking

## Concept

Count a message as "seen" when a browser tab/PWA that is **visible** (not just focused) has drawn the message in the viewport.

### Use cases

1. **Unread count in titlebar** — show `(3) msg` or a dot indicator for unread messages
2. **Push suppression** — if the user is actively viewing the room on one device, don't send a push notification to their other devices

---

## Design notes

### "Focus" vs. "Visibility"

Use `document.visibilityState === 'visible'` (Page Visibility API), not `document.hasFocus()`.

- A tab can be **visible** (on screen, user is looking at it) without having keyboard focus (e.g. user is reading but typing in a terminal elsewhere).
- `hasFocus()` would miss this common case.

### "Drawn" vs. "In viewport"

A message being rendered in the DOM does not mean the user has seen it — they may have scrolled past it. Use the **Intersection Observer API** to mark a message as seen only when it actually enters the viewport while the tab is visible.

### Persistence

The read cursor must survive page reloads and device switches — it must be stored in Redis.

Proposed key: `users:{uuid}:rooms:{roomId}:last_read` = unix ms timestamp of last seen message.

### Multi-tab sync

Two tabs on the same browser can get out of sync. **BroadcastChannel API** can propagate cursor updates to sibling tabs cheaply. Cross-device sync goes through Redis.

---

## Unified mechanism: `last_active` per room

Both use cases are served by a single `last_active` timestamp, scoped per user per room.

**Redis key:** `users:{uuid}:rooms:{roomId}:last_active` = unix ms timestamp

### Push suppression

Skip push if `last_active` for the target room is within the last N minutes (e.g. 5 min). More accurate than checking SSE connection presence, which can stay open for hours after the user has walked away.

### Unread count / read cursor

Messages with `created_at > last_active` = unread. This is the read cursor.

Precision trade-off: if the user is in the room but scrolled up reading history while new messages arrive below the fold, those messages are implicitly marked read. Acceptable for a chat hint — this is not a legal audit trail.

### When to update `last_active`

Client signals to server via a lightweight POST/PUT:
- Tab becomes visible (`visibilitychange` → visible)
- User sends a message (already implies presence)
- Optionally: periodic heartbeat while tab is visible (keep TTL alive)

### Why not SSE connection presence alone

SSE connections stay open indefinitely — no server-side idle timeout, no keepalive heartbeat. A tab left open overnight still has a live SSE connection. `last_active` with a recency threshold is a much more accurate signal.

---

## Implementation status

### Push suppression — **implemented**

- **Redis key:** `users:{uuid}:rooms:{roomId}:last_active` (String, unix ms; TTL 30 days)
- **Suppression threshold:** 2 minutes — push skipped if `now - last_active < 2 min`
- **Heartbeat interval:** 60 seconds while tab is visible
- **Server endpoint:** `POST /rooms/{id}/active` → 204 (auth required)
- **Client signals:** page load (if visible), `visibilitychange` → visible, every 60 s
- **Message send:** also updates `last_active` for the sender (fire-and-forget)

### Unread count / read cursor — **not yet implemented**

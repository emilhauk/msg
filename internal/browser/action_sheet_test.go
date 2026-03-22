//go:build !short

package browser_test

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const actionSheetRoom = "room-action-sheet"

// touchPage creates a page that emulates a touch device (hover: none,
// maxTouchPoints > 0) so that action-sheet.js activates. The emulation
// must be set before navigation so the module-level guard runs under
// the emulated conditions.
func touchPage(t *testing.T, b *rod.Browser, ts *testutil.TestServer, user model.User, room string) *rod.Page {
	t.Helper()
	ts.GrantAccess(t, room, user.ID)

	parsed, err := url.Parse(ts.Server.URL)
	require.NoError(t, err)

	page := b.MustPage("")

	// Emulate hover: none so matchMedia('(hover: none)') matches.
	require.NoError(t, proto.EmulationSetEmulatedMedia{
		Features: []*proto.EmulationMediaFeature{
			{Name: "hover", Value: "none"},
		},
	}.Call(page))

	// Emulate mobile device with touch support.
	require.NoError(t, proto.EmulationSetDeviceMetricsOverride{
		Width:             375,
		Height:            812,
		DeviceScaleFactor: 2,
		Mobile:            true,
	}.Call(page))
	maxTouch := 5
	require.NoError(t, proto.EmulationSetTouchEmulationEnabled{
		Enabled:        true,
		MaxTouchPoints: &maxTouch,
	}.Call(page))

	// Set cookie and navigate.
	cookie := ts.AuthCookie(t, user)
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

// isVisibleInSheet checks that a CSS selector inside the action sheet
// preview is rendered, has non-zero size, is not hidden by CSS, and is
// not clipped away by any ancestor's overflow. We walk up the DOM tree
// checking each overflow-clipping ancestor to ensure the element is
// actually painted on screen.
const isVisibleInSheetJS = `(selector) => {
	const el = document.querySelector(selector);
	if (!el) return { found: false, reason: 'element not found' };

	const s = getComputedStyle(el);
	if (s.display === 'none') return { found: true, visible: false, reason: 'display:none' };
	if (s.visibility === 'hidden') return { found: true, visible: false, reason: 'visibility:hidden' };

	const elRect = el.getBoundingClientRect();
	if (elRect.height === 0 || elRect.width === 0)
		return { found: true, visible: false, reason: 'zero size' };

	// Walk up ancestors and check if any overflow:hidden ancestor clips this element.
	let node = el.parentElement;
	while (node && node !== document.documentElement) {
		const ps = getComputedStyle(node);
		if (ps.overflow === 'hidden' || ps.overflowY === 'hidden') {
			const parentRect = node.getBoundingClientRect();
			// Check if the element is fully clipped.
			if (elRect.bottom <= parentRect.top || elRect.top >= parentRect.bottom) {
				return {
					found: true, visible: false,
					reason: 'clipped by ' + node.className,
					elTop: elRect.top, elBottom: elRect.bottom,
					clipTop: parentRect.top, clipBottom: parentRect.bottom,
				};
			}
		}
		node = node.parentElement;
	}

	return { found: true, visible: true, reason: 'ok' };
}`

// TestActionSheet_PreviewShowsAuthorAndText opens the action sheet on a
// continuation message and verifies that the preview inside the sheet
// shows the author avatar, author name, and message text — even though
// those are hidden in the message list due to grouping.
func TestActionSheet_PreviewShowsAuthorAndText(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}

	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: actionSheetRoom, Name: "Action Sheet Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	now := time.Now()
	// Two messages from the same author within 5 minutes — second is a continuation.
	seedMessageAt(t, ts, alice, actionSheetRoom, "first message", now)
	msg2 := seedMessageAt(t, ts, alice, actionSheetRoom, "second message", now.Add(30*time.Second))

	b := newBrowser(t)
	page := touchPage(t, b, ts, alice, actionSheetRoom)

	// Verify the second message is a continuation (author hidden in list).
	el := page.Timeout(5 * time.Second).MustElement("#msg-" + msg2.ID)
	cls, err := el.Attribute("class")
	require.NoError(t, err)
	require.Contains(t, *cls, "message--continuation",
		"second message should be a continuation in the message list")

	// Tap the continuation message to open the action sheet.
	el.MustClick()

	// The action sheet dialog should be open.
	dialogOpen := page.Timeout(3 * time.Second).MustEval(
		`() => document.getElementById('message-actions')?.open === true`,
	).Bool()
	require.True(t, dialogOpen, "action sheet dialog should be open after tap")

	// Wait for the slide-up animation to finish (250ms Web Animations API).
	time.Sleep(350 * time.Millisecond)

	// Avatar must be visible within the dialog bounds.
	avatarResult := page.MustEval(isVisibleInSheetJS, "#action-sheet-preview .avatar")
	assert.True(t, avatarResult.Get("found").Bool(), "avatar element should exist in preview")
	assert.True(t, avatarResult.Get("visible").Bool(),
		"avatar should be visible within dialog bounds, got: %s", avatarResult.Get("reason").Str())

	// Author name must be visible within the dialog bounds and show "Alice".
	authorResult := page.MustEval(isVisibleInSheetJS, "#action-sheet-preview .message__author")
	assert.True(t, authorResult.Get("found").Bool(), "author element should exist in preview")
	assert.True(t, authorResult.Get("visible").Bool(),
		"author should be visible within dialog bounds, got: %s", authorResult.Get("reason").Str())

	authorText := page.MustEval(`() => {
		const el = document.querySelector('#action-sheet-preview .message__author');
		return el ? el.textContent.trim() : '';
	}`).Str()
	assert.Equal(t, "Alice", authorText, "author name should be 'Alice'")

	// Message text must be visible within the dialog bounds and contain the body.
	textResult := page.MustEval(isVisibleInSheetJS, "#action-sheet-preview .message__text")
	assert.True(t, textResult.Get("found").Bool(), "text element should exist in preview")
	assert.True(t, textResult.Get("visible").Bool(),
		"message text should be visible within dialog bounds, got: %s", textResult.Get("reason").Str())

	textContent := page.MustEval(`() => {
		const el = document.querySelector('#action-sheet-preview .message__text');
		return el ? el.textContent.trim() : '';
	}`).Str()
	assert.Contains(t, textContent, "second message",
		"preview text should contain the message body")

	// Text color must contrast with the dialog background (i.e. be readable).
	// The <dialog> UA stylesheet sets color:black which is invisible on dark themes.
	// We find the effective background by walking up from the text element until
	// we hit an opaque background-color.
	readable := page.MustEval(`() => {
		const text = document.querySelector('#action-sheet-preview .message__text');
		if (!text) return { ok: false, reason: 'text element missing' };
		const fg = getComputedStyle(text).color;

		// Walk up to find the first ancestor with an opaque background.
		let bg = 'rgb(0, 0, 0)';
		let node = text;
		while (node) {
			const raw = getComputedStyle(node).backgroundColor;
			const parts = raw.match(/[\d.]+/g);
			if (parts) {
				const alpha = parts.length >= 4 ? parseFloat(parts[3]) : 1;
				if (alpha > 0.9) { bg = raw; break; }
			}
			node = node.parentElement;
		}

		const parse = (c) => c.match(/\d+/g).slice(0, 3).map(Number);
		const fgRGB = parse(fg);
		const bgRGB = parse(bg);
		const lum = ([r, g, b]) => {
			const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); };
			return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
		};
		const L1 = lum(fgRGB), L2 = lum(bgRGB);
		const ratio = (Math.max(L1, L2) + 0.05) / (Math.min(L1, L2) + 0.05);
		// WCAG AA requires >= 4.5 for normal text.
		return { ok: ratio >= 4.5, ratio: Math.round(ratio * 10) / 10, fg, bg };
	}`)
	assert.True(t, readable.Get("ok").Bool(),
		"text must be readable against dialog background (WCAG AA >= 4.5:1), got ratio=%v fg=%s bg=%s",
		readable.Get("ratio"), readable.Get("fg").Str(), readable.Get("bg").Str())
}

// TestActionSheet_HidesEditDeleteForOtherUser verifies that Edit and Delete
// buttons are not shown in the action sheet when tapping another user's message.
func TestActionSheet_HidesEditDeleteForOtherUser(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}

	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: actionSheetRoom, Name: "Action Sheet Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))

	// Bob's message, viewed by Alice.
	msg := seedMessage(t, ts, bob, actionSheetRoom, "bob says hello")

	b := newBrowser(t)
	page := touchPage(t, b, ts, alice, actionSheetRoom)

	el := page.Timeout(5 * time.Second).MustElement("#msg-" + msg.ID)
	el.MustClick()

	dialogOpen := page.Timeout(3 * time.Second).MustEval(
		`() => document.getElementById('message-actions')?.open === true`,
	).Bool()
	require.True(t, dialogOpen, "action sheet should open")

	// Check computed display — the [hidden] attribute must actually result in
	// display:none, not be overridden by an explicit display:flex rule.
	editDisplay := page.MustEval(`() => {
		const btn = document.querySelector('#message-actions [data-action="edit"]');
		return btn ? getComputedStyle(btn).display : 'none';
	}`).Str()
	assert.Equal(t, "none", editDisplay,
		"Edit button must be display:none for another user's message")

	deleteDisplay := page.MustEval(`() => {
		const btn = document.querySelector('#message-actions [data-action="delete"]');
		return btn ? getComputedStyle(btn).display : 'none';
	}`).Str()
	assert.Equal(t, "none", deleteDisplay,
		"Delete button must be display:none for another user's message")

	separatorDisplay := page.MustEval(`() => {
		const hr = document.querySelector('#message-actions .action-sheet__separator');
		return hr ? getComputedStyle(hr).display : 'none';
	}`).Str()
	assert.Equal(t, "none", separatorDisplay,
		"Separator must be display:none for another user's message")
}

// TestActionSheet_ShowsEditDeleteForOwnMessage verifies that Edit and Delete
// buttons ARE shown when tapping your own message.
func TestActionSheet_ShowsEditDeleteForOwnMessage(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}

	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: actionSheetRoom, Name: "Action Sheet Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	msg := seedMessage(t, ts, alice, actionSheetRoom, "my own message")

	b := newBrowser(t)
	page := touchPage(t, b, ts, alice, actionSheetRoom)

	el := page.Timeout(5 * time.Second).MustElement("#msg-" + msg.ID)
	el.MustClick()

	dialogOpen := page.Timeout(3 * time.Second).MustEval(
		`() => document.getElementById('message-actions')?.open === true`,
	).Bool()
	require.True(t, dialogOpen, "action sheet should open")

	editVisible := page.MustEval(`() => {
		const btn = document.querySelector('#message-actions [data-action="edit"]');
		return btn ? !btn.hidden : false;
	}`).Bool()
	assert.True(t, editVisible, "Edit button must be visible for own message")

	deleteVisible := page.MustEval(`() => {
		const btn = document.querySelector('#message-actions [data-action="delete"]');
		return btn ? !btn.hidden : false;
	}`).Bool()
	assert.True(t, deleteVisible, "Delete button must be visible for own message")
}

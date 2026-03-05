//go:build !short

package browser_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const lightboxRoom = "room-lightbox"

// seedMessageWithImages inserts a message with image attachments directly into
// Redis. URLs should be reachable from the browser; use imgURL() for that.
func seedMessageWithImages(t *testing.T, ts *testutil.TestServer, user model.User, room string, urls ...string) model.Message {
	t.Helper()
	attachments := make([]model.Attachment, len(urls))
	for i, u := range urls {
		attachments[i] = model.Attachment{URL: u, ContentType: "image/png", Filename: fmt.Sprintf("img%d.png", i+1)}
	}
	data, err := json.Marshal(attachments)
	require.NoError(t, err)
	ms := time.Now().UnixMilli()
	msg := model.Message{
		ID:              fmt.Sprintf("%d-%s", ms, user.ID),
		RoomID:          room,
		UserID:          user.ID,
		CreatedAt:       time.UnixMilli(ms),
		CreatedAtMS:     fmt.Sprintf("%d", ms),
		AttachmentsJSON: string(data),
	}
	require.NoError(t, ts.Redis.SaveMessage(context.Background(), msg))
	return msg
}

// imgURL returns a distinct loadable image URL for test image n by appending a
// query param to the served favicon. The browser loads it successfully and the
// src values are unique enough for assertion.
func imgURL(ts *testutil.TestServer, n int) string {
	return fmt.Sprintf("%s/static/favicon.svg?img=%d", ts.Server.URL, n)
}

// lightboxIsOpen reports whether the lightbox dialog currently has the open attribute.
func lightboxIsOpen(page *rod.Page) bool {
	return page.MustEval(`() => document.getElementById('lightbox').open`).Bool()
}

// lightboxSrc returns the current src of the lightbox image element.
func lightboxSrc(page *rod.Page) string {
	return page.MustEval(`() => document.querySelector('.lightbox__img').src`).Str()
}

// TestLightbox_OpenClose verifies that clicking an image opens the lightbox and
// the close button shuts it.
func TestLightbox_OpenClose(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: lightboxRoom, Name: "Lightbox Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessageWithImages(t, ts, alice, lightboxRoom, imgURL(ts, 1))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, lightboxRoom)
	page.Timeout(5 * time.Second).MustElement(".message__media-img").MustClick()

	assert.True(t, lightboxIsOpen(page), "lightbox should be open after clicking an image")

	page.MustElement(".lightbox__close").MustClick()
	assert.False(t, lightboxIsOpen(page), "lightbox should be closed after clicking the close button")
}

// TestLightbox_CloseEscape verifies that the Escape key closes the lightbox.
func TestLightbox_CloseEscape(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: lightboxRoom, Name: "Lightbox Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessageWithImages(t, ts, alice, lightboxRoom, imgURL(ts, 1))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, lightboxRoom)
	page.Timeout(5 * time.Second).MustElement(".message__media-img").MustClick()
	require.True(t, lightboxIsOpen(page), "lightbox must be open before Escape test")

	page.Keyboard.MustType(input.Escape)
	assert.False(t, lightboxIsOpen(page), "lightbox should be closed after pressing Escape")
}

// TestLightbox_CloseBackdrop verifies that clicking the backdrop (outside the
// image) closes the lightbox.
func TestLightbox_CloseBackdrop(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: lightboxRoom, Name: "Lightbox Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessageWithImages(t, ts, alice, lightboxRoom, imgURL(ts, 1))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, lightboxRoom)
	page.Timeout(5 * time.Second).MustElement(".message__media-img").MustClick()
	require.True(t, lightboxIsOpen(page), "lightbox must be open before backdrop test")

	// Click the top-left corner — well clear of the centred image and buttons.
	page.Mouse.MustMoveTo(5, 5)
	page.Mouse.MustClick(proto.InputMouseButtonLeft)
	assert.False(t, lightboxIsOpen(page), "lightbox should be closed after clicking the backdrop")
}

// TestLightbox_SingleImage_NoNav verifies that the prev/next buttons are hidden
// when a message contains only one image.
func TestLightbox_SingleImage_NoNav(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: lightboxRoom, Name: "Lightbox Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessageWithImages(t, ts, alice, lightboxRoom, imgURL(ts, 1))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, lightboxRoom)
	page.Timeout(5 * time.Second).MustElement(".message__media-img").MustClick()
	require.True(t, lightboxIsOpen(page), "lightbox must be open")

	prevHidden := page.MustEval(`() => document.querySelector('.lightbox__nav--prev').hidden`).Bool()
	nextHidden := page.MustEval(`() => document.querySelector('.lightbox__nav--next').hidden`).Bool()
	assert.True(t, prevHidden, "prev button should be hidden for a single image")
	assert.True(t, nextHidden, "next button should be hidden for a single image")
}

// TestLightbox_Navigation seeds a message with 3 images, clicks the second
// (not the first), verifies the correct image is shown, then navigates with
// the prev/next buttons and checks disabled states at the ends.
func TestLightbox_Navigation(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const navRoom = "room-lightbox-nav"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: navRoom, Name: "Lightbox Nav Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	url1, url2, url3 := imgURL(ts, 1), imgURL(ts, 2), imgURL(ts, 3)
	seedMessageWithImages(t, ts, alice, navRoom, url1, url2, url3)

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, navRoom)
	imgs := page.Timeout(5 * time.Second).MustElements("article.message .message__media-img")
	require.Len(t, imgs, 3, "expected 3 image elements in the message")

	// Click the second image (index 1).
	imgs[1].MustClick()
	require.True(t, lightboxIsOpen(page), "lightbox must open on image click")

	// Lightbox should show the second image's src.
	assert.Equal(t, url2, lightboxSrc(page), "lightbox should display the clicked image")

	// Prev button should be enabled (not at start); next button enabled (not at end).
	prevDisabled := page.MustEval(`() => document.querySelector('.lightbox__nav--prev').disabled`).Bool()
	nextDisabled := page.MustEval(`() => document.querySelector('.lightbox__nav--next').disabled`).Bool()
	assert.False(t, prevDisabled, "prev should be enabled when not at first image")
	assert.False(t, nextDisabled, "next should be enabled when not at last image")

	// Navigate to the first image.
	page.MustElement(".lightbox__nav--prev").MustClick()
	assert.Equal(t, url1, lightboxSrc(page), "after prev, lightbox should show first image")
	prevDisabled = page.MustEval(`() => document.querySelector('.lightbox__nav--prev').disabled`).Bool()
	assert.True(t, prevDisabled, "prev should be disabled at the first image")

	// Navigate forward to the third image.
	page.MustElement(".lightbox__nav--next").MustClick()
	page.MustElement(".lightbox__nav--next").MustClick()
	assert.Equal(t, url3, lightboxSrc(page), "after two nexts, lightbox should show third image")
	nextDisabled = page.MustEval(`() => document.querySelector('.lightbox__nav--next').disabled`).Bool()
	assert.True(t, nextDisabled, "next should be disabled at the last image")
}

// TestLightbox_KeyboardNavigation verifies ArrowLeft/ArrowRight and h/l keys
// navigate between images while the lightbox is open.
func TestLightbox_KeyboardNavigation(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const kbRoom = "room-lightbox-kb"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: kbRoom, Name: "Lightbox KB Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	url1, url2, url3 := imgURL(ts, 1), imgURL(ts, 2), imgURL(ts, 3)
	seedMessageWithImages(t, ts, alice, kbRoom, url1, url2, url3)

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, kbRoom)
	imgs := page.Timeout(5 * time.Second).MustElements("article.message .message__media-img")
	require.Len(t, imgs, 3)

	// Open on the second image.
	imgs[1].MustClick()
	require.True(t, lightboxIsOpen(page))
	require.Equal(t, url2, lightboxSrc(page), "should open on the clicked (second) image")

	// ArrowLeft → first image.
	page.Keyboard.MustType(input.ArrowLeft)
	assert.Equal(t, url1, lightboxSrc(page), "ArrowLeft should navigate to first image")

	// ArrowRight → second image.
	page.Keyboard.MustType(input.ArrowRight)
	assert.Equal(t, url2, lightboxSrc(page), "ArrowRight should navigate to second image")

	// 'l' → third image.
	page.Keyboard.MustType(input.Key('l'))
	assert.Equal(t, url3, lightboxSrc(page), "'l' should navigate to third image")

	// 'h' → second image.
	page.Keyboard.MustType(input.Key('h'))
	assert.Equal(t, url2, lightboxSrc(page), "'h' should navigate back to second image")
}

//go:build !short

package browser_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const emojiACRoom = "room-emoji-ac"

// typeQuery sets the textarea value via JavaScript and dispatches an input event,
// reliably triggering the autocomplete handler. go-rod's element.Input() inserts
// text at the cursor rather than replacing it, making it unsuitable here.
func typeQuery(page *rod.Page, text string) {
	page.MustEval(fmt.Sprintf(`() => {
		const ta = document.querySelector('.message-form__textarea');
		ta.focus();
		ta.value = %q;
		// Place cursor at end; getFragment() reads selectionStart, which defaults
		// to 0 after a programmatic value assignment, causing it to find no fragment.
		ta.selectionStart = ta.selectionEnd = ta.value.length;
		ta.dispatchEvent(new Event('input', { bubbles: true }));
	}`, text))
}

// waitForEmojiReady confirms the emoji DB and allEmojis cache are ready by
// triggering a word-search query and waiting for results. It then clears the
// textarea and waits briefly for the getEmojiByGroup calls (fired in parallel
// with waitForDb) to complete.
func waitForEmojiReady(t *testing.T, page *rod.Page) {
	t.Helper()
	// Retry typeQuery in a loop until the autocomplete dropdown appears.
	// The emoji DB loads asynchronously from IndexedDB/CDN; the waitForDb IIFE
	// in room.js polls for window.__EmojiDatabase every 50 ms, so there is a
	// race between the module script setting it and the poll firing to wire up
	// the event listeners. Retrying is more robust than a one-shot approach.
	deadline := time.Now().Add(30 * time.Second)
	for {
		typeQuery(page, ":smile")
		time.Sleep(500 * time.Millisecond)
		hidden := page.MustEval(`() => document.getElementById('emoji-autocomplete').hidden`).Bool()
		if !hidden {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("emoji autocomplete never became ready after 30 s")
		}
		typeQuery(page, "")
		time.Sleep(200 * time.Millisecond)
	}
	typeQuery(page, "")
	// allEmojis is populated by getEmojiByGroup calls that fire alongside waitForDb.
	// 500 ms is generous for IndexedDB reads that are already in-flight.
	time.Sleep(500 * time.Millisecond)
}

// dropdownNames returns the :name: label text of all visible autocomplete items.
func dropdownNames(page *rod.Page) []string {
	arr := page.MustEval(`() => Array.from(
		document.querySelectorAll('.emoji-autocomplete__name')
	).map(el => el.textContent.trim())`).Arr()
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		out = append(out, v.Str())
	}
	return out
}

// dropdownGlyphs returns the unicode strings of all visible autocomplete items.
func dropdownGlyphs(page *rod.Page) []string {
	arr := page.MustEval(`() => Array.from(
		document.querySelectorAll('.emoji-autocomplete__item')
	).map(el => el.dataset.unicode)`).Arr()
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		out = append(out, v.Str())
	}
	return out
}

// clearDropdown resets the textarea and ensures the dropdown is closed.
func clearDropdown(page *rod.Page) {
	typeQuery(page, "")
	time.Sleep(50 * time.Millisecond)
}

// TestEmojiAutocomplete runs all emoji autocomplete sub-tests against a single
// shared browser page. This avoids repeated CDN fetches for the emoji dataset
// (each new Chromium profile must re-download it from jsDelivr) which causes
// intermittent timeouts when tests spin up many browsers in parallel.
func TestEmojiAutocomplete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}

	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: emojiACRoom, Name: "Emoji AC"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, emojiACRoom)
	waitForEmojiReady(t, page)

	// WordSearch: ordinary word-prefix search still works after the fuzzy-merge change.
	t.Run("WordSearch", func(t *testing.T) {
		defer clearDropdown(page)
		typeQuery(page, ":smile")
		page.Timeout(3 * time.Second).MustElement(".emoji-autocomplete__item")

		names := dropdownNames(page)
		assert.NotEmpty(t, names, ":smile should show autocomplete results")
		hidden := page.MustEval(`() => document.getElementById('emoji-autocomplete').hidden`).Bool()
		assert.False(t, hidden, "dropdown should be visible for :smile")
	})

	// FuzzyOnly: a query with no word-prefix match still surfaces results via
	// fuzzy (subsequence) matching. ":arup" — no shortcode word starts with "arup".
	t.Run("FuzzyOnly", func(t *testing.T) {
		defer clearDropdown(page)
		typeQuery(page, ":arup")
		page.Timeout(3 * time.Second).MustElement(".emoji-autocomplete__item")

		names := dropdownNames(page)
		require.NotEmpty(t, names, "fuzzy :arup should show results")
		found := false
		for _, n := range names {
			if strings.Contains(n, "arrow_up") || strings.Contains(n, "up_arrow") {
				found = true
				break
			}
		}
		assert.True(t, found, "fuzzy :arup must match an arrow_up emoji; got: %v", names)
	})

	// FuzzyThumbsUp: ":thup" must match 👍. The primary shortcode in emojibase is
	// "+1", which doesn't contain t/h/u/p — fuzzySearch must also check secondary
	// shortcodes and the annotation ("thumbs up") to find the match.
	t.Run("FuzzyThumbsUp", func(t *testing.T) {
		defer clearDropdown(page)
		typeQuery(page, ":thup")
		page.Timeout(3 * time.Second).MustElement(".emoji-autocomplete__item")

		glyphs := dropdownGlyphs(page)
		require.NotEmpty(t, glyphs, "fuzzy :thup should show results")
		// The DB stores "👍️" (with U+FE0F variation selector), so check with HasPrefix
		// rather than exact match to handle either representation.
		found := false
		for _, g := range glyphs {
			if strings.HasPrefix(g, "👍") {
				found = true
				break
			}
		}
		assert.True(t, found, "fuzzy :thup must include 👍 (thumbs up); got: %v", glyphs)
	})

	// FuzzyHeart: ":hrt" matches the heart emoji via subsequence matching.
	// "hrt" is a subsequence of "heart" (h→e→a→r→t hitting h,r,t).
	// "heart" is the universal shortcode for ❤️ across all emoji databases.
	t.Run("FuzzyHeart", func(t *testing.T) {
		defer clearDropdown(page)
		typeQuery(page, ":hrt")
		page.Timeout(3 * time.Second).MustElement(".emoji-autocomplete__item")

		names := dropdownNames(page)
		require.NotEmpty(t, names, "fuzzy :hrt should show results")
		found := false
		for _, n := range names {
			if strings.Contains(n, "heart") {
				found = true
				break
			}
		}
		assert.True(t, found, "fuzzy :hrt must match a heart emoji; got: %v", names)
	})

	// FuzzyFlag: ":flno" must match both :flamingo: and :flag_no:. Country flags
	// live in emoji group 9; this catches a regression where only groups 0–8 were
	// loaded, silently dropping all flag emoji from fuzzy results.
	t.Run("FuzzyFlag", func(t *testing.T) {
		defer clearDropdown(page)
		typeQuery(page, ":flno")
		page.Timeout(3 * time.Second).MustElement(".emoji-autocomplete__item")

		glyphs := dropdownGlyphs(page)
		require.NotEmpty(t, glyphs, "fuzzy :flno should show results")
		assert.Contains(t, glyphs, "🦩", "fuzzy :flno must include flamingo 🦩; got: %v", glyphs)
		assert.Contains(t, glyphs, "🇳🇴", "fuzzy :flno must include Norwegian flag 🇳🇴 (flag_no); got: %v", glyphs)
	})

	// MinLength: a single character after ":" must not trigger the dropdown.
	t.Run("MinLength", func(t *testing.T) {
		defer clearDropdown(page)
		typeQuery(page, ":s")
		time.Sleep(300 * time.Millisecond)

		hidden := page.MustEval(`() => document.getElementById('emoji-autocomplete').hidden`).Bool()
		assert.True(t, hidden, "dropdown must not appear for a single-char query (:s)")
	})

	// Escape: closes the dropdown without modifying the textarea.
	t.Run("Escape", func(t *testing.T) {
		defer clearDropdown(page)
		typeQuery(page, ":smile")
		page.Timeout(3 * time.Second).MustElement(".emoji-autocomplete__item")

		page.Keyboard.MustType(input.Escape)

		hidden := page.MustEval(`() => document.getElementById('emoji-autocomplete').hidden`).Bool()
		assert.True(t, hidden, "Escape must close the autocomplete dropdown")

		val := page.MustEval(`() => document.querySelector('.message-form__textarea').value`).Str()
		assert.Equal(t, ":smile", val, "Escape must not modify the textarea content")
	})

	// TabInsertsEmoji: Tab selects the first item and replaces the :fragment.
	t.Run("TabInsertsEmoji", func(t *testing.T) {
		defer clearDropdown(page)
		typeQuery(page, ":smile")
		page.Timeout(3 * time.Second).MustElement(".emoji-autocomplete__item")

		page.Keyboard.MustType(input.Tab)

		val := page.MustEval(`() => document.querySelector('.message-form__textarea').value`).Str()
		assert.NotContains(t, val, ":", "Tab must replace :smile with the emoji glyph (no colon left)")
		assert.NotEmpty(t, val, "textarea must contain the inserted emoji")

		hidden := page.MustEval(`() => document.getElementById('emoji-autocomplete').hidden`).Bool()
		assert.True(t, hidden, "dropdown must close after Tab selection")
	})

	// ArrowKeys: keyboard navigation highlights items in order.
	t.Run("ArrowKeys", func(t *testing.T) {
		defer clearDropdown(page)
		typeQuery(page, ":smile")
		page.Timeout(3 * time.Second).MustElement(".emoji-autocomplete__item")

		selectedIdx := func() int {
			return int(page.MustEval(`() => {
				const items = document.querySelectorAll('.emoji-autocomplete__item');
				return Array.from(items).findIndex(el => el.getAttribute('aria-selected') === 'true');
			}`).Int())
		}

		assert.Equal(t, -1, selectedIdx(), "no item should be highlighted before ArrowDown")

		page.Keyboard.MustType(input.ArrowDown)
		assert.Equal(t, 0, selectedIdx(), "ArrowDown should highlight the first item")

		page.Keyboard.MustType(input.ArrowDown)
		assert.Equal(t, 1, selectedIdx(), "second ArrowDown should highlight the second item")
	})
}

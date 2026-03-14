// User-level SSE: listens on /user/events for cross-room notifications
// (unread badge increments). Follows the same lifecycle as room/sse.js.

function syncTopbarUnread() {
  const el = document.getElementById('topbar-unread');
  if (!el) return;

  const badges = document.querySelectorAll('.room-sidebar__badge:not([hidden])');
  if (!badges.length) {
    el.hidden = true;
    return;
  }

  // Find the badge with the highest count and collect totals.
  let maxCount = 0;
  let maxName = '';
  let maxHref = '#';
  badges.forEach((badge) => {
    const text = badge.textContent.trim();
    const count = text === '99+' ? 100 : (parseInt(text, 10) || 0);
    if (count > maxCount) {
      maxCount = count;
      const link = badge.closest('.room-sidebar__link');
      const nameEl = link?.querySelector('.room-sidebar__name');
      maxName = nameEl ? nameEl.textContent.trim() : '';
      maxHref = link ? link.href : '#';
    }
  });

  const hint = document.getElementById('update-hint');

  if (maxCount <= 0) {
    el.hidden = true;
    return;
  }

  document.getElementById('topbar-unread-room').textContent = maxName;
  document.getElementById('topbar-unread-count').textContent = maxCount > 99 ? '99+' : String(maxCount);
  el.href = maxHref;

  const otherCount = badges.length - 1;
  const moreEl = document.getElementById('topbar-unread-more');
  moreEl.textContent = otherCount > 0 ? `+${otherCount}` : '';

  // Suppress the update-available hint while the unread indicator is shown.
  if (hint) hint.hidden = true;
  el.hidden = false;
}

function attachUserListeners(source) {
  source.addEventListener('unread', (e) => {
    let data;
    try { data = JSON.parse(e.data); } catch (_) { return; }
    const rid = data.roomId;
    if (!rid || rid === window.roomID) return;

    const badge = document.getElementById(`unread-badge-${rid}`);
    if (!badge) return;

    const current = parseInt(badge.textContent, 10) || 0;
    const next = current + 1;
    badge.textContent = next > 99 ? '99+' : String(next);
    badge.hidden = false;

    syncTopbarUnread();
  });
}

function openUserEs() {
  if (userEs) userEs.close();
  userEs = new EventSource('/user/events');
  attachUserListeners(userEs);
}

let userEs = null;
if (window.roomID) openUserEs();

// Clear badge on sidebar link click for instant feedback.
document.addEventListener('click', (e) => {
  const link = e.target.closest('.room-sidebar__link[data-room-id]');
  if (!link) return;
  const rid = link.dataset.roomId;
  const badge = document.getElementById(`unread-badge-${rid}`);
  if (badge) {
    badge.hidden = true;
    badge.textContent = '0';
  }
  syncTopbarUnread();
});

// Fetch unread counts from the server and update sidebar badges.
// Called on tab resume to catch up on messages missed while hidden.
async function fetchUnreadCounts() {
  try {
    const resp = await fetch('/rooms/unread-counts');
    if (!resp.ok) return;
    const counts = await resp.json();

    document.querySelectorAll('.room-sidebar__badge').forEach((badge) => {
      const link = badge.closest('.room-sidebar__link[data-room-id]');
      if (!link) return;
      const rid = link.dataset.roomId;
      // Skip the current room — user is already viewing it.
      if (rid === window.roomID) return;
      const count = counts[rid] || 0;
      if (count > 0) {
        badge.textContent = count > 99 ? '99+' : String(count);
        badge.hidden = false;
      } else {
        badge.textContent = '0';
        badge.hidden = true;
      }
    });

    syncTopbarUnread();
  } catch (_) { /* network error — ignore */ }
}

// Lifecycle: close on hide, reopen on visible/pageshow.
window.addEventListener('pagehide', () => { if (userEs) { userEs.close(); userEs = null; } });

document.addEventListener('visibilitychange', () => {
  if (document.hidden) {
    if (userEs) { userEs.close(); userEs = null; }
  } else if (window.roomID) {
    openUserEs();
    fetchUnreadCounts();
  }
});

window.addEventListener('pageshow', (e) => {
  if (e.persisted && window.roomID) {
    openUserEs();
    fetchUnreadCounts();
  }
});

// Sync on initial load to pick up server-rendered unread counts.
syncTopbarUnread();

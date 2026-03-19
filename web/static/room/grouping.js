// Message grouping: collapse avatar/name for consecutive messages from the
// same author within a 5-minute window.

const GROUP_THRESHOLD_MS = 5 * 60 * 1000;

function tsFromId(id) {
  // id format: "msg-{unixMs}-{uuid}"
  return parseInt(id.slice(4), 10);
}

function isSystem(el) {
  return el.classList.contains('message--system');
}

function evaluatePair(el) {
  const prev = el.previousElementSibling;
  if (
    !prev ||
    !prev.classList.contains('message') ||
    isSystem(el) ||
    isSystem(prev)
  ) {
    el.classList.remove('message--continuation');
    return;
  }

  const sameAuthor = el.dataset.authorId === prev.dataset.authorId;
  const withinThreshold =
    tsFromId(el.id) - tsFromId(prev.id) < GROUP_THRESHOLD_MS;

  el.classList.toggle('message--continuation', sameAuthor && withinThreshold);
}

/** Full scan of all messages in the list. */
export function applyGrouping() {
  const articles = document.querySelectorAll(
    '#message-list-content > article.message',
  );
  for (const el of articles) {
    evaluatePair(el);
  }
}

/** Lightweight: re-evaluate el and its next sibling only. */
export function applyGroupingAround(el) {
  if (!el || !el.classList.contains('message')) return;
  evaluatePair(el);
  const next = el.nextElementSibling;
  if (next?.classList.contains('message')) {
    evaluatePair(next);
  }
}

// -- Self-register event listeners --

// Initial page load
applyGrouping();

// New SSE message inserted by HTMX
document.body.addEventListener('htmx:sseMessage', () => {
  const target = document.getElementById('sse-message-target');
  if (!target) return;
  // The new message is inserted just before the target; its previousSibling
  // is the newly inserted article.
  const inserted = target.previousElementSibling;
  if (inserted?.classList.contains('message')) {
    applyGroupingAround(inserted);
  }
});

// History prepend (infinite scroll sentinel swap)
document.body.addEventListener('htmx:afterSwap', (e) => {
  if (e.detail?.target?.classList?.contains('scroll-sentinel')) {
    applyGrouping();
  }
});

// Format message timestamps in the user's local timezone.
function formatMessageTimes() {
  const now = new Date();
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);

  document.querySelectorAll('time.message__time, time.message--system__time').forEach((el) => {
    const dt = new Date(el.getAttribute('datetime'));
    if (Number.isNaN(dt)) return;

    const timeStr = dt.toLocaleTimeString(navigator.languages, { hour: '2-digit', minute: '2-digit' });

    if (dt.toDateString() === now.toDateString()) {
      el.textContent = timeStr;
    } else if (dt.toDateString() === yesterday.toDateString()) {
      el.textContent = `Yesterday at ${timeStr}`;
    } else if (dt.getFullYear() === now.getFullYear()) {
      el.textContent =
        dt.toLocaleDateString(navigator.languages, { weekday: 'short', day: 'numeric', month: 'short' }) +
        ' at ' +
        timeStr;
    } else {
      el.textContent =
        dt.toLocaleDateString(navigator.languages, {
          weekday: 'short',
          day: 'numeric',
          month: 'short',
          year: 'numeric',
        }) +
        ' at ' +
        timeStr;
    }
  });
}

document.addEventListener('DOMContentLoaded', formatMessageTimes);
document.addEventListener('htmx:afterSettle', formatMessageTimes);

export { formatMessageTimes };

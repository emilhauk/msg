// Quote-reply: pick a message, show a "Replying to" strip above the composer,
// send its ID as reply_to with the next message.

const input = document.getElementById('reply-to-input');
const strip = document.getElementById('reply-strip');
const nameEl = document.getElementById('reply-strip-name');
const textEl = document.getElementById('reply-strip-text');
const form = document.querySelector('.message-form');
const composer = document.querySelector('.message-form__textarea');

export function startReply(msgId) {
  const article = document.getElementById(`msg-${msgId}`);
  if (!article) return;
  input.value = msgId;
  nameEl.textContent = article.querySelector('.message__author')?.textContent.trim() ?? '';
  const t = document.getElementById(`text-${msgId}`);
  textEl.textContent = t ? t.innerText.trim() : 'Attachment';
  strip.hidden = false;
  composer?.focus();
}

export function clearReply() {
  input.value = '';
  strip.hidden = true;
}

window.__startReply = startReply;

document.addEventListener('click', (e) => {
  const trigger = e.target.closest('[data-reply-trigger]');
  if (trigger) {
    startReply(trigger.dataset.replyTrigger);
    return;
  }
  if (e.target.closest('[data-reply-cancel]')) clearReply();
});

form?.addEventListener('htmx:afterRequest', (e) => {
  if (e.detail.successful) clearReply();
});

composer?.addEventListener('keydown', (e) => {
  if (e.key === 'Escape' && !strip.hidden) clearReply();
});

// Media upload (paste + drag-and-drop + file picker).
// Uploads files directly to S3 via a presigned PUT URL and queues them as
// attachment chips on the form.
const ALLOWED_TYPES = {
  'image/jpeg': true,
  'image/png': true,
  'image/gif': true,
  'image/webp': true,
  'video/mp4': true,
  'video/webm': true,
};
// Server re-encodes these to H.264 MP4 (POST /rooms/{id}/transcode).
const TRANSCODE_TYPES = { 'video/quicktime': true };
const ta = document.querySelector('.message-form__textarea');
const form = document.querySelector('.message-form');
const previewsEl = document.getElementById('attachment-previews');
const inputEl = document.getElementById('attachment-input');
const fileInput = document.getElementById('file-input');
if (ta && form && previewsEl && inputEl) {
  const draftKey = `draft-attachments:${window.roomID}`;
  let pendingAttachments = []; // [{url, content_type, filename}]
  let uploadCount = 0; // number of uploads currently in-flight

  function syncInput() {
    inputEl.value = pendingAttachments.length ? JSON.stringify(pendingAttachments) : '';
    // Persist finished attachments to localStorage for draft recovery.
    if (pendingAttachments.length) localStorage.setItem(draftKey, JSON.stringify(pendingAttachments));
    else localStorage.removeItem(draftKey);
  }

  function setSendDisabled(disabled) {
    const btn = form.querySelector('.message-form__send');
    if (btn) btn.disabled = disabled;
  }

  // Restore saved attachments from a previous draft.
  function restoreDraftAttachments() {
    const raw = localStorage.getItem(draftKey);
    if (!raw) return;
    let saved;
    try { saved = JSON.parse(raw); } catch (_) { return; }
    if (!Array.isArray(saved) || saved.length === 0) return;

    for (const att of saved) {
      const idx = pendingAttachments.length;
      pendingAttachments.push(att);

      const chip = document.createElement('div');
      chip.className = 'attachment-chip attachment-chip--done';
      chip.dataset.attachmentIdx = idx;

      if (att.content_type.startsWith('image/')) {
        const thumb = document.createElement('img');
        thumb.src = att.url;
        thumb.alt = '';
        chip.appendChild(thumb);
      } else {
        const icon = document.createElement('span');
        icon.className = 'attachment-chip__icon';
        icon.textContent = '\uD83C\uDFA5';
        chip.appendChild(icon);
      }

      const removeBtn = document.createElement('button');
      removeBtn.type = 'button';
      removeBtn.className = 'attachment-chip__remove';
      removeBtn.setAttribute('aria-label', 'Remove attachment');
      removeBtn.textContent = '\u00D7';
      removeBtn.addEventListener('click', () => {
        const i = parseInt(chip.dataset.attachmentIdx, 10);
        if (!Number.isNaN(i)) {
          pendingAttachments.splice(i, 1);
          const chips = previewsEl.querySelectorAll('.attachment-chip[data-attachment-idx]');
          let counter = 0;
          chips.forEach((c) => { c.dataset.attachmentIdx = counter++; });
          syncInput();
        }
        chip.remove();
        if (previewsEl.children.length === 0) previewsEl.hidden = true;
      });
      chip.appendChild(removeBtn);

      previewsEl.appendChild(chip);
    }
    previewsEl.hidden = false;
    syncInput();
  }
  restoreDraftAttachments();

  // Generate a 12-character random hex string to use as the file's key stem.
  function randomHex(len) {
    const arr = new Uint8Array(Math.ceil(len / 2));
    crypto.getRandomValues(arr);
    return Array.from(arr, (b) => b.toString(16).padStart(2, '0'))
      .join('')
      .slice(0, len);
  }

  // Build a preview chip element for a pending upload.
  function makeChip(contentType, objectURL) {
    const chip = document.createElement('div');
    chip.className = 'attachment-chip';

    if (contentType.startsWith('image/')) {
      const thumb = document.createElement('img');
      thumb.src = objectURL;
      thumb.alt = '';
      chip.appendChild(thumb);
    } else {
      const icon = document.createElement('span');
      icon.className = 'attachment-chip__icon';
      icon.textContent = '\uD83C\uDFA5'; // 🎥
      chip.appendChild(icon);
    }

    const spinner = document.createElement('span');
    spinner.className = 'attachment-chip__spinner';
    chip.appendChild(spinner);

    const removeBtn = document.createElement('button');
    removeBtn.type = 'button';
    removeBtn.className = 'attachment-chip__remove';
    removeBtn.setAttribute('aria-label', 'Remove attachment');
    removeBtn.textContent = '\u00D7'; // ×
    removeBtn.addEventListener('click', () => {
      const idx = parseInt(chip.dataset.attachmentIdx, 10);
      if (!Number.isNaN(idx)) {
        pendingAttachments.splice(idx, 1);
        // Re-index remaining chips.
        const chips = previewsEl.querySelectorAll('.attachment-chip[data-attachment-idx]');
        let counter = 0;
        chips.forEach((c) => {
          c.dataset.attachmentIdx = counter++;
        });
        syncInput();
      }
      chip.remove();
      if (previewsEl.children.length === 0) previewsEl.hidden = true;
    });
    chip.appendChild(removeBtn);

    previewsEl.hidden = false;
    previewsEl.appendChild(chip);
    return chip;
  }

  // Show a chip for file and run upload(), which resolves to an attachment {url, content_type, filename}.
  function track(file, upload) {
    const objectURL = URL.createObjectURL(file);
    const chip = makeChip(file.type, objectURL);

    uploadCount++;
    setSendDisabled(true);

    upload()
      .then((att) => {
        const idx = pendingAttachments.length;
        pendingAttachments.push(att);
        chip.dataset.attachmentIdx = idx;
        syncInput();
        chip.classList.add('attachment-chip--done');
        chip.querySelector('.attachment-chip__spinner').remove();
      })
      .catch(() => {
        chip.classList.add('attachment-chip--error');
        const spinner = chip.querySelector('.attachment-chip__spinner');
        if (spinner) spinner.remove();
      })
      .finally(() => {
        URL.revokeObjectURL(objectURL);
        uploadCount--;
        if (uploadCount === 0) setSendDisabled(false);
      });
  }

  // presign → PUT directly to S3.
  function uploadFile(file) {
    track(file, () => {
      const hash = randomHex(12);
      const params = new URLSearchParams({
        hash: hash,
        content_type: file.type,
        content_length: file.size,
      });
      return fetch(`/rooms/${window.roomID}/upload-url?${params}`, { credentials: 'same-origin' })
        .then((r) => {
          if (!r.ok) throw new Error(`presign failed: ${r.status}`);
          return r.json();
        })
        .then((data) =>
          fetch(data.upload_url, {
            method: 'PUT',
            headers: { 'Content-Type': file.type },
            body: file,
          }).then((r) => {
            if (!r.ok) throw new Error(`upload failed: ${r.status}`);
            return { url: data.public_url, content_type: file.type, filename: hash };
          }),
        );
    });
  }

  // Stream to the server, which transcodes and stores the result. Response is newline heartbeats followed by one JSON line.
  function transcodeFile(file) {
    track(file, () =>
      fetch(`/rooms/${window.roomID}/transcode`, {
        method: 'POST',
        headers: { 'Content-Type': file.type },
        body: file,
        credentials: 'same-origin',
      })
        .then((r) => {
          if (!r.ok) throw new Error(`transcode failed: ${r.status}`);
          return r.text();
        })
        .then((text) => {
          const res = JSON.parse(text.trim().split('\n').pop());
          if (res.error) throw new Error(`transcode failed: ${res.error}`);
          return res;
        }),
    );
  }

  // Safari is the only browser that decodes HEIC; re-encode on the sender's device so everyone else can view it.
  function toJpeg(file) {
    return createImageBitmap(file).then((bmp) => {
      const canvas = document.createElement('canvas');
      canvas.width = bmp.width;
      canvas.height = bmp.height;
      canvas.getContext('2d').drawImage(bmp, 0, 0);
      bmp.close();
      return new Promise((resolve, reject) =>
        canvas.toBlob((blob) => (blob ? resolve(new File([blob], 'image.jpg', { type: 'image/jpeg' })) : reject()), 'image/jpeg', 0.9),
      );
    });
  }

  function rejectChip(file) {
    const chip = makeChip('', '');
    chip.classList.add('attachment-chip--error');
    chip.querySelector('.attachment-chip__icon').textContent = '\u26A0\uFE0F';
    chip.title = `Unsupported file type: ${file.type || file.name}`;
    chip.querySelector('.attachment-chip__spinner').remove();
  }

  function intake(file) {
    if (ALLOWED_TYPES[file.type]) return uploadFile(file);
    if (TRANSCODE_TYPES[file.type]) return transcodeFile(file);
    if (file.type.startsWith('video/')) return rejectChip(file);
    toJpeg(file).then(uploadFile, () => rejectChip(file));
  }

  // ---- File picker button ----
  const attachBtn = document.querySelector('[data-attach-trigger]');
  if (attachBtn && fileInput) {
    attachBtn.addEventListener('click', () => {
      fileInput.click();
    });
    fileInput.addEventListener('change', () => {
      Array.from(fileInput.files || []).forEach(intake);
      // Reset so selecting the same file again triggers another change event.
      fileInput.value = '';
    });
  }

  // ---- Paste handler ----
  ta.addEventListener('paste', (e) => {
    const items = Array.from(e.clipboardData?.items || []);
    const mediaItems = items.filter((i) => i.kind === 'file');
    if (mediaItems.length === 0) return;

    // Prevent the browser pasting binary data as text into the textarea.
    e.preventDefault();

    mediaItems.forEach((item) => {
      const file = item.getAsFile();
      if (!file) return;
      intake(file);
    });
  });

  // ---- Drag-and-drop handler ----
  const roomMain = document.querySelector('.room-main');
  const overlay = document.getElementById('drop-overlay');
  if (roomMain && overlay) {
    // Track enter/leave depth to avoid flicker when crossing child elements.
    let dragDepth = 0;

    function hasDragFiles(e) {
      const types = e.dataTransfer?.types;
      if (!types) return false;
      return Array.prototype.indexOf.call(types, 'Files') >= 0;
    }

    function showOverlay() {
      overlay.removeAttribute('hidden');
      requestAnimationFrame(() => {
        overlay.classList.add('drop-overlay--active');
      });
    }

    function hideOverlay() {
      overlay.classList.remove('drop-overlay--active');
      overlay.setAttribute('hidden', '');
      dragDepth = 0;
    }

    roomMain.addEventListener('dragenter', (e) => {
      if (!hasDragFiles(e)) return;
      e.preventDefault();
      dragDepth++;
      if (dragDepth === 1) showOverlay();
    });

    roomMain.addEventListener('dragover', (e) => {
      if (!hasDragFiles(e)) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = 'copy';
    });

    roomMain.addEventListener('dragleave', () => {
      dragDepth--;
      if (dragDepth <= 0) hideOverlay();
    });

    roomMain.addEventListener('drop', (e) => {
      e.preventDefault();
      hideOverlay();

      const files = Array.from(e.dataTransfer?.files || []);
      files.forEach(intake);

      if (ta) ta.focus();
    });
  }

  // Clear attachments when the form resets (fires after successful HTMX send).
  form.addEventListener('reset', () => {
    pendingAttachments = [];
    localStorage.removeItem(draftKey);
    syncInput();
    previewsEl.innerHTML = '';
    previewsEl.hidden = true;
    setSendDisabled(false);
    uploadCount = 0;
  });
}

// Swipe gestures for mobile navigation:
// - Swipe right from left edge  → open left sidebar
// - Swipe left while sidebar open → close left sidebar
let startX = 0;
let startY = 0;

document.addEventListener('touchstart', e => {
  startX = e.touches[0].clientX;
  startY = e.touches[0].clientY;
}, { passive: true });

document.addEventListener('touchend', e => {
  const dx = e.changedTouches[0].clientX - startX;
  const dy = e.changedTouches[0].clientY - startY;
  const horizontal = Math.abs(dx) > Math.abs(dy) * 1.5;
  if (!horizontal) return;

  if (dx > 60 && startX <= 30 && window.__openSidebar) {
    // Right swipe from left edge → open sidebar
    window.__openSidebar();
  } else if (dx < -60 && window.__isSidebarOpen && window.__isSidebarOpen() && window.__closeSidebar) {
    // Left swipe while sidebar open → close sidebar
    window.__closeSidebar();
  }
}, { passive: true });

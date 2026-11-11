// The body scroll lock.
//
// Two overlays lock the body: the Focus Mode overlay and the mobile navigation sheet. Both have to
// release the lock when they close, so the lock is counted rather than set. Two overlays open at
// once, and the first one to close must not unlock the page underneath the second.
//
// The count lives on document.body.dataset rather than in a module variable because the two
// overlays are in two separate bundles. A module variable would be two variables, one per bundle,
// and the two overlays could not see each other's count.
//
// Before v0.5.0 the two scripts shared this through window.verseLockBodyScroll, because two
// classic scripts cannot import from one another. They are modules now, so the import is the
// sharing, and the globals are gone.

/** How many overlays currently hold the body locked. */
function bodyScrollLockCount(): number {
  return Number(document.body.dataset.verseScrollLocks || "0");
}

/** Takes the body scroll lock. */
export function lockBodyScroll(): void {
  document.body.dataset.verseScrollLocks = String(bodyScrollLockCount() + 1);
  document.body.style.overflow = "hidden";
}

/** Releases one hold on the body scroll lock. */
export function unlockBodyScroll(): void {
  const next = Math.max(0, bodyScrollLockCount() - 1);
  if (next === 0) {
    delete document.body.dataset.verseScrollLocks;
    document.body.style.overflow = "";
    return;
  }

  document.body.dataset.verseScrollLocks = String(next);
}

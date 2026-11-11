(() => {
  // web/ts/dom.ts
  function closestElement(target, selector) {
    return target instanceof Element ? target.closest(selector) : null;
  }

  // web/ts/scroll_lock.ts
  function bodyScrollLockCount() {
    return Number(document.body.dataset.verseScrollLocks || "0");
  }
  function lockBodyScroll() {
    document.body.dataset.verseScrollLocks = String(bodyScrollLockCount() + 1);
    document.body.style.overflow = "hidden";
  }
  function unlockBodyScroll() {
    const next = Math.max(0, bodyScrollLockCount() - 1);
    if (next === 0) {
      delete document.body.dataset.verseScrollLocks;
      document.body.style.overflow = "";
      return;
    }
    document.body.dataset.verseScrollLocks = String(next);
  }

  // web/ts/editor.ts
  function editorRoot(node) {
    return node.closest("[data-editor-root]");
  }
  function editorViewport() {
    return document.getElementById("viewport");
  }
  function setEditorOverlayState(open) {
    const viewport = editorViewport();
    if (!viewport) return;
    if (open) {
      viewport.dataset.editorOverlayActive = "true";
      return;
    }
    delete viewport.dataset.editorOverlayActive;
  }
  function openEditorOverlay(node) {
    const root = editorRoot(node);
    if (!root) return;
    const overlay = root.querySelector("[data-editor-overlay]");
    const base = root.querySelector("[data-editor-base]");
    const overlayTextarea = root.querySelector(
      "[data-editor-overlay-textarea]",
    );
    if (!overlay || !base || !overlayTextarea) return;
    overlayTextarea.value = base.value;
    overlay.showModal();
    setEditorOverlayState(true);
    lockBodyScroll();
    requestAnimationFrame(() => overlayTextarea.focus());
  }
  function closeEditorOverlay(node) {
    const root = editorRoot(node);
    if (!root) return;
    const overlay = root.querySelector("[data-editor-overlay]");
    if (!overlay) return;
    overlay.close();
  }
  function syncEditorOverlay(node) {
    const root = editorRoot(node);
    if (!root) return;
    const base = root.querySelector("[data-editor-base]");
    if (!base) return;
    base.value = node.value;
  }
  document.addEventListener("click", (event) => {
    const opener = closestElement(event.target, "[data-editor-overlay-open]");
    if (opener) {
      openEditorOverlay(opener);
      return;
    }
    const closer = closestElement(event.target, "[data-editor-overlay-close]");
    if (closer) {
      closeEditorOverlay(closer);
    }
  });
  document.addEventListener("input", (event) => {
    const field = closestElement(
      event.target,
      "[data-editor-overlay-textarea]",
    );
    if (field instanceof HTMLTextAreaElement) {
      syncEditorOverlay(field);
    }
  });
  var editorOverlay = document.querySelector("[data-editor-overlay]");
  if (editorOverlay) {
    editorOverlay.addEventListener("close", () => {
      const root = editorOverlay.closest("[data-editor-root]");
      const base = root ? root.querySelector("[data-editor-base]") : null;
      const overlayTextarea = editorOverlay.querySelector(
        "[data-editor-overlay-textarea]",
      );
      if (base && overlayTextarea) {
        base.value = overlayTextarea.value;
      }
      setEditorOverlayState(false);
      unlockBodyScroll();
      if (base) {
        requestAnimationFrame(() => base.focus());
      }
    });
    editorOverlay.addEventListener("click", (event) => {
      const card = editorOverlay.querySelector(".verse-editor-overlay-card");
      const target = event.target;
      if (card && target instanceof Node && !card.contains(target)) {
        closeEditorOverlay(editorOverlay);
      }
    });
  }
})();

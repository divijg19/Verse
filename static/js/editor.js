// The editor's Focus Mode.
//
// This used to be an inline <script> block in templ/editor.templ, driving the overlay through four
// inline onclick/oninput attributes. None of it ran, in any browser, from v0.3.7 onward: the content
// security policy sets script-src 'self' with no 'unsafe-inline' and no nonce anywhere in this
// application, so the block was blocked before it executed and the attributes never fired. The Full
// screen button was inert and the overlay was unreachable.
//
// The policy is correct and stays exactly as it is. The code moved instead. Adding 'unsafe-inline' to
// make this go away would re-open the ability to inject script from anywhere in the markup, which is
// the whole point of having the header.
//
// Loaded from templ/editor.templ rather than layout.templ, so it is fetched only on the editor and
// not on every other surface, which is where the inline block sat too.

function verseEditorRoot(node) {
  return node.closest("[data-editor-root]");
}

function verseEditorViewport() {
  return document.getElementById("viewport");
}

// The viewport flag is deliberately not data-editor-overlay-open. That name belongs to the Full
// screen button, and the delegated click handler looks for it with closest(). The viewport is an
// ancestor of the overlay, so reusing the name made every click inside an open overlay match the
// viewport first, call the open path, and return before the Close button or backdrop could run.
function verseSetEditorOverlayState(open) {
  const viewport = verseEditorViewport();
  if (!viewport) return;
  if (open) {
    viewport.dataset.editorOverlayActive = "true";
    return;
  }
  delete viewport.dataset.editorOverlayActive;
}

function verseOpenEditorOverlay(node) {
  const root = verseEditorRoot(node);
  if (!root) return;
  const overlay = root.querySelector("[data-editor-overlay]");
  const base = root.querySelector("[data-editor-base]");
  const overlayTextarea = root.querySelector("[data-editor-overlay-textarea]");
  if (!overlay || !base || !overlayTextarea) return;
  overlayTextarea.value = base.value;
  overlay.showModal();
  verseSetEditorOverlayState(true);
  if (window.verseLockBodyScroll) {
    window.verseLockBodyScroll();
  } else {
    document.body.style.overflow = "hidden";
  }
  requestAnimationFrame(() => overlayTextarea.focus());
}

function verseCloseEditorOverlay(node) {
  const root = verseEditorRoot(node);
  if (!root) return;
  const overlay = root.querySelector("[data-editor-overlay]");
  if (!overlay) return;
  overlay.close();
}

function verseSyncEditorOverlay(node) {
  const root = verseEditorRoot(node);
  if (!root) return;
  const base = root.querySelector("[data-editor-base]");
  if (!base) return;
  base.value = node.value;
}

// verseClosest is event.target.closest with the guard the shorthand needs.
//
// The target of a click or an input event is not always an Element -- it can be a text node or the
// document itself -- and calling closest on those is a TypeError rather than a false result. An
// inline attribute never had to think about it, because the browser passed the element.
function verseClosest(target, selector) {
  return target instanceof Element ? target.closest(selector) : null;
}

// One delegated click listener for both directions, rather than one per control.
//
// Delegation is what allows the handlers to live in a file the policy permits. A listener attached
// directly to each button would be equally acceptable to the policy and would not need this guard,
// but the controls are inside an overlay that is hidden and re-shown, and delegation means the
// wiring cannot be lost by a control being replaced without this file being touched.
document.addEventListener("click", (event) => {
  const opener = verseClosest(event.target, "[data-editor-overlay-open]");
  if (opener) {
    verseOpenEditorOverlay(opener);
    return;
  }

  const closer = verseClosest(event.target, "[data-editor-overlay-close]");
  if (closer) {
    verseCloseEditorOverlay(closer);
  }
});

// input rather than keyup or change: the original attribute was oninput, and syncing on every
// keystroke is what keeps the base textarea current if the overlay is closed without the close
// handler running.
document.addEventListener("input", (event) => {
  const field = verseClosest(event.target, "[data-editor-overlay-textarea]");
  if (field) {
    verseSyncEditorOverlay(field);
  }
});

// The overlay is a native dialog, so Escape and the backdrop both close it. The close
// event is the single place that copies the overlay text back to the base textarea,
// clears the viewport state, and restores focus.
const editorOverlay = document.querySelector("[data-editor-overlay]");
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
    verseSetEditorOverlayState(false);
    if (window.verseUnlockBodyScroll) {
      window.verseUnlockBodyScroll();
    } else {
      document.body.style.overflow = "";
    }
    if (base) {
      requestAnimationFrame(() => base.focus());
    }
  });

  editorOverlay.addEventListener("click", (event) => {
    const card = editorOverlay.querySelector(".verse-editor-overlay-card");
    if (card && !card.contains(event.target)) {
      verseCloseEditorOverlay(editorOverlay);
    }
  });
}

import { closestElement } from "./dom";
import { lockBodyScroll, unlockBodyScroll } from "./scroll_lock";

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

/** The element that holds the overlay, the base textarea and the overlay's own textarea. */
function editorRoot(node: Element): HTMLElement | null {
  return node.closest<HTMLElement>("[data-editor-root]");
}

/** The viewport, whose dataset attribute says whether an overlay is open. */
function editorViewport(): HTMLElement | null {
  return document.getElementById("viewport");
}

/**
 * Records whether an overlay is open, on the viewport.
 *
 * The flag is deliberately not named data-editor-overlay-open. That name belongs to the Full screen
 * button, and the delegated click handler looks for it with closest(). The viewport is an ancestor
 * of the overlay, so reusing the name made every click inside an open overlay match the viewport
 * first, call the open path, and return before the Close button or backdrop could run.
 */
function setEditorOverlayState(open: boolean): void {
  const viewport = editorViewport();
  if (!viewport) return;

  if (open) {
    viewport.dataset.editorOverlayActive = "true";
    return;
  }
  delete viewport.dataset.editorOverlayActive;
}

/** Opens the overlay and copies the base textarea's text into it. */
function openEditorOverlay(node: Element): void {
  const root = editorRoot(node);
  if (!root) return;

  const overlay = root.querySelector<HTMLDialogElement>(
    "[data-editor-overlay]",
  );
  const base = root.querySelector<HTMLTextAreaElement>("[data-editor-base]");
  const overlayTextarea = root.querySelector<HTMLTextAreaElement>(
    "[data-editor-overlay-textarea]",
  );
  if (!overlay || !base || !overlayTextarea) return;

  overlayTextarea.value = base.value;
  overlay.showModal();
  setEditorOverlayState(true);
  lockBodyScroll();
  requestAnimationFrame(() => overlayTextarea.focus());
}

/** Closes the overlay. */
function closeEditorOverlay(node: Element): void {
  const root = editorRoot(node);
  if (!root) return;

  const overlay = root.querySelector<HTMLDialogElement>(
    "[data-editor-overlay]",
  );
  if (!overlay) return;

  overlay.close();
}

/** Keeps the base textarea current while the overlay's own textarea is edited. */
function syncEditorOverlay(node: HTMLTextAreaElement): void {
  const root = editorRoot(node);
  if (!root) return;

  const base = root.querySelector<HTMLTextAreaElement>("[data-editor-base]");
  if (!base) return;

  base.value = node.value;
}

// One delegated click listener for both directions, rather than one per control.
//
// Delegation is what allows the handlers to live in a file the policy permits. A listener attached
// directly to each button would be equally acceptable to the policy and would not need this guard,
// but the controls are inside an overlay that is hidden and re-shown, and delegation means the
// wiring cannot be lost by a control being replaced without this file being touched.
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

// input rather than keyup or change: the original attribute was oninput, and syncing on every
// keystroke is what keeps the base textarea current if the overlay is closed without the close
// handler running.
document.addEventListener("input", (event) => {
  const field = closestElement(event.target, "[data-editor-overlay-textarea]");
  if (field instanceof HTMLTextAreaElement) {
    syncEditorOverlay(field);
  }
});

// The overlay is a native dialog, so Escape and the backdrop both close it. The close
// event is the single place that copies the overlay text back to the base textarea,
// clears the viewport state, and restores focus.
const editorOverlay = document.querySelector<HTMLDialogElement>(
  "[data-editor-overlay]",
);
if (editorOverlay) {
  editorOverlay.addEventListener("close", () => {
    const root = editorOverlay.closest<HTMLElement>("[data-editor-root]");
    const base = root
      ? root.querySelector<HTMLTextAreaElement>("[data-editor-base]")
      : null;
    const overlayTextarea = editorOverlay.querySelector<HTMLTextAreaElement>(
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

    // The target is checked for being a Node before it goes into contains(). A click can land on
    // something that is not a node at all, and contains() rejects those with a TypeError rather
    // than answering the question.
    const target = event.target;
    if (card && target instanceof Node && !card.contains(target)) {
      closeEditorOverlay(editorOverlay);
    }
  });
}

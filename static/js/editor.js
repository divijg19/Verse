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

function verseSetEditorOverlayState(open) {
    const viewport = verseEditorViewport();
    if (!viewport) return;
    if (open) {
        viewport.dataset.editorOverlayOpen = "true";
        return;
    }
    delete viewport.dataset.editorOverlayOpen;
}

function verseOpenEditorOverlay(node) {
    const root = verseEditorRoot(node);
    if (!root) return;
    const overlay = root.querySelector("[data-editor-overlay]");
    const base = root.querySelector("[data-editor-base]");
    const overlayTextarea = root.querySelector("[data-editor-overlay-textarea]");
    if (!overlay || !base || !overlayTextarea) return;
    overlayTextarea.value = base.value;
    overlay.hidden = false;
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
    const base = root.querySelector("[data-editor-base]");
    const overlayTextarea = root.querySelector("[data-editor-overlay-textarea]");
    if (!overlay || !base || !overlayTextarea) return;
    base.value = overlayTextarea.value;
    overlay.hidden = true;
    verseSetEditorOverlayState(false);
    if (window.verseUnlockBodyScroll) {
        window.verseUnlockBodyScroll();
    } else {
        document.body.style.overflow = "";
    }
    requestAnimationFrame(() => base.focus());
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

if (!window.verseEditorEscapeBound) {
    window.verseEditorEscapeBound = true;
    document.addEventListener("keydown", function(event) {
        if (event.key !== "Escape") return;
        const overlay = document.querySelector("[data-editor-overlay]:not([hidden])");
        if (!overlay) return;
        const closeButton = overlay.querySelector("[aria-label='Close full screen editor']");
        if (closeButton) {
            verseCloseEditorOverlay(closeButton);
        }
    });
}

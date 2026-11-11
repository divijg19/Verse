import { closestElement } from "./dom";
import { lockBodyScroll, unlockBodyScroll } from "./scroll_lock";

// The navigation.
//
// Two things live here: the screen transition that htmx's swap runs, and the mobile navigation
// sheet that the desktop rails collapse into below 1024px.
//
// Before v0.5.0 the mobile navigation's open and close functions were published on window as
// verseOpenMobileNav and verseCloseMobileNav. Nothing called them: the sheet is opened and closed
// by the delegated listeners below. They are module functions now, and the globals are gone.

/**
 * The #screen element, when the event is a swap into it.
 *
 * The check is on identity rather than on a class or an attribute, so a swap anywhere else on the
 * page -- a library page's own swap, say -- does not move the screen out from under the reader.
 */
function activeScreenSwapTarget(event: Event): HTMLElement | null {
  const screen = document.getElementById("screen");
  const detail = (event as CustomEvent<VerseHtmxEventDetail>).detail;
  const target = detail ? detail.target : null;
  if (!screen || target !== screen) {
    return null;
  }
  return screen;
}

/** Removes the transition classes a swap adds. */
function restoreScreenTransition(event: Event): void {
  const screen = activeScreenSwapTarget(event);
  if (!screen) {
    return;
  }

  requestAnimationFrame(() => {
    screen.classList.remove("opacity-0", "translate-y-2");
  });
}

/** The state of the mobile navigation sheet. */
const mobileNavState: { open: boolean; lastTrigger: HTMLElement | null } = {
  open: false,
  lastTrigger: null,
};

/** The mobile navigation's root element. */
function mobileNavRoot(): HTMLElement | null {
  return document.getElementById("mobile-nav");
}

/** The sheet that holds the mobile navigation's links. */
function mobileNavSheet(): HTMLElement | null {
  const root = mobileNavRoot();
  return root
    ? root.querySelector<HTMLElement>("[data-mobile-nav-sheet]")
    : null;
}

/** The button that opens the sheet. */
function mobileNavToggle(): HTMLElement | null {
  const root = mobileNavRoot();
  return root
    ? root.querySelector<HTMLElement>("[data-mobile-nav-toggle]")
    : null;
}

/** The link the sheet should focus when it opens: the current surface's, or the first one. */
function mobileNavFocusTarget(): HTMLElement | null {
  const sheet = mobileNavSheet();
  if (!sheet) {
    return null;
  }

  return (
    sheet.querySelector<HTMLElement>(
      "[data-mobile-nav-link][data-active='true']",
    ) || sheet.querySelector<HTMLElement>("[data-mobile-nav-link]")
  );
}

/** Records the sheet's state on the button that opens it. */
function syncMobileNavButton(expanded: boolean): void {
  const toggle = mobileNavToggle();
  if (!toggle) {
    return;
  }

  toggle.setAttribute("aria-expanded", expanded ? "true" : "false");
}

/** Opens the mobile navigation sheet. */
function openMobileNav(node?: Element | null): void {
  const sheet = mobileNavSheet();
  if (!sheet || mobileNavState.open) {
    return;
  }

  mobileNavState.open = true;
  mobileNavState.lastTrigger =
    (node as HTMLElement | null) ?? mobileNavToggle();
  sheet.hidden = false;
  syncMobileNavButton(true);
  lockBodyScroll();

  const focusTarget = mobileNavFocusTarget();
  if (focusTarget) {
    requestAnimationFrame(() => focusTarget.focus());
  }
}

/** Closes the mobile navigation sheet and returns focus to the button that opened it. */
function closeMobileNav(): void {
  const sheet = mobileNavSheet();
  if (sheet) {
    sheet.hidden = true;
  }

  if (mobileNavState.open) {
    unlockBodyScroll();
  }

  mobileNavState.open = false;
  syncMobileNavButton(false);

  if (mobileNavState.lastTrigger && mobileNavState.lastTrigger.isConnected) {
    const focusTarget = mobileNavState.lastTrigger;
    requestAnimationFrame(() => focusTarget.focus());
  }
  mobileNavState.lastTrigger = null;
}

document.body.addEventListener("htmx:beforeRequest", (event) => {
  const screen = activeScreenSwapTarget(event);
  if (!screen) {
    return;
  }

  screen.classList.add("opacity-0", "translate-y-2");
});

document.body.addEventListener("htmx:afterSwap", (event) => {
  restoreScreenTransition(event);

  if (activeScreenSwapTarget(event)) {
    closeMobileNav();
  }
});

// Ensure the screen is restored even if the request fails or no swap occurs.
document.body.addEventListener("htmx:afterRequest", restoreScreenTransition);

// Also handle network/response errors explicitly.
document.body.addEventListener("htmx:responseError", restoreScreenTransition);

document.addEventListener("keydown", (event) => {
  if (event.key !== "Escape") {
    return;
  }

  if (mobileNavState.open) {
    closeMobileNav();
  }
});

window.addEventListener("resize", () => {
  if (window.innerWidth >= 1024 && mobileNavState.open) {
    closeMobileNav();
  }
});

// Delegated listeners for the mobile navigation.
//
// Both of these were onclick attributes until v0.4.8, and neither had run since v0.3.7: the content
// security policy sets script-src 'self' with no 'unsafe-inline' and no nonce anywhere in this
// application, so every inline attribute was blocked in every browser. The desktop nav is
// display:none below 1024px, which left the hamburger as the only route to the navigation on a
// phone, pointing at nothing.
//
// The functions below are unchanged. openMobileNav takes the triggering element and uses it only to
// remember where focus came from, so the matched element is the same argument the inline attribute
// passed as `this`. closeMobileNav takes none, and the attribute's `this` was already being
// discarded.
//
// The target is checked for being an Element by closestElement: a click can land on a text node or
// the document itself, and calling closest on those is a TypeError rather than a false result. An
// inline attribute never had to consider it, because the browser passed the element directly.
document.addEventListener("click", (event) => {
  const toggle = closestElement(event.target, "[data-mobile-nav-toggle]");
  if (toggle) {
    openMobileNav(toggle);
    return;
  }

  if (closestElement(event.target, "[data-mobile-nav-close]")) {
    closeMobileNav();
  }
});

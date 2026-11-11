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

  // web/ts/navigation.ts
  function activeScreenSwapTarget(event) {
    const screen = document.getElementById("screen");
    const detail = event.detail;
    const target = detail ? detail.target : null;
    if (!screen || target !== screen) {
      return null;
    }
    return screen;
  }
  function restoreScreenTransition(event) {
    const screen = activeScreenSwapTarget(event);
    if (!screen) {
      return;
    }
    requestAnimationFrame(() => {
      screen.classList.remove("opacity-0", "translate-y-2");
    });
  }
  var mobileNavState = {
    open: false,
    lastTrigger: null,
  };
  function mobileNavRoot() {
    return document.getElementById("mobile-nav");
  }
  function mobileNavSheet() {
    const root = mobileNavRoot();
    return root ? root.querySelector("[data-mobile-nav-sheet]") : null;
  }
  function mobileNavToggle() {
    const root = mobileNavRoot();
    return root ? root.querySelector("[data-mobile-nav-toggle]") : null;
  }
  function mobileNavFocusTarget() {
    const sheet = mobileNavSheet();
    if (!sheet) {
      return null;
    }
    return (
      sheet.querySelector("[data-mobile-nav-link][data-active='true']") ||
      sheet.querySelector("[data-mobile-nav-link]")
    );
  }
  function syncMobileNavButton(expanded) {
    const toggle = mobileNavToggle();
    if (!toggle) {
      return;
    }
    toggle.setAttribute("aria-expanded", expanded ? "true" : "false");
  }
  function openMobileNav(node) {
    const sheet = mobileNavSheet();
    if (!sheet || mobileNavState.open) {
      return;
    }
    mobileNavState.open = true;
    mobileNavState.lastTrigger = node ?? mobileNavToggle();
    sheet.hidden = false;
    syncMobileNavButton(true);
    lockBodyScroll();
    const focusTarget = mobileNavFocusTarget();
    if (focusTarget) {
      requestAnimationFrame(() => focusTarget.focus());
    }
  }
  function closeMobileNav() {
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
  document.body.addEventListener("htmx:afterRequest", restoreScreenTransition);
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
})();

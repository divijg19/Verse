function verseBodyScrollLockCount() {
    return Number(document.body.dataset.verseScrollLocks || "0");
}

function verseLockBodyScroll() {
    const next = verseBodyScrollLockCount() + 1;
    document.body.dataset.verseScrollLocks = String(next);
    document.body.style.overflow = "hidden";
}

function verseUnlockBodyScroll() {
    const next = Math.max(0, verseBodyScrollLockCount() - 1);
    if (next === 0) {
        delete document.body.dataset.verseScrollLocks;
        document.body.style.overflow = "";
        return;
    }

    document.body.dataset.verseScrollLocks = String(next);
}

window.verseLockBodyScroll = verseLockBodyScroll;
window.verseUnlockBodyScroll = verseUnlockBodyScroll;

function activeScreenSwapTarget(event) {
    const screen = document.getElementById("screen");
    const detail = event && event.detail ? event.detail : null;
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

const verseMobileNavState = {
    open: false,
    lastTrigger: null,
};

function verseMobileNavRoot() {
    return document.getElementById("mobile-nav");
}

function verseMobileNavSheet() {
    const root = verseMobileNavRoot();
    return root ? root.querySelector("[data-mobile-nav-sheet]") : null;
}

function verseMobileNavToggle() {
    const root = verseMobileNavRoot();
    return root ? root.querySelector("[data-mobile-nav-toggle]") : null;
}

function verseSyncMobileNavButton(expanded) {
    const toggle = verseMobileNavToggle();
    if (!toggle) {
        return;
    }

    toggle.setAttribute("aria-expanded", expanded ? "true" : "false");
}

function verseOpenMobileNav(node) {
    const sheet = verseMobileNavSheet();
    if (!sheet || verseMobileNavState.open) {
        return;
    }

    verseMobileNavState.open = true;
    verseMobileNavState.lastTrigger = node || verseMobileNavToggle();
    sheet.hidden = false;
    verseSyncMobileNavButton(true);
    verseLockBodyScroll();

    const closeButton = sheet.querySelector("[aria-label='Close navigation menu']");
    if (closeButton) {
        requestAnimationFrame(() => closeButton.focus());
    }
}

function verseCloseMobileNav() {
    const sheet = verseMobileNavSheet();
    if (sheet) {
        sheet.hidden = true;
    }

    if (verseMobileNavState.open) {
        verseUnlockBodyScroll();
    }

    verseMobileNavState.open = false;
    verseSyncMobileNavButton(false);

    if (verseMobileNavState.lastTrigger && verseMobileNavState.lastTrigger.isConnected) {
        const focusTarget = verseMobileNavState.lastTrigger;
        requestAnimationFrame(() => focusTarget.focus());
    }
    verseMobileNavState.lastTrigger = null;
}

window.verseOpenMobileNav = verseOpenMobileNav;
window.verseCloseMobileNav = verseCloseMobileNav;

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
        verseCloseMobileNav();
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

    if (verseMobileNavState.open) {
        verseCloseMobileNav();
    }
});

window.addEventListener("resize", () => {
    if (window.innerWidth >= 1024 && verseMobileNavState.open) {
        verseCloseMobileNav();
    }
});

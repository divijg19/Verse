document.body.addEventListener("htmx:beforeRequest", () => {
    const screen = document.getElementById("screen");
    if (screen) {
        screen.classList.add("opacity-0", "translate-y-2");
    }
});

document.body.addEventListener("htmx:afterSwap", () => {
    const screen = document.getElementById("screen");
    if (screen) {
        requestAnimationFrame(() => {
            screen.classList.remove("opacity-0", "translate-y-2");
        });
    }
});

// Ensure the screen is restored even if the request fails or no swap occurs.
document.body.addEventListener("htmx:afterRequest", () => {
    const screen = document.getElementById("screen");
    if (screen) {
        requestAnimationFrame(() => {
            screen.classList.remove("opacity-0", "translate-y-2");
        });
    }
});

// Also handle network/response errors explicitly.
document.body.addEventListener("htmx:responseError", () => {
    const screen = document.getElementById("screen");
    if (screen) {
        requestAnimationFrame(() => {
            screen.classList.remove("opacity-0", "translate-y-2");
        });
    }
});

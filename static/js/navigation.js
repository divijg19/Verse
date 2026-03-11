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

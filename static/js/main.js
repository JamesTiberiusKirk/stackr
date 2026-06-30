// HTMX event handlers for daemon.

// Log HTMX errors to the console.
document.addEventListener('htmx:responseError', function(evt) {
    console.error('HTMX request failed:', evt.detail.xhr.status, evt.detail.xhr.statusText);
});

// Re-initialize components after HTMX swaps.
document.addEventListener('htmx:afterSwap', function(evt) {
    // Scroll to the first validation error if present.
    var firstError = evt.detail.target.querySelector('.field-error');
    if (firstError && firstError.textContent.trim() !== '') {
        firstError.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }

    // Auto-remove server-rendered ws-banners (e.g. cron Run-now ack)
    // on the same 6s schedule the WS-side renderer uses, so synchronous
    // and live banners feel identical. Banners opt in via data-auto-remove.
    evt.detail.target.querySelectorAll('.ws-banner[data-auto-remove]').forEach(function(banner) {
        if (banner.dataset.autoRemoveScheduled) return;
        banner.dataset.autoRemoveScheduled = 'true';
        setTimeout(function() { banner.remove(); }, 6000);
    });
});

// Handle HTMX send errors (network failures).
document.addEventListener('htmx:sendError', function(evt) {
    console.error('HTMX network error for:', evt.detail.elt);
});

// Locks page scroll while at least one modal is open, so scrolling with the cursor
// over the darkened overlay (rather than directly over the modal's own
// scrollable .modal-content) doesn't scroll the page behind it instead — previously
// nothing prevented that, so the modal stayed put on screen while its background
// moved underneath it.
//
// Reference-counted rather than a plain boolean: multiple modals can technically be
// open at once (e.g. AppModal showing a success/error message on top of one of
// Dashboard.vue's setup modals), and closing just one of them must not re-enable
// scroll while another is still up.
let openCount = 0
let previousOverflow = ''

export function lockBodyScroll() {
    if (openCount === 0) {
        previousOverflow = document.body.style.overflow
        document.body.style.overflow = 'hidden'
    }
    openCount++
}

export function unlockBodyScroll() {
    openCount = Math.max(0, openCount - 1)
    if (openCount === 0) {
        document.body.style.overflow = previousOverflow
    }
}

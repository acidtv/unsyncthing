package com.acidtv.unsyncthing

/**
 * Guards the "Connecting…" dialog against a cancel race.
 *
 * Connect walks the peer's candidate addresses in the Go layer, firing an
 * `OnDialing` callback per address that posts `UiState.Connecting`. When the
 * user taps Cancel we abort the dial loop, but a callback already dispatched
 * from the Go thread can still land *after* the cancel has reset the state to
 * Idle. If that late post is applied the dialog reappears, "trying the next
 * address". This guard lets the ViewModel drop those late dialing posts once a
 * cancel has been requested.
 *
 * [arm] must be called when a new connect starts so a previous cancel doesn't
 * leave the next connect permanently suppressed.
 *
 * Safe for concurrent use: [cancel] is called from the UI thread while
 * [allowed] is checked from the gomobile callback thread.
 */
class ConnectCancelGuard {
    @Volatile
    private var cancelled = false

    /** Start tracking a fresh connect attempt, clearing any prior cancel. */
    fun arm() {
        cancelled = false
    }

    /** Record that the active connect attempt was cancelled. */
    fun cancel() {
        cancelled = true
    }

    /** True while dialing updates should be shown; false once cancelled. */
    fun allowed(): Boolean = !cancelled
}

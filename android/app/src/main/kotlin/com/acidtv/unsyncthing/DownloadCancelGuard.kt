package com.acidtv.unsyncthing

/**
 * Guards the download footer against a cancel race.
 *
 * When the user cancels a download, the block already in flight fires one more
 * `OnProgress` callback *after* the cancel has hidden the footer. If that late
 * progress post is applied, the footer reappears and stays visible even though
 * the download has stopped. This guard lets the ViewModel drop progress posts
 * once a cancel has been requested.
 *
 * [arm] must be called when a new download starts so a previous cancel doesn't
 * leave the next download permanently suppressed.
 *
 * Safe for concurrent use: [cancel] is called from the UI thread while
 * [progressAllowed] is checked from the gomobile callback thread.
 */
class DownloadCancelGuard {
    @Volatile
    private var cancelled = false

    /** Start tracking a fresh download, clearing any prior cancel. */
    fun arm() {
        cancelled = false
    }

    /** Record that the active download was cancelled. */
    fun cancel() {
        cancelled = true
    }

    /** True while progress updates should be shown; false once cancelled. */
    fun progressAllowed(): Boolean = !cancelled
}

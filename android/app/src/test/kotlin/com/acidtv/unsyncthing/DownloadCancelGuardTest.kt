package com.acidtv.unsyncthing

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class DownloadCancelGuardTest {

    @Test
    fun progressAllowedWhileDownloading() {
        val g = DownloadCancelGuard()
        g.arm()
        assertTrue("progress must show during a normal download", g.progressAllowed())
    }

    @Test
    fun progressDroppedAfterCancel() {
        // Regression: a block already in flight fires one more OnProgress after
        // the user cancels. If it were applied the footer would reappear and
        // stay visible even though the download has stopped.
        val g = DownloadCancelGuard()
        g.arm()
        g.cancel()
        assertFalse("late progress after cancel must be dropped", g.progressAllowed())
    }

    @Test
    fun newDownloadReArmsAfterPreviousCancel() {
        // Regression for "cancel one download, then the next download's footer
        // never updates": arming a fresh download must clear the prior cancel.
        val g = DownloadCancelGuard()
        g.arm()
        g.cancel()
        assertFalse(g.progressAllowed())

        g.arm()
        assertTrue("a fresh download must show progress again", g.progressAllowed())
    }
}

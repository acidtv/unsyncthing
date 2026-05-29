package com.acidtv.unsyncthing

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ConnectCancelGuardTest {

    @Test
    fun dialingAllowedWhileConnecting() {
        val g = ConnectCancelGuard()
        g.arm()
        assertTrue("dialing updates must show during a normal connect", g.allowed())
    }

    @Test
    fun dialingDroppedAfterCancel() {
        // Regression: an OnDialing callback already dispatched from the Go
        // thread fires after the user cancels. If it were applied the
        // connecting dialog would reappear, "trying the next address".
        val g = ConnectCancelGuard()
        g.arm()
        g.cancel()
        assertFalse("late dialing post after cancel must be dropped", g.allowed())
    }

    @Test
    fun newConnectReArmsAfterPreviousCancel() {
        // Cancelling one connect must not permanently suppress the next one's
        // "Connecting to …" updates.
        val g = ConnectCancelGuard()
        g.arm()
        g.cancel()
        assertFalse(g.allowed())

        g.arm()
        assertTrue("a fresh connect must show dialing updates again", g.allowed())
    }
}

package com.acidtv.unsyncthing

import org.junit.Assert.assertEquals
import org.junit.Test

class MergeKnownPeersTest {

    @Test
    fun dropsThePrimary() {
        val result = mergeKnownPeers(listOf("PRIMARY", "PEER2", "PEER3"), "PRIMARY")
        assertEquals(listOf("PEER2", "PEER3"), result)
    }

    @Test
    fun deduplicates() {
        val result = mergeKnownPeers(listOf("PEER2", "PEER2", "PEER3"), "PRIMARY")
        assertEquals(listOf("PEER2", "PEER3"), result)
    }

    @Test
    fun emptyWhenOnlyPrimaryDiscovered() {
        assertEquals(emptyList<String>(), mergeKnownPeers(listOf("PRIMARY"), "PRIMARY"))
    }

    @Test
    fun emptyWhenNothingDiscovered() {
        assertEquals(emptyList<String>(), mergeKnownPeers(emptyList(), "PRIMARY"))
    }

    @Test
    fun keepsAllWhenPrimaryAbsent() {
        // Connected via a fallback: the primary may not appear in the peer's list.
        val result = mergeKnownPeers(listOf("PEER2", "PEER3"), "PRIMARY")
        assertEquals(listOf("PEER2", "PEER3"), result)
    }
}

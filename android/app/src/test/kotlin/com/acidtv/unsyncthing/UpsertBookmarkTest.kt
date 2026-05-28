package com.acidtv.unsyncthing

import org.junit.Assert.assertEquals
import org.junit.Test

class UpsertBookmarkTest {

    @Test
    fun appendsWhenEmpty() {
        val result = upsertBookmark(emptyList(), Bookmark("Home", "PEER1", "default"))
        assertEquals(listOf(Bookmark("Home", "PEER1", "default")), result)
    }

    @Test
    fun appendsWhenPeerOrFolderDiffers() {
        val existing = listOf(Bookmark("Home", "PEER1", "default"))
        val added = Bookmark("Work", "PEER2", "default")
        assertEquals(listOf(existing[0], added), upsertBookmark(existing, added))
    }

    @Test
    fun updatesNameWhenPeerAndFolderMatch() {
        val existing = listOf(
            Bookmark("Old name", "PEER1", "default"),
            Bookmark("Other", "PEER2", "music"),
        )
        val result = upsertBookmark(existing, Bookmark("New name", "PEER1", "default"))
        assertEquals(
            listOf(
                Bookmark("New name", "PEER1", "default"),
                Bookmark("Other", "PEER2", "music"),
            ),
            result,
        )
    }

    @Test
    fun samePeerDifferentFolderIsSeparateBookmark() {
        val existing = listOf(Bookmark("Docs", "PEER1", "docs"))
        val result = upsertBookmark(existing, Bookmark("Music", "PEER1", "music"))
        assertEquals(2, result.size)
    }
}

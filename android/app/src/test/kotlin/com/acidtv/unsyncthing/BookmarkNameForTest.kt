package com.acidtv.unsyncthing

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class BookmarkNameForTest {

    @Test
    fun returnsNullWhenListIsEmpty() {
        assertNull(bookmarkNameFor(emptyList(), "PEER1", "default"))
    }

    @Test
    fun returnsNullWhenNeitherPeerNorFolderMatches() {
        val bookmarks = listOf(Bookmark("Home", "PEER1", "default"))
        assertNull(bookmarkNameFor(bookmarks, "PEER2", "other"))
    }

    @Test
    fun returnsNullWhenPeerMatchesButFolderDiffers() {
        val bookmarks = listOf(Bookmark("Home", "PEER1", "default"))
        assertNull(bookmarkNameFor(bookmarks, "PEER1", "other"))
    }

    @Test
    fun returnsNullWhenFolderMatchesButPeerDiffers() {
        val bookmarks = listOf(Bookmark("Home", "PEER1", "default"))
        assertNull(bookmarkNameFor(bookmarks, "PEER2", "default"))
    }

    @Test
    fun returnsNameWhenBothMatch() {
        val bookmarks = listOf(Bookmark("My NAS", "PEER1", "default"))
        assertEquals("My NAS", bookmarkNameFor(bookmarks, "PEER1", "default"))
    }

    @Test
    fun returnsFirstMatchAmongMultipleBookmarks() {
        val bookmarks = listOf(
            Bookmark("Docs", "PEER1", "docs"),
            Bookmark("Music", "PEER1", "music"),
            Bookmark("Work", "PEER2", "docs"),
        )
        assertEquals("Music", bookmarkNameFor(bookmarks, "PEER1", "music"))
        assertEquals("Work", bookmarkNameFor(bookmarks, "PEER2", "docs"))
    }
}

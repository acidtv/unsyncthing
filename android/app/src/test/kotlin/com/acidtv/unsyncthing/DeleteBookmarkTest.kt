package com.acidtv.unsyncthing

import org.junit.Assert.assertEquals
import org.junit.Test

class DeleteBookmarkTest {

    @Test
    fun removesMatchingBookmark() {
        val existing = listOf(
            Bookmark("Home", "PEER1", "default"),
            Bookmark("Work", "PEER2", "docs"),
        )
        val result = removeBookmark(existing, "PEER1", "default")
        assertEquals(listOf(Bookmark("Work", "PEER2", "docs")), result)
    }

    @Test
    fun noOpWhenListIsEmpty() {
        val result = removeBookmark(emptyList(), "PEER1", "default")
        assertEquals(emptyList<Bookmark>(), result)
    }

    @Test
    fun noOpWhenPeerIDDoesNotMatch() {
        val existing = listOf(Bookmark("Home", "PEER1", "default"))
        val result = removeBookmark(existing, "PEER2", "default")
        assertEquals(existing, result)
    }

    @Test
    fun noOpWhenFolderIDDoesNotMatch() {
        val existing = listOf(Bookmark("Home", "PEER1", "default"))
        val result = removeBookmark(existing, "PEER1", "music")
        assertEquals(existing, result)
    }

    @Test
    fun requiresBothPeerAndFolderToMatch() {
        val existing = listOf(
            Bookmark("A", "PEER1", "docs"),
            Bookmark("B", "PEER1", "music"),
        )
        val result = removeBookmark(existing, "PEER1", "docs")
        assertEquals(listOf(Bookmark("B", "PEER1", "music")), result)
    }
}

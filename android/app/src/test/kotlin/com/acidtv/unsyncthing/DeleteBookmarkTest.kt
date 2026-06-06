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
        val result = removeBookmark(existing, "Home")
        assertEquals(listOf(Bookmark("Work", "PEER2", "docs")), result)
    }

    @Test
    fun noOpWhenListIsEmpty() {
        val result = removeBookmark(emptyList(), "Home")
        assertEquals(emptyList<Bookmark>(), result)
    }

    @Test
    fun noOpWhenNameDoesNotMatch() {
        val existing = listOf(Bookmark("Home", "PEER1", "default"))
        val result = removeBookmark(existing, "Work")
        assertEquals(existing, result)
    }
}

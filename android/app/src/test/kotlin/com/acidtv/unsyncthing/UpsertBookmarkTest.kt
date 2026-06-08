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
    fun appendsWhenNameDiffers() {
        val existing = listOf(Bookmark("Home", "PEER1", "default"))
        val added = Bookmark("Work", "PEER2", "default")
        assertEquals(listOf(existing[0], added), upsertBookmark(existing, added))
    }

    @Test
    fun replacesWhenNameMatches() {
        val existing = listOf(
            Bookmark("Home", "PEER1", "default"),
            Bookmark("Other", "PEER2", "music"),
        )
        // Same name, changed peer + folder — should replace, not duplicate.
        val result = upsertBookmark(existing, Bookmark("Home", "PEER9", "videos"))
        assertEquals(
            listOf(
                Bookmark("Home", "PEER9", "videos"),
                Bookmark("Other", "PEER2", "music"),
            ),
            result,
        )
    }

    @Test
    fun samePeerDifferentNameIsSeparateBookmark() {
        val existing = listOf(Bookmark("Docs", "PEER1", "docs"))
        val result = upsertBookmark(existing, Bookmark("Music", "PEER1", "music"))
        assertEquals(2, result.size)
    }

    @Test
    fun replaceBookmarkSwapsByOriginalName() {
        val existing = listOf(
            Bookmark("Home", "PEER1", "default"),
            Bookmark("Work", "PEER2", "docs"),
        )
        val result = replaceBookmark(existing, "Home", Bookmark("Renamed", "PEER1", "default"))
        assertEquals(
            listOf(
                Bookmark("Renamed", "PEER1", "default"),
                Bookmark("Work", "PEER2", "docs"),
            ),
            result,
        )
    }

    @Test
    fun replaceBookmarkAppendsWhenNotFound() {
        val existing = listOf(Bookmark("Home", "PEER1", "default"))
        val result = replaceBookmark(existing, "Missing", Bookmark("New", "PEER2", "docs"))
        assertEquals(2, result.size)
    }
}

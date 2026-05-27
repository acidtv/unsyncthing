package com.acidtv.unsyncthing

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class UiStateFileListTest {

    private fun file(name: String, path: String, size: Long = 10, modified: Long = 0) =
        FileEntry(name, path, size, modified, isDir = false)

    private fun dir(name: String, path: String, modified: Long = 0) =
        FileEntry(name, path, 0, modified, isDir = true)

    @Test
    fun emptyAllEntriesReturnsEmpty() {
        val s = UiState.FileList("folder", emptyList())
        assertEquals(emptyList<FileEntry>(), s.entries)
    }

    @Test
    fun rootFlatSortsDirsFirstThenAlphabetical() {
        val s = UiState.FileList("folder", listOf(
            file("zeta.txt", "zeta.txt"),
            file("alpha.txt", "alpha.txt"),
            dir("zulu", "zulu"),
            dir("aardvark", "aardvark"),
        ))
        assertEquals(
            listOf("aardvark", "zulu", "alpha.txt", "zeta.txt"),
            s.entries.map { it.name },
        )
    }

    @Test
    fun fileInSubdirSynthesisesImmediateParentAtRoot() {
        val s = UiState.FileList("folder", listOf(
            file("nested.txt", "sub/nested.txt"),
        ))
        assertEquals(listOf("sub"), s.entries.map { it.name })
        val synth = s.entries.single()
        assertTrue("synthesised entry should be a directory", synth.isDir)
        assertEquals("sub", synth.path)
    }

    @Test
    fun explicitDirEntryPreferredOverSynthesised() {
        val explicit = dir("sub", "sub", modified = 12345L)
        val s = UiState.FileList("folder", listOf(
            file("nested.txt", "sub/nested.txt"),
            explicit,
        ))
        val onlyEntry = s.entries.single()
        assertEquals("sub", onlyEntry.name)
        // Preserved timestamp proves the explicit entry won, not a synthesised stub.
        assertEquals(12345L, onlyEntry.modified)
    }

    @Test
    fun currentDirFiltersAndStripsPrefix() {
        val s = UiState.FileList("folder", listOf(
            file("a.txt", "sub/a.txt"),
            file("b.txt", "sub/b.txt"),
            file("other.txt", "elsewhere/other.txt"),
        ), currentDir = "sub")
        assertEquals(listOf("a.txt", "b.txt"), s.entries.map { it.name })
    }

    @Test
    fun entriesOutsideCurrentDirAreDropped() {
        val s = UiState.FileList("folder", listOf(
            file("inside.txt", "sub/inside.txt"),
            file("outside.txt", "outside.txt"),
            // "subway/..." must NOT match "sub/" prefix.
            file("trap.txt", "subway/trap.txt"),
        ), currentDir = "sub")
        assertEquals(listOf("inside.txt"), s.entries.map { it.name })
    }

    @Test
    fun duplicateNameAppearsOnlyOnce() {
        // Two files share the same immediate child name "x.txt" at root —
        // shouldn't happen in a real index, but the dedup map should still
        // keep the first one.
        val first = file("x.txt", "x.txt", size = 1)
        val second = file("x.txt", "x.txt", size = 2)
        val s = UiState.FileList("folder", listOf(first, second))
        val only = s.entries.single()
        assertEquals(1L, only.size)
    }
}

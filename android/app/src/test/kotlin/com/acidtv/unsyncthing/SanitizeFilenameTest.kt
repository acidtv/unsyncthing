package com.acidtv.unsyncthing

import org.junit.Assert.assertEquals
import org.junit.Test

class SanitizeFilenameTest {

    @Test
    fun stripsDirectoryComponents() {
        assertEquals("file.txt", sanitizeFilename("folder/sub/file.txt"))
    }

    @Test
    fun stripsBackslashDirectoryComponents() {
        assertEquals("b.txt", sanitizeFilename("a\\b.txt"))
    }

    @Test
    fun stripsUnsafeCharacters() {
        assertEquals("abc.txt", sanitizeFilename("a b*c?.txt"))
    }

    @Test
    fun keepsAllowedPunctuation() {
        assertEquals("my_file-1.0.txt", sanitizeFilename("my_file-1.0.txt"))
    }

    @Test
    fun emptyFallsBackToDownload() {
        assertEquals("download", sanitizeFilename(""))
    }

    @Test
    fun dotAndDotDotFallBackToDownload() {
        assertEquals("download", sanitizeFilename("."))
        assertEquals("download", sanitizeFilename(".."))
        assertEquals("download", sanitizeFilename("path/to/.."))
    }

    @Test
    fun allUnsafeInputFallsBackToDownload() {
        assertEquals("download", sanitizeFilename("***???"))
    }
}

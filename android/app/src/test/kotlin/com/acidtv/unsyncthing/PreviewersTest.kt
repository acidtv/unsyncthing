package com.acidtv.unsyncthing

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class PreviewersTest {

    private fun entry(name: String, path: String = name) =
        FileEntry(name = name, path = path, size = 1, modified = 0, isDir = false)

    @Test
    fun recognisesCommonTextExtensions() {
        for (n in listOf("notes.txt", "README.md", "data.json", "main.go", "App.kt", "style.css")) {
            assertEquals("expected TEXT for $n", PreviewType.TEXT, Previewers.typeFor(entry(n)))
        }
    }

    @Test
    fun extensionMatchIsCaseInsensitive() {
        assertEquals(PreviewType.TEXT, Previewers.typeFor(entry("LOG.TXT")))
        assertEquals(PreviewType.TEXT, Previewers.typeFor(entry("Main.KT")))
    }

    @Test
    fun recognisesExtensionlessTextFilenames() {
        assertEquals(PreviewType.TEXT, Previewers.typeFor(entry("Makefile")))
        assertEquals(PreviewType.TEXT, Previewers.typeFor(entry("LICENSE")))
        assertEquals(PreviewType.TEXT, Previewers.typeFor(entry("Dockerfile")))
    }

    @Test
    fun recognisesDotfiles() {
        assertEquals(PreviewType.TEXT, Previewers.typeFor(entry(".gitignore")))
    }

    @Test
    fun recognisesImageExtensions() {
        for (n in listOf("photo.png", "pic.JPG", "scan.jpeg", "anim.gif",
                         "img.webp", "old.bmp", "phone.heic", "x.heif")) {
            assertEquals("expected IMAGE for $n", PreviewType.IMAGE, Previewers.typeFor(entry(n)))
        }
    }

    @Test
    fun rejectsBinaryAndUnknownTypes() {
        assertNull(Previewers.typeFor(entry("archive.zip")))
        assertNull(Previewers.typeFor(entry("noextension")))
        assertNull(Previewers.typeFor(entry("clip.mp4")))
    }

    @Test
    fun usesBasenameNotDirectoryPath() {
        // A directory component that looks like an extension must not fool the
        // String overload, which extracts the basename itself.
        assertEquals(PreviewType.TEXT, Previewers.typeFor("a.png/sub/file.txt"))
        assertNull(Previewers.typeFor("docs.txt/movie.mp4"))
    }
}

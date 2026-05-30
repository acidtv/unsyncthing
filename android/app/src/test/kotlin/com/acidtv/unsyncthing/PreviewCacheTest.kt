package com.acidtv.unsyncthing

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

class PreviewCacheTest {

    @get:Rule
    val tmp = TemporaryFolder()

    private fun cache() = PreviewCache(File(tmp.root, "preview"))

    @Test
    fun keyIsDeterministic() {
        val c = cache()
        assertEquals(
            c.key("folder", "dir/a.txt", 100, 200),
            c.key("folder", "dir/a.txt", 100, 200),
        )
    }

    @Test
    fun keyChangesWithModifiedOrSize() {
        val c = cache()
        val base = c.key("folder", "a.txt", 100, 200)
        assertNotEquals(base, c.key("folder", "a.txt", 101, 200))
        assertNotEquals(base, c.key("folder", "a.txt", 100, 201))
    }

    @Test
    fun keyDistinguishesSameBasenameInDifferentDirs() {
        val c = cache()
        assertNotEquals(
            c.key("folder", "x/a.txt", 1, 1),
            c.key("folder", "y/a.txt", 1, 1),
        )
    }

    @Test
    fun keyContainsNoPathSeparators() {
        val key = cache().key("folder", "../../etc/passwd", 1, 1)
        assertFalse(key.contains('/'))
        assertFalse(key.contains(".."))
    }

    @Test
    fun isFreshHonoursTtl() {
        val c = cache().also { it.ensureDir() }
        val f = c.resolve("k").apply { writeText("hi") }
        val now = f.lastModified()
        assertTrue(c.isFresh(f, ttlMs = 1000, now = now + 500))
        assertFalse(c.isFresh(f, ttlMs = 1000, now = now + 1500))
    }

    @Test
    fun sweepDeletesOnlyExpiredEntries() {
        val c = cache().also { it.ensureDir() }
        val now = System.currentTimeMillis()
        val fresh = c.resolve("fresh").apply { writeText("a"); setLastModified(now) }
        val stale = c.resolve("stale").apply { writeText("b"); setLastModified(now - 10_000) }

        val removed = c.sweep(ttlMs = 5_000, now = now)

        assertEquals(1, removed)
        assertTrue(fresh.exists())
        assertFalse(stale.exists())
    }

    @Test
    fun sweepOnMissingDirIsNoOp() {
        assertEquals(0, cache().sweep(ttlMs = 1000))
    }
}

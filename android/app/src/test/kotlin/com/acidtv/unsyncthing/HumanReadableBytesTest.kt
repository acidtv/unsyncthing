package com.acidtv.unsyncthing

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class HumanReadableBytesTest {

    @Test
    fun zeroBytes() {
        assertEquals("0 B", humanReadableBytes(0))
    }

    @Test
    fun belowThousandStaysInBytes() {
        assertEquals("999 B", humanReadableBytes(999))
    }

    @Test
    fun thousandRollsOverToKilobytes() {
        // 1000 / 1000 = 1.0 kB; decimal separator depends on locale.
        assertMatchesNumberWithUnit(humanReadableBytes(1000), expectedNumber = 1.0, unit = "kB")
    }

    @Test
    fun edgeJustBelowMegabyteStaysInKilobytes() {
        // The loop's condition is `>= 999_950`, so 999_949 must still be kB.
        val s = humanReadableBytes(999_949)
        assertTrue("expected kB tier, got: $s", s.endsWith(" kB"))
    }

    @Test
    fun edgeAtMegabyteRollsOver() {
        val s = humanReadableBytes(999_950)
        assertTrue("expected MB tier, got: $s", s.endsWith(" MB"))
    }

    @Test
    fun megabyteValue() {
        assertMatchesNumberWithUnit(humanReadableBytes(5_000_000), expectedNumber = 5.0, unit = "MB")
    }

    @Test
    fun gigabyteValue() {
        assertMatchesNumberWithUnit(humanReadableBytes(2_000_000_000), expectedNumber = 2.0, unit = "GB")
    }

    // Locale-independent check: parse the number portion, accepting `.` or `,` as the
    // decimal separator. Builders with non-en_US default locale format `%.1f` as `1,0`.
    private fun assertMatchesNumberWithUnit(actual: String, expectedNumber: Double, unit: String) {
        assertTrue("expected to end with ' $unit', got: $actual", actual.endsWith(" $unit"))
        val numberPart = actual.removeSuffix(" $unit").replace(',', '.')
        val parsed = numberPart.toDoubleOrNull()
        assertTrue("could not parse number from: $actual", parsed != null)
        assertEquals(expectedNumber, parsed!!, 0.05)
    }
}

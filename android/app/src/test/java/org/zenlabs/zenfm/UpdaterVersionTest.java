package org.zenlabs.zenfm;

import static org.junit.Assert.assertTrue;

import org.junit.Test;

public final class UpdaterVersionTest {
    @Test public void stableReleaseWinsOverMatchingBeta() {
        assertTrue(ZenFMUpdater.compareVersions("1.3.0", "1.3.0-beta10") > 0);
    }

    @Test public void newerBetaNumbersUseNumericOrder() {
        assertTrue(ZenFMUpdater.compareVersions("1.3.0-beta10", "1.3.0-beta2") > 0);
    }

    @Test public void newerBetaBaseWinsOverOlderStable() {
        assertTrue(ZenFMUpdater.compareVersions("1.4.0-beta1", "1.3.0") > 0);
    }
}

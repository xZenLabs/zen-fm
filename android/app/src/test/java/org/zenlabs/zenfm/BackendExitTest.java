package org.zenlabs.zenfm;

import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;
import org.junit.Test;

public final class BackendExitTest {
    @Test public void cleanConfiguredExitIsAnIdleTimeout() {
        assertTrue(BackendExit.isIdleTimeout(0, "30m", false));
        assertTrue(BackendExit.isIdleTimeout(0, "720m", false));
    }

    @Test public void disabledFailedOrDeliberateExitsAreNotIdleTimeouts() {
        assertFalse(BackendExit.isIdleTimeout(0, "0", false));
        assertFalse(BackendExit.isIdleTimeout(1, "30m", false));
        assertFalse(BackendExit.isIdleTimeout(0, "30m", true));
    }
}

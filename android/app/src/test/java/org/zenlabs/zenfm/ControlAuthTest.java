package org.zenlabs.zenfm;

import static org.junit.Assert.assertEquals;
import org.junit.Test;

public final class ControlAuthTest {
    private static final String TOKEN = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";

    @Test public void firstExplicitStartRequiresUserConfirmation() {
        assertEquals(2, ControlAuth.authorizePairing(null, "start", TOKEN));
    }

    @Test public void nonStartCommandsCannotCreateFirstPairing() {
        assertEquals(0, ControlAuth.authorizePairing(null, "stop", TOKEN));
        assertEquals(0, ControlAuth.authorizePairing(null, "status", TOKEN));
        assertEquals(0, ControlAuth.authorizePairing(null, "update", TOKEN));
    }

    @Test public void pairedTokenMustMatchInConstantTimePath() {
        assertEquals(1, ControlAuth.authorizePairing(TOKEN, "stop", TOKEN));
        assertEquals(0, ControlAuth.authorizePairing(TOKEN, "stop",
            "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"));
    }

    @Test public void sharedStorageTokenCannotSilentlyAuthorizeSensitiveCommands() {
        assertEquals(true, ControlAuth.requiresUserConfirmation("start"));
        assertEquals(true, ControlAuth.requiresUserConfirmation("reset"));
        assertEquals(true, ControlAuth.requiresUserConfirmation("update"));
        assertEquals(false, ControlAuth.requiresUserConfirmation("stop"));
        assertEquals(false, ControlAuth.requiresUserConfirmation("status"));
    }
}

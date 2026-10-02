package org.zenlabs.zenfm;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertNull;

import org.junit.Test;

public final class PeerNotificationTest {
    @Test public void acceptsOnlyBackendPeerNotificationLines() {
        assertEquals(Boolean.TRUE, ZenFMService.peerNotification("ZenFM peer incoming pending"));
        assertEquals(Boolean.FALSE, ZenFMService.peerNotification("ZenFM peer incoming clear"));
        assertNull(ZenFMService.peerNotification("debug: ZenFM peer incoming pending"));
        assertNull(ZenFMService.peerNotification("ZenFM peer incoming pending forged"));
    }
}

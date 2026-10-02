package org.zenlabs.zenfm;

import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;
import org.junit.Test;

public final class PeerCommandTest {
    @Test public void bridgeAcceptsOnlyBoundedPeerControlFields() {
        String id = "0123456789abcdef0123456789abcdef";
        String fingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
        assertTrue(ZenFMService.validPeerCommand("peer-discover " + id));
        assertTrue(ZenFMService.validPeerCommand("peer-send " + id + " " + fingerprint + " L3NkY2FyZC9Cb29rcw"));
        assertTrue(ZenFMService.validPeerCommand("peer-accept " + id));
        assertTrue(ZenFMService.validPeerCommand("peer-decline " + id));
        assertTrue(ZenFMService.validPeerCommand("peer-cancel " + id));
        assertTrue(ZenFMService.validPeerCommand("peer-status"));

        assertFalse(ZenFMService.validPeerCommand("peer-send " + id + " " + fingerprint + " /sdcard/Books"));
        assertFalse(ZenFMService.validPeerCommand("peer-accept ../../forged"));
        assertFalse(ZenFMService.validPeerCommand("peer-unknown " + id));
        assertFalse(ZenFMService.validPeerCommand("peer-status extra"));
        assertFalse(ZenFMService.validPeerCommand("peer-status extra extra extra extra"));
        assertFalse(ZenFMService.validPeerCommand("peer-send "
            + new String(new char[8200]).replace('\0', 'a')));
    }
}

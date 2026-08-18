package org.zenlabs.zenfm;

import static org.junit.Assert.assertEquals;
import org.junit.Test;

public final class CommandRequestTest {
    private static final String ID = "0123456789abcdef0123456789abcdef";

    @Test public void terminalStatusIsBoundToFreshRequest() {
        assertEquals("ok running https://127.0.0.1:8443 - request=" + ID,
            CommandRequest.status("ok running https://127.0.0.1:8443 -", ID));
        assertEquals("stopped request=" + ID, CommandRequest.status("stopped", ID));
    }

    @Test public void coldStatusWithoutLiveControlSocketReportsStopped() {
        assertEquals("stopped request=" + ID, CommandRequest.liveStatus(null, ID));
        assertEquals("stopped request=" + ID,
            CommandRequest.liveStatus("ok stopping", ID));
    }

    @Test public void liveControlStatusIsBoundToFreshRequest() {
        assertEquals("ok running https://127.0.0.1:8443 - request=" + ID,
            CommandRequest.liveStatus("ok running https://127.0.0.1:8443 -", ID));
    }

    @Test(expected = IllegalArgumentException.class)
    public void shortRequestIdsAreRejected() { CommandRequest.require("0123"); }

    @Test(expected = IllegalArgumentException.class)
    public void nonHexRequestIdsAreRejected() {
        CommandRequest.require("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz");
    }

    @Test public void customAutoStopMinutesAreAccepted() {
        assertEquals("0", CommandRequest.requireAutoStop("0"));
        assertEquals("30m", CommandRequest.requireAutoStop("30m"));
        assertEquals("45m", CommandRequest.requireAutoStop("45m"));
        assertEquals("720m", CommandRequest.requireAutoStop("720m"));
    }

    @Test(expected = IllegalArgumentException.class)
    public void fractionalAutoStopIsRejected() { CommandRequest.requireAutoStop("1.5m"); }

    @Test(expected = IllegalArgumentException.class)
    public void oversizedAutoStopIsRejected() { CommandRequest.requireAutoStop("721m"); }
}

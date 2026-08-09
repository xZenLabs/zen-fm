package org.zenlabs.zenfm;

/** Request identifiers prevent KOReader from accepting a stale mirrored lifecycle status. */
final class CommandRequest {
    private CommandRequest() {}

    static String require(String value) {
        if (value == null || !value.matches("[0-9a-f]{32}")) {
            throw new IllegalArgumentException("request_id");
        }
        return value;
    }

    static String status(String value, String requestId) {
        return requestId == null || requestId.isEmpty() ? value : value + " request=" + require(requestId);
    }

    static boolean isRunning(String controlResponse) {
        return controlResponse != null && controlResponse.startsWith("ok running ");
    }

    static String liveStatus(String controlResponse, String requestId) {
        return status(isRunning(controlResponse) ? controlResponse : "stopped", requestId);
    }
}

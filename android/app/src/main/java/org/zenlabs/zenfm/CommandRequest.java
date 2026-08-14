package org.zenlabs.zenfm;

/** Request identifiers prevent KOReader from accepting a stale mirrored lifecycle status. */
final class CommandRequest {
    private static final int MAX_AUTO_STOP_MINUTES = 12 * 60;

    private CommandRequest() {}

    static String require(String value) {
        if (value == null || !value.matches("[0-9a-f]{32}")) {
            throw new IllegalArgumentException("request_id");
        }
        return value;
    }

    static String requireAutoStop(String value) {
        if ("0".equals(value)) return value;
        if (value == null || !value.matches("[1-9][0-9]{0,2}m")) {
            throw new IllegalArgumentException("auto_stop");
        }
        int minutes = Integer.parseInt(value.substring(0, value.length() - 1));
        if (minutes > MAX_AUTO_STOP_MINUTES) throw new IllegalArgumentException("auto_stop");
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

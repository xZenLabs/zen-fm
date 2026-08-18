package org.zenlabs.zenfm;

final class BackendExit {
    private BackendExit() {}

    static boolean isIdleTimeout(int status, String autoStop, boolean deliberatelyStopped) {
        return status == 0 && autoStop != null && !"0".equals(autoStop) && !deliberatelyStopped;
    }
}

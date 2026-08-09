package org.zenlabs.zenfm;

/** Pure update state transitions, kept Android-free so the safety rules can be unit tested. */
final class UpdateState {
    enum Phase { READY, INSTALLER_OPENED, VERIFYING, RECOVERY_REQUIRED }
    enum Outcome { NONE, INSTALL_PENDING, PENDING_APK_MISSING, VERIFY_PENDING, RECOVERY_REQUIRED }
    enum BackendGate { NORMAL, VERIFY_UPDATE, BLOCK_FOR_RECOVERY }

    static final class Record {
        final Phase phase;
        final String sourceVersion;
        final long sourceVersionCode;
        final String targetVersion;
        final long targetVersionCode;
        final String sha256;
        final long size;
        final String fileName;
        final String home;
        final String failure;

        Record(Phase phase, String sourceVersion, long sourceVersionCode,
            String targetVersion, long targetVersionCode, String sha256, long size,
            String fileName, String home, String failure) {
            this.phase = phase;
            this.sourceVersion = sourceVersion;
            this.sourceVersionCode = sourceVersionCode;
            this.targetVersion = targetVersion;
            this.targetVersionCode = targetVersionCode;
            this.sha256 = sha256;
            this.size = size;
            this.fileName = fileName;
            this.home = home;
            this.failure = failure == null ? "" : failure;
        }

        Record withPhase(Phase next, String message) {
            return new Record(next, sourceVersion, sourceVersionCode, targetVersion,
                targetVersionCode, sha256, size, fileName, home, message);
        }

        boolean valid() {
            return phase != null && sourceVersion != null && !sourceVersion.isEmpty()
                && sourceVersionCode >= 0 && targetVersion != null && !targetVersion.isEmpty()
                && targetVersionCode > sourceVersionCode && sha256 != null
                && sha256.matches("[0-9a-f]{64}") && size > 0 && fileName != null
                && fileName.matches("zenfm-update-[A-Za-z0-9._-]+\\.apk")
                && home != null && home.startsWith("/");
        }
    }

    private UpdateState() {}

    static Outcome reconcile(Record record, String installedVersion,
        long installedVersionCode, boolean apkAvailable) {
        if (record == null) return Outcome.NONE;
        if (!record.valid() || record.phase == Phase.RECOVERY_REQUIRED) {
            return Outcome.RECOVERY_REQUIRED;
        }
        if (samePackage(record.targetVersion, record.targetVersionCode,
                installedVersion, installedVersionCode)) {
            return Outcome.VERIFY_PENDING;
        }
        if (samePackage(record.sourceVersion, record.sourceVersionCode,
                installedVersion, installedVersionCode)) {
            return apkAvailable ? Outcome.INSTALL_PENDING : Outcome.PENDING_APK_MISSING;
        }
        return Outcome.RECOVERY_REQUIRED;
    }

    static BackendGate backendGate(Outcome outcome) {
        if (outcome == Outcome.VERIFY_PENDING) return BackendGate.VERIFY_UPDATE;
        if (outcome == Outcome.RECOVERY_REQUIRED) return BackendGate.BLOCK_FOR_RECOVERY;
        return BackendGate.NORMAL;
    }

    private static boolean samePackage(String expectedVersion, long expectedCode,
        String actualVersion, long actualCode) {
        return expectedCode == actualCode && expectedVersion.equals(actualVersion);
    }
}

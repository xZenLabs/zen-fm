package org.zenlabs.zenfm;

import static org.junit.Assert.assertEquals;
import org.junit.Test;

public final class UpdateStateTest {
    private static final UpdateState.Record READY = new UpdateState.Record(
        UpdateState.Phase.READY, "1.0.0", 1000, "1.1.0", 1100,
        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        1234, "zenfm-update-123.apk", "/storage/emulated/0/.adds/zenfm", "");

    @Test public void sourcePackageWithVerifiedApkResumesInstaller() {
        assertEquals(UpdateState.Outcome.INSTALL_PENDING,
            UpdateState.reconcile(READY, "1.0.0", 1000, true));
    }

    @Test public void installedTargetRequiresBackendVerification() {
        assertEquals(UpdateState.Outcome.VERIFY_PENDING,
            UpdateState.reconcile(READY, "1.1.0", 1100, false));
        assertEquals(UpdateState.BackendGate.VERIFY_UPDATE,
            UpdateState.backendGate(UpdateState.Outcome.VERIFY_PENDING));
    }

    @Test public void missingPendingApkDoesNotLockOutKnownSourceVersion() {
        assertEquals(UpdateState.Outcome.PENDING_APK_MISSING,
            UpdateState.reconcile(READY, "1.0.0", 1000, false));
        assertEquals(UpdateState.BackendGate.NORMAL,
            UpdateState.backendGate(UpdateState.Outcome.PENDING_APK_MISSING));
    }

    @Test public void unexpectedInstalledPackageFailsClosed() {
        assertEquals(UpdateState.Outcome.RECOVERY_REQUIRED,
            UpdateState.reconcile(READY, "1.2.0", 1200, true));
    }

    @Test public void explicitRecoveryStateCannotStartBackend() {
        UpdateState.Record recovery = READY.withPhase(UpdateState.Phase.RECOVERY_REQUIRED,
            "new backend did not become healthy");
        assertEquals(UpdateState.Outcome.RECOVERY_REQUIRED,
            UpdateState.reconcile(recovery, "1.1.0", 1100, true));
        assertEquals(UpdateState.BackendGate.BLOCK_FOR_RECOVERY,
            UpdateState.backendGate(UpdateState.Outcome.RECOVERY_REQUIRED));
    }

    @Test public void invalidOrDowngradeRecordFailsClosed() {
        UpdateState.Record downgrade = new UpdateState.Record(UpdateState.Phase.READY,
            "2.0.0", 2000, "1.9.0", 1900, READY.sha256, READY.size,
            READY.fileName, READY.home, "");
        assertEquals(UpdateState.Outcome.RECOVERY_REQUIRED,
            UpdateState.reconcile(downgrade, "2.0.0", 2000, true));
    }

    @Test public void noJournalUsesNormalBackendPath() {
        assertEquals(UpdateState.Outcome.NONE,
            UpdateState.reconcile(null, "1.0.0", 1000, false));
        assertEquals(UpdateState.BackendGate.NORMAL,
            UpdateState.backendGate(UpdateState.Outcome.NONE));
    }
}

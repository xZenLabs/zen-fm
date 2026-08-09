package org.zenlabs.zenfm;

import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import org.junit.Test;

public final class ManifestPolicyTest {
    @Test public void longRunningLocalServerUsesDeclaredSpecialUseType() throws Exception {
        String manifest = new String(Files.readAllBytes(source("src/main/AndroidManifest.xml")),
            StandardCharsets.UTF_8);
        assertTrue(manifest.contains("android.permission.FOREGROUND_SERVICE_SPECIAL_USE"));
        assertTrue(manifest.contains("android:foregroundServiceType=\"specialUse\""));
        assertTrue(manifest.contains("android.app.PROPERTY_SPECIAL_USE_FGS_SUBTYPE"));
        assertTrue(manifest.contains("Owner-requested local KOReader file server"));
        assertTrue(manifest.contains("android:requestLegacyExternalStorage=\"true\""));
        assertTrue(manifest.contains("android:dataExtractionRules=\"@xml/backup_rules\""));
        assertTrue(manifest.contains("android:fullBackupContent=\"false\""));
        assertTrue(manifest.contains("android:icon=\"@drawable/zenfm_icon\""));
        assertTrue(Files.size(source("src/main/res/drawable-nodpi/zenfm_icon.png")) > 0);
        assertFalse(manifest.contains("android.permission.FOREGROUND_SERVICE_DATA_SYNC"));
        assertFalse(manifest.contains("android:foregroundServiceType=\"dataSync\""));

        String backupRules = new String(Files.readAllBytes(source("src/main/res/xml/backup_rules.xml")),
            StandardCharsets.UTF_8);
        assertTrue(backupRules.contains("<cloud-backup>"));
        assertTrue(backupRules.contains("<device-transfer>"));
        assertTrue(backupRules.contains("domain=\"sharedpref\""));

        String service = new String(Files.readAllBytes(source(
            "src/main/java/org/zenlabs/zenfm/ZenFMService.java")), StandardCharsets.UTF_8);
        assertTrue(service.contains("ServiceInfo.FOREGROUND_SERVICE_TYPE_MANIFEST"));
        assertTrue(service.contains("verifyBundledBackendVersion(expectedUpdateVersion)"));
        assertTrue(service.contains("ACTION_STATUS"));
        assertTrue(service.contains("CommandRequest.liveStatus(control(\"status\"), requestId)"));
        assertTrue(service.contains("CompanionLog.status(this, home, \"stopping\")"));
        assertTrue(service.contains("Config saved = config == null ? Config.load(this) : config"));

        String build = new String(Files.readAllBytes(source("build.gradle")), StandardCharsets.UTF_8);
        assertTrue(build.contains("inputs.file(rootProject.file('../VERSION'))"));
        assertTrue(build.contains("inputs.file(rootProject.file('../go.mod'))"));
        assertTrue(build.contains("inputs.file(rootProject.file('../go.sum'))"));
        assertTrue(build.contains("jniLibs { useLegacyPackaging true }"));
    }

    private static Path source(String relative) {
        Path fromRoot = Paths.get("app").resolve(relative);
        return Files.exists(fromRoot) ? fromRoot : Paths.get(relative);
    }
}

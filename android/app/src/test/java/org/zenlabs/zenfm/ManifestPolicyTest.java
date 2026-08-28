package org.zenlabs.zenfm;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNotNull;
import static org.junit.Assert.assertTrue;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import javax.xml.parsers.DocumentBuilderFactory;
import org.junit.Test;
import org.w3c.dom.Document;
import org.w3c.dom.Element;
import org.w3c.dom.NodeList;

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
        assertTrue(manifest.contains("android.intent.action.MAIN"));
        assertTrue(manifest.contains("android.intent.category.LAUNCHER"));
        assertFalse(manifest.contains("android:scheme=\"zenfm\""));
        assertTrue(Files.size(source("src/main/res/drawable-nodpi/zenfm_icon.png")) > 0);
        assertFalse(Files.exists(source("src/main/res/drawable-night-nodpi/zenfm_icon.png")));
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
        assertTrue(service.contains("CommandRequest.status(\"idle_stopped\""));
        assertTrue(service.contains("android.os.Process.killProcess(android.os.Process.myPid())"));

        String activity = new String(Files.readAllBytes(source(
            "src/main/java/org/zenlabs/zenfm/ZenFMActivity.java")), StandardCharsets.UTF_8);
        assertTrue(activity.contains("Intent.ACTION_MAIN.equals(incoming.getAction())"));
        assertTrue(activity.contains("Settings.ACTION_APPLICATION_DETAILS_SETTINGS"));
        assertTrue(activity.contains("showConfirmation(\"Start ZenFM?\""));
        assertTrue(activity.contains("This first approval links the companion to KOReader."));
        assertFalse(activity.contains("Pairing code:"));

        Document document = DocumentBuilderFactory.newInstance().newDocumentBuilder()
            .parse(source("src/main/AndroidManifest.xml").toFile());
        Element commandActivity = component(document, "activity", ".ZenFMActivity");
        assertNotNull(commandActivity);
        assertEquals("true", commandActivity.getAttribute("android:exported"));
        assertEquals(0, commandActivity.getElementsByTagName("intent-filter").getLength());
        Element launcher = component(document, "activity-alias", ".ZenFMLauncher");
        assertNotNull(launcher);
        assertEquals("true", launcher.getAttribute("android:exported"));
        assertEquals(".ZenFMActivity", launcher.getAttribute("android:targetActivity"));
        assertTrue(hasNamedChild(launcher, "action", "android.intent.action.MAIN"));
        assertTrue(hasNamedChild(launcher, "category", "android.intent.category.LAUNCHER"));

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

    private static Element component(Document document, String tag, String name) {
        NodeList nodes = document.getElementsByTagName(tag);
        for (int index = 0; index < nodes.getLength(); index++) {
            Element element = (Element) nodes.item(index);
            if (name.equals(element.getAttribute("android:name"))) return element;
        }
        return null;
    }

    private static boolean hasNamedChild(Element parent, String tag, String name) {
        NodeList nodes = parent.getElementsByTagName(tag);
        for (int index = 0; index < nodes.getLength(); index++) {
            if (name.equals(((Element) nodes.item(index)).getAttribute("android:name"))) return true;
        }
        return false;
    }
}

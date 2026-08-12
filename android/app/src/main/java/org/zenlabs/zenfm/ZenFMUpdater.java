package org.zenlabs.zenfm;

import android.app.Activity;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageInfo;
import android.content.pm.PackageManager;
import android.content.pm.Signature;
import android.net.Uri;
import android.os.Build;
import android.provider.Settings;
import org.json.JSONArray;
import org.json.JSONObject;
import java.lang.ref.WeakReference;
import java.io.BufferedInputStream;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.security.MessageDigest;
import java.util.Arrays;
import java.util.Locale;
import java.util.concurrent.atomic.AtomicBoolean;

final class ZenFMUpdater {
    static final String ACTION_CONTINUE_INSTALL = "org.zenlabs.zenfm.action.CONTINUE_UPDATE_INSTALL";
    static final int INSTALLER_STARTED = 1;
    static final int INSTALL_PERMISSION_REQUIRED = 2;
    static final int INSTALLED_AWAITING_HEALTH = 3;
    static final int INSTALL_RETRYABLE_FAILURE = 4;
    static final int INSTALL_RECOVERY_REQUIRED = 5;

    private static final String RELEASES = "https://api.github.com/repos/xZenLabs/zen-fm/releases?per_page=20";
    private static final long MAXIMUM_APK = 200L * 1024L * 1024L;
    private static final AtomicBoolean RUNNING = new AtomicBoolean();
    private ZenFMUpdater() {}

    static void start(ZenFMActivity activity, final String home) {
        if (!RUNNING.compareAndSet(false, true)) {
            activity.onUpdateFailed("an update check is already running");
            return;
        }
        final WeakReference<ZenFMActivity> owner = new WeakReference<ZenFMActivity>(activity);
        final Context application = activity.getApplicationContext();
        CompanionLog.updateStatus(application, home, "checking");
        new Thread(new Runnable() {
            @Override public void run() {
                File destination = null;
                try {
                    Release release = latest(application);
                    destination = File.createTempFile("zenfm-update-", ".apk", application.getCacheDir());
                    download(release, destination);
                    PackageIdentity source = installedIdentity(application);
                    PackageIdentity target = validate(application, destination, release.version);
                    if (target.versionCode <= source.versionCode) {
                        throw new IOException("APK version code is not newer than the installed companion");
                    }
                    UpdateState.Record record = new UpdateState.Record(UpdateState.Phase.READY,
                        source.version, source.versionCode, target.version, target.versionCode,
                        release.sha256, release.size, destination.getName(), home, "");
                    if (!UpdateJournal.save(application, record)) {
                        throw new IOException("could not persist the verified update hand-off");
                    }
                    destination = null; // The journal owns and preserves this file across package replacement.
                    CompanionLog.updateStatus(application, home, "ready_to_install " + target.version);
                    deliverReady(owner);
                } catch (Exception error) {
                    if (destination != null) destination.delete();
                    String detail = clean(error.getMessage());
                    CompanionLog.write(application, home, "Companion update failed: " + detail);
                    CompanionLog.updateStatus(application, home, "failed " + detail);
                    deliverFailure(owner, detail);
                } finally {
                    RUNNING.set(false);
                }
            }
        }, "ZenFMUpdater").start();
    }

    static UpdateState.Outcome inspect(Context context) {
        UpdateState.Record record = UpdateJournal.load(context);
        if (record == null) return UpdateState.Outcome.NONE;
        try {
            PackageIdentity installed = installedIdentity(context);
            File apk = UpdateJournal.cacheFile(context, record);
            boolean available = apk != null && apk.isFile() && apk.length() == record.size;
            UpdateState.Outcome outcome = UpdateState.reconcile(record, installed.version,
                installed.versionCode, available);
            if (outcome == UpdateState.Outcome.PENDING_APK_MISSING) {
                CompanionLog.write(context, record.home,
                    "Discarded a pending companion update because Android removed its cached APK.");
                CompanionLog.updateStatus(context, record.home, "download_missing_retry_update");
                UpdateJournal.clear(context);
                return UpdateState.Outcome.NONE;
            }
            if (outcome == UpdateState.Outcome.RECOVERY_REQUIRED
                && record.phase != UpdateState.Phase.RECOVERY_REQUIRED) {
                requireRecovery(context, "update state no longer matches the installed companion");
            }
            return outcome;
        } catch (Exception error) {
            requireRecovery(context, "could not inspect the installed companion: " + clean(error.getMessage()));
            return UpdateState.Outcome.RECOVERY_REQUIRED;
        }
    }

    static UpdateState.BackendGate prepareBackendStart(Context context) {
        UpdateState.BackendGate gate = UpdateState.backendGate(inspect(context));
        if (gate == UpdateState.BackendGate.VERIFY_UPDATE
            && !UpdateJournal.changePhase(context, UpdateState.Phase.VERIFYING, "")) {
            requireRecovery(context, "could not persist the backend health check");
            return UpdateState.BackendGate.BLOCK_FOR_RECOVERY;
        }
        return gate;
    }

    static boolean backendHealthy(Context context) {
        UpdateState.Record record = UpdateJournal.load(context);
        if (record == null || inspect(context) != UpdateState.Outcome.VERIFY_PENDING) return false;
        if (!UpdateJournal.clear(context)) {
            requireRecovery(context, "could not commit the successful backend health check");
            return false;
        }
        CompanionLog.write(context, record.home,
            "Verified bundled backend health after companion update to " + record.targetVersion + ".");
        CompanionLog.updateStatus(context, record.home, "verified " + record.targetVersion);
        return true;
    }

    static void backendFailed(Context context, String reason) {
        requireRecovery(context, "new bundled backend failed its startup health check: " + clean(reason));
    }

    static String pendingTargetVersion(Context context) {
        UpdateState.Record record = UpdateJournal.load(context);
        return record == null ? null : record.targetVersion;
    }

    static int continueInstall(Activity activity, int permissionRequest, int installerRequest) {
        UpdateState.Outcome outcome = inspect(activity);
        UpdateState.Record record = UpdateJournal.load(activity);
        if (outcome == UpdateState.Outcome.VERIFY_PENDING) {
            if (record != null) CompanionLog.updateStatus(activity, record.home,
                "installed_awaiting_backend_health " + record.targetVersion);
            return INSTALLED_AWAITING_HEALTH;
        }
        if (outcome != UpdateState.Outcome.INSTALL_PENDING || record == null) {
            return outcome == UpdateState.Outcome.RECOVERY_REQUIRED
                ? INSTALL_RECOVERY_REQUIRED : INSTALL_RETRYABLE_FAILURE;
        }
        try {
            validatePending(activity, record);
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
                && !activity.getPackageManager().canRequestPackageInstalls()) {
                CompanionLog.updateStatus(activity, record.home,
                    "install_permission_required " + record.targetVersion);
                Intent settings = new Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                    Uri.parse("package:" + activity.getPackageName()));
                activity.startActivityForResult(settings, permissionRequest);
                return INSTALL_PERMISSION_REQUIRED;
            }
            if (!UpdateJournal.changePhase(activity, UpdateState.Phase.INSTALLER_OPENED, "")) {
                throw new IOException("could not persist the Package Installer hand-off");
            }
            Uri apk = Uri.parse("content://" + activity.getPackageName() + ".updates/update.apk");
            Intent installer = new Intent(Intent.ACTION_VIEW);
            installer.setDataAndType(apk, "application/vnd.android.package-archive");
            installer.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
            activity.startActivityForResult(installer, installerRequest);
            CompanionLog.updateStatus(activity, record.home, "installer_opened " + record.targetVersion);
            return INSTALLER_STARTED;
        } catch (Exception error) {
            String detail = clean(error.getMessage());
            UpdateJournal.changePhase(activity, UpdateState.Phase.READY, "");
            CompanionLog.write(activity, record.home, "Could not continue companion install: " + detail);
            CompanionLog.updateStatus(activity, record.home, "install_retry_required " + detail);
            return INSTALL_RETRYABLE_FAILURE;
        }
    }

    static boolean discardPending(Context context) {
        UpdateState.Record record = UpdateJournal.load(context);
        if (record == null) return true;
        if (!UpdateJournal.clear(context)) return false;
        CompanionLog.updateStatus(context, record.home, "cancelled");
        CompanionLog.write(context, record.home, "Discarded the pending companion update.");
        return true;
    }

    static String recoveryMessage(Context context) {
        UpdateState.Record record = UpdateJournal.load(context);
        String reason = record == null || record.failure.isEmpty()
            ? "The companion update could not be verified." : record.failure;
        return reason + "\n\nZenFM has stopped the server. Android does not allow an ordinary "
            + "sideloaded app to silently downgrade itself. Install a newer signed ZenFM update, "
            + "or manually reinstall a previously trusted APK. Android may require uninstalling "
            + "the app first, which deletes its private ZenFM state.";
    }

    static File providerFile(Context context) {
        UpdateState.Record record = UpdateJournal.load(context);
        File file = UpdateJournal.cacheFile(context, record);
        boolean installable = record != null && (record.phase == UpdateState.Phase.READY
            || record.phase == UpdateState.Phase.INSTALLER_OPENED);
        return installable && file != null && file.isFile() && file.length() == record.size ? file : null;
    }

    private static void requireRecovery(Context context, String reason) {
        UpdateState.Record record = UpdateJournal.load(context);
        if (record == null) return;
        String detail = clean(reason);
        UpdateJournal.changePhase(context, UpdateState.Phase.RECOVERY_REQUIRED, detail);
        CompanionLog.write(context, record.home, "Companion update recovery required: " + detail);
        CompanionLog.updateStatus(context, record.home, "recovery_required " + detail);
        CompanionLog.status(context, record.home, "error update-recovery-required");
    }

    private static void deliverReady(WeakReference<ZenFMActivity> owner) {
        final ZenFMActivity activity = owner.get();
        if (activity == null || activity.isFinishing() || activity.isDestroyed()) return;
        activity.runOnUiThread(new Runnable() {
            @Override public void run() { activity.onUpdateReady(); }
        });
    }

    private static void deliverFailure(WeakReference<ZenFMActivity> owner, final String detail) {
        final ZenFMActivity activity = owner.get();
        if (activity == null || activity.isFinishing() || activity.isDestroyed()) return;
        activity.runOnUiThread(new Runnable() {
            @Override public void run() { activity.onUpdateFailed(detail); }
        });
    }

    private static Release latest(Context context) throws Exception {
        HttpURLConnection connection = open(new URL(RELEASES));
        if (connection.getResponseCode() != 200) throw new IOException("release metadata HTTP " + connection.getResponseCode());
        JSONArray releases = new JSONArray(new String(read(connection.getInputStream(), 1024 * 1024), "UTF-8"));
        String installed = installedIdentity(context).version;
        for (int index = 0; index < releases.length(); index++) {
            JSONObject release = releases.getJSONObject(index);
            if (release.optBoolean("draft") || release.optBoolean("prerelease")) continue;
            String version = release.optString("tag_name").replaceFirst("^v", "");
            if (compare(version, installed) <= 0) continue;
            String name = "ZenFM-android-" + version + ".apk";
            JSONArray assets = release.optJSONArray("assets");
            if (assets == null) continue;
            for (int assetIndex = 0; assetIndex < assets.length(); assetIndex++) {
                JSONObject asset = assets.getJSONObject(assetIndex);
                String url = asset.optString("browser_download_url");
                String digest = asset.optString("digest");
                long size = asset.optLong("size", -1);
                if (name.equals(asset.optString("name")) && digest.matches("sha256:[0-9a-fA-F]{64}")
                    && size > 0 && size <= MAXIMUM_APK && trusted(new URL(url))) {
                    return new Release(version, url,
                        digest.substring("sha256:".length()).toLowerCase(Locale.US), size);
                }
            }
        }
        throw new IOException("no newer compatible companion release");
    }

    private static void download(Release release, File destination) throws Exception {
        URL url = new URL(release.url);
        for (int redirects = 0; redirects < 6; redirects++) {
            if (!trusted(url)) throw new IOException("untrusted update URL");
            HttpURLConnection connection = open(url); connection.setInstanceFollowRedirects(false);
            int status = connection.getResponseCode();
            if (status >= 300 && status < 400) {
                String location = connection.getHeaderField("Location");
                if (location == null) throw new IOException("invalid update redirect");
                url = new URL(url, location); continue;
            }
            if (status != 200) throw new IOException("download HTTP " + status);
            MessageDigest digest = MessageDigest.getInstance("SHA-256");
            InputStream input = new BufferedInputStream(connection.getInputStream());
            FileOutputStream output = new FileOutputStream(destination);
            long total = 0;
            try {
                byte[] buffer = new byte[64 * 1024]; int count;
                while ((count = input.read(buffer)) != -1) {
                    total += count;
                    if (total > release.size || total > MAXIMUM_APK) throw new IOException("APK exceeds recorded size");
                    digest.update(buffer, 0, count); output.write(buffer, 0, count);
                }
                output.getFD().sync();
            } finally { input.close(); output.close(); }
            if (total != release.size || !hex(digest.digest()).equals(release.sha256)) {
                destination.delete(); throw new IOException("APK checksum or size did not match");
            }
            return;
        }
        throw new IOException("too many redirects");
    }

    private static void validatePending(Context context, UpdateState.Record record) throws Exception {
        File apk = UpdateJournal.cacheFile(context, record);
        if (apk == null || !apk.isFile() || apk.length() != record.size) throw new IOException("verified APK is missing");
        if (!sha256(apk, record.size).equals(record.sha256)) throw new IOException("verified APK changed on disk");
        PackageIdentity identity = validate(context, apk, record.targetVersion);
        if (identity.versionCode != record.targetVersionCode) throw new IOException("APK version code changed");
    }

    @SuppressWarnings("deprecation")
    private static PackageIdentity validate(Context context, File apk, String expectedVersion) throws Exception {
        PackageManager manager = context.getPackageManager();
        PackageInfo downloaded = manager.getPackageArchiveInfo(apk.getPath(), PackageManager.GET_SIGNATURES);
        PackageInfo installed = manager.getPackageInfo(context.getPackageName(), PackageManager.GET_SIGNATURES);
        if (downloaded == null || !context.getPackageName().equals(downloaded.packageName)
            || !sameSignatures(installed.signatures, downloaded.signatures)) {
            throw new IOException("APK package or signing certificate did not match");
        }
        PackageIdentity identity = identity(downloaded);
        if (!expectedVersion.equals(identity.version)) throw new IOException("APK version did not match the GitHub release");
        return identity;
    }

    private static PackageIdentity installedIdentity(Context context) throws Exception {
        return identity(context.getPackageManager().getPackageInfo(context.getPackageName(), 0));
    }

    @SuppressWarnings("deprecation")
    private static PackageIdentity identity(PackageInfo info) throws IOException {
        if (info.versionName == null || info.versionName.isEmpty()) throw new IOException("package version is missing");
        long code = Build.VERSION.SDK_INT >= Build.VERSION_CODES.P ? info.getLongVersionCode() : info.versionCode;
        return new PackageIdentity(info.versionName, code);
    }

    private static boolean sameSignatures(Signature[] left, Signature[] right) {
        if (left == null || right == null || left.length != right.length) return false;
        byte[][] a = new byte[left.length][], b = new byte[right.length][];
        for (int index = 0; index < left.length; index++) {
            a[index] = left[index].toByteArray(); b[index] = right[index].toByteArray();
        }
        java.util.Comparator<byte[]> comparator = new java.util.Comparator<byte[]>() {
            @Override public int compare(byte[] x, byte[] y) { return hex(x).compareTo(hex(y)); }
        };
        Arrays.sort(a, comparator); Arrays.sort(b, comparator);
        for (int index = 0; index < a.length; index++) {
            if (!MessageDigest.isEqual(a[index], b[index])) return false;
        }
        return true;
    }

    private static HttpURLConnection open(URL url) throws IOException {
        HttpURLConnection connection = (HttpURLConnection) url.openConnection();
        connection.setRequestProperty("User-Agent", "ZenFM-Companion");
        connection.setConnectTimeout(15000); connection.setReadTimeout(30000);
        return connection;
    }

    private static boolean trusted(URL url) {
        if (!"https".equals(url.getProtocol())) return false;
        String host = url.getHost().toLowerCase(Locale.US);
        if ("github.com".equals(host)) return url.getPath().startsWith("/xZenLabs/zen-fm/releases/download/");
        return "api.github.com".equals(host) || "objects.githubusercontent.com".equals(host)
            || "release-assets.githubusercontent.com".equals(host) || "github-releases.githubusercontent.com".equals(host);
    }

    private static byte[] read(InputStream input, int maximum) throws IOException {
        try {
            ByteArrayOutputStream output = new ByteArrayOutputStream(); byte[] buffer = new byte[8192]; int count;
            while ((count = input.read(buffer)) != -1) {
                if (output.size() + count > maximum) throw new IOException("response too large");
                output.write(buffer, 0, count);
            }
            return output.toByteArray();
        } finally { input.close(); }
    }

    private static String sha256(File file, long maximum) throws Exception {
        MessageDigest digest = MessageDigest.getInstance("SHA-256");
        InputStream input = new BufferedInputStream(new FileInputStream(file));
        long total = 0;
        try {
            byte[] buffer = new byte[64 * 1024]; int count;
            while ((count = input.read(buffer)) != -1) {
                total += count;
                if (total > maximum || total > MAXIMUM_APK) throw new IOException("APK exceeds recorded size");
                digest.update(buffer, 0, count);
            }
        } finally { input.close(); }
        if (total != maximum) throw new IOException("APK size changed");
        return hex(digest.digest());
    }

    private static String hex(byte[] value) {
        StringBuilder result = new StringBuilder(value.length * 2);
        for (byte item : value) result.append(String.format(Locale.US, "%02x", item & 0xff));
        return result.toString();
    }

    private static int compare(String left, String right) {
        String[] a = left.split("[-+]", 2)[0].split("\\."), b = right.split("[-+]", 2)[0].split("\\.");
        for (int index = 0; index < 3; index++) {
            int x = index < a.length ? Integer.parseInt(a[index]) : 0;
            int y = index < b.length ? Integer.parseInt(b[index]) : 0;
            if (x != y) return x < y ? -1 : 1;
        }
        return 0;
    }

    private static String clean(String message) {
        if (message == null || message.isEmpty()) return "unknown error";
        String value = message.replace('\n', ' ').replace('\r', ' ').trim();
        return value.length() > 240 ? value.substring(0, 240) : value;
    }

    private static final class PackageIdentity {
        final String version; final long versionCode;
        PackageIdentity(String version, long versionCode) { this.version = version; this.versionCode = versionCode; }
    }

    private static final class Release {
        final String version, url, sha256; final long size;
        Release(String version, String url, String sha256, long size) {
            this.version = version; this.url = url; this.sha256 = sha256; this.size = size;
        }
    }
}

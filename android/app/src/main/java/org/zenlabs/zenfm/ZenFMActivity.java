package org.zenlabs.zenfm;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Environment;
import android.provider.Settings;
import java.io.File;
import java.io.IOException;

public final class ZenFMActivity extends Activity {
    private static final int REQUEST_ALL_FILES = 4197;
    private static final int REQUEST_LEGACY_STORAGE = 4198;
    private static final int REQUEST_INSTALL_PERMISSION = 4199;
    private static final int REQUEST_PACKAGE_INSTALLER = 4200;
    private static final String STATE_PENDING_START = "pending-start";
    private static final String STATE_UPDATE_PERMISSION = "awaiting-update-permission";
    private static final String STATE_PACKAGE_INSTALLER = "awaiting-package-installer";
    private static final String STATE_COMMAND_REQUEST = "command-request";
    private static final String STATE_COMMAND_HOME = "command-home";
    private Intent pendingStart;
    private boolean awaitingUpdatePermission;
    private boolean awaitingPackageInstaller;
    private boolean resumed;
    private boolean updateDownloadReady;
    private String updateFailure;
    private AlertDialog updateProgress;
    private String commandRequestId;
    private String commandHome;

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        if (state != null) {
            pendingStart = state.getParcelable(STATE_PENDING_START);
            awaitingUpdatePermission = state.getBoolean(STATE_UPDATE_PERMISSION, false);
            awaitingPackageInstaller = state.getBoolean(STATE_PACKAGE_INSTALLER, false);
            commandRequestId = state.getString(STATE_COMMAND_REQUEST);
            commandHome = state.getString(STATE_COMMAND_HOME);
            if (pendingStart != null || awaitingUpdatePermission || awaitingPackageInstaller) return;
        }
        if (getIntent() != null && ZenFMUpdater.ACTION_CONTINUE_INSTALL.equals(getIntent().getAction())) {
            continueUpdateInstall();
            return;
        }
        Intent incoming = getIntent();
        Uri uri = incoming == null ? null : incoming.getData();
        if (uri == null && incoming != null && Intent.ACTION_MAIN.equals(incoming.getAction())) {
            // Keep a normal launcher entry so BOOX exposes the companion in
            // App Info, Auto Start, and App Freeze. Tapping it opens the
            // system management page; KOReader still starts the backend.
            startActivity(new Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                Uri.parse("package:" + getPackageName())));
            finish();
            return;
        }
        String action = uri == null ? null : uri.getHost();
        String token = uri == null ? null : uri.getQueryParameter("token");
        if (action != null && ControlAuth.authorize(this, action, token)) {
            if (!prepareCommand(uri)) return;
            if (handlePendingUpdate(action)) return;
            if (ControlAuth.requiresUserConfirmation(action)) {
                confirmPairedCommand(action, uri);
                return;
            }
            handleAuthorized(action, uri);
            return;
        }
        if (action != null && ControlAuth.needsPairingConfirmation(this, action, token)) {
            if (!prepareCommand(uri)) return;
            confirmFirstPairing(uri, token);
            return;
        }
        CompanionLog.write(this, null, "Rejected unauthenticated companion command.");
        finish();
    }

    private boolean handlePendingUpdate(String action) {
        UpdateState.Outcome pending = ZenFMUpdater.inspect(this);
        if (pending == UpdateState.Outcome.NONE) return false;
        if ("stop".equals(action) || "status".equals(action)) return false;
        if (pending == UpdateState.Outcome.INSTALL_PENDING) {
            commandFailed("pending-update-must-finish");
            showPendingInstaller();
            return true;
        }
        if (pending == UpdateState.Outcome.VERIFY_PENDING) {
            if ("start".equals(action)) return false;
            commandFailed("installed-update-needs-health-check");
            showMessage("Verify the ZenFM update",
                "The new companion is installed, but its bundled backend has not passed its startup health check. "
                    + "Start ZenFM from KOReader before using other companion actions.");
            return true;
        }
        if ("update".equals(action)) return false;
        commandFailed("update-recovery-required");
        showRecovery();
        return true;
    }

    private void showPendingInstaller() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) getWindow().setHideOverlayWindows(true);
        AlertDialog dialog = new AlertDialog.Builder(this)
            .setTitle("Finish the ZenFM update?")
            .setMessage("A signed companion APK is ready. Continue the Android Package Installer, "
                + "leave it pending, or discard this downloaded update.")
            .setNegativeButton("Not now", (clicked, which) -> finish())
            .setNeutralButton("Discard", (clicked, which) -> {
                if (!ZenFMUpdater.discardPending(this)) {
                    CompanionLog.write(this, null, "Could not discard the pending companion update.");
                }
                finish();
            })
            .setPositiveButton("Continue", (clicked, which) -> continueUpdateInstall())
            .setOnCancelListener(cancelled -> finish())
            .create();
        protectDialog(dialog);
        dialog.show();
    }

    private void continueUpdateInstall() {
        int result = ZenFMUpdater.continueInstall(this, REQUEST_INSTALL_PERMISSION, REQUEST_PACKAGE_INSTALLER);
        awaitingUpdatePermission = result == ZenFMUpdater.INSTALL_PERMISSION_REQUIRED;
        awaitingPackageInstaller = result == ZenFMUpdater.INSTALLER_STARTED;
        if (awaitingUpdatePermission || awaitingPackageInstaller) return;
        if (result == ZenFMUpdater.INSTALLED_AWAITING_HEALTH) {
            showMessage("ZenFM update installed",
                "Start ZenFM from KOReader. The server will remain unavailable unless the newly bundled backend "
                    + "passes its startup health check.");
        } else if (result == ZenFMUpdater.INSTALL_RECOVERY_REQUIRED) {
            showRecovery();
        } else {
            showMessage("ZenFM update needs attention",
                "The Package Installer could not be opened. The signed APK remains pending; retry the update from KOReader.");
        }
    }

    private void beginUpdate(String home, boolean allowPrerelease) {
        UpdateState.Outcome pending = ZenFMUpdater.inspect(this);
        if (pending == UpdateState.Outcome.INSTALL_PENDING
            || pending == UpdateState.Outcome.VERIFY_PENDING) {
            continueUpdateInstall();
            return;
        }
        updateProgress = new AlertDialog.Builder(this).setTitle("Checking for a ZenFM update")
            .setMessage("Downloading and verifying release metadata. Keep this window open to continue directly to Android's installer.")
            .setCancelable(false).create();
        updateProgress.show();
        ZenFMUpdater.start(this, home, allowPrerelease);
    }

    void onUpdateReady() {
        updateDownloadReady = true;
        dismissUpdateProgress();
        if (resumed) {
            updateDownloadReady = false;
            continueUpdateInstall();
        }
    }

    void onUpdateFailed(String detail) {
        updateFailure = detail;
        dismissUpdateProgress();
        if (resumed) {
            String failure = updateFailure;
            updateFailure = null;
            showMessage("ZenFM update failed", failure);
        }
    }

    private void dismissUpdateProgress() {
        if (updateProgress != null) {
            updateProgress.dismiss();
            updateProgress = null;
        }
    }

    private void showRecovery() {
        showMessage("ZenFM update recovery required", ZenFMUpdater.recoveryMessage(this));
    }

    private boolean prepareCommand(Uri uri) {
        try {
            commandRequestId = CommandRequest.require(uri.getQueryParameter("request_id"));
            commandHome = validatedHome(uri);
            return true;
        } catch (IllegalArgumentException error) {
            CompanionLog.write(this, null, "Rejected companion command with invalid request metadata.");
            finish();
            return false;
        }
    }

    private void commandFailed(String reason) {
        if (commandRequestId != null && commandHome != null) {
            CompanionLog.status(this, commandHome,
                CommandRequest.status("error " + reason, commandRequestId));
        }
    }

    private void showMessage(String title, String message) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) getWindow().setHideOverlayWindows(true);
        AlertDialog dialog = new AlertDialog.Builder(this).setTitle(title).setMessage(message)
            .setPositiveButton(android.R.string.ok, (clicked, which) -> finish())
            .setOnCancelListener(cancelled -> finish()).create();
        protectDialog(dialog);
        dialog.show();
    }

    private static void protectDialog(final AlertDialog dialog) {
        dialog.setOnShowListener(shown -> {
            dialog.getButton(AlertDialog.BUTTON_POSITIVE).setFilterTouchesWhenObscured(true);
            if (dialog.getButton(AlertDialog.BUTTON_NEGATIVE) != null) {
                dialog.getButton(AlertDialog.BUTTON_NEGATIVE).setFilterTouchesWhenObscured(true);
            }
            if (dialog.getButton(AlertDialog.BUTTON_NEUTRAL) != null) {
                dialog.getButton(AlertDialog.BUTTON_NEUTRAL).setFilterTouchesWhenObscured(true);
            }
        });
    }

    private void confirmFirstPairing(final Uri uri, final String token) {
        final Intent service;
        try {
            service = startIntent(uri);
        } catch (IllegalArgumentException error) {
            CompanionLog.write(this, null, "Rejected invalid first pairing: " + error.getMessage());
            commandFailed("invalid-command");
            finish();
            return;
        }
        String transport = "1".equals(uri.getQueryParameter("insecure")) ? "HTTP" : "HTTPS";
        showConfirmation("Start ZenFM?",
            "Only approve if you just selected Start ZenFM in KOReader.\n\n"
                + "This first approval links the companion to KOReader.\n\nRoot: "
                + service.getStringExtra("root")
                + "\nTransport: " + transport,
            "User rejected first companion pairing.", () -> {
                if (!ControlAuth.confirmPairing(this, token)) {
                    CompanionLog.write(this, null, "Could not establish companion pairing.");
                    finish();
                    return;
                }
                start(service);
            });
    }

    private void confirmPairedCommand(final String action, final Uri uri) {
        final String title;
        final String message;
        try {
            if ("start".equals(action)) {
                final Intent service = startIntent(uri);
                title = "Start ZenFM?";
                message = "Approve this request from KOReader.\n\nRoot: " + service.getStringExtra("root")
                    + "\nTransport: " + (service.getBooleanExtra("insecure", false) ? "HTTP" : "HTTPS");
                showConfirmation(title, message, "User rejected companion start.", () -> start(service));
                return;
            }
            if ("reset".equals(action)) {
                String home = uri.getQueryParameter("home");
                if (home != null && !home.isEmpty()) absolute(home, false, "home");
                title = "Reset ZenFM login?";
                message = "This revokes every browser session and API token, then restores the public setup credentials.";
            } else {
                validatedHome(uri);
                title = "Update ZenFM companion?";
                message = "ZenFM will download a signed release and ask Android to install it.";
            }
            showConfirmation(title, message, "User rejected companion " + action + ".",
                () -> handleAuthorized(action, uri));
        } catch (IllegalArgumentException error) {
            CompanionLog.write(this, null, "Rejected invalid companion command: " + error.getMessage());
            commandFailed("invalid-command");
            finish();
        }
    }

    private void showConfirmation(String title, String message, final String rejectionLog, final Runnable approved) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) getWindow().setHideOverlayWindows(true);
        AlertDialog dialog = new AlertDialog.Builder(this)
            .setTitle(title)
            .setMessage(message)
            .setNegativeButton(android.R.string.cancel, (clicked, which) -> {
                CompanionLog.write(this, null, rejectionLog);
                commandFailed("rejected");
                finish();
            })
            .setPositiveButton(android.R.string.ok, (clicked, which) -> approved.run())
            .setOnCancelListener(cancelled -> {
                commandFailed("rejected");
                finish();
            })
            .create();
        protectDialog(dialog);
        dialog.show();
    }

    private void handleAuthorized(String action, Uri uri) {
        try {
            if ("start".equals(action)) {
                start(uri);
                return;
            }
            if ("stop".equals(action)) sendAction(ZenFMService.ACTION_STOP);
            else if ("status".equals(action)) sendAction(ZenFMService.ACTION_STATUS);
            else if ("reset".equals(action)) sendAction(ZenFMService.ACTION_RESET);
            else if ("update".equals(action)) {
                beginUpdate(validatedHome(uri), "1".equals(uri.getQueryParameter("beta")));
                return;
            }
            else {
                CompanionLog.write(this, null, "Rejected unknown companion command.");
                commandFailed("unknown-command");
            }
        } catch (IllegalArgumentException error) {
            CompanionLog.write(this, null, "Rejected invalid companion command: " + error.getMessage());
            commandFailed("invalid-command");
        }
        finish();
    }

    private void start(Uri uri) {
        Intent service;
        try {
            service = startIntent(uri);
        } catch (IllegalArgumentException error) {
            CompanionLog.write(this, null, "Rejected invalid companion command: " + error.getMessage());
            commandFailed("invalid-command");
            finish();
            return;
        }
        start(service);
    }

    private void start(Intent service) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R && !Environment.isExternalStorageManager()) {
            pendingStart = service;
            startActivityForResult(new Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION,
                Uri.parse("package:" + getPackageName())), REQUEST_ALL_FILES);
            return;
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M && Build.VERSION.SDK_INT < Build.VERSION_CODES.R
                && checkSelfPermission(Manifest.permission.WRITE_EXTERNAL_STORAGE) != PackageManager.PERMISSION_GRANTED) {
            pendingStart = service;
            requestPermissions(new String[]{Manifest.permission.WRITE_EXTERNAL_STORAGE}, REQUEST_LEGACY_STORAGE);
            return;
        }
        launch(service);
        finish();
    }

    private Intent startIntent(Uri uri) {
        String home = validatedHome(uri);
        String root = absolute(uri.getQueryParameter("root"), true, "root");
        int port = integer(uri.getQueryParameter("port"), 1, 65535, "port");
        boolean insecure = "1".equals(uri.getQueryParameter("insecure"));
        String autoStop = CommandRequest.requireAutoStop(uri.getQueryParameter("auto_stop"));
        String certificate = optionalAbsolute(uri.getQueryParameter("tls_cert"), "certificate");
        String key = optionalAbsolute(uri.getQueryParameter("tls_key"), "private key");
        if ((certificate.isEmpty()) != (key.isEmpty())) throw new IllegalArgumentException("both TLS paths are required");

        Intent service = new Intent(this, ZenFMService.class);
        service.setAction(ZenFMService.ACTION_START);
        service.putExtra("home", home);
        service.putExtra("root", root);
        service.putExtra("port", port);
        service.putExtra("insecure", insecure);
        service.putExtra("auto_stop", autoStop);
        service.putExtra("tls_cert", certificate);
        service.putExtra("tls_key", key);
        service.putExtra("request_id", commandRequestId);
        return service;
    }

    private void launch(Intent service) {
        CompanionLog.write(this, service.getStringExtra("home"), "Accepted authenticated KOReader start command.");
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) startForegroundService(service); else startService(service);
    }

    @Override protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == REQUEST_INSTALL_PERMISSION) {
            awaitingUpdatePermission = false;
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
                && getPackageManager().canRequestPackageInstalls()) {
                continueUpdateInstall();
            } else {
                showMessage("Install permission is required",
                    "Android did not allow ZenFM to request package installs. The signed APK remains pending; "
                        + "grant permission and retry from KOReader.");
            }
            return;
        }
        if (requestCode == REQUEST_PACKAGE_INSTALLER) {
            awaitingPackageInstaller = false;
            UpdateState.Outcome outcome = ZenFMUpdater.inspect(this);
            if (outcome == UpdateState.Outcome.VERIFY_PENDING) {
                showMessage("ZenFM update installed",
                    "Start ZenFM from KOReader to verify the newly bundled backend.");
            } else if (outcome == UpdateState.Outcome.RECOVERY_REQUIRED) {
                showRecovery();
            } else {
                showMessage("ZenFM update remains pending",
                    "Android did not install the signed APK. Retry or discard the pending update from KOReader.");
            }
            return;
        }
        if (requestCode != REQUEST_ALL_FILES) return;
        Intent service = pendingStart;
        pendingStart = null;
        if (service != null && Build.VERSION.SDK_INT >= Build.VERSION_CODES.R
            && Environment.isExternalStorageManager()) launch(service);
        else {
            CompanionLog.write(this, null, "Storage access was not granted; ZenFM was not started.");
            commandFailed("storage-access-denied");
        }
        finish();
    }

    @Override protected void onSaveInstanceState(Bundle state) {
        super.onSaveInstanceState(state);
        if (pendingStart != null) state.putParcelable(STATE_PENDING_START, pendingStart);
        state.putBoolean(STATE_UPDATE_PERMISSION, awaitingUpdatePermission);
        state.putBoolean(STATE_PACKAGE_INSTALLER, awaitingPackageInstaller);
        state.putString(STATE_COMMAND_REQUEST, commandRequestId);
        state.putString(STATE_COMMAND_HOME, commandHome);
    }

    @Override protected void onResume() {
        super.onResume();
        resumed = true;
        if (updateDownloadReady) {
            updateDownloadReady = false;
            continueUpdateInstall();
        } else if (updateFailure != null) {
            String failure = updateFailure;
            updateFailure = null;
            showMessage("ZenFM update failed", failure);
        }
    }

    @Override protected void onPause() {
        resumed = false;
        super.onPause();
    }

    @Override public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] results) {
        super.onRequestPermissionsResult(requestCode, permissions, results);
        if (requestCode != REQUEST_LEGACY_STORAGE) return;
        Intent service = pendingStart;
        pendingStart = null;
        if (service != null && results.length > 0 && results[0] == PackageManager.PERMISSION_GRANTED) launch(service);
        else {
            CompanionLog.write(this, null, "Storage access was not granted; ZenFM was not started.");
            commandFailed("storage-access-denied");
        }
        finish();
    }

    private void sendAction(String action) {
        Intent service = new Intent(this, ZenFMService.class);
        service.setAction(action);
        service.putExtra("home", commandHome);
        service.putExtra("request_id", commandRequestId);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) startForegroundService(service); else startService(service);
    }

    private String validatedHome(Uri uri) {
        String home = absolute(uri.getQueryParameter("home"), false, "home");
        try {
            String storage = Environment.getExternalStorageDirectory().getCanonicalPath();
            if (!home.equals(storage) && !home.startsWith(storage + File.separator)) {
                throw new IllegalArgumentException("home is outside shared storage");
            }
            return home;
        } catch (IOException error) {
            throw new IllegalArgumentException("home could not be resolved");
        }
    }

    private static String optionalAbsolute(String value, String label) {
        return value == null || value.isEmpty() ? "" : absolute(value, false, label);
    }

    private static String absolute(String value, boolean allowRoot, String label) {
        if (value == null || value.length() > 4096 || value.indexOf('\0') >= 0 || !value.startsWith("/")) {
            throw new IllegalArgumentException(label);
        }
        try {
            String canonical = new File(value).getCanonicalPath();
            if (!allowRoot && "/".equals(canonical)) throw new IllegalArgumentException(label);
            return canonical;
        } catch (IOException error) {
            throw new IllegalArgumentException(label);
        }
    }

    private static int integer(String value, int minimum, int maximum, String label) {
        try {
            int parsed = Integer.parseInt(value);
            if (parsed < minimum || parsed > maximum) throw new IllegalArgumentException(label);
            return parsed;
        } catch (NumberFormatException error) {
            throw new IllegalArgumentException(label);
        }
    }
}

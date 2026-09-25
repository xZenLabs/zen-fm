package org.zenlabs.zenfm;

import android.annotation.SuppressLint;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.ServiceInfo;
import android.net.LocalSocket;
import android.net.LocalSocketAddress;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.IBinder;
import java.io.BufferedReader;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.util.ArrayList;
import java.util.List;

public final class ZenFMService extends Service {
    static final String ACTION_START = "org.zenlabs.zenfm.action.START";
    static final String ACTION_STOP = "org.zenlabs.zenfm.action.STOP";
    static final String ACTION_STATUS = "org.zenlabs.zenfm.action.STATUS";
    static final String ACTION_RESET = "org.zenlabs.zenfm.action.RESET";
    static final String ACTION_PEER = "org.zenlabs.zenfm.action.PEER";
    private static final String CHANNEL = "zenfm-server";
    private static final int NOTIFICATION = 4197;
    private static final int DEFAULT_PORT = 54321;
    private static final String PEER_PENDING = "ZenFM peer incoming pending";
    private static final String PEER_CLEAR = "ZenFM peer incoming clear";
    private Process process;
    private Thread worker;
    private Config config;
    private Config pendingConfig;
    private int pendingStartId;
    private int latestStartId;
    private boolean recoveryRequired;
    private boolean stopRequested;
    private boolean resetInProgress;
    private volatile boolean peerOfferPending;
    private WifiManager.MulticastLock peerDiscoveryLock;
    private String pendingLifecycleAction;
    private String pendingLifecycleRequestId;
    private String pendingLifecycleHome;

    @Override public void onCreate() {
        super.onCreate();
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationManager manager = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
            manager.createNotificationChannel(new NotificationChannel(CHANNEL, "ZenFM server", NotificationManager.IMPORTANCE_LOW));
        }
    }

    @Override public synchronized int onStartCommand(Intent intent, int flags, int startId) {
        latestStartId = startId;
        foreground();
        String action = intent == null ? ACTION_START : intent.getAction();
        String requestId = intent == null ? "" : intent.getStringExtra("request_id");
        if (requestId == null) requestId = "";
        if (!requestId.isEmpty()) {
            try { requestId = CommandRequest.require(requestId); }
            catch (IllegalArgumentException invalid) {
                stopForeground(true);
                stopSelfResult(startId);
                return START_NOT_STICKY;
            }
        }
        if (ACTION_STATUS.equals(action)) {
            Config saved = config == null ? Config.load(this) : config;
            String home = intent == null ? null : intent.getStringExtra("home");
            if (home == null && saved != null) home = saved.home;
            String result;
            UpdateState.Outcome update = ZenFMUpdater.inspect(this);
            if (update == UpdateState.Outcome.RECOVERY_REQUIRED) {
                result = CommandRequest.status("error update-recovery-required", requestId);
            } else if (update == UpdateState.Outcome.VERIFY_PENDING) {
                result = CommandRequest.status("error update-health-pending", requestId);
            } else {
                result = CommandRequest.liveStatus(control("status"), requestId);
            }
            if (home != null) CompanionLog.status(this, home, result);
            boolean supervising = (worker != null && worker.isAlive()) || resetInProgress;
            if (!supervising) {
                stopForeground(true);
                stopSelfResult(startId);
            }
            return supervising ? START_STICKY : START_NOT_STICKY;
        }
        if (ACTION_PEER.equals(action)) {
            Config saved = config == null ? Config.load(this) : config;
            String home = intent == null ? null : intent.getStringExtra("home");
            if (home == null && saved != null) home = saved.home;
            String command = intent == null ? null : intent.getStringExtra("peer_command");
            String response = validPeerCommand(command) ? control(command) : null;
            if (home != null) CompanionLog.status(this, home,
                CommandRequest.status(response != null && response.startsWith("ok") ? "ok peer" : "error peer-rejected", requestId));
            boolean supervising = worker != null && worker.isAlive();
            if (!supervising) {
                stopForeground(true);
                stopSelfResult(startId);
            }
            return supervising ? START_STICKY : START_NOT_STICKY;
        }
        if (ACTION_STOP.equals(action)) {
            stopRequested = true;
            pendingConfig = null;
            Config saved = config == null ? Config.load(this) : config;
            String home = intent == null ? null : intent.getStringExtra("home");
            if (home == null && saved != null) home = saved.home;
            pendingLifecycleAction = "stop";
            pendingLifecycleRequestId = requestId;
            pendingLifecycleHome = home;
            if (home != null) CompanionLog.status(this, home, "stopping");
            stopBackend();
            if (process == null && (worker == null || !worker.isAlive())) {
                if (home != null) CompanionLog.status(this, home,
                    CommandRequest.status("stopped", requestId));
                clearPendingLifecycle();
                stopForeground(true);
                stopSelfResult(startId);
            }
            return START_NOT_STICKY;
        }
        if (ACTION_RESET.equals(action)) {
            stopRequested = true;
            final String home = intent == null ? null : intent.getStringExtra("home");
            pendingConfig = null;
            resetInProgress = true;
            pendingLifecycleAction = "reset";
            pendingLifecycleRequestId = requestId;
            pendingLifecycleHome = home;
            if (home != null) CompanionLog.status(this, home, "resetting");
            stopBackend();
            runReset(home, requestId);
            return START_NOT_STICKY;
        }
        Config requested = intent == null ? Config.load(this) : Config.from(intent);
        if (requested == null) {
            CompanionLog.write(this, null, "No saved ZenFM configuration was available.");
            stopForeground(true);
            stopSelfResult(startId);
            return START_NOT_STICKY;
        }
        stopRequested = false;
        recoveryRequired = false;
        if (!requested.requestId.isEmpty()) CompanionLog.status(this, requested.home, "starting");
        requested.save(this);
        if (worker != null && worker.isAlive()) {
            if (!requested.sameAs(config)) {
                pendingConfig = requested;
                pendingStartId = startId;
                CompanionLog.write(this, requested.home, "Restarting ZenFM to apply changed server settings.");
                CompanionLog.status(this, requested.home, "restarting");
                stopBackend();
            } else {
                String current = ZenFMUpdater.inspect(this) == UpdateState.Outcome.VERIFY_PENDING
                    ? null : control("status");
                if (!CommandRequest.isRunning(current)) {
                    pendingConfig = requested;
                    pendingStartId = startId;
                    stopBackend();
                } else {
                    CompanionLog.status(this, requested.home,
                        CommandRequest.status(current, requested.requestId));
                }
            }
            return START_STICKY;
        }
        config = requested;
        startBackend(startId);
        return START_STICKY;
    }

    private void foreground() {
        Notification notification = serverNotification();
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startForeground(NOTIFICATION, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_MANIFEST);
        } else {
            startForeground(NOTIFICATION, notification);
        }
    }

    private Notification serverNotification() {
        Notification.Builder builder = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
            ? new Notification.Builder(this, CHANNEL) : new Notification.Builder(this);
        return builder.setContentTitle("ZenFM")
            .setContentText(peerOfferPending ? "Incoming transfer waiting; open KOReader" : "Serving files in the background")
            .setSmallIcon(android.R.drawable.stat_sys_upload).setOngoing(true).build();
    }

    private synchronized void updatePeerNotification(boolean pending) {
        peerOfferPending = pending;
        if (process == null) return;
        NotificationManager manager = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        try { manager.notify(NOTIFICATION, serverNotification()); }
        catch (RuntimeException ignored) {}
    }

    private synchronized void startBackend(final int startId) {
        final UpdateState.BackendGate updateGate = ZenFMUpdater.prepareBackendStart(this);
        if (updateGate == UpdateState.BackendGate.BLOCK_FOR_RECOVERY) {
            recoveryRequired = true;
            if (config != null) CompanionLog.status(this, config.home,
                CommandRequest.status("error update-recovery-required", config.requestId));
            showRecoveryAndStop();
            return;
        }
        final Config launchedConfig = config;
        final String expectedUpdateVersion = updateGate == UpdateState.BackendGate.VERIFY_UPDATE
            ? ZenFMUpdater.pendingTargetVersion(this) : null;
        if (!launchedConfig.insecure) acquirePeerDiscovery(launchedConfig.home);
        worker = new Thread(new Runnable() {
            @Override public void run() {
                boolean updateFailure = false;
                boolean updateSettled = false;
                boolean healthFailure = false;
                int exitStatus = -1;
                try {
                    if (updateGate == UpdateState.BackendGate.VERIFY_UPDATE) {
                        verifyBundledBackendVersion(expectedUpdateVersion);
                    }
                    ProcessBuilder builder = new ProcessBuilder(command(launchedConfig));
                    builder.redirectErrorStream(true);
                    final Process launched = builder.start();
                    boolean superseded;
                    synchronized (ZenFMService.this) {
                        process = launched;
                        superseded = pendingConfig != null || stopRequested;
                    }
                    if (superseded) launched.destroy();
                    Thread logs = new Thread(new Runnable() {
                        @Override public void run() {
                            try {
                                BufferedReader reader = new BufferedReader(new InputStreamReader(launched.getInputStream()));
                                String line;
                                while ((line = reader.readLine()) != null) {
                                    Boolean pending = peerNotification(line);
                                    if (pending != null) updatePeerNotification(pending.booleanValue());
                                    CompanionLog.write(ZenFMService.this, launchedConfig.home, line);
                                }
                            } catch (Exception ignored) {}
                        }
                    }, "ZenFMLogs");
                    logs.start();
                    if (!superseded) {
                        String healthyStatus = waitForHealthyStatus(launchedConfig);
                        boolean healthy = healthyStatus != null;
                        boolean deliberatelyStopped;
                        synchronized (ZenFMService.this) {
                            deliberatelyStopped = stopRequested || pendingConfig != null;
                        }
                        healthFailure = !healthy && !deliberatelyStopped;
                        if (updateGate == UpdateState.BackendGate.VERIFY_UPDATE) {
                            if (healthy && !ZenFMUpdater.backendHealthy(ZenFMService.this)) {
                                healthy = false;
                                updateFailure = true;
                            }
                            else if (!deliberatelyStopped) {
                                if (!healthy) {
                                    ZenFMUpdater.backendFailed(ZenFMService.this,
                                        "control socket did not become healthy");
                                    updateFailure = true;
                                }
                            }
                            updateSettled = healthy || !deliberatelyStopped;
                        }
                        if (healthy) CompanionLog.status(ZenFMService.this, launchedConfig.home,
                            CommandRequest.status(healthyStatus, launchedConfig.requestId));
                        if (!healthy) launched.destroy();
                    }
                    exitStatus = launched.waitFor();
                    CompanionLog.write(ZenFMService.this, launchedConfig.home,
                        "Backend exited with status " + exitStatus + ".");
                } catch (Exception error) {
                    CompanionLog.write(ZenFMService.this, launchedConfig.home, "Backend failed: " + error.getMessage());
                    boolean restarting;
                    synchronized (ZenFMService.this) { restarting = pendingConfig != null || stopRequested; }
                    healthFailure = !restarting;
                    if (updateGate == UpdateState.BackendGate.VERIFY_UPDATE && !updateSettled && !restarting) {
                        ZenFMUpdater.backendFailed(ZenFMService.this, error.getMessage());
                        updateFailure = true;
                        updateSettled = true;
                    }
                } finally {
                    Config restart;
                    int restartId;
                    int serviceStopId;
                    boolean deliberatelyStopped;
                    synchronized (ZenFMService.this) {
                        process = null;
                        worker = null;
                        restart = pendingConfig;
                        restartId = pendingStartId;
                        pendingConfig = null;
                        serviceStopId = latestStartId;
                        deliberatelyStopped = stopRequested || pendingLifecycleAction != null;
                        if (restart != null) config = restart;
                    }
                    releasePeerDiscovery();
                    if (restart != null) {
                        CompanionLog.status(ZenFMService.this, restart.home, "restarting");
                        startBackend(restartId);
                        return;
                    }
                    if (resetInProgress) return;
                    if (updateFailure) {
                        recoveryRequired = true;
                        CompanionLog.status(ZenFMService.this, launchedConfig.home,
                            CommandRequest.status("error update-recovery-required", launchedConfig.requestId));
                        showRecoveryAndStop();
                        return;
                    }
                    boolean idleTimeout = !healthFailure
                        && BackendExit.isIdleTimeout(exitStatus, launchedConfig.autoStop, deliberatelyStopped);
                    if (healthFailure) {
                        CompanionLog.status(ZenFMService.this, launchedConfig.home,
                            CommandRequest.status("error backend-health-failed", launchedConfig.requestId));
                    } else if ("stop".equals(pendingLifecycleAction)) {
                        CompanionLog.status(ZenFMService.this, pendingLifecycleHome,
                            CommandRequest.status("stopped", pendingLifecycleRequestId));
                    } else if (idleTimeout) {
                        CompanionLog.write(ZenFMService.this, launchedConfig.home,
                            "Backend reached its inactivity timeout; stopping the companion process.");
                        CompanionLog.status(ZenFMService.this, launchedConfig.home,
                            CommandRequest.status("idle_stopped", launchedConfig.requestId));
                    } else {
                        CompanionLog.status(ZenFMService.this, launchedConfig.home,
                            CommandRequest.status("stopped", launchedConfig.requestId));
                    }
                    clearPendingLifecycle();
                    stopForeground(true);
                    boolean serviceStopped = stopSelfResult(serviceStopId);
                    if (idleTimeout && serviceStopped) {
                        android.os.Process.killProcess(android.os.Process.myPid());
                    }
                }
            }
        }, "ZenFMBackend");
        worker.start();
    }

    private List<String> command(Config value) {
        String executable = backendExecutable();
        List<String> command = new ArrayList<String>();
        command.add(executable); command.add("serve");
        command.add("--root"); command.add(value.root);
        command.add("--peer-source-root"); command.add(value.peerSourceRoot);
        command.add("--peer-receive-root"); command.add(value.peerReceiveRoot);
        command.add("--default-directory"); command.add(value.defaultDirectory);
        command.add("--data-dir"); command.add(getFilesDir().getAbsolutePath());
        command.add("--listen"); command.add("0.0.0.0:" + value.port);
        command.add("--control-socket"); command.add(socketPath());
        command.add("--peer-name"); command.add(value.deviceName);
        command.add("--peer-events"); command.add(new File(value.home, "peer-events.json").getAbsolutePath());
        command.add("--auto-stop"); command.add(value.autoStop);
        if (value.debug) command.add("--debug");
        if (value.useDeviceNameAsTitle) command.add("--use-device-name-as-title");
        if (value.insecure) command.add("--insecure-http");
        else if (!value.certificate.isEmpty()) {
            command.add("--tls-cert"); command.add(value.certificate);
            command.add("--tls-key"); command.add(value.key);
        }
        return command;
    }

    private String backendExecutable() {
        return getApplicationInfo().nativeLibraryDir + "/libzenfm_exec.so";
    }

    private void verifyBundledBackendVersion(String expected) throws Exception {
        if (expected == null || expected.isEmpty()) throw new IOException("target version is missing from update journal");
        Process probe = new ProcessBuilder(backendExecutable(), "version").redirectErrorStream(true).start();
        boolean exited = false;
        for (int attempt = 0; attempt < 50; attempt++) {
            try {
                if (probe.exitValue() != 0) throw new IOException("bundled backend version probe failed");
                exited = true;
                break;
            } catch (IllegalThreadStateException running) {
                Thread.sleep(100);
            }
        }
        if (!exited) {
            probe.destroy();
            throw new IOException("bundled backend version probe timed out");
        }
        InputStream input = probe.getInputStream();
        ByteArrayOutputStream output = new ByteArrayOutputStream();
        try {
            byte[] buffer = new byte[128]; int count;
            while ((count = input.read(buffer)) != -1) {
                if (output.size() + count > 256) throw new IOException("bundled backend version output was invalid");
                output.write(buffer, 0, count);
            }
        } finally { input.close(); }
        String actual = new String(output.toByteArray(), "UTF-8").trim();
        if (!expected.equals(actual)) {
            throw new IOException("bundled backend version did not match installed companion");
        }
    }

    private String socketPath() { return new File(getFilesDir(), "zenfm.sock").getAbsolutePath(); }

    private String waitForHealthyStatus(Config value) {
        for (int attempt = 0; attempt < 150; attempt++) {
            String response = control("status");
            if (CommandRequest.isRunning(response)) {
                return response;
            }
            try { Thread.sleep(100); }
            catch (InterruptedException ignored) { Thread.currentThread().interrupt(); return null; }
        }
        CompanionLog.status(this, value.home,
            CommandRequest.status("error backend-health-timeout", value.requestId));
        return null;
    }

    private synchronized int latestServiceStartId() { return latestStartId; }

    private synchronized void clearPendingLifecycle() {
        pendingLifecycleAction = null;
        pendingLifecycleRequestId = null;
        pendingLifecycleHome = null;
    }

    private void showRecoveryAndStop() {
        Notification.Builder builder = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
            ? new Notification.Builder(this, CHANNEL) : new Notification.Builder(this);
        Notification notification = builder.setContentTitle("ZenFM update needs recovery")
            .setContentText("Server stopped because the new backend failed verification")
            .setSmallIcon(android.R.drawable.stat_notify_error).setOngoing(false).setAutoCancel(true).build();
        NotificationManager manager = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        manager.notify(NOTIFICATION, notification);
        stopForeground(false);
        stopSelfResult(latestServiceStartId());
    }

    private String control(String command) {
        LocalSocket socket = new LocalSocket();
        try {
            socket.connect(new LocalSocketAddress(socketPath(), LocalSocketAddress.Namespace.FILESYSTEM));
            socket.setSoTimeout(2000);
            OutputStream output = socket.getOutputStream();
            output.write((command + "\n").getBytes("US-ASCII"));
            output.flush();
            return new BufferedReader(new InputStreamReader(socket.getInputStream(), "US-ASCII")).readLine();
        } catch (Exception ignored) {
            return null;
        } finally {
            try { socket.close(); } catch (Exception ignored) {}
        }
    }

    static boolean validPeerCommand(String command) {
        if (command == null || command.length() > 8191) return false;
        String id = "[A-Za-z0-9_-]{16,80}";
        return command.equals("peer-status")
            || command.matches("peer-(discover|accept|decline|cancel) " + id)
            || command.matches("peer-send " + id + " [A-Fa-f0-9]{64} [A-Za-z0-9_-]{1,6000}");
    }

    static Boolean peerNotification(String line) {
        if (PEER_PENDING.equals(line)) return Boolean.TRUE;
        if (PEER_CLEAR.equals(line)) return Boolean.FALSE;
        return null;
    }

    private synchronized void stopBackend() {
        control("stop");
        final Process current = process;
        if (current != null) {
            new Thread(new Runnable() {
                @Override public void run() {
                    for (int attempt = 0; attempt < 30; attempt++) {
                        try { current.exitValue(); return; }
                        catch (IllegalThreadStateException running) {
                            try { Thread.sleep(100); } catch (InterruptedException ignored) { break; }
                        }
                    }
                    current.destroy();
                }
            }, "ZenFMGracefulStop").start();
        }
    }

    private synchronized void acquirePeerDiscovery(String home) {
        if (peerDiscoveryLock != null) return;
        WifiManager wifi = (WifiManager) getApplicationContext().getSystemService(Context.WIFI_SERVICE);
        if (wifi == null) {
            CompanionLog.write(this, home, "Peer discovery could not access the Wi-Fi service.");
            return;
        }
        try {
            WifiManager.MulticastLock lock = wifi.createMulticastLock("ZenFM-peer-discovery");
            lock.setReferenceCounted(false);
            lock.acquire();
            peerDiscoveryLock = lock;
            CompanionLog.write(this, home, "Peer discovery Wi-Fi receive lock acquired.");
        } catch (RuntimeException error) {
            CompanionLog.write(this, home, "Peer discovery Wi-Fi receive lock failed: " + error.getMessage());
        }
    }

    private synchronized void releasePeerDiscovery() {
        if (peerDiscoveryLock == null) return;
        try { peerDiscoveryLock.release(); }
        catch (RuntimeException ignored) {}
        peerDiscoveryLock = null;
    }

    private void runReset(final String home, final String requestId) {
        new Thread(new Runnable() {
            @Override public void run() {
                boolean reset = false;
                try {
                    Process running;
                    synchronized (ZenFMService.this) { running = process; }
                    if (running != null) {
                        for (int attempt = 0; attempt < 40; attempt++) {
                            try { running.exitValue(); break; }
                            catch (IllegalThreadStateException active) { Thread.sleep(100); }
                        }
                    }
                    for (int attempt = 0; attempt < 40; attempt++) {
                        synchronized (ZenFMService.this) {
                            if (worker == null) break;
                            if (attempt == 39) throw new IOException("backend worker did not stop before reset-login");
                        }
                        Thread.sleep(50);
                    }
                    String executable = backendExecutable();
                    int status = new ProcessBuilder(executable, "reset-login", "--data-dir", getFilesDir().getAbsolutePath())
                        .redirectErrorStream(true).start().waitFor();
                    CompanionLog.write(ZenFMService.this, home, "reset-login exited with status " + status + ".");
                    reset = status == 0;
                } catch (Exception error) {
                    CompanionLog.write(ZenFMService.this, home, "reset-login failed: " + error.getMessage());
                } finally {
                    if (home != null) CompanionLog.status(ZenFMService.this, home,
                        CommandRequest.status(reset ? "reset_done" : "error reset-failed", requestId));
                    synchronized (ZenFMService.this) {
                        resetInProgress = false;
                        clearPendingLifecycle();
                    }
                    stopForeground(true); stopSelf();
                }
            }
        }, "ZenFMReset").start();
    }

    @Override public synchronized void onDestroy() {
        stopRequested = true;
        pendingConfig = null;
        stopBackend();
        super.onDestroy();
    }
    @Override public IBinder onBind(Intent intent) { return null; }

    private static final class Config {
        final String home, root, peerSourceRoot, peerReceiveRoot, defaultDirectory, deviceName, autoStop, certificate, key, requestId;
        final int port;
        final boolean insecure, debug, useDeviceNameAsTitle;
        Config(String home, String root, String peerSourceRoot, String peerReceiveRoot, String defaultDirectory, int port, boolean insecure, boolean debug,
            String deviceName, boolean useDeviceNameAsTitle, String autoStop,
            String certificate, String key, String requestId) {
            this.home = home; this.root = root; this.port = port; this.insecure = insecure; this.debug = debug;
            this.peerSourceRoot = peerSourceRoot;
            this.peerReceiveRoot = peerReceiveRoot;
            this.defaultDirectory = defaultDirectory;
            this.deviceName = deviceName; this.useDeviceNameAsTitle = useDeviceNameAsTitle;
            this.autoStop = autoStop; this.certificate = certificate; this.key = key;
            this.requestId = requestId == null ? "" : requestId;
        }
        boolean sameAs(Config other) {
            return other != null && home.equals(other.home) && root.equals(other.root) && peerSourceRoot.equals(other.peerSourceRoot)
                && peerReceiveRoot.equals(other.peerReceiveRoot)
                && defaultDirectory.equals(other.defaultDirectory) && port == other.port
                && insecure == other.insecure && debug == other.debug && autoStop.equals(other.autoStop)
                && deviceName.equals(other.deviceName) && useDeviceNameAsTitle == other.useDeviceNameAsTitle
                && certificate.equals(other.certificate) && key.equals(other.key);
        }
        static Config from(Intent intent) {
            String home = intent.getStringExtra("home"), root = intent.getStringExtra("root");
            String peerSourceRoot = intent.getStringExtra("peer_source_root");
            String peerReceiveRoot = intent.getStringExtra("peer_receive_root");
            String defaultDirectory = intent.getStringExtra("default_directory");
            if (home == null || root == null || defaultDirectory == null) return null;
            if (peerSourceRoot == null) peerSourceRoot = root;
            if (peerReceiveRoot == null) peerReceiveRoot = root;
            return new Config(home, root, peerSourceRoot, peerReceiveRoot, defaultDirectory, intent.getIntExtra("port", DEFAULT_PORT), intent.getBooleanExtra("insecure", false),
                intent.getBooleanExtra("debug", false),
                intent.getStringExtra("device_name"), intent.getBooleanExtra("use_device_name_as_title", true),
                intent.getStringExtra("auto_stop"), intent.getStringExtra("tls_cert"), intent.getStringExtra("tls_key"),
                intent.getStringExtra("request_id"));
        }
        @SuppressLint("ApplySharedPref")
        void save(Service service) {
            // A command may immediately kill this process; the service config must be durable first.
            service.getSharedPreferences("server", MODE_PRIVATE).edit().putString("home", home).putString("root", root)
                .putString("peer_source_root", peerSourceRoot)
                .putString("peer_receive_root", peerReceiveRoot)
                .putString("default_directory", defaultDirectory)
                .putString("device_name", deviceName).putBoolean("use_device_name_as_title", useDeviceNameAsTitle)
                .putInt("port", port).putBoolean("insecure", insecure).putBoolean("debug", debug).putString("auto_stop", autoStop)
                .putString("certificate", certificate).putString("key", key).commit();
        }
        static Config load(Service service) {
            SharedPreferences p = service.getSharedPreferences("server", MODE_PRIVATE);
            String home = p.getString("home", null), root = p.getString("root", null);
            if (home == null || root == null) return null;
            return new Config(home, root, p.getString("peer_source_root", root), p.getString("peer_receive_root", root), p.getString("default_directory", "/"), p.getInt("port", DEFAULT_PORT), p.getBoolean("insecure", false),
                p.getBoolean("debug", false),
                p.getString("device_name", Build.MODEL == null ? "Android" : Build.MODEL), p.getBoolean("use_device_name_as_title", true),
                p.getString("auto_stop", "0"), p.getString("certificate", ""), p.getString("key", ""), "");
        }
    }
}

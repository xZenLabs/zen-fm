package org.zenlabs.zenfm;

import android.content.Context;
import android.content.SharedPreferences;
import java.io.File;

/** Durable hand-off between the downloader, Package Installer, and the replacement app. */
final class UpdateJournal {
    private static final String PREFS = "zenfm-update";
    private static final String PHASE = "phase";
    private static final String SOURCE_VERSION = "source-version";
    private static final String SOURCE_CODE = "source-code";
    private static final String TARGET_VERSION = "target-version";
    private static final String TARGET_CODE = "target-code";
    private static final String SHA256 = "sha256";
    private static final String SIZE = "size";
    private static final String FILE_NAME = "file-name";
    private static final String HOME = "home";
    private static final String FAILURE = "failure";

    private UpdateJournal() {}

    static synchronized UpdateState.Record load(Context context) {
        SharedPreferences values = preferences(context);
        String phase;
        try {
            phase = values.getString(PHASE, null);
        } catch (RuntimeException corrupt) {
            return corruptRecord();
        }
        if (phase == null) return null;
        UpdateState.Phase parsed;
        try {
            parsed = UpdateState.Phase.valueOf(phase);
        } catch (IllegalArgumentException error) {
            parsed = UpdateState.Phase.RECOVERY_REQUIRED;
        }
        try {
            return new UpdateState.Record(parsed,
                values.getString(SOURCE_VERSION, ""), values.getLong(SOURCE_CODE, -1),
                values.getString(TARGET_VERSION, ""), values.getLong(TARGET_CODE, -1),
                values.getString(SHA256, ""), values.getLong(SIZE, -1),
                values.getString(FILE_NAME, ""), values.getString(HOME, ""),
                values.getString(FAILURE, "update journal is incomplete"));
        } catch (RuntimeException corrupt) {
            return corruptRecord();
        }
    }

    static synchronized boolean save(Context context, UpdateState.Record record) {
        if (record == null || !record.valid()) return false;
        UpdateState.Record previous = load(context);
        if (!write(context, record)) return false;
        if (previous != null && !record.fileName.equals(previous.fileName)) {
            deleteCacheFile(context, previous.fileName);
        }
        return true;
    }

    static synchronized boolean changePhase(Context context, UpdateState.Phase phase, String failure) {
        UpdateState.Record current = load(context);
        return current != null && write(context, current.withPhase(phase, failure));
    }

    static synchronized boolean clear(Context context) {
        UpdateState.Record current = load(context);
        if (!preferences(context).edit().clear().commit()) return false;
        if (current != null) deleteCacheFile(context, current.fileName);
        return true;
    }

    static File cacheFile(Context context, UpdateState.Record record) {
        if (record == null || record.fileName == null
            || !record.fileName.matches("zenfm-update-[A-Za-z0-9._-]+\\.apk")) return null;
        try {
            File cache = context.getCacheDir().getCanonicalFile();
            File candidate = new File(cache, record.fileName).getCanonicalFile();
            return cache.equals(candidate.getParentFile()) ? candidate : null;
        } catch (Exception error) {
            return null;
        }
    }

    private static SharedPreferences preferences(Context context) {
        return context.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    private static boolean write(Context context, UpdateState.Record record) {
        return preferences(context).edit()
            .putString(PHASE, record.phase.name())
            .putString(SOURCE_VERSION, safe(record.sourceVersion)).putLong(SOURCE_CODE, record.sourceVersionCode)
            .putString(TARGET_VERSION, safe(record.targetVersion)).putLong(TARGET_CODE, record.targetVersionCode)
            .putString(SHA256, safe(record.sha256)).putLong(SIZE, record.size)
            .putString(FILE_NAME, safe(record.fileName)).putString(HOME, safe(record.home))
            .putString(FAILURE, safe(record.failure)).commit();
    }

    private static String safe(String value) { return value == null ? "" : value; }

    private static UpdateState.Record corruptRecord() {
        return new UpdateState.Record(UpdateState.Phase.RECOVERY_REQUIRED,
            "", -1, "", -1, "", -1, "", "", "update journal is corrupt");
    }

    private static void deleteCacheFile(Context context, String name) {
        UpdateState.Record placeholder = new UpdateState.Record(UpdateState.Phase.RECOVERY_REQUIRED,
            "", -1, "", -1, "", -1, name, "", "");
        File file = cacheFile(context, placeholder);
        if (file != null) file.delete();
    }
}

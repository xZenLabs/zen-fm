package org.zenlabs.zenfm;

import android.content.Context;
import java.io.File;
import java.io.FileWriter;
import java.io.IOException;
import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.Locale;
import java.util.TimeZone;

final class CompanionLog {
    private CompanionLog() {}

    static synchronized void write(Context context, String home, String message) {
        writeFile(context, home, "android-companion.log", timestamp() + "  " + message + "\n", true);
    }

    static synchronized void status(Context context, String home, String value) {
        writeFile(context, home, "android-companion.status", value + "\n", false);
    }

    static synchronized void updateStatus(Context context, String home, String value) {
        writeFile(context, home, "android-companion-update.status", value + "\n", false);
    }

    private static void writeFile(Context context, String home, String name, String value, boolean append) {
        File directory = home == null ? context.getExternalFilesDir(null) : new File(home);
        if (directory == null || (!directory.exists() && !directory.mkdirs())) return;
        try {
            FileWriter writer = new FileWriter(new File(directory, name), append);
            writer.write(value);
            writer.close();
        } catch (IOException ignored) {}
    }

    private static String timestamp() {
        SimpleDateFormat format = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", Locale.US);
        format.setTimeZone(TimeZone.getTimeZone("UTC"));
        return format.format(new Date());
    }
}

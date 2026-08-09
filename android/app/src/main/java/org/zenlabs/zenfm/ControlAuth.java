package org.zenlabs.zenfm;

import android.content.Context;
import android.content.SharedPreferences;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;

final class ControlAuth {
    private static final String PREFS = "zenfm-control";
    private static final String KEY = "paired-token";

    private ControlAuth() {}

    static boolean authorize(Context context, String action, String token) {
        if (token == null || !token.matches("[0-9a-f]{64}")) return false;
        SharedPreferences preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
        String paired = preferences.getString(KEY, null);
        return authorizePairing(paired, action, token) == 1;
    }

    static boolean needsPairingConfirmation(Context context, String action, String token) {
        String paired = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY, null);
        return authorizePairing(paired, action, token) == 2;
    }

    static synchronized boolean confirmPairing(Context context, String token) {
        if (token == null || !token.matches("[0-9a-f]{64}")) return false;
        SharedPreferences preferences = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
        String paired = preferences.getString(KEY, null);
        if (paired != null) {
            return MessageDigest.isEqual(paired.getBytes(StandardCharsets.US_ASCII),
                token.getBytes(StandardCharsets.US_ASCII));
        }
        return preferences.edit().putString(KEY, token).commit();
    }

    static boolean requiresUserConfirmation(String action) {
        return "start".equals(action) || "reset".equals(action) || "update".equals(action);
    }

    // 0 rejects, 1 accepts an existing pairing, and 2 requires explicit user
    // confirmation before a first start may establish the pairing.
    static int authorizePairing(String paired, String action, String token) {
        if (token == null || !token.matches("[0-9a-f]{64}")) return 0;
        if (paired == null) return "start".equals(action) ? 2 : 0;
        return MessageDigest.isEqual(paired.getBytes(StandardCharsets.US_ASCII),
            token.getBytes(StandardCharsets.US_ASCII)) ? 1 : 0;
    }
}

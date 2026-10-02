package org.zenlabs.zenfm;

import static org.junit.Assert.assertEquals;

import java.lang.reflect.Method;
import org.junit.Test;

public final class ZenFMActivityTest {
    @Test public void displayValidatesUntrustedLabels() throws Exception {
        Method display = ZenFMActivity.class.getDeclaredMethod("display", String.class);
        display.setAccessible(true);
        assertEquals("", display.invoke(null, (Object) null));
        assertEquals("", display.invoke(null, ""));
        String label = "ZenFM — 你好 📚\u0080\u009f\u2028\u2029";
        assertEquals(label, display.invoke(null, label));
        String maximum = new String(new char[200]).replace('\0', 'a');
        assertEquals(maximum, display.invoke(null, maximum));
        assertEquals("", display.invoke(null, maximum + "a"));
        assertEquals("", display.invoke(null, "first\nsecond\nthird"));
        assertEquals("", display.invoke(null, "label\r\n"));
        for (char control = 0; control < 32; control++) {
            assertEquals("", display.invoke(null, control + "label"));
            assertEquals("", display.invoke(null, "la" + control + "bel"));
            assertEquals("", display.invoke(null, "label" + control));
        }
        assertEquals("", display.invoke(null, "\u007flabel"));
        assertEquals("", display.invoke(null, "la\u007fbel"));
        assertEquals("", display.invoke(null, "label\u007f"));
        assertEquals("", display.invoke(null, maximum.substring(1) + '\0'));
        assertEquals("", display.invoke(null, maximum.replace('a', '\u007f')));
    }
}

-- Release-note bullet lists, keyed by the stable version string.
-- Add an entry for each release with noteworthy changes. Beta releases use the
-- entry for their stable base version unless an exact beta entry is provided.

return {
    ["1.0.1"] = {
        "Add show and hide controls to password fields",
        "Hide misleading folder sizes and keep folders alphabetical when sorting by size",
        "Add opt-in beta updates with correct prerelease version ordering",
        "Better logging",
        "Open file fullscreen",
        "Find in page",
        "Single http/https port",
        "Redirect http -> https if http is disabled",
        "Customize inactivity timeout",
        "Bug fixes"
    },
    ["1.0.2"] = {
        "Remember the current location across sign-in",
        "Choose Home and a startup directory",
        "Restart a running server after plugin updates",
        "Reduce routine logging and follow KOReader's debug setting",
        "Prevent duplicate inactivity-stop notifications",
        "Update updater flow",
        "Allow copy/paste in same dir with duplicate/overwrite",
    },
    ["1.0.3"] = {
        "Fix ZenFM startup on PocketBook firmware without shell arithmetic support",
    },
    ["1.0.4"] = {
        "Add QR code",
        "Keep ZenFM icons visible in dark mode",
        "Reorganize settings",
    },
    ["1.0.5"] = {
        "Add PWA manifest",
        "Use white favicon for dark mode browser",
        "Add show IP/QR code to ZenOS settings",
        "Fix showing entire filesystem",
    },
    ["1.0.6"] = {
        "Add SVG & Image previews",
        "Sticky toolbar",
    },
    ["1.1.0"] = {
        "Add favorites/bookmarks",
        "Add secure ZenFM device-to-device file and folder sending",
        "Fix ZenFM Send discovery polling and improve discovery diagnostics",
        "Faster copy/upload to device",
        "Fix image thumbnails not all showing",
        "Queue multiple uploads",
    },
    ["1.2.0"] = {
        "Direct p2p sharing over https locally (like localsend)",
        "Add scroll to bottom button for long files",
        "Allow changing device name",
        "Show device name in browser tab",
    },

}

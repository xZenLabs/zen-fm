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
        "Choose Home and a default directory",
        "Restart a running server after plugin updates",
        "Reduce routine logging and follow KOReader's debug setting",
        "Prevent duplicate inactivity-stop notifications",
        "Update updater flow",
        "Allow copy/paste in same dir with duplicate/overwrite",
    }
}

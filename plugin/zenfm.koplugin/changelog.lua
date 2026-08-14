-- Release-note bullet lists, keyed by the stable version string.
-- Add an entry for each release with noteworthy changes. Beta releases use the
-- entry for their stable base version unless an exact beta entry is provided.

return {
    ["1.0.1"] = {
        "Add show and hide controls to password fields",
        "Hide misleading folder sizes and keep folders alphabetical when sorting by size",
        "Add opt-in beta updates with correct prerelease version ordering",
        "Better logging",
        "Single http/https port",
        "Redirect http -> https if http is disabled",
        "Customize inactivity timeout"
    },
}

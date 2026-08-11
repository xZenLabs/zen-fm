local root = assert(arg[1], "repository root required")
package.path = root .. "/plugin/zenfm.koplugin/?.lua;" .. package.path

local generic_modules = {}
for _, name in ipairs({ "control", "daemon", "release_public_key", "settings", "signature", "updater", "util" }) do
    generic_modules[name] = { occupied_by_another_plugin = true }
    package.loaded[name] = generic_modules[name]
end

local Daemon = require("zenfm_daemon")
local Settings = require("zenfm_settings")
local Updater = require("zenfm_updater")
local Util = require("zenfm_util")

for name, module in pairs(generic_modules) do
    assert(package.loaded[name] == module, "ZenFM replaced shared module " .. name)
end

local count = 0
local function test(name, callback)
    local ok, err = pcall(callback)
    if not ok then error(name .. ": " .. tostring(err), 0) end
    count = count + 1
end
local function equal(actual, expected)
    assert(actual == expected, "expected " .. tostring(expected) .. ", got " .. tostring(actual))
end
local function contains(value, expected)
    assert(tostring(value):find(expected, 1, true), "missing " .. expected .. " in " .. tostring(value))
end

local function fake_settings(values)
    return {
        values = values,
        default_root = function(self, platform, storage)
            if self.values.advanced_root then return "/" end
            if self.values.custom_root ~= "" then return self.values.custom_root end
            if platform == "kindle" then return "/mnt/us" end
            if platform == "kobo" then return "/mnt/onboard" end
            return storage or "/home/test"
        end,
    }
end

local defaults = Settings.defaults()

test("hard-float Kindle backend", function()
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "kindle",
        settings = fake_settings(defaults),
        path_exists = function(path) return path == "/lib/ld-linux-armhf.so.3" end,
    }
    equal(daemon:backend_candidates()[1], "/plugin/backend/zenfm-hf")
    equal(daemon:root(), "/mnt/us")
end)

test("PocketBook always uses soft float", function()
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "pocketbook",
        settings = fake_settings(defaults), path_exists = function() return true end,
    }
    equal(daemon:backend_candidates()[1], "/plugin/backend/zenfm-sf")
end)

test("actual PocketBook and generic ARM readers are not treated as desktop Linux", function()
    local pocketbook = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", settings = fake_settings(defaults),
        path_exists = function(path) return path == "/ebrmain" end,
        command_output = function(command) return command:find("uname %-s") and "Linux" or "armv7l" end,
    }
    equal(pocketbook:platform(), "pocketbook")
    equal(pocketbook:backend_candidates()[1], "/plugin/backend/zenfm-sf")

    local reader = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", settings = fake_settings(defaults),
        path_exists = function(path) return path == "/lib/ld-linux-armhf.so.3" end,
        command_output = function(command) return command:find("uname %-s") and "Linux" or "armv7l" end,
    }
    equal(reader:platform(), "ereader")
    equal(reader:backend_candidates()[1], "/plugin/backend/zenfm-hf")
end)

test("Linux arm64 selection", function()
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "host", machine = "aarch64",
        settings = fake_settings(defaults), command_output = function() return "Linux" end,
        path_exists = function() return false end,
    }
    equal(daemon:backend_candidates()[1], "/plugin/backend/zenfm-linux-arm64")
end)

test("advanced HTTP arguments", function()
    local values = Settings.defaults()
    values.advanced_root, values.insecure_http, values.auto_stop_minutes = true, true, 30
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "kindle",
        settings = fake_settings(values), path_exists = function() return false end,
    }
    local command = table.concat(daemon:serve_arguments(), " ")
    contains(command, "--root / --data-dir /state")
    contains(command, "--listen 0.0.0.0:8443")
    contains(command, "--auto-stop 30m")
    contains(command, "--insecure-http")
end)

test("runtime control paths stay off user storage and below Unix socket limits", function()
    local commands = {}
    local daemon = Daemon:new{
        plugin_dir = "/mnt/onboard/.adds/koreader/plugins/zenfm.koplugin",
        state_dir = "/mnt/onboard/.adds/koreader/settings/ZenFM/with/a/very/long/path",
        platform = "kobo", settings = fake_settings(Settings.defaults()),
        execute = function(command) table.insert(commands, command) return 0 end,
    }
    assert(daemon:ensure_runtime_dir())
    assert(daemon:control_socket():match("^/tmp/zenfm%-%x+/control%.sock$"))
    assert(#daemon:control_socket() < 108)
    assert(not daemon:control_socket():find("/mnt/onboard", 1, true))
    contains(commands[1], "chmod 700")
end)

test("custom TLS arguments remain separate and quoted", function()
    local values = Settings.defaults()
    values.custom_root = "/books/reader's files"
    values.tls_cert, values.tls_key = "/certs/device cert.pem", "/certs/device key.pem"
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "host",
        settings = fake_settings(values), path_exists = function() return false end,
        command_output = function(command) return command:find("uname %-s") and "Linux" or "x86_64" end,
    }
    local command = daemon:serve_command("/bin/zenfm")
    contains(command, "'--tls-cert' '/certs/device cert.pem'")
    contains(command, "'/books/reader'\\''s files'")
end)

test("custom TLS refuses an incomplete certificate pair", function()
    local values = Settings.defaults()
    values.tls_cert = "/certs/device.pem"
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "host",
        settings = fake_settings(values), path_exists = function() return false end,
    }
    daemon.status = function() return false end
    local ok, err = daemon:start()
    assert(not ok)
    contains(err, "both a certificate and private-key")
end)

test("status address falls back to ifconfig without choosing loopback", function()
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "host",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        command_output = function(command)
            if command:find("ifconfig", 1, true) then
                return "lo0: inet 127.0.0.1 netmask 0xff000000\nwlan0: inet addr:192.168.4.12"
            end
            return ""
        end,
    }
    equal(daemon:local_ip(), "192.168.4.12")
end)

test("status address falls back to an assigned IPv4 address without a default route", function()
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "host",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        command_output = function(command)
            if command:find("ip %-4 addr") then
                return "3: wlan0    inet 192.168.4.12/24 brd 192.168.4.255 scope global wlan0"
            end
            return ""
        end,
    }
    equal(daemon:local_ip(), "192.168.4.12")
end)

test("control status protocol", function()
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "host",
        settings = fake_settings(defaults), path_exists = function() return false end,
        command_output = function(command)
            if command:find("ip %-4 route") then return "1.1.1.1 via 192.168.4.1 dev wlan0 src 192.168.4.12" end
            return ""
        end,
        control_request = function(_, command)
            equal(command, "status")
            return "ok running https://0.0.0.0:8443 sha256:abc"
        end,
    }
    local details = daemon:status_details()
    assert(details.running)
    equal(details.scheme, "https")
    equal(details.listen, "0.0.0.0:8443")
    equal(details.address, "192.168.4.12")
    equal(details.port, "8443")
    equal(details.url, "https://192.168.4.12:8443")
    equal(details.fingerprint, "sha256:abc")
end)

test("status never presents a wildcard listener as the device address", function()
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "host",
        settings = fake_settings(defaults), path_exists = function() return false end,
        command_output = function() return "" end,
        control_request = function()
            return "ok running http://0.0.0.0:8080 -"
        end,
    }
    local details = daemon:status_details()
    equal(details.address, nil)
    equal(details.port, "8080")
    equal(details.url, nil)
end)

test("start waits for control-socket health", function()
    local executed, attempts = nil, 0
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "host",
        settings = fake_settings(defaults), path_exists = function() return false end,
        execute = function(command) executed = command return 0 end,
    }
    daemon.ensure_backend = function() return true end
    daemon.supervisor_command = function() return "'/plugin/supervisor.sh' -- '/state/backend/zenfm' serve" end
    daemon.status = function()
        attempts = attempts + 1
        return attempts >= 3, attempts >= 3 and "ok running https://0.0.0.0:8443 sha256:x" or "not ready"
    end
    local ok = daemon:start()
    assert(ok and attempts >= 3)
    contains(executed, "trap '' HUP")
    contains(executed, "2>&1 </dev/null")
end)

test("Android handoff carries paired token and validated settings", function()
    local state = os.tmpname() .. ".d"
    local values = Settings.defaults()
    values.port, values.auto_stop_minutes = 9443, 30
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android",
        settings = fake_settings(values), path_exists = function() return false end,
        android = { getExternalStoragePath = function() return "/storage/emulated/0" end },
    }
    local uri = assert(daemon:android_uri("start"))
    contains(uri, "zenfm://start?token=")
    assert(uri:match("request_id=[0-9a-f]+"))
    contains(uri, "home=" .. Util.url_encode(state))
    contains(uri, "root=%2Fstorage%2Femulated%2F0")
    contains(uri, "port=9443")
    contains(uri, "auto_stop=30m")
    os.remove(state .. "/android-control.token")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("Android handoff always targets the companion explicitly", function()
    local state = os.tmpname() .. ".d"
    local executed
    local implicit_calls = 0
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        android = {
            getExternalStoragePath = function() return "/storage/emulated/0" end,
            openLink = function() implicit_calls = implicit_calls + 1 return true end,
        },
        execute = function(command)
            executed = command
            local request_id = assert(command:match("request_id=([0-9a-f]+)"))
            assert(Util.write_atomic(state .. "/android-companion.status",
                "ok running https://0.0.0.0:8443 - request=" .. request_id .. "\n", "600"))
            return 0
        end,
        sleep = function() end,
        android_poll_attempts = 2,
    }
    local ok, err = daemon:open_android("start")
    assert(ok, tostring(err))
    equal(implicit_calls, 0)
    contains(executed, "-n org.zenlabs.zenfm/.ZenFMActivity")
    contains(executed, "zenfm://start?token=")
    contains(executed, "</dev/null >/dev/null 2>&1")
    os.remove(state .. "/android-control.token")
    os.remove(state .. "/android-companion.status")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("Android lifecycle waits for a fresh request-bound result", function()
    local state = os.tmpname() .. ".fresh"
    assert(Util.ensure_dir(state))
    assert(Util.write_atomic(state .. "/android-companion.status",
        "ok running https://stale:8443 - request=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", "600"))
    local calls = 0
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        android = { getExternalStoragePath = function() return "/storage/emulated/0" end },
        execute = function(command)
            calls = calls + 1
            local request_id = assert(command:match("request_id=([0-9a-f]+)"))
            assert(Util.write_atomic(state .. "/android-companion.status",
                "ok running https://0.0.0.0:8443 sha256:fresh request=" .. request_id .. "\n", "600"))
            return 0
        end,
        sleep = function() end,
        android_poll_attempts = 2,
    }
    local ok, detail = daemon:start()
    assert(ok, tostring(detail))
    equal(calls, 1)
    contains(detail, "sha256:fresh")
    os.remove(state .. "/android-control.token")
    os.remove(state .. "/android-companion.status")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("Android cold stop carries home and waits for its terminal marker", function()
    local state = os.tmpname() .. ".stop"
    local executed
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        android = { getExternalStoragePath = function() return "/storage/emulated/0" end },
        execute = function(command)
            executed = command
            local request_id = assert(command:match("request_id=([0-9a-f]+)"))
            assert(Util.write_atomic(state .. "/android-companion.status",
                "stopped request=" .. request_id .. "\n", "600"))
            return 0
        end,
        sleep = function() end,
        android_poll_attempts = 2,
    }
    local ok, detail = daemon:open_android("stop")
    assert(ok, tostring(detail))
    contains(executed, "home=" .. Util.url_encode(state))
    contains(detail, "stopped request=")
    os.remove(state .. "/android-control.token")
    os.remove(state .. "/android-companion.status")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("Android status rejects stale persisted running state", function()
    local state = os.tmpname() .. ".status-stale"
    assert(Util.ensure_dir(state))
    assert(Util.write_atomic(state .. "/android-companion.status",
        "ok running https://stale:8443 - request=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", "600"))
    local calls = 0
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        android = { getExternalStoragePath = function() return "/storage/emulated/0" end },
        execute = function(command)
            calls = calls + 1
            contains(command, "zenfm://status?")
            local request_id = assert(command:match("request_id=([0-9a-f]+)"))
            assert(Util.write_atomic(state .. "/android-companion.status",
                "stopped request=" .. request_id .. "\n", "600"))
            return 0
        end,
        sleep = function() end,
        android_poll_attempts = 2,
    }
    local running, detail = daemon:status()
    assert(not running)
    equal(calls, 1)
    contains(detail, "stopped request=")
    assert(not detail:find("stale", 1, true))
    os.remove(state .. "/android-control.token")
    os.remove(state .. "/android-companion.status")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("Android status accepts only its fresh live response", function()
    local state = os.tmpname() .. ".status-live"
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        android = { getExternalStoragePath = function() return "/storage/emulated/0" end },
        execute = function(command)
            local request_id = assert(command:match("request_id=([0-9a-f]+)"))
            assert(Util.write_atomic(state .. "/android-companion.status",
                "ok running https://0.0.0.0:8443 sha256:fresh request=" .. request_id .. "\n", "600"))
            return 0
        end,
        sleep = function() end,
        android_poll_attempts = 2,
    }
    local running, detail = daemon:status()
    assert(running, tostring(detail))
    contains(detail, "sha256:fresh")
    os.remove(state .. "/android-control.token")
    os.remove(state .. "/android-companion.status")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("settings validation", function()
    local values = Settings.sanitize{
        port = 70000, advanced_root = true, insecure_http = true,
        custom_root = "relative", auto_stop_minutes = 45, tls_cert = "/cert", tls_key = "relative",
    }
    equal(values.port, 8443)
    assert(values.advanced_root and values.insecure_http)
    equal(values.custom_root, "")
    equal(values.auto_stop_minutes, 0)
    equal(values.tls_key, "")
    equal(Settings.sanitize{ custom_root = "/" }.custom_root, "")
    equal(Settings.sanitize{ custom_root = "/safe/../" }.custom_root, "")
end)

test("normal mode never falls back to literal root when home is unavailable", function()
    local settings = fake_settings(Settings.defaults())
    local original_getenv = os.getenv
    os.getenv = function(name)
        if name == "HOME" then return nil end
        return original_getenv(name)
    end
    local ok, root = pcall(function() return Settings.default_root(settings, "host", nil) end)
    os.getenv = original_getenv
    assert(ok)
    equal(root, nil)
    equal(Settings.default_root(settings, "android", nil), nil)
    settings.values.advanced_root = true
    equal(Settings.default_root(settings, "host", nil), "/")
end)

test("start fails closed before launch when no safe root exists", function()
    local launches = 0
    local settings = fake_settings(Settings.defaults())
    settings.default_root = function() return nil end
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "host",
        settings = settings, path_exists = function() return false end,
        execute = function() launches = launches + 1 return 0 end,
    }
    local ok, err = daemon:start()
    assert(not ok)
    contains(err, "safe storage root")
    equal(launches, 0)
end)

local function base64_zero_signature()
    return string.rep("A", 86) .. "=="
end

test("signed manifest verification is injectable and strict", function()
    local digest = string.rep("a", 64)
    local body = "zenfm-release-manifest-v1\nversion\t1.2.3\nasset\tZenFM-koreader-linux-1.2.3.zip\t42\t" .. digest .. "\n"
    local manifest = assert(Updater.verify_manifest(body, base64_zero_signature(), string.rep("11", 32),
        function(message, signature, public)
            equal(message, body); equal(#signature, 64); equal(#public, 32); return true
        end))
    equal(manifest.version, "1.2.3")
    equal(manifest.assets["ZenFM-koreader-linux-1.2.3.zip"].size, 42)
    local invalid = Updater.verify_manifest(body .. "extra", base64_zero_signature(), string.rep("11", 32),
        function() return true end)
    assert(not invalid)
end)

test("staged plugin Lua is compiled before activation", function()
    local directory = os.tmpname() .. ".lua-tree"
    assert(Util.ensure_dir(directory))
    assert(Util.write_atomic(directory .. "/valid.lua", "return { ok = true }\n", "600"))
    local valid, valid_err = Updater.validate_lua_tree(directory)
    assert(valid, tostring(valid_err))
    assert(Util.write_atomic(directory .. "/broken.lua", "local = broken\n", "600"))
    local accepted, syntax_err = Updater.validate_lua_tree(directory)
    assert(not accepted)
    contains(syntax_err, "invalid Lua")
    local parent = directory:match("^(.*)/[^/]+$")
    if parent then Util.remove_tree(directory, parent) end
end)

test("bundled backend verifies updater signatures without Lua crypto", function()
    local state = os.tmpname() .. ".verify"
    local executed
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "host",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        execute = function(command)
            executed = command
            return 0
        end,
    }
    daemon.ensure_backend = function() return true end
    local ok, err = daemon:verify_release_manifest("signed manifest\n", "c2lnbmF0dXJl\n", string.rep("ab", 32))
    assert(ok, tostring(err))
    contains(executed, "verify-manifest")
    contains(executed, "--public-key")
    assert(not executed:find("signed manifest", 1, true))
    local parent = state:match("^(.*)/[^/]+$")
    if parent then Util.remove_tree(state, parent) end
end)

test("update health verification restores a stopped service", function()
    local plugin_dir = os.tmpname() .. ".plugin"
    assert(Util.ensure_dir(plugin_dir))
    local backup = plugin_dir .. ".rollback"
    assert(Util.write_atomic(plugin_dir .. "/.update-pending", backup .. "\nstop\n", "600"))
    local stops = 0
    local running = false
    local daemon = {
        plugin_dir = plugin_dir,
        ensure_backend = function() return true end,
        start = function() running = true return true end,
        status = function() return running end,
        stop = function() stops = stops + 1 running = false return true end,
    }
    local healthy, err = Updater.finalize_pending(daemon)
    assert(healthy, tostring(err))
    equal(stops, 1)
    assert(not running)
    assert(not Util.path_exists(plugin_dir .. "/.update-pending"))
    local parent = plugin_dir:match("^(.*)/[^/]+$")
    if parent then Util.remove_tree(plugin_dir, parent) end
end)

test("failed updated backend rolls the plugin directory back", function()
    local plugin_dir = os.tmpname() .. ".plugin"
    local backup = plugin_dir .. ".rollback"
    assert(Util.ensure_dir(plugin_dir))
    assert(Util.ensure_dir(backup))
    assert(Util.write_atomic(plugin_dir .. "/new-version", "new\n", "600"))
    assert(Util.write_atomic(backup .. "/old-version", "old\n", "600"))
    assert(Util.write_atomic(plugin_dir .. "/.update-pending", backup .. "\nresume\n", "600"))
    local daemon = {
        plugin_dir = plugin_dir,
        ensure_backend = function() return false, "broken backend" end,
        start = function() error("must not start") end,
        status = function() return false end,
        stop = function() return true end,
    }
    local healthy, err = Updater.finalize_pending(daemon)
    assert(not healthy)
    contains(err, "previous plugin restored")
    equal(Util.trim(Util.read_all(plugin_dir .. "/old-version", 32)), "old")
    assert(not Util.path_exists(plugin_dir .. "/new-version"))
    local parent = plugin_dir:match("^(.*)/[^/]+$")
    if parent then Util.remove_tree(plugin_dir, parent) end
end)

test("shell quoting does not create a second command", function()
    equal(Util.sh_quote("x'; touch /tmp/owned; '"), "'x'\\''; touch /tmp/owned; '\\'''" )
end)

test("status notice shows the device address and port without the TLS fingerprint or wildcard listener", function()
    local module_names = {
        "dispatcher", "ui/widget/infomessage", "ui/widget/inputdialog", "ui/widget/confirmbox",
        "ui/uimanager", "ui/widget/container/widgetcontainer", "gettext", "zenfm_daemon", "zenfm_updater",
    }
    local saved, shown = {}, nil
    for _, name in ipairs(module_names) do saved[name] = package.loaded[name] end
    package.loaded["dispatcher"] = { registerAction = function() end }
    package.loaded["ui/widget/infomessage"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/inputdialog"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/confirmbox"] = { new = function(_, options) return options end }
    package.loaded["ui/uimanager"] = { show = function(_, message) shown = message end }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = { finalize_pending = function() return true end }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local owner = setmetatable({
        daemon = {
            settings = { values = { advanced_root = false } },
            status_details = function()
                return { running = true, scheme = "https", url = "https://192.168.4.12:8443", port = "8443", fingerprint = "sha256:secret" }
            end,
        },
    }, { __index = ZenFM })
    owner:onShowZenFMStatus()
    equal(shown.text, "ZenFM is running.\n\nhttps://192.168.4.12:8443")
    assert(not shown.text:find("sha256:secret", 1, true))

    owner.daemon.status_details = function()
        return { running = true, scheme = "http", listen = "0.0.0.0:8080", port = "8080" }
    end
    owner:onShowZenFMStatus()
    contains(shown.text, "Listening port: 8080")
    contains(shown.text, "Warning: unencrypted HTTP is enabled.")
    assert(not shown.text:find("0.0.0.0", 1, true))

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

io.stdout:write(string.format("ok - %d plugin tests\n", count))

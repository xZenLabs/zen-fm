local root = assert(arg[1], "repository root required")
package.path = root .. "/plugin/zenfm.koplugin/?.lua;" .. package.path

local generic_modules = {}
for _, name in ipairs({ "android_intent", "control", "daemon", "i18n", "settings", "updater", "util" }) do
    generic_modules[name] = { occupied_by_another_plugin = true }
    package.loaded[name] = generic_modules[name]
end

local Daemon = require("zenfm_daemon")
local AndroidIntent = require("zenfm_android_intent")
local I18n = require("zenfm_i18n")
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
            if platform == "pocketbook" then return "/mnt/ext1" end
            return storage or "/home/test"
        end,
    }
end

local function exercise_android_intent(uri, reject_start)
    local previous_ffi = package.loaded.ffi
    package.loaded.ffi = {
        new = function(kind)
            if kind == "jvalue[1]" then return { [0] = {} } end
            if kind == "jvalue[2]" then return { [0] = {}, [1] = {} } end
            error("unexpected FFI allocation " .. tostring(kind))
        end,
    }

    local state = { clears = 0, pops = 0, started = false }
    local pending_exception
    local uri_class, intent_class, activity_class = {}, {}, {}
    local parsed_uri, intent, activity = {}, {}, {}
    local api = {}
    function api.PushLocalFrame(_, capacity) equal(capacity, 16) return 0 end
    function api.PopLocalFrame(_, result) assert(result == nil) state.pops = state.pops + 1 end
    function api.ExceptionOccurred() return pending_exception end
    function api.ExceptionClear() pending_exception = nil state.clears = state.clears + 1 end
    function api.DeleteLocalRef() end
    function api.FindClass(_, name)
        if name == "android/net/Uri" then return uri_class end
        if name == "android/content/Intent" then return intent_class end
        error("unexpected Java class " .. tostring(name))
    end
    function api.GetStaticMethodID(_, class, name, signature)
        assert(class == uri_class)
        equal(name, "parse")
        equal(signature, "(Ljava/lang/String;)Landroid/net/Uri;")
        return "parse"
    end
    function api.NewStringUTF(_, value) return { value = value } end
    function api.CallStaticObjectMethodA(_, class, method, arguments)
        assert(class == uri_class)
        equal(method, "parse")
        equal(arguments[0].l.value, uri)
        return parsed_uri
    end
    function api.GetMethodID(_, class, name, signature)
        if class == intent_class and name == "<init>" then
            equal(signature, "(Ljava/lang/String;Landroid/net/Uri;)V")
            return "intent-constructor"
        end
        if class == intent_class and name == "setClassName" then
            equal(signature, "(Ljava/lang/String;Ljava/lang/String;)Landroid/content/Intent;")
            return "set-class"
        end
        if class == activity_class and name == "startActivity" then
            equal(signature, "(Landroid/content/Intent;)V")
            return "start-activity"
        end
        error("unexpected Java method " .. tostring(name))
    end
    function api.NewObjectA(_, class, method, arguments)
        assert(class == intent_class)
        equal(method, "intent-constructor")
        equal(arguments[0].l.value, AndroidIntent.ACTION)
        assert(arguments[1].l == parsed_uri)
        return intent
    end
    function api.CallObjectMethodA(_, object, method, arguments)
        assert(object == intent)
        equal(method, "set-class")
        equal(arguments[0].l.value, AndroidIntent.PACKAGE)
        equal(arguments[1].l.value, AndroidIntent.CLASS)
        return intent
    end
    function api.GetObjectClass(_, object) assert(object == activity) return activity_class end
    function api.CallVoidMethodA(_, object, method, arguments)
        assert(object == activity)
        equal(method, "start-activity")
        assert(arguments[0].l == intent)
        state.started = true
        if reject_start then pending_exception = {} end
    end

    local android = {
        app = { activity = { vm = {}, clazz = activity } },
        jni = {
            context = function(_, _, callback)
                return callback({ env = { [0] = api } })
            end,
        },
    }
    local called, opened = pcall(AndroidIntent.open, android, uri)
    package.loaded.ffi = previous_ffi
    assert(called, tostring(opened))
    return opened, state
end

local defaults = Settings.defaults()

test("translations stay scoped to ZenFM and follow KOReader's language", function()
    local previous_settings = _G.G_reader_settings
    local previous_gettext = package.loaded["gettext"]
    local language = "de_DE"
    _G.G_reader_settings = { readSetting = function() return language end }

    equal(I18n.translate("Settings"), "Einstellungen")
    equal(I18n.translate("A message absent from the ZenFM catalog"),
        "A message absent from the ZenFM catalog")
    assert(package.loaded["gettext"] == previous_gettext)
    assert(package.loaded["i18n"] == generic_modules.i18n)

    language = "ja_JP"
    equal(I18n.translate("Lifetime"), "有効期間")
    _G.G_reader_settings = previous_settings
    I18n.refresh()
end)

test("Android defaults auto-stop to 30 minutes and preserves custom and off settings", function()
    local state = os.tmpname() .. ".android-defaults"
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android", android = {},
        path_exists = function() return false end,
    }
    equal(daemon.settings.values.auto_stop_minutes, 30)
    equal(daemon.settings.values.auto_stop_last_minutes, 30)
    assert(daemon.settings:set("auto_stop_minutes", 45))
    assert(daemon.settings:set("beta_updates", true))

    local reloaded = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android", android = {},
        path_exists = function() return false end,
    }
    equal(reloaded.settings.values.auto_stop_minutes, 45)
    equal(reloaded.settings.values.auto_stop_last_minutes, 45)
    assert(reloaded.settings:set("auto_stop_minutes", 0))
    reloaded = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android", android = {},
        path_exists = function() return false end,
    }
    equal(reloaded.settings.values.auto_stop_minutes, 0)
    equal(reloaded.settings.values.auto_stop_last_minutes, 45)
    assert(reloaded.settings.values.beta_updates)
    os.remove(state .. "/settings.lua")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("non-Android platforms keep auto-stop disabled by default", function()
    local state = os.tmpname() .. ".host-defaults"
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "host",
        path_exists = function() return false end,
    }
    equal(daemon.settings.values.auto_stop_minutes, 0)
    equal(daemon.settings.values.auto_stop_last_minutes, 30)
    os.remove(state .. "/settings.lua")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("fresh installations use the shared static high port", function()
    local state = os.tmpname() .. ".static-port"
    local settings = Settings:new(state)
    equal(settings.values.port, 54321)
    equal(settings.values.default_directory, "/")
    assert(settings.values.show_qr_code)
    assert(settings:set("default_directory", "/Books/Unread"))
    assert(settings:set("show_qr_code", false))
    local reloaded = Settings:new(state)
    equal(reloaded.values.port, 54321)
    equal(reloaded.values.default_directory, "/Books/Unread")
    assert(not reloaded.values.show_qr_code)

    os.remove(state .. "/settings.lua")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("legacy transport defaults migrate to the shared static high port", function()
    local state = os.tmpname() .. ".legacy-port"
    assert(Util.ensure_dir(state))
    assert(Util.write_atomic(state .. "/settings.lua", "return { port = 8080, insecure_http = true }\n", "600"))
    local settings = Settings:new(state)
    equal(settings.values.port, 54321)
    assert(settings.values.insecure_http)
    equal(Settings:new(state).values.port, 54321)

    os.remove(state .. "/settings.lua")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("previous shared default migrates to the memorable private port", function()
    local state = os.tmpname() .. ".previous-default-port"
    assert(Util.ensure_dir(state))
    assert(Util.write_atomic(state .. "/settings.lua", "return { settings_version = 2, port = 53241 }\n", "600"))
    local settings = Settings:new(state)
    equal(settings.values.port, 54321)
    equal(settings.values.settings_version, 4)
    equal(Settings:new(state).values.port, 54321)

    os.remove(state .. "/settings.lua")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("legacy custom ports remain unchanged", function()
    local state = os.tmpname() .. ".custom-port"
    assert(Util.ensure_dir(state))
    assert(Util.write_atomic(state .. "/settings.lua", "return { port = 53000 }\n", "600"))
    equal(Settings:new(state).values.port, 53000)

    os.remove(state .. "/settings.lua")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("hard-float Kindle backend", function()
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "kindle",
        settings = fake_settings(defaults),
        path_exists = function(path) return path == "/lib/ld-linux-armhf.so.3" end,
    }
    equal(daemon:backend_candidates()[1], "/plugin/backend/zenfm-hf")
    equal(daemon:root(), "/mnt/us")
end)

test("KOReader backend output uses crash.log", function()
    local previous = package.loaded.datastorage
    package.loaded.datastorage = {
        getFullDataDir = function() return "/mnt/us/koreader" end,
    }
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "kindle",
        settings = fake_settings(defaults), path_exists = function() return false end,
    }
    equal(daemon:log_path(), "/mnt/us/koreader/crash.log")
    package.loaded.datastorage = previous
end)

test("backend debug logging follows KOReader's debug setting", function()
    local previous = rawget(_G, "G_reader_settings")
    local enabled = false
    _G.G_reader_settings = {
        isTrue = function(_, key)
            equal(key, "debug")
            return enabled
        end,
    }
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "host",
        settings = fake_settings(defaults), path_exists = function() return false end,
    }
    local function has_debug_argument()
        for _, argument in ipairs(daemon:serve_arguments()) do
            if argument == "--debug" then return true end
        end
        return false
    end

    assert(not has_debug_argument())
    enabled = true
    assert(has_debug_argument())
    _G.G_reader_settings = previous
end)

test("installed backend version prefers the installed marker", function()
    local base = os.tmpname() .. ".version"
    local plugin_dir, state_dir = base .. "/plugin", base .. "/state"
    assert(Util.ensure_dir(plugin_dir))
    assert(Util.ensure_dir(state_dir .. "/backend"))
    assert(Util.write_atomic(plugin_dir .. "/VERSION", "2.0.0\n", "600"))
    assert(Util.write_atomic(state_dir .. "/backend/source.version", "1.9.0\nzenfm-hf\nsignature\n", "600"))
    local daemon = Daemon:new{
        plugin_dir = plugin_dir, state_dir = state_dir, platform = "kindle",
        settings = fake_settings(defaults),
    }
    equal(daemon:bundled_backend_version(), "2.0.0")
    equal(daemon:installed_backend_version(), "1.9.0")
    assert(Util.remove_tree(base, base:match("^(.*)/[^/]+$")))
end)

test("PocketBook always uses soft float", function()
    local real_settings = setmetatable({ values = Settings.defaults() }, { __index = Settings })
    equal(real_settings:default_root("pocketbook"), "/mnt/ext1")
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "pocketbook",
        settings = fake_settings(defaults), path_exists = function() return true end,
    }
    equal(daemon:backend_candidates()[1], "/plugin/backend/zenfm-sf")
    equal(daemon:root(), "/mnt/ext1")
    local arguments = table.concat(daemon:serve_arguments(), " ")
    contains(arguments, "--root /mnt/ext1")
    contains(arguments, "--mode-less-filesystem")
end)

test("PocketBook runs the bundled backend without copying it to settings", function()
    local directories, copies = {}, 0
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "pocketbook",
        settings = fake_settings(defaults),
        path_exists = function(path) return path == "/plugin/backend/zenfm-sf" end,
    }
    daemon.ensure_runtime_dir = function() return true end

    local previous_ensure_dir, previous_copy_atomic = Util.ensure_dir, Util.copy_atomic
    Util.ensure_dir = function(path) table.insert(directories, path) return true end
    Util.copy_atomic = function() copies = copies + 1 return false end
    local called, ready, err = pcall(daemon.ensure_backend, daemon)
    Util.ensure_dir, Util.copy_atomic = previous_ensure_dir, previous_copy_atomic

    assert(called, tostring(ready))
    assert(ready, tostring(err))
    equal(#directories, 1)
    equal(directories[1], "/state")
    equal(copies, 0)
    equal(daemon:backend_path(), "/plugin/backend/zenfm-sf")
    contains(daemon:serve_command(nil, false), "'/plugin/backend/zenfm-sf' 'serve'")
    local supervisor = daemon:supervisor_command()
    contains(supervisor, "sh '/plugin/supervisor.sh'")
    assert(not supervisor:find("/state/backend/zenfm", 1, true))
end)

test("PocketBook reset-login uses the bundled backend and mode-less storage", function()
    local command
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "pocketbook",
        settings = fake_settings(defaults),
        path_exists = function(path) return path == "/plugin/backend/zenfm-sf" end,
        execute = function(value) command = value return 0 end,
    }
    daemon.ensure_dirs = function() return true end
    daemon.stop = function() return true end
    daemon.status = function() return false end
    local ok = daemon:reset_login()
    assert(ok)
    contains(command, "sh '/plugin/log-prefix.sh' '/state/zenfm.log'")
    contains(command, "'/plugin/backend/zenfm-sf' reset-login --data-dir '/state' --mode-less-filesystem")
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
    values.advanced_root, values.insecure_http, values.auto_stop_minutes = true, true, 45
    values.default_directory = "/Books"
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "kindle",
        settings = fake_settings(values), path_exists = function() return false end,
    }
    local command = table.concat(daemon:serve_arguments(), " ")
    contains(command, "--root / --default-directory /Books --data-dir /state")
    contains(command, "--listen 0.0.0.0:" .. tostring(values.port))
    contains(command, "--auto-stop 45m")
    contains(command, "--insecure-http")
end)

test("Kobo enables the hidden-file default for every backend initialization path", function()
    local kobo = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "kobo",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
    }
    contains(table.concat(kobo:serve_arguments(), " "), "--show-hidden-by-default")

    local reset_command
    kobo.stop = function() return true end
    kobo.status = function() return false end
    kobo.ensure_backend = function() return true end
    kobo.execute = function(command) reset_command = command return 0 end
    assert(kobo:reset_login())
    contains(reset_command, "reset-login --data-dir '/state' --show-hidden-by-default")

    local kindle = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "kindle",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
    }
    assert(not table.concat(kindle:serve_arguments(), " "):find("--show-hidden-by-default", 1, true))
end)

test("Kindle supervisor requests firewall setup", function()
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "kindle",
        settings = fake_settings(Settings.defaults()), execute = function() return 0 end,
    }
    contains(daemon:supervisor_command(), "--port '54321' --kindle --")
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
    assert(not commands[1]:find("|| true", 1, true))
end)

test("PocketBook runtime directory keeps creation checks while chmod is best effort", function()
    local command
    local daemon = Daemon:new{
        plugin_dir = "/mnt/ext1/applications/koreader/plugins/zenfm.koplugin",
        state_dir = "/mnt/ext1/applications/koreader/settings/ZenFM",
        platform = "pocketbook", settings = fake_settings(Settings.defaults()),
        execute = function(value) command = value return 0 end,
    }
    assert(daemon:ensure_runtime_dir())
    contains(command, "|| [ -d ")
    contains(command, "] || exit 1; [ ! -L ")
    contains(command, "chmod 700 ")
    contains(command, ">/dev/null 2>&1 || true")
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

test("Android status address falls back to the routed socket address", function()
    local closed = false
    local udp = {
        setpeername = function(_, host, port)
            equal(host, "1.1.1.1")
            equal(port, 53)
            return 1
        end,
        getsockname = function() return "192.168.4.12", 49152 end,
        close = function() closed = true end,
    }
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", android = {},
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        command_output = function() return "" end,
        socket = { udp = function() return udp end },
    }
    local details = daemon:status_details_from_raw("ok running https://0.0.0.0:8443 sha256:abc")
    equal(details.url, "https://192.168.4.12:8443")
    assert(closed)
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
        socket = false,
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
    contains(executed, "sh '/plugin/log-prefix.sh' '/state/zenfm.log'")
    contains(executed, "</dev/null")
end)

test("restart waits for the old supervisor before starting again", function()
    local events, pid_checks, lock_checks = {}, 0, 0
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = "/state", platform = "host",
        settings = fake_settings(defaults),
        path_exists = function(path)
            if path:match("supervisor%.pid$") then
                pid_checks = pid_checks + 1
                return pid_checks < 3
            end
            if path:match("supervisor%.pid%.lock$") then
                lock_checks = lock_checks + 1
                return lock_checks < 3
            end
            return false
        end,
        sleep = function() table.insert(events, "sleep") end,
    }
    daemon.stop = function() table.insert(events, "stop") return true end
    daemon.status = function() return false end
    daemon.start = function() table.insert(events, "start") return true, "restarted" end

    local ok, detail = daemon:restart()
    assert(ok)
    equal(detail, "restarted")
    equal(events[1], "stop")
    equal(events[#events], "start")
    equal(pid_checks, 5)
    equal(lock_checks, 3)
end)

test("Android handoff carries paired token and validated settings", function()
    local state = os.tmpname() .. ".d"
    local values = Settings.defaults()
    values.port, values.auto_stop_minutes, values.default_directory = 9443, 45, "/Books/Unread"
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
    contains(uri, "default_directory=%2FBooks%2FUnread")
    contains(uri, "port=9443")
    contains(uri, "auto_stop=45m")
    assert(not uri:find("beta=", 1, true))
    values.beta_updates = true
    local update_uri = assert(daemon:android_uri("update"))
    contains(update_uri, "beta=1")
    os.remove(state .. "/android-control.token")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("Android intent bridge launches the explicit companion component", function()
    local uri = "zenfm://status?token=" .. string.rep("ab", 32)
    local opened, state = exercise_android_intent(uri, false)
    assert(opened)
    assert(state.started)
    equal(state.clears, 0)
    equal(state.pops, 1)
end)

test("Android intent bridge clears launch exceptions without describing them", function()
    local uri = "zenfm://status?token=" .. string.rep("cd", 32)
    local opened, state = exercise_android_intent(uri, true)
    assert(not opened)
    assert(state.started)
    equal(state.clears, 1)
    equal(state.pops, 1)
end)

test("Android handoff launches explicitly and returns before polling", function()
    local state = os.tmpname() .. ".d"
    local launched_uri
    local shell_calls = 0
    local implicit_calls = 0
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        android = {
            getExternalStoragePath = function() return "/storage/emulated/0" end,
            openLink = function() implicit_calls = implicit_calls + 1 return true end,
        },
        android_launcher = function(uri) launched_uri = uri return true end,
        execute = function() shell_calls = shell_calls + 1 return 1 end,
        sleep = function() error("Android handoff blocked while polling") end,
        android_poll_attempts = 2,
    }
    local ok, request_id = daemon:begin_android("start")
    assert(ok, tostring(request_id))
    assert(request_id:match("^[0-9a-f]+$") and #request_id == 32)
    equal(AndroidIntent.ACTION, "android.intent.action.VIEW")
    equal(AndroidIntent.PACKAGE, "org.zenlabs.zenfm")
    equal(AndroidIntent.CLASS, "org.zenlabs.zenfm.ZenFMActivity")
    equal(implicit_calls, 0)
    equal(shell_calls, 0)
    contains(launched_uri, "zenfm://start?token=")
    os.remove(state .. "/android-control.token")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("Android launch failures are redacted and do not poll", function()
    local state = os.tmpname() .. ".launch-failure"
    local token = string.rep("ab", 32)
    assert(Util.ensure_dir(state))
    assert(Util.write_atomic(state .. "/android-control.token", token .. "\n", "600"))
    local polls = 0
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        android = { getExternalStoragePath = function() return "/storage/emulated/0" end },
        android_launcher = function(uri) error("launch rejected: " .. uri) end,
        execute = function() error("Android handoff used a shell") end,
        sleep = function() polls = polls + 1 end,
        android_poll_attempts = 2,
    }
    local ok, detail = daemon:begin_android("start")
    assert(not ok)
    equal(polls, 0)
    assert(not detail:find(token, 1, true))
    assert(not detail:find("zenfm://", 1, true))
    assert(not Util.path_exists(state .. "/android-companion.status"))
    assert(not Util.path_exists(state .. "/android-companion.log"))
    os.remove(state .. "/android-control.token")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("Android handoff fails closed without the native intent bridge", function()
    local state = os.tmpname() .. ".missing-bridge"
    local implicit_calls = 0
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        android = {
            getExternalStoragePath = function() return "/storage/emulated/0" end,
            openLink = function() implicit_calls = implicit_calls + 1 return true end,
        },
        execute = function() error("Android handoff used a shell") end,
        sleep = function() error("failed launch was polled") end,
        android_poll_attempts = 2,
    }
    local ok, detail = daemon:begin_android("status")
    assert(not ok)
    equal(implicit_calls, 0)
    contains(detail, "could not open its Android companion")
    os.remove(state .. "/android-control.token")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("Android lifecycle checks one fresh request-bound result at a time", function()
    local state = os.tmpname() .. ".fresh"
    assert(Util.ensure_dir(state))
    assert(Util.write_atomic(state .. "/android-companion.status",
        "ok running https://stale:8443 - request=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", "600"))
    local calls = 0
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        android = { getExternalStoragePath = function() return "/storage/emulated/0" end },
        android_launcher = function() calls = calls + 1 return true end,
        execute = function() error("Android handoff used a shell") end,
        sleep = function() error("Android result check slept") end,
        android_poll_attempts = 2,
    }
    local ok, request_id = daemon:begin_android("start")
    assert(ok, tostring(request_id))
    equal(calls, 1)
    local done = daemon:check_android_result("start", request_id)
    assert(not done)
    assert(Util.write_atomic(state .. "/android-companion.status",
        "starting request=" .. request_id .. "\n", "600"))
    done = daemon:check_android_result("start", request_id)
    assert(not done)
    assert(Util.write_atomic(state .. "/android-companion.status",
        "ok running https://0.0.0.0:8443 sha256:fresh request=" .. request_id .. "\n", "600"))
    local success, detail
    done, success, detail = daemon:check_android_result("start", request_id)
    assert(done and success, tostring(detail))
    contains(detail, "sha256:fresh")
    os.remove(state .. "/android-control.token")
    os.remove(state .. "/android-companion.status")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("Android cold stop carries home and recognizes its terminal marker", function()
    local state = os.tmpname() .. ".stop"
    local launched_uri
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        android = { getExternalStoragePath = function() return "/storage/emulated/0" end },
        android_launcher = function(uri)
            launched_uri = uri
            local request_id = assert(uri:match("request_id=([0-9a-f]+)"))
            assert(Util.write_atomic(state .. "/android-companion.status",
                "stopped request=" .. request_id .. "\n", "600"))
            return true
        end,
        execute = function() error("Android handoff used a shell") end,
        sleep = function() end,
        android_poll_attempts = 2,
    }
    local ok, request_id = daemon:begin_android("stop")
    assert(ok, tostring(request_id))
    contains(launched_uri, "home=" .. Util.url_encode(state))
    local done, success, detail = daemon:check_android_result("stop", request_id)
    assert(done and success, tostring(detail))
    contains(detail, "stopped request=")
    os.remove(state .. "/android-control.token")
    os.remove(state .. "/android-companion.status")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("Android result check rejects stale persisted running state", function()
    local state = os.tmpname() .. ".status-stale"
    assert(Util.ensure_dir(state))
    assert(Util.write_atomic(state .. "/android-companion.status",
        "ok running https://stale:8443 - request=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", "600"))
    local calls = 0
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        android = { getExternalStoragePath = function() return "/storage/emulated/0" end },
        android_launcher = function(uri) calls = calls + 1 contains(uri, "zenfm://status?") return true end,
        execute = function() error("Android handoff used a shell") end,
        sleep = function() end,
        android_poll_attempts = 2,
    }
    local ok, request_id = daemon:begin_android("status")
    assert(ok, tostring(request_id))
    equal(calls, 1)
    local done = daemon:check_android_result("status", request_id)
    assert(not done)
    assert(Util.write_atomic(state .. "/android-companion.status",
        "stopped request=" .. request_id .. "\n", "600"))
    local success, detail
    done, success, detail = daemon:check_android_result("status", request_id)
    assert(done and success, tostring(detail))
    contains(detail, "stopped request=")
    assert(not detail:find("stale", 1, true))
    os.remove(state .. "/android-control.token")
    os.remove(state .. "/android-companion.status")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("Android cached status never launches the companion", function()
    local state = os.tmpname() .. ".status-live"
    assert(Util.ensure_dir(state))
    assert(Util.write_atomic(state .. "/android-companion.status",
        "ok running https://0.0.0.0:8443 sha256:fresh request=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", "600"))
    local launches = 0
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        android = { getExternalStoragePath = function() return "/storage/emulated/0" end },
        android_launcher = function() launches = launches + 1 return true end,
        execute = function() error("Android handoff used a shell") end,
        sleep = function() end,
        android_poll_attempts = 2,
    }
    local running, detail = daemon:status()
    assert(running, tostring(detail))
    equal(launches, 0)
    contains(detail, "sha256:fresh")
    os.remove(state .. "/android-control.token")
    os.remove(state .. "/android-companion.status")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("Android cached status reports an inactivity stop without relaunching", function()
    local state = os.tmpname() .. ".idle-stopped"
    assert(Util.ensure_dir(state))
    assert(Util.write_atomic(state .. "/android-companion.status",
        "idle_stopped request=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", "600"))
    local launches = 0
    local daemon = Daemon:new{
        plugin_dir = "/plugin", state_dir = state, platform = "android",
        settings = fake_settings(Settings.defaults()), path_exists = function() return false end,
        android = { getExternalStoragePath = function() return "/storage/emulated/0" end },
        android_launcher = function() launches = launches + 1 return true end,
    }
    local running, detail = daemon:status()
    assert(not running)
    contains(detail, "idle_stopped")
    equal(launches, 0)
    os.remove(state .. "/android-companion.status")
    os.execute("rmdir " .. Util.sh_quote(state) .. " >/dev/null 2>&1")
end)

test("settings validation", function()
    local values = Settings.sanitize{
        port = 70000, advanced_root = true, insecure_http = true,
        custom_root = "relative", default_directory = "/Books/../private", auto_stop_minutes = 45, beta_updates = "yes",
        tls_cert = "/cert", tls_key = "relative",
    }
    equal(values.port, 54321)
    assert(values.advanced_root and values.insecure_http)
    equal(values.custom_root, "")
    equal(values.default_directory, "/")
    equal(values.auto_stop_minutes, 45)
    assert(not values.beta_updates)
    assert(Settings.sanitize{ beta_updates = true }.beta_updates)
    equal(Settings.sanitize{ auto_stop_minutes = -1 }.auto_stop_minutes, 0)
    equal(Settings.sanitize{ auto_stop_minutes = 1.5 }.auto_stop_minutes, 0)
    equal(Settings.sanitize{ auto_stop_minutes = 720 }.auto_stop_minutes, 720)
    equal(Settings.sanitize{ auto_stop_minutes = 721 }.auto_stop_minutes, 0)
    equal(values.tls_key, "")
    equal(Settings.sanitize{ custom_root = "/" }.custom_root, "")
    equal(Settings.sanitize{ custom_root = "/safe/../" }.custom_root, "")
    equal(Settings.sanitize{ default_directory = "/Books" }.default_directory, "/Books")
    equal(Settings.sanitize{ default_directory = "/Books/" }.default_directory, "/")
end)

test("normal mode never falls back to literal root when home is unavailable", function()
    local settings = setmetatable({ values = Settings.defaults() }, { __index = Settings })
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

test("release selection requires a GitHub digest and bounded size", function()
    local daemon = {
        is_android = function() return false end,
        platform = function() return "host" end,
        kernel = function() return "linux" end,
    }
    local function release(digest, size)
        return {
            tag_name = "v1.2.3",
            assets = {{
                name = "ZenFM-koreader-linux-1.2.3.zip",
                browser_download_url = "https://github.com/xZenLabs/zen-fm/releases/download/v1.2.3/ZenFM-koreader-linux-1.2.3.zip",
                digest = digest,
                size = size,
            }},
        }
    end
    local selected = assert(Updater.select_release({ release("sha256:" .. string.rep("A", 64), 42) }, daemon, "1.2.2"))
    equal(selected.version, "1.2.3")
    equal(selected.digest, string.rep("a", 64))
    equal(selected.size, 42)
    assert(not Updater.select_release({ release("", 42) }, daemon, "1.2.2"))
    assert(not Updater.select_release({ release("sha256:" .. string.rep("a", 64), 0) }, daemon, "1.2.2"))
    assert(not Updater.select_release({ release("sha256:" .. string.rep("a", 64), 201 * 1024 * 1024) }, daemon, "1.2.2"))
end)

test("beta release selection remains stable-first for matching versions", function()
    local daemon = {
        is_android = function() return false end,
        platform = function() return "host" end,
        kernel = function() return "linux" end,
    }
    local function release(version, prerelease)
        return {
            tag_name = "v" .. version,
            prerelease = prerelease,
            assets = {{
                name = "ZenFM-koreader-linux-" .. version .. ".zip",
                browser_download_url = "https://github.com/xZenLabs/zen-fm/releases/download/v"
                    .. version .. "/ZenFM-koreader-linux-" .. version .. ".zip",
                digest = "sha256:" .. string.rep("a", 64),
                size = 42,
            }},
        }
    end

    local releases = {
        release("1.3.0-beta10", true),
        release("1.2.0", false),
    }
    equal(Updater.select_release(releases, daemon, "1.2.0"), nil)
    equal(assert(Updater.select_release(releases, daemon, "1.2.0", true)).version, "1.3.0-beta10")
    equal(assert(Updater.select_release({
        release("1.3.0-beta10", true),
        release("1.3.0", false),
    }, daemon, "1.2.0", true)).version, "1.3.0")
    assert(Updater.version_greater("1.3.0-beta10", "1.3.0-beta2"))
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

test("archive extraction validates layout before preserving release modes", function()
    local previous_archiver = package.loaded["ffi/archiver"]
    local function extract(entries, extraction_entries)
        local extracted, readers = {}, 0
        package.loaded["ffi/archiver"] = {
            Reader = {
                new = function()
                    readers = readers + 1
                    -- KOReader caches headers on a Reader. Reopening this same
                    -- object must still expose its original manifest.
                    local selected = readers == 1 and entries or (extraction_entries or entries)
                    local reader = { err = nil }
                    function reader:open() return true end
                    function reader:iterate()
                        local index = 0
                        return function()
                            index = index + 1
                            return selected[index]
                        end
                    end
                    function reader:extractToPath(path, destination)
                        table.insert(extracted, { path = path, destination = destination })
                        return true
                    end
                    function reader:close() self.err = nil end
                    return reader
                end,
            },
        }
        local called, accepted, err = pcall(Updater.extract_archive, "/update.zip", "/stage")
        package.loaded["ffi/archiver"] = previous_archiver
        assert(called, tostring(accepted))
        return accepted, err, extracted, readers
    end

    local accepted, err, extracted, readers = extract({
        { path = "zenfm.koplugin/", mode = "directory", size = 0 },
        { path = "zenfm.koplugin/backend/", mode = "directory", size = 0 },
        { path = "zenfm.koplugin/backend/zenfm-sf", mode = "file", size = 42 },
    })
    assert(accepted, tostring(err))
    equal(readers, 2)
    equal(#extracted, 3)
    equal(extracted[3].path, "zenfm.koplugin/backend/zenfm-sf")
    equal(extracted[3].destination, "/stage/zenfm.koplugin/backend/zenfm-sf")

    accepted, err, extracted = extract({
        { path = "zenfm.koplugin/main.lua", mode = "file", size = 1 },
        { path = "zenfm.koplugin/../escape", mode = "file", size = 1 },
    })
    assert(not accepted)
    contains(err, "invalid layout")
    equal(#extracted, 0)

    accepted, err, extracted = extract({
        { path = "zenfm.koplugin/link", mode = "link", size = 0 },
    })
    assert(not accepted)
    contains(err, "invalid layout")
    equal(#extracted, 0)

    accepted, err, extracted, readers = extract({
        { path = "zenfm.koplugin/main.lua", mode = "file", size = 1 },
    }, {
        { path = "zenfm.koplugin/backend/zenfm-sf", mode = "file", size = 1 },
    })
    assert(not accepted)
    equal(readers, 2)
    contains(err, "changed during extraction")
    equal(#extracted, 0)
end)

test("PocketBook activation moves the extracted tree without losing executable mode", function()
    local base = os.tmpname() .. ".pocketbook-update"
    local plugin_dir = base .. "/zenfm.koplugin"
    local stage = plugin_dir .. ".update-stage"
    local stage_root = stage .. "/zenfm.koplugin"
    assert(Util.ensure_dir(plugin_dir))
    assert(Util.ensure_dir(stage_root .. "/backend"))
    assert(Util.write_atomic(plugin_dir .. "/old-version", "old\n", "600"))
    assert(Util.write_atomic(stage_root .. "/supervisor.sh", "#!/bin/sh\n", "700"))
    local backend = stage_root .. "/backend/zenfm-sf"
    assert(Util.write_atomic(backend, "#!/bin/sh\nexit 0\n", "700"))
    local executable_modes = Util.execute("chmod 700 " .. Util.sh_quote(backend)) == 0
        and Util.execute("test -x " .. Util.sh_quote(backend)) == 0

    local previous_copy_tree, previous_execute = Util.copy_tree, os.execute
    local commands = {}
    Util.copy_tree = function() error("PocketBook update copied its extracted tree") end
    os.execute = function(command)
        table.insert(commands, command)
        return previous_execute(command)
    end
    local called, installed, detail = pcall(Updater.install_stage, {
        plugin_dir = plugin_dir,
        is_pocketbook = function() return true end,
    }, stage_root, false)
    Util.copy_tree, os.execute = previous_copy_tree, previous_execute

    assert(called, tostring(installed))
    assert(installed, tostring(detail))
    equal(Util.trim(Util.read_all(plugin_dir .. "/backend/zenfm-sf", 64)), "#!/bin/sh\nexit 0")
    if executable_modes then
        equal(Util.execute("test -x " .. Util.sh_quote(plugin_dir .. "/backend/zenfm-sf")), 0)
    end
    for _, command in ipairs(commands) do
        assert(not command:find("supervisor.sh", 1, true), "PocketBook activation attempted chmod")
    end
    local parent = base:match("^(.*)/[^/]+$")
    if parent then Util.remove_tree(base, parent) end
end)

test("activation reports a rollback rename failure", function()
    local base = os.tmpname() .. ".activation-rollback"
    local plugin_dir = base .. "/zenfm.koplugin"
    local stage_root = base .. "/stage"
    local incoming, backup = plugin_dir .. ".incoming", plugin_dir .. ".rollback"
    assert(Util.ensure_dir(plugin_dir))
    assert(Util.ensure_dir(stage_root))
    assert(Util.write_atomic(plugin_dir .. "/old-version", "old\n", "600"))
    assert(Util.write_atomic(stage_root .. "/new-version", "new\n", "600"))

    local previous_rename = os.rename
    os.rename = function(source, destination)
        if source == incoming and destination == plugin_dir then
            return nil, "activation denied"
        end
        if source == backup and destination == plugin_dir then
            return nil, "restore denied"
        end
        return previous_rename(source, destination)
    end
    local called, installed, detail = pcall(Updater.install_stage, {
        plugin_dir = plugin_dir,
        is_pocketbook = function() return true end,
    }, stage_root, false)
    os.rename = previous_rename

    assert(called, tostring(installed))
    assert(not installed)
    contains(detail, "rollback failed")
    contains(detail, "restore denied")
    local parent = base:match("^(.*)/[^/]+$")
    if parent then Util.remove_tree(base, parent) end
end)

test("pending-marker failure reports a rollback rename failure", function()
    local base = os.tmpname() .. ".marker-rollback"
    local plugin_dir = base .. "/zenfm.koplugin"
    local stage_root = base .. "/stage"
    local backup = plugin_dir .. ".rollback"
    assert(Util.ensure_dir(plugin_dir))
    assert(Util.ensure_dir(stage_root))
    assert(Util.write_atomic(plugin_dir .. "/old-version", "old\n", "600"))
    assert(Util.write_atomic(stage_root .. "/new-version", "new\n", "600"))

    local previous_rename, previous_write_atomic = os.rename, Util.write_atomic
    os.rename = function(source, destination)
        if source == backup and destination == plugin_dir then return nil, "restore denied" end
        return previous_rename(source, destination)
    end
    Util.write_atomic = function(path, ...)
        if path == plugin_dir .. "/.update-pending" then return false end
        return previous_write_atomic(path, ...)
    end
    local called, installed, detail = pcall(Updater.install_stage, {
        plugin_dir = plugin_dir,
        is_pocketbook = function() return true end,
    }, stage_root, false)
    os.rename, Util.write_atomic = previous_rename, previous_write_atomic

    assert(called, tostring(installed))
    assert(not installed)
    contains(detail, "rollback failed")
    contains(detail, "restore denied")
    local parent = base:match("^(.*)/[^/]+$")
    if parent then Util.remove_tree(base, parent) end
end)

test("a second update cannot erase rollback state before restart", function()
    local plugin_dir = os.tmpname() .. ".pending-update"
    assert(Util.ensure_dir(plugin_dir))
    assert(Util.write_atomic(plugin_dir .. "/.update-pending", "pending\n", "600"))
    local prepared, detail = Updater.prepare_latest({
        plugin_dir = plugin_dir,
        state_dir = plugin_dir .. "/data",
    }, false)
    assert(not prepared)
    contains(detail, "Restart KOReader")
    local available, check_detail = Updater.check_latest({ plugin_dir = plugin_dir }, false)
    assert(not available)
    contains(check_detail, "Restart KOReader")
    local parent = plugin_dir:match("^(.*)/[^/]+$")
    if parent then Util.remove_tree(plugin_dir, parent) end
end)

test("activation waits for supervisor exit and restores a running service on failure", function()
    local previous_install_stage = Updater.install_stage
    local events = {}
    Updater.install_stage = function(_, root, resume_after_update)
        equal(root, "/prepared/plugin")
        assert(resume_after_update)
        table.insert(events, "install")
        return false, "activation failed"
    end
    local called, installed, detail = pcall(Updater.activate_stage, {
        is_android = function() return false end,
        status = function() table.insert(events, "status") return true end,
        stop = function() table.insert(events, "stop") return true end,
        wait_until_stopped = function() table.insert(events, "wait") return true end,
        start = function() table.insert(events, "start") return true end,
    }, "/prepared/plugin")
    Updater.install_stage = previous_install_stage

    assert(called, tostring(installed))
    assert(not installed)
    equal(detail, "activation failed")
    equal(table.concat(events, ","), "status,stop,wait,install,start")
end)

test("activation replaces and restarts a running server", function()
    local previous_install_stage = Updater.install_stage
    local events = {}
    Updater.install_stage = function(_, root, resume_after_update)
        equal(root, "/prepared/plugin")
        assert(resume_after_update)
        table.insert(events, "install")
        return true, "updated"
    end
    local called, installed, detail = pcall(Updater.activate_stage, {
        is_android = function() return false end,
        status = function() table.insert(events, "status") return true end,
        stop = function() table.insert(events, "stop") return true end,
        wait_until_stopped = function() table.insert(events, "wait") return true end,
        start = function() table.insert(events, "start") return true end,
    }, "/prepared/plugin")
    Updater.install_stage = previous_install_stage

    assert(called, tostring(installed))
    assert(installed)
    equal(detail, "updated")
    equal(table.concat(events, ","), "status,stop,wait,install,start")
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
        is_android = function() return false end,
        ensure_backend = function() return true end,
        start = function() running = true return true end,
        status = function() return running end,
        stop = function() stops = stops + 1 running = false return true end,
        wait_until_stopped = function() return true end,
    }
    local healthy, err = Updater.finalize_pending(daemon)
    assert(healthy, tostring(err))
    equal(stops, 2)
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
        is_android = function() return false end,
        ensure_backend = function() return false, "broken backend" end,
        start = function() error("must not start") end,
        status = function() return false end,
        stop = function() return true end,
        wait_until_stopped = function() return true end,
    }
    local healthy, err = Updater.finalize_pending(daemon)
    assert(not healthy)
    contains(err, "previous plugin restored")
    equal(Util.trim(Util.read_all(plugin_dir .. "/old-version", 32)), "old")
    assert(not Util.path_exists(plugin_dir .. "/new-version"))
    local parent = plugin_dir:match("^(.*)/[^/]+$")
    if parent then Util.remove_tree(plugin_dir, parent) end
end)

test("Android pending plugin update commits without controlling the companion", function()
    local plugin_dir = os.tmpname() .. ".plugin"
    local backup = plugin_dir .. ".rollback"
    assert(Util.ensure_dir(plugin_dir))
    assert(Util.ensure_dir(backup))
    assert(Util.write_atomic(plugin_dir .. "/.update-pending", backup .. "\nstop\n", "600"))
    local function forbidden() error("Android plugin activation controlled the companion") end
    local daemon = {
        plugin_dir = plugin_dir,
        is_android = function() return true end,
        ensure_backend = forbidden,
        start = forbidden,
        status = forbidden,
        stop = forbidden,
    }
    local healthy, err = Updater.finalize_pending(daemon)
    assert(healthy, tostring(err))
    assert(not Util.path_exists(plugin_dir .. "/.update-pending"))
    assert(not Util.path_exists(backup))
    local parent = plugin_dir:match("^(.*)/[^/]+$")
    if parent then Util.remove_tree(plugin_dir, parent) end
end)

test("shell quoting does not create a second command", function()
    equal(Util.sh_quote("x'; touch /tmp/owned; '"), "'x'\\''; touch /tmp/owned; '\\'''" )
end)

test("opening the Android menu uses cached state and exit preserves the service", function()
    local module_names = {
        "dispatcher", "ui/widget/infomessage", "ui/widget/inputdialog", "ui/widget/confirmbox",
        "ui/uimanager", "ui/widget/container/widgetcontainer", "gettext", "zenfm_daemon", "zenfm_updater",
    }
    local saved = {}
    for _, name in ipairs(module_names) do saved[name] = package.loaded[name] end
    package.loaded["dispatcher"] = { registerAction = function() end }
    package.loaded["ui/widget/infomessage"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/inputdialog"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/confirmbox"] = { new = function(_, options) return options end }
    package.loaded["ui/uimanager"] = { show = function() end, scheduleIn = function() end }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = { finalize_pending = function() return true end }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local cached_calls = 0
    local owner = setmetatable({
        daemon = {
            settings = { values = Settings.defaults() },
            is_android = function() return true end,
            cached_android_status = function() cached_calls = cached_calls + 1 return false, "stopped" end,
            status = function() error("Android menu performed a live status request") end,
            stop = function() error("KOReader exit stopped ZenFM") end,
        },
    }, { __index = ZenFM })
    local menu = {}
    owner:addToMainMenu(menu)
    equal(menu.zenfm.sub_item_table[1].text_func(), "Start ZenFM")
    equal(cached_calls, 1)
    owner.android_pending = {}
    owner.android_running = true
    equal(owner:onExit(), nil)
    assert(owner.android_pending == nil)
    assert(not owner.android_running)

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

test("dispatcher exposes the server toggle and settings end with the version", function()
    local module_names = {
        "dispatcher", "ui/widget/infomessage", "ui/widget/inputdialog", "ui/widget/confirmbox",
        "ui/uimanager", "ui/widget/container/widgetcontainer", "gettext", "zenfm_daemon", "zenfm_updater",
    }
    local saved, actions = {}, {}
    for _, name in ipairs(module_names) do saved[name] = package.loaded[name] end
    package.loaded["dispatcher"] = {
        registerAction = function(_, name, action) actions[name] = action end,
    }
    package.loaded["ui/widget/infomessage"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/inputdialog"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/confirmbox"] = { new = function(_, options) return options end }
    package.loaded["ui/uimanager"] = { show = function() end }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = { finalize_pending = function() return true end }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    ZenFM:onDispatcherRegisterActions()
    equal(actions.zenfm_toggle.event, "ToggleZenFM")
    equal(actions.zenfm_toggle.title, "ZenFM: Toggle server")
    assert(actions.zenfm_toggle.general)
    assert(type(ZenFM["on" .. actions.zenfm_toggle.event]) == "function")

    local owner = setmetatable({
        daemon = {
            settings = { values = Settings.defaults() },
            root = function() return "/mnt/us" end,
            installed_backend_version = function() return "9.8.7" end,
        },
    }, { __index = ZenFM })
    local main_menu = {}
    owner:addToMainMenu(main_menu)
    local root_menu = main_menu.zenfm.sub_item_table
    assert(root_menu[1].keep_menu_open)
    assert(root_menu[2].keep_menu_open)
    local toggles, menu_updates = 0, 0
    owner.onToggleZenFM = function() toggles = toggles + 1 end
    root_menu[1].callback({ updateItems = function() menu_updates = menu_updates + 1 end })
    equal(toggles, 1)
    equal(menu_updates, 1)
    local settings_menu = root_menu[3].sub_item_table
    equal(#settings_menu, 9)
    equal(settings_menu[3].text_func(), "Default directory: /mnt/us")
    local advanced_menu = settings_menu[5].sub_item_table
    equal(settings_menu[5].text, "Advanced")
    equal(#advanced_menu, 3)
    equal(advanced_menu[1].text, "Port: 54321")
    equal(advanced_menu[2].text, "Root: expose /")
    equal(advanced_menu[3].text, "Reset owner login")
    equal(settings_menu[#settings_menu - 3].text, "Beta updates")
    assert(not settings_menu[#settings_menu - 3].checked_func())
    equal(settings_menu[#settings_menu - 2].text, "Show QR code")
    assert(settings_menu[#settings_menu - 2].checked_func())
    equal(settings_menu[#settings_menu - 1].text, "Update")
    assert(settings_menu[#settings_menu - 1].keep_menu_open)
    equal(settings_menu[#settings_menu].text_func(), "Version: 9.8.7")
    assert(not settings_menu[#settings_menu].enabled_func())

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

test("root and default directory settings use KOReader's folder chooser", function()
    local module_names = {
        "dispatcher", "ui/widget/infomessage", "ui/widget/inputdialog", "ui/widget/confirmbox",
        "ui/widget/pathchooser", "ui/uimanager", "ui/widget/container/widgetcontainer", "gettext",
        "zenfm_daemon", "zenfm_updater",
    }
    local saved, shown = {}, {}
    for _, name in ipairs(module_names) do saved[name] = package.loaded[name] end
    package.loaded["dispatcher"] = { registerAction = function() end }
    package.loaded["ui/widget/infomessage"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/inputdialog"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/confirmbox"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/pathchooser"] = { new = function(_, options) return options end }
    package.loaded["ui/uimanager"] = { show = function(_, widget) table.insert(shown, widget) end }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = { finalize_pending = function() return true end }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local settings = {
        values = Settings.defaults(),
        set = function(self, key, value) self.values[key] = value return true end,
    }
    local daemon = { settings = settings }
    function daemon:device_root() return "/mnt/us" end
    function daemon:root()
        if self.settings.values.advanced_root then return "/" end
        return self.settings.values.custom_root ~= "" and self.settings.values.custom_root or self:device_root()
    end
    local owner = setmetatable({ daemon = daemon }, { __index = ZenFM })
    local menu_updates = 0
    local touchmenu = { updateItems = function() menu_updates = menu_updates + 1 end }
    equal(owner:settings_menu()[2].text_func(), "Home: /mnt/us")
    equal(owner:settings_menu()[3].text_func(), "Default directory: /mnt/us")

    owner:settings_menu()[2].callback(touchmenu)
    local root_chooser = shown[#shown]
    assert(root_chooser.select_directory and not root_chooser.select_file and not root_chooser.show_files)
    equal(root_chooser.path, "/mnt/us")
    root_chooser.onConfirm("/mnt/us/Library")
    equal(settings.values.custom_root, "/mnt/us/Library")
    equal(owner:settings_menu()[3].text_func(), "Default directory: /mnt/us/Library")
    equal(menu_updates, 1)

    owner:settings_menu()[3].callback(touchmenu)
    local default_chooser = shown[#shown]
    equal(default_chooser.path, "/mnt/us/Library")
    default_chooser.onConfirm("/mnt/us/Library/Books")
    equal(settings.values.default_directory, "/Books")
    equal(owner:settings_menu()[3].text_func(), "Default directory: /mnt/us/Library/Books")
    equal(menu_updates, 2)

    owner:settings_menu()[3].callback(touchmenu)
    local outside_chooser = shown[#shown]
    outside_chooser.onConfirm("/mnt/us/Elsewhere")
    equal(settings.values.default_directory, "/Books")
    equal(menu_updates, 2)
    equal(shown[#shown].text, "Choose a folder within ZenFM Home.")

    owner:settings_menu()[2].callback(touchmenu)
    local device_root_chooser = shown[#shown]
    device_root_chooser.onConfirm("/mnt/us")
    equal(settings.values.custom_root, "")
    equal(settings.values.default_directory, "/")
    equal(menu_updates, 3)
    contains(shown[#shown].text, "default directory was reset to Home")

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

test("starting waits for KOReader network connection but stopping does not", function()
    local module_names = {
        "dispatcher", "ui/widget/infomessage", "ui/widget/inputdialog", "ui/widget/confirmbox",
        "ui/uimanager", "ui/widget/container/widgetcontainer", "gettext", "zenfm_daemon", "zenfm_updater",
        "ui/network/manager",
    }
    local saved = {}
    for _, name in ipairs(module_names) do saved[name] = package.loaded[name] end
    package.loaded["dispatcher"] = { registerAction = function() end }
    package.loaded["ui/widget/infomessage"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/inputdialog"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/confirmbox"] = { new = function(_, options) return options end }
    package.loaded["ui/uimanager"] = { show = function() end }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = { finalize_pending = function() return true end }

    local connected, retry, network_checks = false, nil, 0
    package.loaded["ui/network/manager"] = {
        willRerunWhenConnected = function(_, callback)
            network_checks = network_checks + 1
            if connected then return false end
            retry = callback
            return true
        end,
    }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local running, starts, stops = false, 0, 0
    local owner = setmetatable({
        daemon = {
            is_android = function() return false end,
            status = function() return running end,
            start = function() starts = starts + 1 running = true return true end,
            stop = function() stops = stops + 1 running = false return true end,
        },
        start_server_monitor = function() end,
        onShowZenFMStatus = function() end,
    }, { __index = ZenFM })

    assert(owner:onToggleZenFM())
    equal(starts, 0)
    equal(stops, 0)
    equal(network_checks, 1)
    assert(type(retry) == "function")

    connected = true
    retry()
    equal(starts, 1)
    equal(network_checks, 2)

    connected = false
    assert(owner:onToggleZenFM())
    equal(stops, 1)
    equal(network_checks, 2)

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

test("inactivity timeout label opens a number wheel and its checkbox only toggles it", function()
    local module_names = {
        "dispatcher", "ui/widget/infomessage", "ui/widget/inputdialog", "ui/widget/confirmbox",
        "ui/widget/spinwidget", "ui/uimanager", "ui/widget/container/widgetcontainer", "gettext",
        "zenfm_daemon", "zenfm_updater",
    }
    local saved, shown = {}, {}
    for _, name in ipairs(module_names) do saved[name] = package.loaded[name] end
    package.loaded["dispatcher"] = { registerAction = function() end }
    package.loaded["ui/widget/infomessage"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/inputdialog"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/confirmbox"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/spinwidget"] = { new = function(_, options) return options end }
    package.loaded["ui/uimanager"] = {
        show = function(_, widget) table.insert(shown, widget) end,
    }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = { finalize_pending = function() return true end }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local settings = {
        values = Settings.defaults(),
        set = function(self, key, value) self.values[key] = value return true end,
    }
    local owner = setmetatable({ daemon = { settings = settings } }, { __index = ZenFM })
    local menu_updates = 0
    local touchmenu = { updateItems = function() menu_updates = menu_updates + 1 end }
    local item = owner:settings_menu()[4]
    equal(item.text_func(), "Inactivity timeout: 30 min")
    assert(not item.checked_func())
    item.callback(touchmenu)
    local spinner = shown[#shown]
    equal(spinner.value, 30)
    equal(spinner.value_min, 1)
    equal(spinner.value_max, 720)
    equal(spinner.default_value, 30)
    assert(spinner.extra_text == nil)
    spinner.callback{ value = 45 }
    equal(settings.values.auto_stop_minutes, 0)
    equal(settings.values.auto_stop_last_minutes, 45)
    equal(menu_updates, 1)
    assert(not item.checked_func())
    equal(item.text_func(), "Inactivity timeout: 45 min")

    local shown_before_toggle = #shown
    item.checkmark_callback()
    equal(#shown, shown_before_toggle)
    equal(settings.values.auto_stop_minutes, 45)
    assert(item.checked_func())
    equal(item.text_func(), "Inactivity timeout: 45 min")

    item.checkmark_callback()
    equal(settings.values.auto_stop_minutes, 0)
    equal(settings.values.auto_stop_last_minutes, 45)
    assert(not item.checked_func())
    equal(item.text_func(), "Inactivity timeout: 45 min")

    item.callback(touchmenu)
    spinner = shown[#shown]
    equal(spinner.value, 45)
    spinner.callback{ value = 60 }
    equal(settings.values.auto_stop_minutes, 0)
    equal(settings.values.auto_stop_last_minutes, 60)
    equal(menu_updates, 2)
    item.checkmark_callback()
    equal(settings.values.auto_stop_minutes, 60)
    assert(item.checked_func())
    equal(item.text_func(), "Inactivity timeout: 60 min")

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

test("changing server settings while running restarts and refreshes the menu", function()
    local module_names = {
        "dispatcher", "ui/widget/infomessage", "ui/widget/inputdialog", "ui/widget/confirmbox",
        "ui/uimanager", "ui/widget/container/widgetcontainer", "gettext", "zenfm_daemon", "zenfm_updater",
    }
    local saved, shown, events, settings = {}, {}, {}, nil
    for _, name in ipairs(module_names) do saved[name] = package.loaded[name] end
    package.loaded["dispatcher"] = { registerAction = function() end }
    package.loaded["ui/widget/infomessage"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/inputdialog"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/confirmbox"] = { new = function(_, options) return options end }
    package.loaded["ui/uimanager"] = {
        show = function(_, message) table.insert(shown, message) end,
        forceRePaint = function()
            table.insert(events, "repaint:" .. tostring(settings.values.insecure_http))
        end,
    }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = { finalize_pending = function() return true end }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local port, restarts, menu_refreshes = 55432, 0, 0
    settings = {
        values = { port = port, insecure_http = false, advanced_root = false },
        set = function(self, key, value) self.values[key] = value return true end,
    }
    local owner = setmetatable({
        suspended = true,
        daemon = {
            settings = settings,
            is_android = function() return false end,
            status = function() return true end,
            restart = function()
                restarts = restarts + 1
                table.insert(events, "restart:" .. tostring(settings.values.insecure_http))
                local scheme = settings.values.insecure_http and "http" or "https"
                return true, "ok running " .. scheme .. "://0.0.0.0:" .. port .. " -"
            end,
            status_details_from_raw = function(_, raw)
                local scheme = assert(raw:match("ok running (https?)://"))
                return { running = true, scheme = scheme, url = scheme .. "://192.168.1.2:" .. port }
            end,
        },
    }, { __index = ZenFM })

    local touchmenu = { updateItems = function()
        menu_refreshes = menu_refreshes + 1
        table.insert(events, "menu:" .. tostring(settings.values.insecure_http))
    end }
    local http_item = owner:settings_menu()[1]
    http_item.callback(touchmenu)
    equal(shown[1].ok_text, "Enable HTTP")
    shown[1].ok_callback()
    assert(settings.values.insecure_http)
    equal(menu_refreshes, 1)
    equal(settings.values.port, port)
    equal(restarts, 1)
    equal(table.concat(events, ","), "menu:true,repaint:true,restart:true")
    contains(shown[2].text, "http://192.168.1.2:" .. port)
    contains(shown[2].text, "Warning: unencrypted HTTP is enabled.")

    assert(owner:confirm_http(touchmenu))
    assert(not settings.values.insecure_http)
    equal(menu_refreshes, 2)
    equal(settings.values.port, port)
    equal(restarts, 2)
    equal(table.concat(events, ","),
        "menu:true,repaint:true,restart:true,menu:false,repaint:false,restart:false")
    contains(shown[3].text, "https://192.168.1.2:" .. port)

    local advanced_item = owner:settings_menu()[5].sub_item_table[2]
    assert(not advanced_item.checked_func())
    advanced_item.callback({ updateItems = function() menu_refreshes = menu_refreshes + 1 end })
    equal(shown[4].ok_text, "Expose entire filesystem")
    shown[4].ok_callback()
    assert(advanced_item.checked_func())
    equal(menu_refreshes, 3)
    equal(restarts, 3)
    contains(shown[5].text, "Warning: advanced root mode exposes the entire filesystem")

    assert(owner:confirm_advanced_root())
    assert(not advanced_item.checked_func())
    equal(restarts, 4)

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

test("changing HTTP restarts an active Android companion", function()
    local module_names = {
        "dispatcher", "ui/widget/infomessage", "ui/widget/inputdialog", "ui/widget/confirmbox",
        "ui/uimanager", "ui/widget/container/widgetcontainer", "gettext", "zenfm_daemon", "zenfm_updater",
    }
    local saved = {}
    for _, name in ipairs(module_names) do saved[name] = package.loaded[name] end
    package.loaded["dispatcher"] = { registerAction = function() end }
    package.loaded["ui/widget/infomessage"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/inputdialog"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/confirmbox"] = { new = function(_, options) return options end }
    package.loaded["ui/uimanager"] = { show = function() end }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = { finalize_pending = function() return true end }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local action, port = nil, 55432
    local settings = {
        values = { port = port, insecure_http = false, advanced_root = false },
        set = function(self, key, value) self.values[key] = value return true end,
    }
    local owner = setmetatable({
        suspended = true,
        daemon = {
            settings = settings,
            is_android = function() return true end,
            cached_android_status = function() return true end,
            status_details_from_raw = function()
                return { running = true, scheme = "http", url = "http://192.168.1.2:" .. port }
            end,
        },
        begin_android_action = function(_, requested, complete)
            action = requested
            complete(true, "ok running http://0.0.0.0:" .. port .. " -")
            return true
        end,
    }, { __index = ZenFM })

    assert(owner:set_http(true))
    equal(action, "start")
    equal(settings.values.port, port)
    assert(settings.values.insecure_http)

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

test("Android toggle restarts the companion after an inactivity stop", function()
    local module_names = {
        "dispatcher", "ui/widget/infomessage", "ui/widget/inputdialog", "ui/widget/confirmbox",
        "ui/uimanager", "ui/widget/container/widgetcontainer", "gettext", "zenfm_daemon", "zenfm_updater",
        "ui/network/manager",
    }
    local saved, scheduled, shown = {}, {}, nil
    for _, name in ipairs(module_names) do saved[name] = package.loaded[name] end
    package.loaded["dispatcher"] = { registerAction = function() end }
    package.loaded["ui/widget/infomessage"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/inputdialog"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/confirmbox"] = { new = function(_, options) return options end }
    package.loaded["ui/uimanager"] = {
        show = function(_, message) shown = message end,
        scheduleIn = function(_, _, callback) table.insert(scheduled, callback) end,
        unschedule = function(_, callback)
            for index = #scheduled, 1, -1 do
                if scheduled[index] == callback then table.remove(scheduled, index) end
            end
        end,
    }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = { finalize_pending = function() return true end }
    package.loaded["ui/network/manager"] = { willRerunWhenConnected = function() return false end }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local begin_actions, checks = {}, 0
    local request_id = string.rep("1", 32)
    local owner = setmetatable({
        daemon = {
            android_poll_attempts = 3,
            settings = { values = Settings.defaults() },
            is_android = function() return true end,
            cached_android_status = function() return false, "idle_stopped" end,
            status = function() error("Android toggle performed a live status preflight") end,
            begin_android = function(_, action)
                table.insert(begin_actions, action)
                return true, request_id
            end,
            check_android_result = function(_, action, actual_request_id)
                equal(action, "start")
                equal(actual_request_id, request_id)
                checks = checks + 1
                if checks == 1 then return false end
                return true, true, "ok running https://0.0.0.0:8443 - request=" .. request_id
            end,
            status_details_from_raw = function()
                return { running = true, scheme = "https", url = "https://192.168.4.12:8443" }
            end,
        },
    }, { __index = ZenFM })
    assert(owner:onToggleZenFM())
    equal(begin_actions[1], "start")
    equal(#begin_actions, 1)
    equal(checks, 0)
    equal(#scheduled, 0)

    owner:onResume()
    equal(#scheduled, 1)
    table.remove(scheduled, 1)()
    equal(checks, 1)
    equal(#scheduled, 1)
    assert(shown == nil)

    owner:onSuspend()
    equal(#scheduled, 0)
    owner:onResume()
    equal(#scheduled, 1)
    table.remove(scheduled, 1)()
    equal(checks, 2)
    equal(#scheduled, 1)
    equal(shown.text, "ZenFM is running.\n\nhttps://192.168.4.12:8443")

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

test("KOReader reports an inactivity stop once across concurrent monitors", function()
    local module_names = {
        "dispatcher", "ui/widget/infomessage", "ui/widget/inputdialog", "ui/widget/confirmbox",
        "ui/uimanager", "ui/widget/container/widgetcontainer", "gettext", "zenfm_daemon", "zenfm_updater",
    }
    local saved, scheduled, shown, shown_count = {}, {}, nil, 0
    for _, name in ipairs(module_names) do saved[name] = package.loaded[name] end
    package.loaded["dispatcher"] = { registerAction = function() end }
    package.loaded["ui/widget/infomessage"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/inputdialog"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/confirmbox"] = { new = function(_, options) return options end }
    package.loaded["ui/uimanager"] = {
        show = function(_, message)
            shown = message
            shown_count = shown_count + 1
        end,
        scheduleIn = function(_, delay, callback)
            table.insert(scheduled, { delay = delay, callback = callback })
        end,
    }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = { finalize_pending = function() return true end }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local OtherZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local running = true
    local daemon = {
        status = function()
            return running, running and "ok running"
                or "idle_stopped request=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
        end,
    }
    local owner = setmetatable({ daemon = daemon }, { __index = ZenFM })
    local other_owner = setmetatable({ daemon = daemon }, { __index = OtherZenFM })

    owner:start_server_monitor()
    equal(scheduled[1].delay, 60)
    table.remove(scheduled, 1).callback()
    equal(scheduled[1].delay, 60)
    assert(shown == nil)

    other_owner:start_server_monitor()
    equal(#scheduled, 2)
    running = false
    table.remove(scheduled, 1).callback()
    equal(shown.text, "ZenFM stopped after inactivity.")
    assert(owner.server_monitor == nil)
    table.remove(scheduled, 1).callback()
    equal(shown_count, 1)
    assert(other_owner.server_monitor == nil)

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

test("reset keeps setup credentials visible and disarms the stopped notice", function()
    local module_names = {
        "dispatcher", "ui/widget/infomessage", "ui/widget/inputdialog", "ui/widget/confirmbox",
        "ui/uimanager", "ui/widget/container/widgetcontainer", "gettext", "zenfm_daemon", "zenfm_updater",
    }
    local saved, scheduled, shown = {}, {}, nil
    for _, name in ipairs(module_names) do saved[name] = package.loaded[name] end
    package.loaded["dispatcher"] = { registerAction = function() end }
    package.loaded["ui/widget/infomessage"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/inputdialog"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/confirmbox"] = { new = function(_, options) return options end }
    package.loaded["ui/uimanager"] = {
        show = function(_, message) shown = message end,
        scheduleIn = function(_, _, callback) table.insert(scheduled, callback) end,
    }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = { finalize_pending = function() return true end }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local owner
    owner = setmetatable({
        daemon = {
            is_android = function() return false end,
            status = function() return true end,
            reset_login = function()
                assert(owner.server_monitor == nil, "server monitor remained armed during reset")
                return true
            end,
        },
    }, { __index = ZenFM })

    owner:start_server_monitor()
    equal(#scheduled, 1)
    local stale_monitor_callback = scheduled[1]
    owner:confirm_reset_login()
    shown.ok_callback()
    equal(shown.text, "Login reset. Use koreader123456789 and choose a new password.")
    equal(shown.timeout, false)

    stale_monitor_callback()
    equal(shown.text, "Login reset. Use koreader123456789 and choose a new password.")

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

test("server monitoring pauses in standby and checks immediately on resume", function()
    local module_names = {
        "dispatcher", "ui/widget/infomessage", "ui/widget/inputdialog", "ui/widget/confirmbox",
        "ui/uimanager", "ui/widget/container/widgetcontainer", "gettext", "zenfm_daemon", "zenfm_updater",
    }
    local saved, scheduled, shown = {}, {}, nil
    for _, name in ipairs(module_names) do saved[name] = package.loaded[name] end
    package.loaded["dispatcher"] = { registerAction = function() end }
    package.loaded["ui/widget/infomessage"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/inputdialog"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/confirmbox"] = { new = function(_, options) return options end }
    package.loaded["ui/uimanager"] = {
        show = function(_, message) shown = message end,
        scheduleIn = function(_, delay, callback)
            table.insert(scheduled, { delay = delay, callback = callback })
        end,
        unschedule = function(_, callback)
            for index = #scheduled, 1, -1 do
                if scheduled[index].callback == callback then table.remove(scheduled, index) end
            end
        end,
    }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = { finalize_pending = function() return true end }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local statuses = { true, true, false }
    local status_index = 0
    local owner = setmetatable({
        suspended = false,
        daemon = {
            status = function()
                status_index = status_index + 1
                return statuses[status_index]
            end,
        },
    }, { __index = ZenFM })

    owner:start_server_monitor()
    equal(status_index, 1)
    equal(#scheduled, 1)

    owner:onSuspend()
    equal(status_index, 1)
    equal(#scheduled, 0)

    owner:onResume()
    equal(status_index, 2)
    equal(#scheduled, 1)
    assert(shown == nil)

    owner:onSuspend()
    equal(#scheduled, 0)
    owner:onResume()
    equal(status_index, 3)
    equal(#scheduled, 0)
    equal(shown.text, "ZenFM stopped.")
    assert(owner.server_monitor == nil)

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

test("e-reader menu update offers install later before installing and restarting", function()
    local module_names = {
        "dispatcher", "ui/widget/infomessage", "ui/widget/inputdialog", "ui/widget/confirmbox",
        "ui/uimanager", "ui/widget/container/widgetcontainer", "ui/trapper", "gettext",
        "zenfm_daemon", "zenfm_updater",
    }
    local saved, events, scheduled, shown = {}, {}, {}, {}
    local update_prompt, restart_prompt
    local restart_callbacks = {}
    for _, name in ipairs(module_names) do saved[name] = package.loaded[name] end
    package.loaded["dispatcher"] = { registerAction = function() end }
    package.loaded["ui/widget/infomessage"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/inputdialog"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/confirmbox"] = { new = function(_, options) return options end }
    package.loaded["ui/uimanager"] = {
        show = function(_, message)
            shown[message] = true
            if message.ok_text == "Install now" then
                update_prompt = message
                table.insert(events, "confirm:" .. message.text)
            elseif message.ok_text == "Restart now" then
                restart_prompt = message
                table.insert(events, "confirm:" .. message.text)
            else
                table.insert(events, "notice:" .. message.text)
            end
        end,
        close = function(_, message) shown[message] = nil end,
        forceRePaint = function() table.insert(events, "repaint") end,
        isWidgetShown = function(_, message) return shown[message] == true end,
        scheduleIn = function(_, _, callback) table.insert(scheduled, callback) end,
        unschedule = function(_, callback)
            for index = #scheduled, 1, -1 do
                if scheduled[index] == callback then table.remove(scheduled, index) end
            end
        end,
        nextTick = function(_, callback)
            table.insert(events, "restart-tick-scheduled")
            table.insert(restart_callbacks, callback)
        end,
        restartKOReader = function() table.insert(events, "restart") end,
    }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["ui/trapper"] = {
        wrap = function(_, task)
            local ok, err = coroutine.resume(coroutine.create(task))
            assert(ok, tostring(err))
        end,
        dismissableRunInSubprocess = function(_, task) return true, task() end,
    }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = {
        finalize_pending = function() return true end,
        check_latest = function(_, beta_updates)
            assert(not beta_updates)
            table.insert(events, "check")
            return true, "2.4.0"
        end,
        prepare_latest = function(_, beta_updates, version)
            assert(not beta_updates)
            equal(version, "2.4.0")
            table.insert(events, "prepare")
            return true, "/prepared/plugin"
        end,
        activate_stage = function(_, root)
            equal(root, "/prepared/plugin")
            table.insert(events, "activate")
            return true, "ZenFM updated. Restart KOReader to finish activation."
        end,
    }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local owner = setmetatable({
        daemon = {
            settings = { values = { beta_updates = false } },
            is_android = function() return false end,
        },
    }, { __index = ZenFM })

    assert(owner:update())
    equal(events[1], "notice:Checking for a ZenFM update…")
    equal(events[2], "repaint")
    equal(#scheduled, 1)
    table.remove(scheduled, 1)()
    equal(events[3], "check")
    equal(events[4], "confirm:ZenFM update v2.4.0 is available.")
    equal(update_prompt.ok_text, "Install now")
    equal(update_prompt.cancel_text, "Install later")
    equal(#events, 4)
    equal(#scheduled, 0)

    update_prompt.ok_callback()
    equal(events[5], "notice:Installing ZenFM update…")
    equal(events[6], "repaint")
    equal(#scheduled, 1)
    table.remove(scheduled, 1)()
    equal(events[7], "prepare")
    equal(events[8], "activate")
    equal(events[9], "confirm:A restart is required to take effect.")
    equal(restart_prompt.ok_text, "Restart now")
    equal(restart_prompt.cancel_text, "Restart later")
    equal(#restart_callbacks, 0)
    equal(#events, 9)
    equal(#scheduled, 0)

    restart_prompt.ok_callback()
    equal(events[10], "notice:Restarting…")
    equal(events[11], "repaint")
    equal(events[12], "restart-tick-scheduled")
    equal(#restart_callbacks, 1)
    equal(#events, 12)

    table.remove(restart_callbacks, 1)()
    equal(events[13], "restart-tick-scheduled")
    equal(#restart_callbacks, 1)
    equal(#events, 13)

    table.remove(restart_callbacks, 1)()
    equal(events[14], "restart")
    equal(#restart_callbacks, 0)

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

test("Android update opens the companion only after the plugin update check", function()
    local module_names = {
        "dispatcher", "ui/widget/infomessage", "ui/widget/inputdialog", "ui/widget/confirmbox",
        "ui/uimanager", "ui/widget/container/widgetcontainer", "ui/trapper", "gettext",
        "zenfm_daemon", "zenfm_updater",
    }
    local saved, events, scheduled, shown = {}, {}, {}, {}
    for _, name in ipairs(module_names) do saved[name] = package.loaded[name] end
    package.loaded["dispatcher"] = { registerAction = function() end }
    package.loaded["ui/widget/infomessage"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/inputdialog"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/confirmbox"] = { new = function(_, options) return options end }
    package.loaded["ui/uimanager"] = {
        show = function(_, message)
            shown[message] = true
            table.insert(events, "notice:" .. message.text)
        end,
        close = function(_, message) shown[message] = nil end,
        forceRePaint = function() table.insert(events, "repaint") end,
        isWidgetShown = function(_, message) return shown[message] == true end,
        scheduleIn = function(_, _, callback) table.insert(scheduled, callback) end,
        unschedule = function(_, callback)
            for index = #scheduled, 1, -1 do
                if scheduled[index] == callback then table.remove(scheduled, index) end
            end
        end,
    }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["ui/trapper"] = {
        wrap = function(_, task)
            local ok, err = coroutine.resume(coroutine.create(task))
            assert(ok, tostring(err))
        end,
        dismissableRunInSubprocess = function(_, task) return true, task() end,
    }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = {
        finalize_pending = function() return true end,
        check_latest = function(_, beta_updates)
            assert(beta_updates)
            table.insert(events, "plugin-check")
            return false, "ZenFM is up to date"
        end,
    }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local owner = setmetatable({
        daemon = {
            settings = { values = { beta_updates = true } },
            is_android = function() return true end,
            cached_android_status = function() return true end,
            open_android = function(_, action)
                equal(action, "update")
                table.insert(events, "companion-update")
                return true, "request sent"
            end,
        },
    }, { __index = ZenFM })
    local ok, detail = owner:update()
    assert(ok, tostring(detail))
    equal(events[1], "notice:Checking for a ZenFM update…")
    equal(events[2], "repaint")
    equal(#scheduled, 1)
    table.remove(scheduled, 1)()
    equal(events[3], "plugin-check")
    contains(events[4], "KOReader plugin bundle: ZenFM is up to date")
    contains(events[4], "Android companion APK: opening updater")
    equal(events[5], "companion-update")
    equal(#events, 5)
    equal(#scheduled, 0)

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

test("Android update starts the companion before checking releases", function()
    local module_names = {
        "dispatcher", "ui/widget/infomessage", "ui/widget/inputdialog", "ui/widget/confirmbox",
        "ui/uimanager", "ui/widget/container/widgetcontainer", "gettext", "zenfm_daemon", "zenfm_updater",
    }
    local saved, events = {}, {}
    for _, name in ipairs(module_names) do saved[name] = package.loaded[name] end
    package.loaded["dispatcher"] = { registerAction = function() end }
    package.loaded["ui/widget/infomessage"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/inputdialog"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/confirmbox"] = { new = function(_, options) return options end }
    package.loaded["ui/uimanager"] = {
        show = function(_, message) table.insert(events, "notice:" .. message.text) end,
        forceRePaint = function() table.insert(events, "repaint") end,
        scheduleIn = function() table.insert(events, "update-scheduled") end,
    }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = { finalize_pending = function() return true end }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local running = false
    local owner = setmetatable({
        daemon = {
            settings = { values = { beta_updates = false } },
            is_android = function() return true end,
            cached_android_status = function() return running end,
        },
        start_server_monitor = function() table.insert(events, "monitor") end,
        begin_android_action = function(_, action, complete)
            equal(action, "start")
            table.insert(events, "companion-start")
            running = true
            complete(true, "ok running")
            return true
        end,
    }, { __index = ZenFM })

    assert(owner:update())
    equal(events[1], "companion-start")
    equal(events[2], "monitor")
    equal(events[3], "notice:Checking for a ZenFM update…")
    equal(events[4], "repaint")
    equal(events[5], "update-scheduled")

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

test("status notice reports stopped cleanly and shows the running device address", function()
    local module_names = {
        "dispatcher", "ui/widget/infomessage", "ui/widget/inputdialog", "ui/widget/confirmbox",
        "ui/uimanager", "ui/widget/container/widgetcontainer", "ui/widget/qrwidget", "device",
        "gettext", "zenfm_daemon", "zenfm_updater",
    }
    local saved, shown, qr_options = {}, nil, nil
    for _, name in ipairs(module_names) do saved[name] = package.loaded[name] end
    package.loaded["dispatcher"] = { registerAction = function() end }
    package.loaded["ui/widget/infomessage"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/inputdialog"] = { new = function(_, options) return options end }
    package.loaded["ui/widget/confirmbox"] = { new = function(_, options) return options end }
    package.loaded["ui/uimanager"] = { show = function(_, message) shown = message end }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["ui/widget/qrwidget"] = { new = function(_, options)
        qr_options = options
        return { image = "qr:" .. options.text }
    end }
    package.loaded["device"] = { screen = {
        getWidth = function() return 600 end,
        getHeight = function() return 800 end,
    } }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = { finalize_pending = function() return true end }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local settings = {
        values = { advanced_root = false, show_qr_code = true },
        set = function(self, key, value) self.values[key] = value return true end,
    }
    local owner = setmetatable({
        daemon = {
            settings = settings,
            is_android = function() return false end,
            status_details = function()
                return { running = false, detail = "connect failed" }
            end,
        },
    }, { __index = ZenFM })
    owner:onShowZenFMStatus()
    equal(shown.text, "ZenFM is stopped.")
    assert(shown.icon == nil)

    owner.daemon.status_details = function()
        return { running = true, scheme = "https", url = "https://192.168.4.12:8443", port = "8443", fingerprint = "sha256:secret" }
    end
    owner:onShowZenFMStatus()
    equal(shown.text, "ZenFM is running.\n\nhttps://192.168.4.12:8443")
    equal(qr_options.text, "https://192.168.4.12:8443")
    equal(qr_options.width, 210)
    equal(qr_options.height, 210)
    equal(shown.image, "qr:https://192.168.4.12:8443")
    equal(shown.width, 540)
    assert(not shown.text:find("sha256:secret", 1, true))

    local qr_item
    for _, item in ipairs(owner:settings_menu()) do
        if item.text == "Show QR code" then qr_item = item break end
    end
    assert(qr_item and qr_item.checked_func())
    qr_item.callback()
    assert(not qr_item.checked_func())
    qr_options = nil
    owner:onShowZenFMStatus()
    assert(qr_options == nil and shown.image == nil and shown.width == nil)

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

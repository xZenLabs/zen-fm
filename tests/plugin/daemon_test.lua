local root = assert(arg[1], "repository root required")
package.path = root .. "/plugin/zenfm.koplugin/?.lua;" .. package.path

local generic_modules = {}
for _, name in ipairs({ "android_intent", "control", "daemon", "settings", "updater", "util" }) do
    generic_modules[name] = { occupied_by_another_plugin = true }
    package.loaded[name] = generic_modules[name]
end

local Daemon = require("zenfm_daemon")
local AndroidIntent = require("zenfm_android_intent")
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
        is_android = function() return false end,
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

test("opening the Android menu uses cached state and exit stops the service", function()
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
    local cached_calls, stops = 0, 0
    local owner = setmetatable({
        daemon = {
            settings = { values = Settings.defaults() },
            is_android = function() return true end,
            cached_android_status = function() cached_calls = cached_calls + 1 return false, "stopped" end,
            status = function() error("Android menu performed a live status request") end,
            stop = function() stops = stops + 1 return true end,
        },
    }, { __index = ZenFM })
    local menu = {}
    owner:addToMainMenu(menu)
    equal(menu.zenfm.sub_item_table[1].text_func(), "Start ZenFM")
    equal(cached_calls, 1)
    owner.android_pending = {}
    owner.android_running = true
    equal(owner:onExit(), nil)
    equal(stops, 1)
    assert(owner.android_pending == nil)
    assert(not owner.android_running)

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

test("Android toggle polls incrementally only after KOReader resumes", function()
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
    local begin_actions, checks = {}, 0
    local request_id = string.rep("1", 32)
    local owner = setmetatable({
        daemon = {
            android_poll_attempts = 3,
            settings = { values = Settings.defaults() },
            is_android = function() return true end,
            cached_android_status = function() return false, "stopped" end,
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
    table.remove(scheduled, 1)()
    equal(checks, 2)
    equal(#scheduled, 0)
    equal(shown.text, "ZenFM is running.\n\nhttps://192.168.4.12:8443")

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
end)

test("Android update opens the companion only after plugin update work", function()
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
    }
    package.loaded["ui/widget/container/widgetcontainer"] = { extend = function(_, definition) return definition end }
    package.loaded["gettext"] = function(value) return value end
    package.loaded["zenfm_daemon"] = { new = function() return {} end }
    package.loaded["zenfm_updater"] = {
        finalize_pending = function() return true end,
        install_latest = function()
            table.insert(events, "plugin-update")
            return false, "ZenFM is up to date"
        end,
    }

    local ZenFM = assert(loadfile(root .. "/plugin/zenfm.koplugin/main.lua"))()
    local owner = setmetatable({
        daemon = {
            is_android = function() return true end,
            open_android = function(_, action)
                equal(action, "update")
                table.insert(events, "companion-update")
                return true, "request sent"
            end,
        },
    }, { __index = ZenFM })
    local ok, detail = owner:update()
    assert(ok, tostring(detail))
    equal(events[1], "notice:Checking for a verified ZenFM update…")
    equal(events[2], "plugin-update")
    contains(events[3], "KOReader plugin bundle: ZenFM is up to date")
    contains(events[3], "Android companion APK: opening updater")
    equal(events[4], "companion-update")
    equal(#events, 4)

    for _, name in ipairs(module_names) do package.loaded[name] = saved[name] end
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
            is_android = function() return false end,
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

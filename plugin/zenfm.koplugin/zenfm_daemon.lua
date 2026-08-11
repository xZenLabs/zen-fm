-- Keep ZenFM modules namespaced to avoid collisions in KOReader's package cache.
local Control = require("zenfm_control")
local Settings = require("zenfm_settings")
local Util = require("zenfm_util")

local ok_android, android = pcall(require, "android")
local ok_lfs, lfs = pcall(require, "libs/libkoreader-lfs")
if not ok_lfs then ok_lfs, lfs = pcall(require, "lfs") end
local ok_socket, socket = pcall(require, "socket")

local Daemon = {}
Daemon.__index = Daemon

local function source_plugin_dir()
    local source = debug.getinfo(1, "S").source or ""
    return source:match("^@(.+)/zenfm_daemon%.lua$") or "."
end

local function file_signature(path)
    if ok_lfs then
        local attrs = lfs.attributes(path)
        if attrs then return tostring(attrs.size or "") .. ":" .. tostring(attrs.modification or "") end
    end
    return Util.path_exists(path) and "present" or "missing"
end

local function basename(path)
    return tostring(path or ""):match("([^/]+)$") or ""
end

local function short_path_id(value)
    local left, right = 5381, 52711
    for index = 1, #value do
        local byte = value:byte(index)
        left = (left * 131 + byte) % 2147483647
        right = (right * 137 + byte + index) % 2147483629
    end
    return string.format("%08x%08x", left, right)
end

function Daemon:new(options)
    options = options or {}
    local object = setmetatable({}, self)
    object.plugin_dir = options.plugin_dir or source_plugin_dir()
    object.platform_override = options.platform
    object.machine_override = options.machine
    object.execute = options.execute or Util.execute
    object.output = options.command_output or Util.command_output
    object.path_exists = options.path_exists or Util.path_exists
    object.control_request = options.control_request or Control.request
    object.sleep = options.sleep or (ok_socket and socket.sleep) or function() end
    object.android_poll_attempts = options.android_poll_attempts or 300
    object.android = options.android or (ok_android and android or nil)
    object.state_dir = options.state_dir or object:default_state_dir()
    object.settings = options.settings or Settings:new(object.state_dir)
    return object
end

function Daemon:default_state_dir()
    local ok, DataStorage = pcall(require, "datastorage")
    if ok and DataStorage and DataStorage.getSettingsDir then
        return DataStorage:getSettingsDir() .. "/ZenFM"
    end
    return self.plugin_dir .. "/data"
end

function Daemon:is_android()
    return self.android ~= nil
end

function Daemon:platform()
    if self.platform_override then return self.platform_override end
    if self:is_android() then return "android" end
    if self.path_exists("/mnt/onboard/.kobo") then return "kobo" end
    if self.path_exists("/mnt/us") then return "kindle" end
    if self.path_exists("/ebrmain") then return "pocketbook" end
    local kernel, machine = self:kernel(), self:machine()
    if kernel == "linux" and (machine == "arm" or machine:match("^armv[4-8]")) then
        return "ereader"
    end
    return "host"
end

function Daemon:machine()
    return self.machine_override or self.output("uname -m 2>/dev/null"):lower()
end

function Daemon:kernel()
    return self.output("uname -s 2>/dev/null"):lower()
end

function Daemon:detect_abi()
    if self.platform_override == "pocketbook" or self.path_exists("/ebrmain") then return "sf" end
    if self.path_exists("/lib/ld-linux-armhf.so.3") then return "hf" end
    return "sf"
end

function Daemon:backend_candidates()
    local directory = self.plugin_dir .. "/backend"
    local platform = self:platform()
    if platform == "kindle" or platform == "kobo" or platform == "ereader" or platform == "pocketbook" then
        if platform == "kobo" and (self:machine() == "aarch64" or self:machine() == "arm64") then
            return { directory .. "/zenfm-linux-arm64", directory .. "/zenfm-linux" }
        end
        return { directory .. "/zenfm-" .. self:detect_abi() }
    end
    local kernel, machine = self:kernel(), self:machine()
    if kernel == "darwin" then return { directory .. "/zenfm-darwin" } end
    if kernel == "linux" and (machine == "aarch64" or machine == "arm64") then
        return { directory .. "/zenfm-linux-arm64", directory .. "/zenfm-linux" }
    end
    if kernel == "linux" and (machine == "x86_64" or machine == "amd64") then
        return { directory .. "/zenfm-linux-amd64", directory .. "/zenfm-linux" }
    end
    return {}
end

function Daemon:bundled_backend()
    for _, candidate in ipairs(self:backend_candidates()) do
        if self.path_exists(candidate) then return candidate end
    end
    return nil
end

function Daemon:backend_dir()
    return self.state_dir .. "/backend"
end

function Daemon:backend_path()
    return self:backend_dir() .. "/zenfm"
end

function Daemon:control_socket()
    return self:runtime_dir() .. "/control.sock"
end

function Daemon:pid_path()
    return self:runtime_dir() .. "/supervisor.pid"
end

function Daemon:runtime_dir()
    return "/tmp/zenfm-" .. short_path_id(self.plugin_dir .. "\0" .. self.state_dir)
end

function Daemon:ensure_runtime_dir()
    local quoted = Util.sh_quote(self:runtime_dir())
    local command = "if [ -L " .. quoted .. " ]; then exit 1; fi; "
        .. "umask 077; mkdir " .. quoted .. " 2>/dev/null || [ -d " .. quoted .. " ]; "
        .. "[ ! -L " .. quoted .. " ] && chmod 700 " .. quoted
    return self.execute(command) == 0
end

function Daemon:log_path()
    return self.state_dir .. "/zenfm.log"
end

function Daemon:token_path()
    return self.state_dir .. "/android-control.token"
end

function Daemon:ensure_dirs()
    return Util.ensure_dir(self.state_dir) and Util.ensure_dir(self:backend_dir()) and self:ensure_runtime_dir()
end

function Daemon:ensure_control_token()
    if not Util.ensure_dir(self.state_dir) then return nil, "could not create ZenFM state directory" end
    local existing = Util.trim(Util.read_all(self:token_path(), 256) or "")
    if existing:match("^[0-9a-f]+$") and #existing == 64 then return existing end
    local token = Util.random_hex(32)
    if not token or not Util.write_atomic(self:token_path(), token .. "\n", "600") then
        return nil, "could not create Android control token"
    end
    return token
end

function Daemon:ensure_backend()
    if self:is_android() then return true end
    if not self:ensure_dirs() then return false, "could not create ZenFM state directory" end
    local source = self:bundled_backend()
    if not source then return false, "no ZenFM backend matches this platform or ABI" end
    local marker_path = self:backend_dir() .. "/source.version"
    local version = Util.trim(Util.read_all(self.plugin_dir .. "/VERSION", 128) or "unknown")
    local marker = version .. "\n" .. basename(source) .. "\n" .. file_signature(source) .. "\n"
    if self.path_exists(self:backend_path()) and Util.read_all(marker_path, 1024) == marker then
        self.execute("chmod 700 " .. Util.sh_quote(self:backend_path()) .. " >/dev/null 2>&1")
        return true
    end
    if not Util.copy_atomic(source, self:backend_path(), true)
        or not Util.write_atomic(marker_path, marker, "600") then
        return false, "could not install the bundled ZenFM backend"
    end
    return true
end

function Daemon:verify_release_manifest(manifest, signature, public_key)
    if self:is_android() then return false, "Android verifies release manifests in the companion" end
    local ready, ready_err = self:ensure_backend()
    if not ready then return false, ready_err end
    if type(manifest) ~= "string" or #manifest > 64 * 1024
        or type(signature) ~= "string" or #signature > 1024
        or type(public_key) ~= "string" or not public_key:match("^[0-9a-fA-F]+$") or #public_key ~= 64 then
        return false, "release signature input is invalid"
    end
    local update_dir = self.state_dir .. "/update"
    if not Util.ensure_dir(update_dir) then return false, "could not create update directory" end
    local nonce = Util.random_hex(16)
    if not nonce then return false, "could not create verifier input names" end
    local manifest_path = update_dir .. "/manifest-" .. nonce .. ".txt"
    local signature_path = update_dir .. "/manifest-" .. nonce .. ".sig"
    if not Util.write_atomic(manifest_path, manifest, "600")
        or not Util.write_atomic(signature_path, signature, "600") then
        os.remove(manifest_path)
        os.remove(signature_path)
        return false, "could not stage release signature inputs"
    end
    local command = Util.sh_quote(self:backend_path())
        .. " verify-manifest --public-key " .. Util.sh_quote(public_key)
        .. " --manifest " .. Util.sh_quote(manifest_path)
        .. " --signature " .. Util.sh_quote(signature_path)
        .. " >/dev/null 2>&1"
    local status = self.execute(command)
    os.remove(manifest_path)
    os.remove(signature_path)
    if status == 0 then return true end
    return false, "release manifest signature did not verify"
end

function Daemon:android_storage()
    if self.android and type(self.android.getExternalStoragePath) == "function" then
        local ok, value = pcall(self.android.getExternalStoragePath)
        if ok and type(value) == "string" and value:sub(1, 1) == "/" then return value end
    end
    return nil
end

function Daemon:root()
    return self.settings:default_root(self:platform(), self:android_storage())
end

function Daemon:serve_arguments()
    local values = self.settings.values
    local arguments = {
        "serve",
        "--root", self:root(),
        "--data-dir", self.state_dir,
        "--listen", "0.0.0.0:" .. tostring(values.port),
        "--control-socket", self:control_socket(),
        "--auto-stop", values.auto_stop_minutes == 30 and "30m" or "0",
    }
    if values.insecure_http then
        table.insert(arguments, "--insecure-http")
    elseif values.tls_cert ~= "" and values.tls_key ~= "" then
        table.insert(arguments, "--tls-cert")
        table.insert(arguments, values.tls_cert)
        table.insert(arguments, "--tls-key")
        table.insert(arguments, values.tls_key)
    end
    return arguments
end

function Daemon:serve_command(backend, use_exec)
    local command = {}
    if use_exec ~= false then table.insert(command, "exec") end
    table.insert(command, Util.sh_quote(backend or self:backend_path()))
    for _, argument in ipairs(self:serve_arguments()) do table.insert(command, Util.sh_quote(argument)) end
    return table.concat(command, " ")
end

function Daemon:supervisor_command()
    local supervisor = self.plugin_dir .. "/supervisor.sh"
    self.execute("chmod 700 " .. Util.sh_quote(supervisor) .. " >/dev/null 2>&1")
    local command = {
        Util.sh_quote(supervisor),
        "--pid-file", Util.sh_quote(self:pid_path()),
        "--socket-file", Util.sh_quote(self:control_socket()),
        "--port", Util.sh_quote(tostring(self.settings.values.port)),
    }
    if self:platform() == "kindle" then table.insert(command, "--kindle") end
    table.insert(command, "--")
    table.insert(command, self:serve_command(nil, false))
    return table.concat(command, " ")
end

function Daemon:android_uri(action, request_id)
    local token, err = self:ensure_control_token()
    if not token then return nil, err end
    request_id = request_id or Util.random_hex(16)
    if not request_id or not request_id:match("^[0-9a-f]+$") or #request_id ~= 32 then
        return nil, "could not create Android lifecycle request ID"
    end
    local query = {
        "token=" .. token,
        "request_id=" .. request_id,
        "home=" .. Util.url_encode(self.state_dir),
    }
    if action == "start" then
        local values = self.settings.values
        local fields = {
            root = self:root(),
            port = tostring(values.port),
            insecure = values.insecure_http and "1" or "0",
            auto_stop = values.auto_stop_minutes == 30 and "30m" or "0",
            tls_cert = values.tls_cert,
            tls_key = values.tls_key,
        }
        for _, key in ipairs({ "root", "port", "insecure", "auto_stop", "tls_cert", "tls_key" }) do
            table.insert(query, key .. "=" .. Util.url_encode(fields[key]))
        end
    end
    return "zenfm://" .. action .. "?" .. table.concat(query, "&"), request_id
end

function Daemon:open_android(action)
    local uri, request_id = self:android_uri(action)
    if not uri then return false, request_id end
    -- Always address the companion explicitly. An implicit custom-scheme intent
    -- would allow another installed application to intercept the control secret.
    local command = "/system/bin/am start -W -n org.zenlabs.zenfm/.ZenFMActivity"
        .. " -a android.intent.action.VIEW -d " .. Util.sh_quote(uri)
    if self.execute(command) ~= 0 then return false, "ZenFM Android companion is not installed" end
    if action == "update" then
        return true, "Update request sent to the Android companion; approve it on the device when prompted."
    end
    for _ = 1, self.android_poll_attempts do
        local status = Util.trim(Util.read_all(self.state_dir .. "/android-companion.status", 1024) or "")
        if status:match(" request=" .. request_id .. "$") then
            if status:match("^error ") then return false, status end
            if action == "start" and status:match("^ok running ") then return true, status end
            if action == "stop" and status:match("^stopped ") then return true, status end
            if action == "reset" and status:match("^reset_done ") then return true, status end
            if action == "status" then
                if status:match("^ok running ") then return true, status end
                if status:match("^stopped ") then return false, status end
            end
        end
        self.sleep(0.1)
    end
    return false, "Android companion did not report a fresh " .. action .. " result within 30 seconds"
end

function Daemon:status()
    if self:is_android() then
        -- KOReader cannot reach the app-private control socket directly. Require
        -- the companion to query it and bind the answer to this fresh request.
        return self:open_android("status")
    end
    local response, err = self.control_request(self:control_socket(), "status", 2)
    if response and response:match("^ok running") then return true, response end
    return false, response or err
end

function Daemon:start()
    local values = self.settings.values
    local root = self:root()
    if type(root) ~= "string" or root:sub(1, 1) ~= "/" then
        return false, "could not determine a safe storage root; configure an absolute custom root"
    end
    if not values.insecure_http and ((values.tls_cert == "") ~= (values.tls_key == "")) then
        return false, "custom TLS requires both a certificate and private-key path"
    end
    if self:is_android() then return self:open_android("start") end
    local running, status = self:status()
    if running then return true, status end
    local ready, err = self:ensure_backend()
    if not ready then return false, err end
    local launch = "( trap '' HUP; " .. self:supervisor_command()
        .. " >>" .. Util.sh_quote(self:log_path()) .. " 2>&1 </dev/null ) &"
    if self.execute(launch) ~= 0 then return false, "could not launch ZenFM" end
    local ok_socket, socket = pcall(require, "socket")
    for _ = 1, 30 do
        local healthy, detail = self:status()
        if healthy then return true, detail end
        if ok_socket and socket.sleep then socket.sleep(0.1) end
    end
    return false, "ZenFM did not pass its control-socket startup check; see " .. self:log_path()
end

function Daemon:stop()
    if self:is_android() then return self:open_android("stop") end
    local response, err = self.control_request(self:control_socket(), "stop", 2)
    if response and response:match("^ok stopping") then return true, response end
    local running = self:status()
    if not running then
        os.remove(self:pid_path())
        return true, "already stopped"
    end
    return false, err or response or "control socket did not accept stop"
end

function Daemon:reset_login()
    local stopped, err = self:stop()
    if not stopped then return false, err end
    if self:is_android() then
        return self:open_android("reset")
    end
    local ok_socket, socket = pcall(require, "socket")
    for _ = 1, 30 do
        if not self:status() then break end
        if ok_socket and socket.sleep then socket.sleep(0.1) end
    end
    if self:status() then return false, "ZenFM did not stop before reset-login" end
    local ready, ready_err = self:ensure_backend()
    if not ready then return false, ready_err end
    local command = Util.sh_quote(self:backend_path()) .. " reset-login --data-dir "
        .. Util.sh_quote(self.state_dir) .. " >>" .. Util.sh_quote(self:log_path()) .. " 2>&1"
    return self.execute(command) == 0, "reset-login failed; see " .. self:log_path()
end

function Daemon:local_ip()
    local route = self.output("ip -4 route get 1.1.1.1 2>/dev/null")
    local address = route:match("%ssrc%s+([0-9]+%.[0-9]+%.[0-9]+%.[0-9]+)")
    if address then return address end
    local interfaces = self.output("ip -4 addr 2>/dev/null")
    for candidate in interfaces:gmatch("%sinet%s+([0-9]+%.[0-9]+%.[0-9]+%.[0-9]+)") do
        if candidate ~= "127.0.0.1" and candidate ~= "0.0.0.0" then return candidate end
    end
    local hosts = self.output("hostname -I 2>/dev/null")
    for candidate in hosts:gmatch("([0-9]+%.[0-9]+%.[0-9]+%.[0-9]+)") do
        if candidate ~= "127.0.0.1" and candidate ~= "0.0.0.0" then return candidate end
    end
    local interfaces = self.output("ifconfig 2>/dev/null")
    for _, pattern in ipairs({ "inet%s+addr:([0-9]+%.[0-9]+%.[0-9]+%.[0-9]+)", "inet%s+([0-9]+%.[0-9]+%.[0-9]+%.[0-9]+)" }) do
        for candidate in interfaces:gmatch(pattern) do
            if candidate ~= "127.0.0.1" and candidate ~= "0.0.0.0" then return candidate end
        end
    end
end

function Daemon:status_details()
    local running, raw = self:status()
    if not running then return { running = false, detail = raw or "stopped" } end
    local scheme, listen, fingerprint = raw:match("^ok running (https?)://([^ ]+) ([^ ]+)")
    local host, port
    if listen then host, port = listen:match("^(.+):([0-9]+)$") end
    if host == "0.0.0.0" or host == "[::]" or host == "::" then host = self:local_ip() end
    return {
        running = true,
        scheme = scheme,
        listen = listen,
        address = host,
        port = port,
        fingerprint = fingerprint ~= "-" and fingerprint or nil,
        url = scheme and host and port and (scheme .. "://" .. host .. ":" .. port) or nil,
        detail = raw,
    }
end

return Daemon

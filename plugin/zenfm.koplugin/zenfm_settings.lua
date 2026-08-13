-- Keep ZenFM modules namespaced to avoid collisions in KOReader's package cache.
local Util = require("zenfm_util")

local Settings = {}
Settings.__index = Settings

local defaults = {
    port = 8443,
    insecure_http = false,
    advanced_root = false,
    custom_root = "",
    auto_stop_minutes = 0,
    tls_cert = "",
    tls_key = "",
}

local function plugin_dir()
    local source = debug.getinfo(1, "S").source or ""
    return source:match("^@(.+)/settings%.lua$") or "."
end

local function default_state_dir()
    local ok, DataStorage = pcall(require, "datastorage")
    if ok and DataStorage and DataStorage.getSettingsDir then
        return DataStorage:getSettingsDir() .. "/ZenFM"
    end
    return plugin_dir() .. "/data"
end

local function copy_defaults(default_auto_stop_minutes)
    local result = {}
    for key, value in pairs(defaults) do result[key] = value end
    if default_auto_stop_minutes == 30 then result.auto_stop_minutes = 30 end
    return result
end

local function safe_custom_root(value)
    if type(value) ~= "string" or value:sub(1, 1) ~= "/" or value:gsub("/", "") == "" then return false end
    for component in value:gmatch("[^/]+") do
        if component == "." or component == ".." then return false end
    end
    return true
end

local function sanitize(value, default_auto_stop_minutes)
    local result = copy_defaults(default_auto_stop_minutes)
    if type(value) ~= "table" then return result end
    local port = tonumber(value.port)
    if port and port >= 1 and port <= 65535 and port % 1 == 0 then result.port = port end
    result.insecure_http = value.insecure_http == true
    result.advanced_root = value.advanced_root == true
    if safe_custom_root(value.custom_root) then
        result.custom_root = value.custom_root
    end
    if value.auto_stop_minutes ~= nil then
        result.auto_stop_minutes = tonumber(value.auto_stop_minutes) == 30 and 30 or 0
    end
    if type(value.tls_cert) == "string" and (value.tls_cert == "" or value.tls_cert:sub(1, 1) == "/") then
        result.tls_cert = value.tls_cert
    end
    if type(value.tls_key) == "string" and (value.tls_key == "" or value.tls_key:sub(1, 1) == "/") then
        result.tls_key = value.tls_key
    end
    return result
end

local function serialize(value)
    local keys = { "port", "insecure_http", "advanced_root", "custom_root", "auto_stop_minutes", "tls_cert", "tls_key" }
    local lines = { "return {" }
    for _, key in ipairs(keys) do
        local item = value[key]
        table.insert(lines, string.format("    [%q] = %s,", key,
            type(item) == "string" and string.format("%q", item) or tostring(item)))
    end
    table.insert(lines, "}")
    return table.concat(lines, "\n") .. "\n"
end

function Settings:new(state_dir, default_auto_stop_minutes)
    local object = setmetatable({
        state_dir = state_dir or default_state_dir(),
        default_auto_stop_minutes = default_auto_stop_minutes,
    }, self)
    object.path = object.state_dir .. "/settings.lua"
    object.values = object:load()
    return object
end

function Settings:load()
    local chunk = loadfile(self.path)
    if not chunk then return copy_defaults(self.default_auto_stop_minutes) end
    local ok, value = pcall(chunk)
    return sanitize(ok and value or nil, self.default_auto_stop_minutes)
end

function Settings:save()
    if not Util.ensure_dir(self.state_dir) then return false end
    self.values = sanitize(self.values, self.default_auto_stop_minutes)
    return Util.write_atomic(self.path, serialize(self.values), "600")
end

function Settings:set(key, value)
    local candidate = {}
    for current_key, current_value in pairs(self.values) do candidate[current_key] = current_value end
    candidate[key] = value
    self.values = sanitize(candidate)
    return self:save()
end

function Settings:default_root(platform, android_storage)
    if self.values.advanced_root then return "/" end
    if self.values.custom_root ~= "" then return self.values.custom_root end
    if platform == "kindle" then return "/mnt/us" end
    if platform == "kobo" then return "/mnt/onboard" end
    if platform == "pocketbook" then return "/mnt/ext1" end
    if platform == "android" then
        if android_storage and android_storage:sub(1, 1) == "/" then return android_storage end
        return nil
    end
    local home = os.getenv("HOME")
    if home and home:sub(1, 1) == "/" then return home end
    return nil
end

function Settings.defaults()
    return copy_defaults()
end

function Settings.sanitize(value)
    return sanitize(value)
end

return Settings

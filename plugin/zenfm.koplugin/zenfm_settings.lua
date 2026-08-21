-- Keep ZenFM modules namespaced to avoid collisions in KOReader's package cache.
local Util = require("zenfm_util")

local Settings = {}
Settings.__index = Settings

local settings_version = 3
local default_port = 54321
local max_auto_stop_minutes = 12 * 60
local defaults = {
    settings_version = settings_version,
    port = default_port,
    insecure_http = false,
    advanced_root = false,
    custom_root = "",
    auto_stop_minutes = 0,
    auto_stop_last_minutes = 30,
    beta_updates = false,
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

local function valid_port(value)
    local port = tonumber(value)
    return port and port >= 1 and port <= 65535 and port % 1 == 0
end

local function valid_auto_stop_minutes(value)
    local minutes = tonumber(value)
    return minutes and minutes >= 0 and minutes <= max_auto_stop_minutes and minutes % 1 == 0
end

local function valid_auto_stop_last_minutes(value)
    local minutes = tonumber(value)
    return minutes and minutes >= 1 and minutes <= max_auto_stop_minutes and minutes % 1 == 0
end

local function sanitize(value, default_auto_stop_minutes)
    local result = copy_defaults(default_auto_stop_minutes)
    if type(value) ~= "table" then return result end
    local stored_version = tonumber(value.settings_version)
    if not stored_version or stored_version < 1 or stored_version > settings_version or stored_version % 1 ~= 0 then
        stored_version = 1
    end
    result.settings_version = stored_version
    if valid_port(value.port) then result.port = tonumber(value.port) end
    result.insecure_http = value.insecure_http == true
    result.advanced_root = value.advanced_root == true
    if safe_custom_root(value.custom_root) then
        result.custom_root = value.custom_root
    end
    if value.auto_stop_minutes ~= nil then
        if valid_auto_stop_minutes(value.auto_stop_minutes) then
            result.auto_stop_minutes = tonumber(value.auto_stop_minutes)
        end
    end
    if valid_auto_stop_last_minutes(value.auto_stop_last_minutes) then
        result.auto_stop_last_minutes = tonumber(value.auto_stop_last_minutes)
    elseif result.auto_stop_minutes > 0 then
        result.auto_stop_last_minutes = result.auto_stop_minutes
    end
    result.beta_updates = value.beta_updates == true
    if type(value.tls_cert) == "string" and (value.tls_cert == "" or value.tls_cert:sub(1, 1) == "/") then
        result.tls_cert = value.tls_cert
    end
    if type(value.tls_key) == "string" and (value.tls_key == "" or value.tls_key:sub(1, 1) == "/") then
        result.tls_key = value.tls_key
    end
    return result
end

local function serialize(value)
    local keys = { "settings_version", "port", "insecure_http", "advanced_root", "custom_root", "auto_stop_minutes", "auto_stop_last_minutes", "beta_updates", "tls_cert", "tls_key" }
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
    local save
    object.values, save = object:load()
    if save then object:save() end
    return object
end

function Settings:load()
    local chunk = loadfile(self.path)
    if not chunk then return copy_defaults(self.default_auto_stop_minutes), true end
    local ok, value = pcall(chunk)
    if not ok or type(value) ~= "table" then
        return copy_defaults(self.default_auto_stop_minutes), true
    end
    local result = sanitize(value, self.default_auto_stop_minutes)
    if result.settings_version < settings_version then
        if result.port == 8080 or result.port == 8443 or result.port == 53241 then result.port = default_port end
        result.settings_version = settings_version
        return result, true
    end
    if not valid_port(value.port) then return result, true end
    return result, false
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
    if key == "auto_stop_minutes" and valid_auto_stop_last_minutes(value) then
        candidate.auto_stop_last_minutes = tonumber(value)
    end
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

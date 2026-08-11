-- Keep ZenFM modules namespaced to avoid collisions in KOReader's package cache.
local Util = {}

local ok_lfs, lfs = pcall(require, "libs/libkoreader-lfs")
if not ok_lfs then ok_lfs, lfs = pcall(require, "lfs") end

function Util.execute(command)
    local result, _, status = os.execute(command)
    if result == true or result == 0 then return 0 end
    return tonumber(status) or tonumber(result) or 1
end

function Util.trim(value)
    return tostring(value or ""):gsub("^%s+", ""):gsub("%s+$", "")
end

function Util.sh_quote(value)
    return "'" .. tostring(value or ""):gsub("'", "'\\''") .. "'"
end

function Util.url_encode(value)
    return (tostring(value or ""):gsub("([^%w%-_%.~])", function(char)
        return string.format("%%%02X", string.byte(char))
    end))
end

function Util.path_exists(path)
    if ok_lfs and lfs.attributes(path) then return true end
    local file = io.open(path, "rb")
    if not file then return false end
    file:close()
    return true
end

function Util.is_directory(path)
    if ok_lfs then
        local attrs = lfs.attributes(path)
        return attrs and attrs.mode == "directory" or false
    end
    return Util.execute("test -d " .. Util.sh_quote(path)) == 0
end

function Util.ensure_dir(path)
    path = tostring(path or "")
    if path == "" then return false end
    if not Util.is_directory(path) and ok_lfs then
        local current = path:sub(1, 1) == "/" and "/" or ""
        for part in path:gmatch("[^/]+") do
            current = current == "/" and current .. part
                or current == "" and part or current .. "/" .. part
            if not Util.is_directory(current) and not lfs.mkdir(current) then return false end
        end
    elseif not Util.is_directory(path) and Util.execute("mkdir -p " .. Util.sh_quote(path)) ~= 0 then
        return false
    end
    Util.execute("chmod 700 " .. Util.sh_quote(path) .. " >/dev/null 2>&1")
    return Util.is_directory(path)
end

function Util.read_all(path, maximum)
    local file = io.open(path, "rb")
    if not file then return nil end
    local data = file:read(maximum and maximum + 1 or "*a")
    file:close()
    if maximum and data and #data > maximum then return nil, "file is too large" end
    return data
end

function Util.write_atomic(path, value, mode)
    local temporary = path .. ".tmp"
    local file = io.open(temporary, "wb")
    if not file then return false end
    local ok = file:write(value or "") ~= nil
    local closed = file:close()
    ok = ok and closed ~= nil
    if not ok then
        os.remove(temporary)
        return false
    end
    Util.execute("chmod " .. tostring(mode or "600") .. " " .. Util.sh_quote(temporary) .. " >/dev/null 2>&1")
    if not os.rename(temporary, path) then
        os.remove(temporary)
        return false
    end
    return true
end

local function direct_child(path, parent)
    local prefix = parent:gsub("/+$", "") .. "/"
    local remainder = path:sub(1, #prefix) == prefix and path:sub(#prefix + 1) or ""
    return remainder ~= "" and not remainder:find("/", 1, true)
        and remainder ~= "." and remainder ~= ".."
end


-- Recursion is deliberately limited to a direct child of an explicit parent.
-- Symlinks are removed/copied as leaf entries and are never followed.
function Util.remove_tree(path, allowed_parent)
    if not ok_lfs or not direct_child(path, allowed_parent) then return false end
    local attrs = lfs.symlinkattributes(path)
    if not attrs then return true end
    if attrs.mode ~= "directory" then return os.remove(path) ~= nil end
    for entry in lfs.dir(path) do
        if entry ~= "." and entry ~= ".." then
            local child = path .. "/" .. entry
            local child_attrs = lfs.symlinkattributes(child)
            if child_attrs and child_attrs.mode == "directory" then
                if not Util.remove_tree(child, path) then return false end
            elseif child_attrs and not os.remove(child) then
                return false
            end
        end
    end
    return lfs.rmdir(path) ~= nil
end

function Util.copy_tree(source, destination, allowed_parent)
    if not ok_lfs or not direct_child(destination, allowed_parent) then return false end
    local source_attrs = lfs.symlinkattributes(source)
    if not source_attrs or source_attrs.mode ~= "directory" or not lfs.mkdir(destination) then return false end
    for entry in lfs.dir(source) do
        if entry ~= "." and entry ~= ".." then
            local from, to = source .. "/" .. entry, destination .. "/" .. entry
            local attrs = lfs.symlinkattributes(from)
            if not attrs or attrs.mode == "link" then return false end
            if attrs.mode == "directory" then
                if not Util.copy_tree(from, to, destination) then return false end
            elseif attrs.mode == "file" then
                if not Util.copy_atomic(from, to, false) then return false end
            else
                return false
            end
        end
    end
    return true
end

function Util.copy_atomic(source, destination, executable)
    local input = io.open(source, "rb")
    if not input then return false end
    local temporary = destination .. ".tmp"
    local output = io.open(temporary, "wb")
    if not output then
        input:close()
        return false
    end
    local ok = true
    while true do
        local chunk = input:read(64 * 1024)
        if not chunk then break end
        if not output:write(chunk) then ok = false break end
    end
    input:close()
    if not output:close() then ok = false end
    if not ok then
        os.remove(temporary)
        return false
    end
    Util.execute("chmod " .. (executable and "700" or "600") .. " " .. Util.sh_quote(temporary) .. " >/dev/null 2>&1")
    if not os.rename(temporary, destination) then
        os.remove(temporary)
        return false
    end
    return true
end

function Util.random_hex(bytes)
    local file = io.open("/dev/urandom", "rb")
    local data = file and file:read(bytes or 32) or nil
    if file then file:close() end
    if not data or #data ~= (bytes or 32) then return nil end
    return (data:gsub(".", function(char) return string.format("%02x", string.byte(char)) end))
end

function Util.command_output(command)
    local process = io.popen(command)
    if not process then return "" end
    local output = process:read("*a") or ""
    process:close()
    return Util.trim(output)
end

return Util

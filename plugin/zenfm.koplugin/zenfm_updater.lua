-- Keep ZenFM modules namespaced to avoid collisions in KOReader's package cache.
local Util = require("zenfm_util")
local ok_lfs, lfs = pcall(require, "libs/libkoreader-lfs")
if not ok_lfs then ok_lfs, lfs = pcall(require, "lfs") end

local Updater = {}

local repository = "xZenLabs/zen-fm"
local releases_url = "https://api.github.com/repos/" .. repository .. "/releases?per_page=20"
local maximum_metadata_bytes = 1024 * 1024
local maximum_package_bytes = 200 * 1024 * 1024
local maximum_archive_entries = 512
local maximum_archive_path_bytes = 4096
local release_root = "zenfm.koplugin"

local trusted_hosts = {
    ["api.github.com"] = true,
    ["github.com"] = true,
    ["objects.githubusercontent.com"] = true,
    ["release-assets.githubusercontent.com"] = true,
    ["github-releases.githubusercontent.com"] = true,
}

local function trusted_url(value, release_asset)
    local scheme, host, path = tostring(value or ""):match("^(https)://([^/%?#]+)([^#]*)$")
    host = host and host:lower() or ""
    if scheme ~= "https" or not trusted_hosts[host] then return false end
    if release_asset and host == "github.com" then
        return path:sub(1, #( "/" .. repository .. "/releases/download/"))
            == "/" .. repository .. "/releases/download/"
    end
    return true
end

local function resolve_redirect(base, location)
    if type(location) ~= "string" or location == "" then return nil end
    if location:match("^https://") then return location end
    local scheme, host, path = base:match("^(https)://([^/%?#]+)([^#]*)$")
    if not scheme then return nil end
    if location:sub(1, 1) == "/" then return scheme .. "://" .. host .. location end
    return scheme .. "://" .. host .. ((path or "/"):match("^(.*)/") or "") .. "/" .. location
end

local function request(url, maximum, sink_file)
    local ok_https, https = pcall(require, "ssl.https")
    local ok_ltn12, ltn12 = pcall(require, "ltn12")
    if not ok_https or not ok_ltn12 then return nil, "HTTPS support is unavailable" end
    local current = url
    for _ = 1, 6 do
        if not trusted_url(current, current ~= releases_url) then return nil, "untrusted update URL" end
        local chunks, total = {}, 0
        local file = sink_file and io.open(sink_file, "wb") or nil
        if sink_file and not file then return nil, "could not create update file" end
        local overflow = false
        local function sink(chunk)
            if not chunk then return 1 end
            total = total + #chunk
            if total > maximum then overflow = true return nil, "response is too large" end
            if file then file:write(chunk) else table.insert(chunks, chunk) end
            return 1
        end
        local _, code, headers = https.request{
            url = current,
            method = "GET",
            headers = { ["User-Agent"] = "zenfm.koplugin", ["Accept"] = "application/vnd.github+json" },
            sink = sink,
            redirect = false,
        }
        if file then file:close() end
        code = tonumber(code)
        if overflow then if sink_file then os.remove(sink_file) end return nil, "update response is too large" end
        if code == 301 or code == 302 or code == 307 or code == 308 then
            if sink_file then os.remove(sink_file) end
            current = resolve_redirect(current, headers and (headers.location or headers.Location))
            if not current then return nil, "invalid update redirect" end
        elseif code == 200 then
            return file and sink_file or table.concat(chunks), nil
        else
            if sink_file then os.remove(sink_file) end
            return nil, "update request returned HTTP " .. tostring(code or "?")
        end
    end
    return nil, "too many update redirects"
end

local function sha256(path)
    local ok, sha2 = pcall(require, "ffi/sha2")
    if not ok or not sha2 or not sha2.sha256 then return nil end
    local file = io.open(path, "rb")
    if not file then return nil end
    local append = sha2.sha256()
    while true do
        local chunk = file:read(64 * 1024)
        if not chunk then break end
        append(chunk)
    end
    file:close()
    local hashed, digest = pcall(append)
    return hashed and type(digest) == "string" and digest:lower() or nil
end

local function version_parts(value)
    local core, prerelease = tostring(value or ""):gsub("^v", ""):match("^([0-9]+%.[0-9]+%.[0-9]+)%-?(.*)$")
    if not core then return nil end
    local major, minor, patch = core:match("^(%d+)%.(%d+)%.(%d+)$")
    local prerelease_name, prerelease_number = prerelease:match("^(.-)(%d+)$")
    return {
        tonumber(major), tonumber(minor), tonumber(patch), prerelease ~= "",
        prerelease_name or prerelease, tonumber(prerelease_number) or 0,
    }
end

function Updater.version_greater(left, right)
    local a, b = version_parts(left), version_parts(right)
    if not a or not b then return false end
    for index = 1, 3 do
        if a[index] ~= b[index] then return a[index] > b[index] end
    end
    if a[4] ~= b[4] then return not a[4] end
    if a[5] ~= b[5] then return a[5] > b[5] end
    return a[6] > b[6]
end

function Updater.asset_name(daemon, version)
    local prefix = "ZenFM-koreader-"
    if daemon:is_android() then return prefix .. "android-" .. version .. ".zip" end
    local platform = daemon:platform()
    if platform == "kindle" or platform == "kobo" or platform == "ereader" or platform == "pocketbook" then
        if platform == "kobo" and (daemon:machine() == "aarch64" or daemon:machine() == "arm64") then
            return prefix .. "linux-" .. version .. ".zip"
        end
        return prefix .. "ereader-" .. version .. ".zip"
    end
    if daemon:kernel() == "darwin" then return prefix .. "macos-" .. version .. ".zip" end
    if daemon:kernel() == "linux" then return prefix .. "linux-" .. version .. ".zip" end
    return nil
end

function Updater.select_release(releases, daemon, current_version, allow_prerelease)
    local best
    for _, release in ipairs(releases or {}) do
        if not release.draft and (allow_prerelease or not release.prerelease) then
            local version = tostring(release.tag_name or ""):gsub("^v", "")
            local expected = Updater.asset_name(daemon, version)
            if expected and Updater.version_greater(version, current_version) then
                local package_asset
                for _, asset in ipairs(release.assets or {}) do
                    local digest = tostring(asset.digest or ""):match("^sha256:([0-9a-fA-F]+)$")
                    local size = tonumber(asset.size)
                    if asset.name == expected and digest and #digest == 64 and size and size > 0
                        and size <= maximum_package_bytes
                        and trusted_url(asset.browser_download_url, true) then package_asset = asset end
                end
                if package_asset
                    and (not best or Updater.version_greater(version, best.version)) then
                    best = {
                        version = version,
                        name = expected,
                        url = package_asset.browser_download_url,
                        digest = package_asset.digest:sub(#"sha256:" + 1):lower(),
                        size = tonumber(package_asset.size),
                    }
                end
            end
        end
    end
    return best
end

local function plugin_version(plugin_dir)
    return Util.trim(Util.read_all(plugin_dir .. "/VERSION", 128) or "0.0.0")
end

local function valid_archive_path(path)
    if type(path) ~= "string" or path == "" or #path > maximum_archive_path_bytes
        or path:sub(1, 1) == "/" or path:find("\\", 1, true)
        or path:find("//", 1, true) then
        return false
    end
    for part in path:gmatch("[^/]+") do
        if part == "." or part == ".." then return false end
    end
    return path == release_root
        or path:sub(1, #release_root + 1) == release_root .. "/"
end

-- KOReader's archiver writes each entry through libarchive, retaining the mode
-- stored in the release. This matters on PocketBook: copying an extracted
-- executable to its settings storage loses the executable mode, and chmod is
-- unavailable there. Validate the complete archive before writing any entry.
function Updater.extract_archive(archive_path, destination)
    local ok_archiver, Archiver = pcall(require, "ffi/archiver")
    if not ok_archiver or not Archiver or not Archiver.Reader or not Archiver.Reader.new then
        return false, "archive extraction is unavailable"
    end
    local created, archive = pcall(Archiver.Reader.new, Archiver.Reader)
    if not created or not archive then return false, "archive extraction is unavailable" end
    if not archive:open(archive_path) then
        return false, archive.err or "could not open update archive"
    end

    local entries, manifest, total = 0, {}, 0
    for entry in archive:iterate() do
        entries = entries + 1
        local declared_size = tonumber(entry.size)
        local size = declared_size or 0
        if entries > maximum_archive_entries or not valid_archive_path(entry.path)
            or (entry.mode ~= "file" and entry.mode ~= "directory")
            or (entry.path == release_root and entry.mode ~= "directory")
            or (entry.mode == "file" and declared_size == nil)
            or size < 0 then
            archive:close()
            return false, "update archive has an invalid layout"
        end
        total = total + size
        if total > maximum_package_bytes then
            archive:close()
            return false, "update archive is too large"
        end
        manifest[entries] = { path = entry.path, mode = entry.mode, size = size }
    end
    local validation_err = archive.err
    archive:close()
    if validation_err then return false, validation_err end
    if entries == 0 then return false, "update archive is empty" end

    -- Reader retains parsed headers when reused. Construct a fresh reader so
    -- this pass observes the archive as it exists immediately before writes.
    created, archive = pcall(Archiver.Reader.new, Archiver.Reader)
    if not created or not archive then return false, "archive extraction is unavailable" end
    if not archive:open(archive_path) then
        return false, archive.err or "could not reopen update archive"
    end
    local extracted = 0
    for entry in archive:iterate() do
        extracted = extracted + 1
        local expected = manifest[extracted]
        local declared_size = tonumber(entry.size)
        local size = declared_size or 0
        if not expected or entry.path ~= expected.path or entry.mode ~= expected.mode
            or (entry.mode == "file" and declared_size == nil) or size ~= expected.size then
            archive:close()
            return false, "update archive changed during extraction"
        end
        if not archive:extractToPath(entry.path, destination .. "/" .. entry.path) then
            local extract_err = archive.err or "could not extract update archive"
            archive:close()
            return false, extract_err
        end
    end
    local extract_err = archive.err
    archive:close()
    if extract_err then return false, extract_err end
    if extracted ~= entries then return false, "update archive changed during extraction" end
    return true
end

function Updater.validate_lua_tree(root)
    if not ok_lfs then return false, "Lua update validation requires filesystem support" end
    local visited, total = 0, 0
    local function walk(directory, depth)
        if depth > 16 then return false, "update tree is too deep" end
        local attrs = lfs.symlinkattributes(directory)
        if not attrs or attrs.mode ~= "directory" then return false, "update tree contains an unsafe directory" end
        for entry in lfs.dir(directory) do
            if entry ~= "." and entry ~= ".." then
                visited = visited + 1
                if visited > 512 then return false, "update tree contains too many entries" end
                local child = directory .. "/" .. entry
                local child_attrs = lfs.symlinkattributes(child)
                if not child_attrs or child_attrs.mode == "link" then
                    return false, "update tree contains a symbolic link"
                elseif child_attrs.mode == "directory" then
                    local ok, err = walk(child, depth + 1)
                    if not ok then return false, err end
                elseif child_attrs.mode == "file" then
                    total = total + (tonumber(child_attrs.size) or 0)
                    if total > maximum_package_bytes then return false, "update tree is too large" end
                    if entry:match("%.lua$") then
                        local chunk, syntax_err = loadfile(child)
                        if not chunk then return false, "update contains invalid Lua: " .. tostring(syntax_err) end
                    end
                else
                    return false, "update tree contains an unsupported entry"
                end
            end
        end
        return true
    end
    return walk(root, 0)
end

local function latest(daemon, allow_prerelease)
    local body, err = request(releases_url, maximum_metadata_bytes)
    if not body then return nil, err end
    local ok_json, JSON = pcall(require, "json")
    if not ok_json then return nil, "JSON support is unavailable" end
    local decoded_ok, releases = pcall(JSON.decode, body)
    if not decoded_ok or type(releases) ~= "table" then return nil, "GitHub returned invalid release metadata" end
    return Updater.select_release(releases, daemon, plugin_version(daemon.plugin_dir), allow_prerelease)
end

local function validate_stage(daemon, directory, version)
    local root = directory .. "/zenfm.koplugin"
    if not Util.is_directory(root) then return nil, "update has an invalid plugin layout" end
    if plugin_version(root) ~= version then return nil, "update version does not match release metadata" end
    for _, required in ipairs({ "_meta.lua", "main.lua", "zenfm_daemon.lua" }) do
        if not Util.path_exists(root .. "/" .. required) then return nil, "update is missing " .. required end
    end
    local syntax_ok, syntax_err = Updater.validate_lua_tree(root)
    if not syntax_ok then return nil, syntax_err end
    if not daemon:is_android() then
        local found = false
        for _, candidate in ipairs({ "zenfm-hf", "zenfm-sf", "zenfm-linux", "zenfm-linux-arm64", "zenfm-linux-amd64", "zenfm-darwin" }) do
            if Util.path_exists(root .. "/backend/" .. candidate) then found = true break end
        end
        if not found then return nil, "update contains no backend executable" end
    end
    return root
end

function Updater.install_stage(daemon, stage_root, resume_after_update)
    local incoming = daemon.plugin_dir .. ".incoming"
    local backup = daemon.plugin_dir .. ".rollback"
    local parent = daemon.plugin_dir:match("^(.*)/[^/]+$")
    if not parent then return false, "plugin path is invalid" end
    if not Util.remove_tree(incoming, parent) or not Util.remove_tree(backup, parent) then
        return false, "could not clear bounded update staging paths"
    end
    -- Prefer moving the archive-extracted tree so executable bits from the
    -- release survive. PocketBook stages beside the plugin below, because a
    -- copied file cannot be made executable afterward.
    local installed = os.rename(stage_root, incoming)
    if not installed and not daemon:is_pocketbook() then
        installed = Util.copy_tree(stage_root, incoming, parent, function(path)
            return path:match("/supervisor%.sh$") ~= nil
                or path:match("/backend/zenfm[^/]*$") ~= nil
        end)
    end
    if not installed then
        return false, "could not stage plugin update"
    end
    if not daemon:is_pocketbook() then
        os.execute("chmod 700 " .. Util.sh_quote(incoming .. "/supervisor.sh") .. " >/dev/null 2>&1")
    end
    if not os.rename(daemon.plugin_dir, backup) then
        Util.remove_tree(incoming, parent)
        return false, "could not create plugin rollback copy"
    end
    local activated, activation_err = os.rename(incoming, daemon.plugin_dir)
    if not activated then
        local restored, restore_err = os.rename(backup, daemon.plugin_dir)
        if not restored then
            return false, "could not activate plugin update and rollback failed: "
                .. tostring(restore_err or activation_err or "could not restore previous version")
        end
        return false, "could not activate plugin update; previous version restored"
    end
    local pending = backup .. "\n" .. (resume_after_update and "resume" or "stop") .. "\n"
    if not Util.write_atomic(daemon.plugin_dir .. "/.update-pending", pending, "600") then
        if not Util.remove_tree(daemon.plugin_dir, parent) then
            return false, "could not mark update healthy and rollback failed: could not remove failed update"
        end
        local restored, restore_err = os.rename(backup, daemon.plugin_dir)
        if not restored then
            return false, "could not mark update healthy and rollback failed: "
                .. tostring(restore_err or "could not restore previous version")
        end
        return false, "could not mark update healthy; previous version restored"
    end
    return true, "ZenFM updated. Restart KOReader to finish activation."
end

function Updater.finalize_pending(daemon)
    local plugin_dir = daemon.plugin_dir
    local marker = plugin_dir .. "/.update-pending"
    local pending = Util.read_all(marker, 4096) or ""
    if pending == "" then return true end
    local backup, desired_state = pending:match("^([^\r\n]+)\n([^\r\n]+)\n$")
    if not backup or (desired_state ~= "resume" and desired_state ~= "stop") then
        return false, "update rollback marker is invalid"
    end
    if backup ~= plugin_dir .. ".rollback" then return false, "update rollback marker is invalid" end
    if daemon:is_android() then
        os.remove(marker)
        local parent = plugin_dir:match("^(.*)/[^/]+$")
        if parent then Util.remove_tree(backup, parent) end
        return true
    end
    local ready, err = daemon:ensure_backend()
    if ready then ready, err = daemon:start() end
    if ready then
        local ok_socket, socket = pcall(require, "socket")
        for _ = 1, 20 do
            if daemon:status() then break end
            if ok_socket and socket.sleep then socket.sleep(0.1) end
        end
        ready = daemon:status()
        if not ready then err = "updated backend failed its control-socket health check" end
    end
    if ready and desired_state == "stop" then
        ready, err = daemon:stop()
        if not ready then err = "updated backend passed health verification but could not be stopped" end
    end
    if ready then
        os.remove(marker)
        local parent = plugin_dir:match("^(.*)/[^/]+$")
        if parent then Util.remove_tree(backup, parent) end
        return true
    end
    daemon:stop()
    local parent = plugin_dir:match("^(.*)/[^/]+$")
    local failed = plugin_dir .. ".failed"
    if not parent or not Util.remove_tree(failed, parent)
        or not os.rename(plugin_dir, failed) or not os.rename(backup, plugin_dir) then
        return false, "updated backend failed health check and rollback failed: " .. tostring(err)
    end
    Util.remove_tree(failed, parent)
    return false, "updated backend failed health check; previous plugin restored. Restart KOReader."
end

function Updater.install_latest(daemon, allow_prerelease)
    local release, err = latest(daemon, allow_prerelease)
    if not release then return false, err or "ZenFM is up to date" end
    if release.size and (release.size <= 0 or release.size > maximum_package_bytes) then
        return false, "release package size is invalid"
    end
    local update_dir = daemon.state_dir .. "/update"
    if not Util.ensure_dir(update_dir) then return false, "could not create update directory" end
    local archive = update_dir .. "/ZenFM-" .. release.version .. ".zip"
    local downloaded, download_err = request(release.url, maximum_package_bytes, archive)
    if not downloaded then return false, download_err end
    local actual = sha256(archive)
    if not actual or actual ~= release.digest then os.remove(archive) return false, "update checksum did not match" end
    local stage, stage_parent = update_dir .. "/stage", update_dir
    if daemon:is_pocketbook() then
        stage_parent = daemon.plugin_dir:match("^(.*)/[^/]+$")
        if not stage_parent then return false, "plugin path is invalid" end
        stage = daemon.plugin_dir .. ".update-stage"
    end
    if not Util.remove_tree(stage, stage_parent) then return false, "could not clear update staging directory" end
    if not Util.ensure_dir(stage) then return false, "could not create update staging directory" end
    local unpacked, unpack_err = Updater.extract_archive(archive, stage)
    os.remove(archive)
    if not unpacked then return false, tostring(unpack_err or "could not extract update") end
    local root, validation_err = validate_stage(daemon, stage, release.version)
    if not root then return false, validation_err end
    local was_running = false
    if not daemon:is_android() then
        was_running = daemon:status()
        local stopped, stop_err = daemon:stop()
        if not stopped then return false, "could not stop ZenFM before update: " .. tostring(stop_err) end
    end
    return Updater.install_stage(daemon, root, was_running)
end

return Updater

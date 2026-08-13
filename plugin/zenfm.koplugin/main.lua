local Dispatcher = require("dispatcher")
local InfoMessage = require("ui/widget/infomessage")
local InputDialog = require("ui/widget/inputdialog")
local ConfirmBox = require("ui/widget/confirmbox")
local UIManager = require("ui/uimanager")
local WidgetContainer = require("ui/widget/container/widgetcontainer")
local _ = require("gettext")

local Daemon = require("zenfm_daemon")
local Updater = require("zenfm_updater")

local ZenFM = WidgetContainer:extend{
    name = "zenfm",
    is_doc_only = false,
}

local server_poll_seconds = 60

local function notice(text, warning, persistent)
    local timeout = warning and 6 or 3
    if persistent then timeout = false end
    UIManager:show(InfoMessage:new{
        text = text,
        icon = warning and "notice-warning" or nil,
        timeout = timeout,
    })
end

function ZenFM:init()
    self.daemon = Daemon:new()
    self.android_pending = nil
    self.server_monitor = nil
    self.suspended = false
    self.ui.menu:registerToMainMenu(self)
    self:onDispatcherRegisterActions()
    local healthy, health_err = Updater.finalize_pending(self.daemon)
    if not healthy then notice(tostring(health_err), true) end
    if healthy then self:start_server_monitor() end
end

function ZenFM:android_cached_running()
    local running = self.daemon:cached_android_status()
    self.android_running = running
    return running
end

function ZenFM:schedule_android_poll(pending)
    if self.suspended or self.android_pending ~= pending or pending.scheduled then return end
    pending.callback = pending.callback or function() self:check_android_poll(pending) end
    pending.scheduled = true
    UIManager:scheduleIn(0.1, pending.callback)
end

function ZenFM:check_android_poll(pending)
    if self.android_pending ~= pending then return end
    pending.scheduled = false
    local checked, done, success, detail = pcall(
        self.daemon.check_android_result, self.daemon, pending.action, pending.request_id)
    if not checked then
        done, success, detail = true, false, "Could not read the Android companion result"
    end
    if done then
        self.android_pending = nil
        pending.complete(success, detail)
        return
    end
    pending.attempts = pending.attempts - 1
    if pending.attempts <= 0 then
        self.android_pending = nil
        pending.complete(false,
            "Android companion did not report a fresh " .. pending.action .. " result within 30 seconds")
        return
    end
    self:schedule_android_poll(pending)
end

function ZenFM:onSuspend()
    self.suspended = true
    local pending = self.android_pending
    if pending and pending.scheduled then
        UIManager:unschedule(pending.callback)
        pending.scheduled = false
    end
    local monitor = self.server_monitor
    if monitor and monitor.scheduled then
        UIManager:unschedule(monitor.callback)
        monitor.scheduled = false
    end
end

function ZenFM:onResume()
    self.suspended = false
    if self.android_pending then self:schedule_android_poll(self.android_pending) end
    local monitor = self.server_monitor
    if monitor then
        if monitor.scheduled then
            UIManager:unschedule(monitor.callback)
            monitor.scheduled = false
        end
        self:check_server_monitor(monitor)
    end
end

function ZenFM:onExit()
    self.android_pending = nil
    self.android_running = false
    self.server_monitor = nil
    self.daemon:stop()
end

function ZenFM:schedule_server_monitor(monitor)
    if self.suspended or self.server_monitor ~= monitor or monitor.scheduled then return end
    monitor.scheduled = true
    UIManager:scheduleIn(server_poll_seconds, monitor.callback)
end

function ZenFM:check_server_monitor(monitor)
    if self.server_monitor ~= monitor then return end
    monitor.scheduled = false
    local running = self.daemon:status()
    if running then
        self:schedule_server_monitor(monitor)
        return
    end
    self.server_monitor = nil
    self.android_running = false
    notice(_("ZenFM stopped."))
end

function ZenFM:start_server_monitor(running)
    self.server_monitor = nil
    if running == nil then running = self.daemon:status() end
    if not running then return end
    local monitor = {}
    monitor.callback = function() self:check_server_monitor(monitor) end
    self.server_monitor = monitor
    self:schedule_server_monitor(monitor)
end

function ZenFM:begin_android_action(action, complete)
    if self.android_pending then
        notice(_("Another ZenFM Android request is still pending."), true)
        return false
    end
    local launched, result = self.daemon:begin_android(action)
    if not launched then
        notice(tostring(result), true)
        return false
    end
    self.android_pending = {
        action = action,
        request_id = result,
        attempts = self.daemon.android_poll_attempts or 300,
        complete = complete,
    }
    return true
end

function ZenFM:onDispatcherRegisterActions()
    Dispatcher:registerAction("zenfm_toggle", {
        category = "none",
        event = "ToggleZenFM",
        title = _("ZenFM: Toggle server"),
        general = true,
    })
    Dispatcher:registerAction("zenfm_status", {
        category = "none",
        event = "ShowZenFMStatus",
        title = _("ZenFM: Status"),
        general = true,
    })
end

function ZenFM:onToggleZenFM()
    if self.daemon:is_android() then
        local running = self:android_cached_running()
        local action = running and "stop" or "start"
        if running then self.server_monitor = nil end
        local started = self:begin_android_action(action, function(ok, detail)
            if not ok then
                if action == "stop" then self:start_server_monitor(true) end
                notice(tostring(detail), true)
                return
            end
            if action == "stop" then
                self.android_running = false
                self.server_monitor = nil
                notice(_("ZenFM stopped."))
                return
            end
            self.android_running = true
            self:start_server_monitor(true)
            self:show_status(self.daemon:status_details_from_raw(detail))
        end)
        if not started and running then self:start_server_monitor(true) end
        return started
    end
    local running = self.daemon:status()
    local ok, detail
    if running then ok, detail = self.daemon:stop() else ok, detail = self.daemon:start() end
    local success = running and _("ZenFM stopped.") or _("ZenFM started.")
    if ok and not running then
        self:start_server_monitor(true)
        self:onShowZenFMStatus()
    else
        if ok and running then self.server_monitor = nil end
        notice(ok and success or tostring(detail), not ok)
    end
    return ok
end

function ZenFM:show_status(status)
    if not status.running then
        notice(_("ZenFM is stopped.") .. (status.detail and "\n" .. status.detail or ""), false, true)
        return
    end
    local lines = { _("ZenFM is running.") }
    if status.url then
        table.insert(lines, status.url)
    elseif status.port then
        table.insert(lines, _("Listening port:") .. " " .. status.port)
    end
    if status.scheme == "http" then
        table.insert(lines, _("Warning: unencrypted HTTP is enabled."))
    end
    if self.daemon.settings.values.advanced_root then
        table.insert(lines, _("Warning: advanced root mode exposes the entire filesystem, including ZenFM state and certificates."))
    end
    notice(table.concat(lines, "\n\n"), status.scheme == "http" or self.daemon.settings.values.advanced_root, true)
end

function ZenFM:onShowZenFMStatus()
    if self.daemon:is_android() then
        return self:begin_android_action("status", function(ok, detail)
            if not ok then
                notice(tostring(detail), true)
                return
            end
            local status = self.daemon:status_details_from_raw(detail)
            self.android_running = status.running
            self:show_status(status)
        end)
    end
    self:show_status(self.daemon:status_details())
end

local function input_dialog(owner, title, value, input_type, save)
    local dialog
    dialog = InputDialog:new{
        title = title,
        input = tostring(value or ""),
        input_type = input_type,
        buttons = {{
            { text = _("Cancel"), callback = function() UIManager:close(dialog) end },
            { text = _("Save"), is_enter_default = true, callback = function()
                local ok, err = save(dialog:getInputText())
                if ok then
                    UIManager:close(dialog)
                    notice(_("Saved. Restart ZenFM to apply the change."))
                else
                    notice(tostring(err or _("Invalid value.")), true)
                end
            end },
        }},
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function ZenFM:show_port_dialog()
    input_dialog(self, _("ZenFM port"), self.daemon.settings.values.port, "number", function(raw)
        local port = tonumber(raw)
        if not port or port < 1 or port > 65535 or port % 1 ~= 0 then
            return false, _("Port must be between 1 and 65535.")
        end
        return self.daemon.settings:set("port", port)
    end)
end

function ZenFM:show_path_dialog(key, title, allow_empty)
    input_dialog(self, title, self.daemon.settings.values[key], nil, function(raw)
        if raw == "" and allow_empty then return self.daemon.settings:set(key, "") end
        if raw:sub(1, 1) ~= "/" then return false, _("Use an absolute path beginning with /.") end
        if key == "custom_root" then
            if raw:gsub("/", "") == "" then
                return false, _("Use Advanced root to expose /. Custom roots cannot contain . or .. components.")
            end
            for component in raw:gmatch("[^/]+") do
                if component == "." or component == ".." then
                    return false, _("Use Advanced root to expose /. Custom roots cannot contain . or .. components.")
                end
            end
        end
        return self.daemon.settings:set(key, raw)
    end)
end

function ZenFM:confirm_http()
    if self.daemon.settings.values.insecure_http then
        self.daemon.settings:set("insecure_http", false)
        if self.daemon.settings.values.port == 8080 then self.daemon.settings:set("port", 8443) end
        return
    end
    UIManager:show(ConfirmBox:new{
        text = _("HTTP sends passwords, session cookies, and file contents without encryption. Enable it anyway?"),
        ok_text = _("Enable HTTP"),
        ok_callback = function()
            self.daemon.settings:set("insecure_http", true)
            if self.daemon.settings.values.port == 8443 then self.daemon.settings:set("port", 8080) end
        end,
    })
end

function ZenFM:confirm_advanced_root()
    if self.daemon.settings.values.advanced_root then
        self.daemon.settings:set("advanced_root", false)
        return
    end
    UIManager:show(ConfirmBox:new{
        text = _("Advanced root mode serves /. It exposes /proc, /sys, /dev, ZenFM's database, certificates, logs, and every file the process can access. Editing or deleting them can damage the device or lock you out."),
        ok_text = _("Expose entire filesystem"),
        ok_callback = function() self.daemon.settings:set("advanced_root", true) end,
    })
end

function ZenFM:confirm_reset_login()
    UIManager:show(ConfirmBox:new{
        text = _("Reset the owner login to the setup-only credentials and revoke every session and API token?"),
        ok_text = _("Reset login"),
        ok_callback = function()
            if self.daemon:is_android() then
                self:begin_android_action("reset", function(ok, detail)
                    notice(ok and _("Login reset. Use koreader / koreader123456789 and choose a new password.")
                        or tostring(detail), not ok)
                end)
                return
            end
            local ok, err = self.daemon:reset_login()
            local success = _("Login reset. Use koreader / koreader123456789 and choose a new password.")
            notice(ok and success or tostring(err), not ok)
        end,
    })
end

function ZenFM:update()
    notice(_("Checking for a ZenFM update…"))
    if self.daemon:is_android() then
        local ok, result = Updater.install_latest(self.daemon)
        local plugin_failed = not ok and result ~= "ZenFM is up to date"
        notice("KOReader plugin bundle: " .. tostring(result)
            .. "\nAndroid companion APK: opening updater…", plugin_failed)
        local companion_ok, companion_result = self.daemon:open_android("update")
        if not companion_ok then notice(tostring(companion_result), true) end
        return companion_ok, companion_result
    end
    local ok, result = Updater.install_latest(self.daemon)
    local plugin_failed = not ok and result ~= "ZenFM is up to date"
    notice(tostring(result), plugin_failed)
    return ok, result
end

function ZenFM:settings_menu()
    local values = self.daemon.settings.values
    return {
        {
            text_func = function()
                return _("Backend version: ") .. self.daemon:installed_backend_version()
            end,
            enabled_func = function() return false end,
        },
        {
            text = _("Port: ") .. tostring(values.port),
            keep_menu_open = true,
            callback = function() self:show_port_dialog() end,
        },
        {
            text = _("Use unencrypted HTTP"),
            checked_func = function() return self.daemon.settings.values.insecure_http end,
            keep_menu_open = true,
            callback = function() self:confirm_http() end,
        },
        {
            text = _("Advanced root: expose /"),
            checked_func = function() return self.daemon.settings.values.advanced_root end,
            keep_menu_open = true,
            callback = function() self:confirm_advanced_root() end,
        },
        {
            text_func = function()
                return _("Root: ") .. (self.daemon:root() or _("not configured"))
            end,
            keep_menu_open = true,
            callback = function() self:show_path_dialog("custom_root", _("Custom root (blank uses device default)"), true) end,
        },
        {
            text = _("Stop after 30 minutes without activity"),
            checked_func = function() return self.daemon.settings.values.auto_stop_minutes == 30 end,
            keep_menu_open = true,
            callback = function()
                local enabled = self.daemon.settings.values.auto_stop_minutes == 30
                self.daemon.settings:set("auto_stop_minutes", enabled and 0 or 30)
            end,
        },
        {
            text_func = function()
                local value = self.daemon.settings.values.tls_cert
                return _("TLS certificate: ") .. (value ~= "" and value or _("generated"))
            end,
            keep_menu_open = true,
            enabled_func = function() return not self.daemon.settings.values.insecure_http end,
            callback = function() self:show_path_dialog("tls_cert", _("TLS certificate path (blank uses generated certificate)"), true) end,
        },
        {
            text_func = function()
                local value = self.daemon.settings.values.tls_key
                return _("TLS private key: ") .. (value ~= "" and value or _("generated"))
            end,
            keep_menu_open = true,
            enabled_func = function() return not self.daemon.settings.values.insecure_http end,
            callback = function() self:show_path_dialog("tls_key", _("TLS private-key path (blank uses generated key)"), true) end,
        },
        {
            text = _("Reset owner login"),
            callback = function() self:confirm_reset_login() end,
        },
        {
            text = _("Update"),
            callback = function() self:update() end,
        },
    }
end

function ZenFM:addToMainMenu(menu_items)
    menu_items.zenfm = {
        text = _("ZenFM"),
        sub_item_table = {
            {
                text_func = function()
                    if self.daemon:is_android() then
                        if self.android_pending then return _("ZenFM request pending…") end
                        return self:android_cached_running() and _("Stop ZenFM") or _("Start ZenFM")
                    end
                    return self.daemon:status() and _("Stop ZenFM") or _("Start ZenFM")
                end,
                callback = function() self:onToggleZenFM() end,
            },
            { text = _("Status and address"), callback = function() self:onShowZenFMStatus() end },
            { text = _("Settings"), sub_item_table = self:settings_menu() },
        },
    }
end

return ZenFM

local Dispatcher = require("dispatcher")
local InfoMessage = require("ui/widget/infomessage")
local InputDialog = require("ui/widget/inputdialog")
local ConfirmBox = require("ui/widget/confirmbox")
local UIManager = require("ui/uimanager")
local WidgetContainer = require("ui/widget/container/widgetcontainer")
local _ = require("zenfm_i18n").translate

local Daemon = require("zenfm_daemon")
local Updater = require("zenfm_updater")
local Util = require("zenfm_util")
if Daemon.stopped_notice_armed == nil then Daemon.stopped_notice_armed = false end

local ZenFM = WidgetContainer:extend{
    name = "zenfm",
    is_doc_only = false,
}

local server_poll_seconds = 60
local update_timeout_seconds = 120

local function notice(text, warning, persistent)
    local timeout = warning and 6 or 3
    if persistent then timeout = false end
    local message = InfoMessage:new{
        text = text,
        icon = warning and "notice-warning" or nil,
        timeout = timeout,
    }
    UIManager:show(message)
    return message
end

local function close_notice(message)
    message.dismiss_callback = nil
    if not UIManager.isWidgetShown or UIManager:isWidgetShown(message) then
        UIManager:close(message)
    end
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
        done, success, detail = true, false, _("Could not read the Android companion result")
    end
    if done then
        self.android_pending = nil
        pending.complete(success, detail)
        return
    end
    pending.attempts = pending.attempts - 1
    if pending.attempts <= 0 then
        self.android_pending = nil
        pending.complete(false, string.format(
            _("Android companion did not report a fresh %s result within 30 seconds."),
            pending.action))
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
    Daemon.stopped_notice_armed = false
end

function ZenFM:schedule_server_monitor(monitor)
    if self.suspended or self.server_monitor ~= monitor or monitor.scheduled then return end
    monitor.scheduled = true
    UIManager:scheduleIn(server_poll_seconds, monitor.callback)
end

function ZenFM:check_server_monitor(monitor)
    if self.server_monitor ~= monitor then return end
    monitor.scheduled = false
    local running, detail = self.daemon:status()
    if running then
        self:schedule_server_monitor(monitor)
        return
    end
    self.server_monitor = nil
    self.android_running = false
    if not Daemon.stopped_notice_armed then return end
    Daemon.stopped_notice_armed = false
    notice(type(detail) == "string" and detail:match("^idle_stopped")
        and _("ZenFM stopped after inactivity.") or _("ZenFM stopped."))
end

function ZenFM:start_server_monitor(running)
    self.server_monitor = nil
    if running == nil then running = self.daemon:status() end
    if not running then return end
    Daemon.stopped_notice_armed = true
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

function ZenFM:wait_for_network_before_start()
    local NetworkMgr = require("ui/network/manager")
    return NetworkMgr:willRerunWhenConnected(function()
        local running = self.daemon:is_android()
            and self:android_cached_running() or self.daemon:status()
        if not running then self:onToggleZenFM() end
    end)
end

function ZenFM:onToggleZenFM()
    if self.daemon:is_android() then
        local running = self:android_cached_running()
        if not running and self:wait_for_network_before_start() then return true end
        local action = running and "stop" or "start"
        if running then
            self.server_monitor = nil
            Daemon.stopped_notice_armed = false
        end
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
    if not running and self:wait_for_network_before_start() then return true end
    local ok, detail
    if running then
        Daemon.stopped_notice_armed = false
        ok, detail = self.daemon:stop()
    else
        ok, detail = self.daemon:start()
    end
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
        notice(_("ZenFM is stopped."), false, true)
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

function ZenFM:show_auto_stop_dialog(touchmenu_instance)
    local SpinWidget = require("ui/widget/spinwidget")
    local settings = self.daemon.settings
    local enabled = settings.values.auto_stop_minutes > 0
    local minutes = enabled and settings.values.auto_stop_minutes
        or settings.values.auto_stop_last_minutes or 30
    local function save(value)
        local saved = settings:set("auto_stop_last_minutes", value)
        if saved and enabled then saved = settings:set("auto_stop_minutes", value) end
        if not saved then
            notice(_("Invalid value."), true)
            return
        end
        if touchmenu_instance then touchmenu_instance:updateItems() end
        notice(_("Saved. Restart ZenFM to apply the change."))
    end
    UIManager:show(SpinWidget:new{
        title_text = _("Inactivity timeout (minutes)"),
        value = minutes > 0 and minutes or 30,
        value_min = 1,
        value_max = 12 * 60,
        value_step = 1,
        value_hold_step = 10,
        default_value = 30,
        ok_text = _("Save"),
        callback = function(spin) save(spin.value) end,
    })
end

local function clean_directory_path(path)
    if type(path) ~= "string" or path:sub(1, 1) ~= "/" then return nil end
    path = path:gsub("/+$", "")
    return path == "" and "/" or path
end

local function canonical_directory_path(path)
    path = clean_directory_path(path)
    if not path then return nil end
    local ok, ffi_util = pcall(require, "ffi/util")
    if ok and ffi_util and type(ffi_util.realpath) == "function" then
        local resolved = ffi_util.realpath(path)
        if resolved then path = clean_directory_path(resolved) or path end
    end
    return path
end

local function directory_within_root(path, root)
    path, root = canonical_directory_path(path), canonical_directory_path(root)
    if not path or not root then return nil end
    if path == root then return "/" end
    if root == "/" then return path end
    if path:sub(1, #root + 1) == root .. "/" then return path:sub(#root + 1) end
    return nil
end

local function directory_from_root(root, relative)
    root = clean_directory_path(root)
    if not root then return nil end
    if relative == "/" then return root end
    return root == "/" and relative or root .. relative
end

function ZenFM:show_directory_chooser(path, on_confirm)
    local PathChooser = require("ui/widget/pathchooser")
    UIManager:show(PathChooser:new{
        select_directory = true,
        select_file = false,
        show_files = false,
        path = path,
        onConfirm = on_confirm,
    })
end

function ZenFM:show_root_chooser(touchmenu_instance)
    local settings = self.daemon.settings
    self:show_directory_chooser(self.daemon:root() or self.daemon:device_root() or "/", function(selected)
        selected = canonical_directory_path(selected)
        if not selected then
            notice(_("Invalid value."), true)
            return
        end
        if selected == "/" then
            if not settings.values.advanced_root then self:confirm_advanced_root(touchmenu_instance) end
            return
        end

        local device_root = canonical_directory_path(self.daemon:device_root())
        local custom_root = selected == device_root and "" or selected
        local saved = settings:set("custom_root", custom_root)
        if saved and settings.values.advanced_root then saved = settings:set("advanced_root", false) end
        local default_reset = false
        local default_directory = settings.values.default_directory or "/"
        if saved and default_directory ~= "/"
            and not Util.is_directory(directory_from_root(selected, default_directory)) then
            saved = settings:set("default_directory", "/")
            default_reset = saved
        end
        if not saved then
            notice(_("Invalid value."), true)
            return
        end
        if touchmenu_instance then touchmenu_instance:updateItems() end
        notice(default_reset
            and _("Saved. The default directory was reset to Home. Restart ZenFM to apply the change.")
            or _("Saved. Restart ZenFM to apply the change."))
    end)
end

function ZenFM:show_default_directory_chooser(touchmenu_instance)
    local root = canonical_directory_path(self.daemon:root())
    if not root then
        notice(_("Configure ZenFM Home first."), true)
        return
    end
    local current = directory_from_root(root, self.daemon.settings.values.default_directory or "/")
    if not Util.is_directory(current) then current = root end
    self:show_directory_chooser(current, function(selected)
        local relative = directory_within_root(selected, root)
        if not relative then
            notice(_("Choose a folder within ZenFM Home."), true)
            return
        end
        if not self.daemon.settings:set("default_directory", relative) then
            notice(_("Invalid value."), true)
            return
        end
        if touchmenu_instance then touchmenu_instance:updateItems() end
        notice(_("Saved. Restart ZenFM to apply the change."))
    end)
end

function ZenFM:restart_after_server_setting_change()
    if self.daemon:is_android() then
        if not self:android_cached_running() then return true end
        self.server_monitor = nil
        Daemon.stopped_notice_armed = false
        local started = self:begin_android_action("start", function(ok, detail)
            if not ok then
                self:start_server_monitor(true)
                notice(tostring(detail), true)
                return
            end
            self.android_running = true
            self:start_server_monitor(true)
            self:show_status(self.daemon:status_details_from_raw(detail))
        end)
        if not started then self:start_server_monitor(true) end
        return started
    end
    if not self.daemon:status() then return true end
    self.server_monitor = nil
    Daemon.stopped_notice_armed = false
    local ok, detail = self.daemon:restart()
    if not ok then
        notice(tostring(detail), true)
        return false
    end
    self:start_server_monitor(true)
    self:show_status(self.daemon:status_details_from_raw(detail))
    return true
end

function ZenFM:set_http(enabled, touchmenu_instance)
    local saved = self.daemon.settings:set("insecure_http", enabled)
    if not saved then
        notice(_("Invalid value."), true)
        return false
    end
    if touchmenu_instance then
        touchmenu_instance:updateItems()
        UIManager:forceRePaint()
    end
    return self:restart_after_server_setting_change()
end

function ZenFM:confirm_http(touchmenu_instance)
    if self.daemon.settings.values.insecure_http then
        return self:set_http(false, touchmenu_instance)
    end
    UIManager:show(ConfirmBox:new{
        text = _("HTTP sends passwords, session cookies, and file contents without encryption. Enable it anyway?"),
        ok_text = _("Enable HTTP"),
        ok_callback = function()
            self:set_http(true, touchmenu_instance)
        end,
    })
end

function ZenFM:set_advanced_root(enabled)
    local saved = self.daemon.settings:set("advanced_root", enabled)
    if not saved then
        notice(_("Invalid value."), true)
        return false
    end
    return self:restart_after_server_setting_change()
end

function ZenFM:confirm_advanced_root(touchmenu_instance)
    if self.daemon.settings.values.advanced_root then
        return self:set_advanced_root(false)
    end
    UIManager:show(ConfirmBox:new{
        text = _("Advanced root mode serves /. It exposes /proc, /sys, /dev, ZenFM's database, certificates, logs, and every file the process can access. Editing or deleting them can damage the device or lock you out."),
        ok_text = _("Expose entire filesystem"),
        ok_callback = function()
            self:set_advanced_root(true)
            touchmenu_instance:updateItems()
        end,
    })
end

function ZenFM:confirm_reset_login()
    UIManager:show(ConfirmBox:new{
        text = _("Reset the owner login to the setup-only password and revoke every session and API token?"),
        ok_text = _("Reset login"),
        ok_callback = function()
            self.server_monitor = nil
            Daemon.stopped_notice_armed = false
            local success = _("Login reset. Use koreader123456789 and choose a new password.")
            if self.daemon:is_android() then
                local started = self:begin_android_action("reset", function(ok, detail)
                    if ok then self.android_running = false else self:start_server_monitor() end
                    notice(ok and success or tostring(detail), not ok, ok)
                end)
                if not started then self:start_server_monitor() end
                return
            end
            local ok, err = self.daemon:reset_login()
            if not ok then self:start_server_monitor() end
            notice(ok and success or tostring(err), not ok, ok)
        end,
    })
end

function ZenFM:prompt_update_restart()
    UIManager:show(ConfirmBox:new{
        text = _("A restart is required to take effect."),
        ok_text = _("Restart now"),
        cancel_text = _("Restart later"),
        ok_callback = function()
            notice(_("Restarting…"), false, true)
            UIManager:forceRePaint()
            UIManager:nextTick(function()
                UIManager:nextTick(function()
                    UIManager:restartKOReader()
                end)
            end)
        end,
    })
end

function ZenFM:install_update(beta_updates, version)
    local progress = notice(_("Installing ZenFM update…"), false, true)
    UIManager:forceRePaint()
    UIManager:scheduleIn(0.1, function()
        local Trapper = require("ui/trapper")
        Trapper:wrap(function()
            local co = coroutine.running()
            local timed_out = false
            local timeout_callback = function()
                timed_out = true
                coroutine.resume(co, false)
            end
            UIManager:scheduleIn(update_timeout_seconds, timeout_callback)
            local completed, prepared, result = Trapper:dismissableRunInSubprocess(function()
                return Updater.prepare_latest(self.daemon, beta_updates, version)
            end, progress)
            UIManager:unschedule(timeout_callback)
            if not completed then
                close_notice(progress)
                notice(timed_out and _("ZenFM update timed out.") or _("ZenFM update cancelled."), timed_out)
                return
            end

            local ok = false
            if prepared then ok, result = Updater.activate_stage(self.daemon, result) end
            close_notice(progress)
            if self.daemon:is_android() then
                local plugin_failed = not ok
                notice(_("KOReader plugin bundle:") .. " " .. tostring(result)
                    .. "\n" .. _("Android companion APK: opening updater…"), plugin_failed)
                local companion_ok, companion_result = self.daemon:open_android("update")
                if not companion_ok then notice(tostring(companion_result), true) end
                if ok then self:prompt_update_restart() end
                return
            end
            if not ok then
                notice(tostring(result), true)
                return
            end
            self:prompt_update_restart()
        end)
    end)
end

function ZenFM:update()
    if self.daemon:is_android() and not self:android_cached_running() then
        return self:begin_android_action("start", function(ok, detail)
            if not ok then
                notice(tostring(detail), true)
                return
            end
            self.android_running = true
            self:start_server_monitor(true)
            self:update()
        end)
    end

    local beta_updates = self.daemon.settings.values.beta_updates == true
    local progress = notice(_("Checking for a ZenFM update…"), false, true)
    UIManager:forceRePaint()
    UIManager:scheduleIn(0.1, function()
        local Trapper = require("ui/trapper")
        Trapper:wrap(function()
            local co = coroutine.running()
            local timed_out = false
            local timeout_callback = function()
                timed_out = true
                coroutine.resume(co, false)
            end
            UIManager:scheduleIn(update_timeout_seconds, timeout_callback)
            local completed, available, result = Trapper:dismissableRunInSubprocess(function()
                return Updater.check_latest(self.daemon, beta_updates)
            end, progress)
            UIManager:unschedule(timeout_callback)
            close_notice(progress)
            if not completed then
                notice(timed_out and _("ZenFM update timed out.") or _("ZenFM update cancelled."), timed_out)
                return
            end

            if self.daemon:is_android() and not available then
                local plugin_failed = result ~= "ZenFM is up to date"
                notice(_("KOReader plugin bundle:") .. " " .. tostring(result)
                    .. "\n" .. _("Android companion APK: opening updater…"), plugin_failed)
                local companion_ok, companion_result = self.daemon:open_android("update")
                if not companion_ok then notice(tostring(companion_result), true) end
                return
            end
            if not available then
                notice(tostring(result), result ~= "ZenFM is up to date")
                return
            end

            UIManager:show(ConfirmBox:new{
                text = string.format(_("ZenFM update v%s is available."), tostring(result)),
                ok_text = _("Install now"),
                cancel_text = _("Install later"),
                ok_callback = function()
                    self:install_update(beta_updates, result)
                end,
            })
        end)
    end)
    return true, "update started"
end

function ZenFM:settings_menu()
    local values = self.daemon.settings.values
    local function auto_stop_minutes()
        local minutes = self.daemon.settings.values.auto_stop_minutes
        return minutes > 0 and minutes
            or self.daemon.settings.values.auto_stop_last_minutes or 30
    end
    return {
        {
            text = _("Port: ") .. tostring(values.port),
            keep_menu_open = true,
            callback = function() self:show_port_dialog() end,
        },
        {
            text = _("Use unencrypted HTTP"),
            checked_func = function() return self.daemon.settings.values.insecure_http end,
            check_callback_updates_menu = true,
            keep_menu_open = true,
            callback = function(touchmenu_instance) self:confirm_http(touchmenu_instance) end,
        },
        {
            text = _("Advanced root: expose /"),
            checked_func = function() return self.daemon.settings.values.advanced_root end,
            keep_menu_open = true,
            callback = function(touchmenu_instance) self:confirm_advanced_root(touchmenu_instance) end,
        },
        {
            text_func = function()
                return _("Home: ") .. (self.daemon:root() or _("not configured"))
            end,
            keep_menu_open = true,
            callback = function(touchmenu_instance) self:show_root_chooser(touchmenu_instance) end,
        },
        {
            text_func = function()
                local directory = directory_from_root(
                    self.daemon:root(),
                    self.daemon.settings.values.default_directory or "/"
                )
                return _("Default directory: ") .. (directory or _("not configured"))
            end,
            keep_menu_open = true,
            callback = function(touchmenu_instance) self:show_default_directory_chooser(touchmenu_instance) end,
        },
        {
            text_func = function()
                return string.format(_("Inactivity timeout: %d min"), auto_stop_minutes())
            end,
            checked_func = function() return self.daemon.settings.values.auto_stop_minutes > 0 end,
            checkmark_callback = function()
                local settings = self.daemon.settings
                local enabled = settings.values.auto_stop_minutes > 0
                local saved = true
                if enabled then
                    saved = settings:set("auto_stop_last_minutes", auto_stop_minutes())
                end
                if saved then
                    saved = settings:set("auto_stop_minutes", enabled and 0 or auto_stop_minutes())
                end
                if not saved then
                    notice(_("Invalid value."), true)
                    return
                end
            end,
            keep_menu_open = true,
            callback = function(touchmenu_instance) self:show_auto_stop_dialog(touchmenu_instance) end,
        },
        {
            text = _("Reset owner login"),
            callback = function() self:confirm_reset_login() end,
        },
        {
            text = _("Beta updates"),
            checked_func = function() return self.daemon.settings.values.beta_updates end,
            keep_menu_open = true,
            callback = function()
                self.daemon.settings:set("beta_updates", not self.daemon.settings.values.beta_updates)
            end,
        },
        {
            text = _("Update"),
            keep_menu_open = true,
            callback = function() self:update() end,
        },
        {
            text_func = function()
                return _("Version") .. ": " .. self.daemon:installed_backend_version()
            end,
            enabled_func = function() return false end,
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
                keep_menu_open = true,
                callback = function(touchmenu_instance)
                    self:onToggleZenFM()
                    touchmenu_instance:updateItems()
                end,
            },
            {
                text = _("Status and address"),
                keep_menu_open = true,
                callback = function() self:onShowZenFMStatus() end,
            },
            { text = _("Settings"), sub_item_table = self:settings_menu() },
        },
    }
end

return ZenFM

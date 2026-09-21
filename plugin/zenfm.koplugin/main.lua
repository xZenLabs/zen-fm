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
local peer_poll_seconds = 0.5

local function notice(text, warning, persistent, image, width)
    local timeout = warning and 6 or 3
    if persistent then timeout = false end
    local message = InfoMessage:new{
        text = text,
        icon = warning and "notice-warning" or nil,
        image = image,
        width = width,
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
    self.peer_seen = {}
    self.peer_replay = true
    self.ui.menu:registerToMainMenu(self)
    self:register_peer_send()
    if self.peer_enabled and UIManager.event_hook then
        UIManager.event_hook:registerWidget("InputEvent", self)
    end
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
    if self.peer_poll_scheduled then
        UIManager:unschedule(self.peer_poll_callback)
        self.peer_poll_scheduled = false
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
    self:start_peer_poll()
end

function ZenFM:onInputEvent()
    local second = os.time()
    if self.peer_input_poll_second == second then return end
    self.peer_input_poll_second = second
    self:check_peer_events()
end

function ZenFM:onExit()
    self.android_pending = nil
    self.android_running = false
    self.server_monitor = nil
    self:stop_peer_receive()
    if self.peer_poll_scheduled then UIManager:unschedule(self.peer_poll_callback) end
    self.peer_poll_scheduled = false
    local ok, FileManager = pcall(require, "apps/filemanager/filemanager")
    if ok and FileManager.removeFileDialogButtons then
        FileManager:removeFileDialogButtons("zenfm_send")
    end
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
        self:check_peer_events()
        return
    end
    self.server_monitor = nil
    self.android_running = false
    self:stop_peer_receive()
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
    self:check_peer_events()
end

function ZenFM:begin_android_action(action, complete, fields)
    if self.android_pending then
        notice(_("Another ZenFM Android request is still pending."), true)
        return false
    end
    local pending = {
        action = action,
        attempts = self.daemon.android_poll_attempts or 300,
        complete = complete,
    }
    self.android_pending = pending
    UIManager:nextTick(function()
        if self.android_pending ~= pending then return end
        local launched, result = self.daemon:begin_android(action, fields)
        if not launched then
            self.android_pending = nil
            complete(false, result)
            return
        end
        pending.request_id = result
    end)
    return true
end

local function peer_id(value)
    return type(value) == "string" and #value >= 16 and #value <= 80
        and value:match("^[A-Za-z0-9_-]+$") ~= nil
end

local function peer_fingerprint(value)
    return type(value) == "string" and value:match("^[A-Fa-f0-9]+$") ~= nil and #value == 64
end

local function peer_text(value)
    return type(value) == "string" and #value > 0 and #value <= 256
        and not value:find("[%z\1-\31\127]")
end

local function peer_size(value)
    value = tonumber(value) or 0
    if value < 0 or value > 2 * 1024 * 1024 * 1024 then value = 0 end
    if value < 1024 then return string.format("%d B", value) end
    if value < 1024 * 1024 then return string.format("%.1f KiB", value / 1024) end
    if value < 1024 * 1024 * 1024 then return string.format("%.1f MiB", value / 1024 / 1024) end
    return string.format("%.1f GiB", value / 1024 / 1024 / 1024)
end

local function peer_count(value, maximum)
    return type(value) == "number" and value >= 0 and value <= maximum and value % 1 == 0
end

function ZenFM:register_peer_send()
    local ok, FileManager = pcall(require, "apps/filemanager/filemanager")
    if not ok or not FileManager.addFileDialogButtons then return end
    self.peer_enabled = true
    FileManager:addFileDialogButtons("zenfm_send", function(file)
        return {{
            text = _("ZenFM Send"),
            callback = function()
                if self.ui and self.ui.file_dialog then UIManager:close(self.ui.file_dialog) end
                self:begin_peer_send(file)
            end,
        }}
    end)
end

function ZenFM:peer_command(action, arguments, fields, complete)
    local command = action .. (#arguments > 0 and " " .. table.concat(arguments, " ") or "")
    if self.daemon:is_android() then
        return self:begin_android_action(action, complete or function() end, fields)
    end
    local ok, detail = self.daemon:peer_command(command)
    if complete then complete(ok, detail) end
    return ok
end

function ZenFM:ensure_peer_server(ready)
    local function finish()
        ready()
        if not self.server_monitor then self:start_server_monitor(true) end
    end
    if self.daemon:is_android() then
        if self:android_cached_running() then
            finish()
            return true
        end
        return self:begin_android_action("start", function(ok, detail)
            if not ok then notice(tostring(detail), true) return end
            self.android_running = true
            finish()
        end)
    end
    if not self.daemon:status() then
        local ok, detail = self.daemon:start()
        if not ok then notice(tostring(detail), true) return false end
    end
    finish()
    return true
end

function ZenFM:begin_peer_send(file)
    if self.daemon.settings.values.insecure_http then
        notice(_("ZenFM Send requires HTTPS."), true)
        return
    end
    local function discover()
        self.peer_send_path = self.daemon:peer_source_path(file)
        self.peer_discovery_id = Util.random_hex(16)
        if not self.peer_discovery_id then
            notice(_("Could not create a secure ZenFM Send request."), true)
            return
        end
        self:show_peer_picker({}, "searching")
        self:start_peer_poll()
        local issued = self:peer_command("peer-discover", { self.peer_discovery_id },
            { peer_request = self.peer_discovery_id }, function(ok, detail)
                if not ok then
                    self.peer_discovery_id = nil
                    notice(tostring(detail or _("ZenFM device discovery failed.")), true)
                end
            end)
        if issued == false then self.peer_discovery_id = nil end
    end
    self:ensure_peer_server(discover)
end

function ZenFM:stop_peer_receive()
    self.peer_receive_mode = nil
    if self.peer_receive_dialog then
        UIManager:close(self.peer_receive_dialog)
        self.peer_receive_dialog = nil
    end
    if self.peer_poll_scheduled
        and not (self.peer_discovery_id or self.peer_transition_id or self.peer_progress_dialog) then
        UIManager:unschedule(self.peer_poll_callback)
        self.peer_poll_scheduled = false
    end
end

function ZenFM:begin_peer_receive()
    if self.daemon.settings.values.insecure_http then
        notice(_("ZenFM Send requires HTTPS."), true)
        return
    end
    self.peer_enabled = true
    self:ensure_peer_server(function()
        local ButtonDialog = require("ui/widget/buttondialog")
        self:stop_peer_receive()
        local dialog
        dialog = ButtonDialog:new{
            title = _("Waiting for a ZenFM sender…"),
            buttons = {{{ text = _("Cancel"), callback = function() self:stop_peer_receive() end }}},
        }
        self.peer_receive_mode = true
        self.peer_receive_dialog = dialog
        UIManager:show(dialog)
        self:start_peer_poll()
    end)
end

function ZenFM:show_peer_picker(peers, status)
    local ButtonDialog = require("ui/widget/buttondialog")
    if self.peer_dialog then UIManager:close(self.peer_dialog) end
    local buttons = {}
    for _, peer in ipairs(peers or {}) do
        if type(peer) == "table" and peer_text(peer.name) and peer_fingerprint(peer.fingerprint) then
            table.insert(buttons, {{
                text = peer.name .. " · " .. peer.fingerprint:sub(-8),
                callback = function() self:send_to_peer(peer) end,
            }})
        end
    end
    table.insert(buttons, {
        {
            text = _("Retry"),
            callback = function() self:begin_peer_send(self.peer_send_path) end,
        },
        {
            text = _("Cancel"),
            callback = function()
                UIManager:close(self.peer_dialog)
                self.peer_dialog = nil
                self.peer_discovery_id = nil
            end,
        },
    })
    local title = status == "searching" and _("Looking for ZenFM devices…")
        or (#buttons == 1 and _("No ZenFM devices found") or _("Send with ZenFM"))
    self.peer_dialog = ButtonDialog:new{ title = title, buttons = buttons }
    UIManager:show(self.peer_dialog)
end

function ZenFM:send_to_peer(peer)
    local request_id = Util.random_hex(16)
    if not request_id or not peer_fingerprint(peer.fingerprint) then return end
    if self.peer_dialog then UIManager:close(self.peer_dialog) self.peer_dialog = nil end
    local encoded = Util.base64url(self.peer_send_path)
    local item = tostring(self.peer_send_path):match("([^/]+)/*$") or self.peer_send_path
    self.peer_transition_id = request_id
    self:start_peer_poll()
    local issued = self:peer_command("peer-send", { request_id, peer.fingerprint, encoded }, {
        peer_request = request_id, fingerprint = peer.fingerprint, path = encoded, item = item,
    }, function(ok, detail)
        if not ok then
            self.peer_transition_id = nil
            notice(tostring(detail or _("ZenFM Send failed.")), true)
        end
    end)
    if issued == false then self.peer_transition_id = nil end
end

function ZenFM:start_peer_poll()
    if not self.peer_enabled or not self.server_monitor or self.daemon.settings.values.insecure_http
        or self.suspended or self.peer_poll_scheduled
        or not (self.peer_receive_mode or self.peer_discovery_id
            or self.peer_transition_id or self.peer_progress_dialog) then return end
    self.peer_poll_callback = self.peer_poll_callback or function() self:poll_peer_events() end
    self.peer_poll_scheduled = true
    UIManager:scheduleIn(peer_poll_seconds, self.peer_poll_callback)
end

function ZenFM:check_peer_events()
    if not self.peer_enabled or not self.server_monitor or self.daemon.settings.values.insecure_http
        or self.suspended or self.peer_poll_scheduled then return end
    self:poll_peer_events()
end

function ZenFM:poll_peer_events()
    self.peer_poll_scheduled = false
    if self.suspended then return end
    local raw = Util.read_all(self.daemon:peer_events_path(), 64 * 1024)
    if raw then
        local ok_json, json = pcall(require, "json")
        local ok, events = false, nil
        if ok_json then ok, events = pcall(json.decode, raw) end
        if ok and type(events) == "table" and events.version == 1 and type(events.revision) == "number"
            and events.revision ~= self.peer_revision then
            self.peer_revision = events.revision
            local replay = self.peer_replay
            self.peer_replay = nil
            self:handle_peer_events(events, replay)
        end
    end
    self:start_peer_poll()
end

function ZenFM:handle_peer_events(events, replay)
    local discovery = events.discovery
    if type(discovery) == "table" and discovery.requestId == self.peer_discovery_id then
        if discovery.status == "ready" then
            self:show_peer_picker(type(discovery.peers) == "table" and discovery.peers or {}, discovery.status)
            self.peer_discovery_id = nil
        elseif discovery.status == "error" then
            self:show_peer_picker({}, "ready")
            self.peer_discovery_id = nil
            notice(peer_text(discovery.error) and discovery.error or _("ZenFM device discovery failed."), true)
        end
    end
    local incoming = events.incoming
    if type(incoming) == "table" and peer_id(incoming.id) and peer_text(incoming.sender)
        and peer_text(incoming.name) and peer_fingerprint(incoming.fingerprint)
        and (incoming.type == "file" or incoming.type == "directory")
        and peer_count(incoming.bytes, 2 * 1024 * 1024 * 1024)
        and peer_count(incoming.fileCount, 10000) and peer_count(incoming.entryCount, 10000)
        and (incoming.status == "pending" or incoming.status == "accepted" or incoming.status == "complete"
            or incoming.status == "declined" or incoming.status == "expired" or incoming.status == "canceled"
            or incoming.status == "error")
        and (incoming.status ~= "pending" or (tonumber(incoming.expiresAt) or 0) > os.time()) then
        self:handle_incoming_peer(incoming, replay)
    end
    local outgoing = events.outgoing
    if type(outgoing) == "table" and peer_id(outgoing.id) and peer_text(outgoing.name)
        and peer_text(outgoing.peer) and peer_fingerprint(outgoing.fingerprint)
        and (outgoing.type == "file" or outgoing.type == "directory")
        and peer_count(outgoing.bytes, 2 * 1024 * 1024 * 1024)
        and peer_count(outgoing.sentBytes, 2 * 1024 * 1024 * 1024)
        and (outgoing.status == "offering" or outgoing.status == "waiting" or outgoing.status == "sending"
            or outgoing.status == "complete" or outgoing.status == "declined" or outgoing.status == "canceled"
            or outgoing.status == "error") then
        self:handle_outgoing_peer(outgoing, replay)
    end
end

function ZenFM:handle_incoming_peer(incoming, replay)
    if self.peer_transition_id == incoming.id and incoming.status ~= "pending" then
        self.peer_transition_id = nil
    end
    local state = incoming.id .. ":" .. tostring(incoming.status) .. ":" .. tostring(incoming.receivedBytes)
    if self.peer_seen.incoming == state then return end
    self.peer_seen.incoming = state
    if replay and incoming.status ~= "pending" and incoming.status ~= "accepted" then return end
    if incoming.status == "pending" then
        self:stop_peer_receive()
        local details = string.format(_("%s wants to send %s\n\nType: %s\nFiles: %d\nSize: %s\nFingerprint: …%s"),
            incoming.sender, incoming.name, incoming.type, tonumber(incoming.fileCount) or 0,
            peer_size(incoming.bytes), incoming.fingerprint:sub(-8))
        UIManager:show(ConfirmBox:new{
            text = details, ok_text = _("Accept"), cancel_text = _("Decline"),
            dismissable = false,
            ok_callback = function()
                self.peer_transition_id = incoming.id
                self:start_peer_poll()
                local issued = self:peer_command("peer-accept", { incoming.id }, { offer_id = incoming.id, item = incoming.name },
                    function(ok, detail)
                        if not ok then
                            self.peer_transition_id = nil
                            notice(tostring(detail), true)
                        end
                    end)
                if issued == false then self.peer_transition_id = nil end
            end,
            cancel_callback = function()
                self:peer_command("peer-decline", { incoming.id }, { offer_id = incoming.id })
            end,
        })
    elseif incoming.status == "accepted" then
        self:show_peer_progress(incoming.id, incoming.name, incoming.receivedBytes, incoming.bytes, true)
    elseif incoming.status == "complete" then
        self:close_peer_progress()
        local destination = peer_text(incoming.destination) and incoming.destination or "/ZenFM Received"
        notice(string.format(_("Received %s in %s"), incoming.name, destination))
    elseif incoming.status == "error" or incoming.status == "expired" then
        self:close_peer_progress()
        notice(peer_text(incoming.error) and incoming.error or _("Incoming ZenFM transfer ended."), true)
    elseif incoming.status == "canceled" or incoming.status == "declined" then
        self:close_peer_progress()
    end
end

function ZenFM:handle_outgoing_peer(outgoing, replay)
    if self.peer_transition_id == outgoing.id then self.peer_transition_id = nil end
    local state = outgoing.id .. ":" .. tostring(outgoing.status) .. ":" .. tostring(outgoing.sentBytes)
    if self.peer_seen.outgoing == state then return end
    self.peer_seen.outgoing = state
    if replay and outgoing.status ~= "offering" and outgoing.status ~= "waiting"
        and outgoing.status ~= "sending" then return end
    if outgoing.status == "offering" or outgoing.status == "waiting" or outgoing.status == "sending" then
        self:show_peer_progress(outgoing.id, outgoing.name, outgoing.sentBytes, outgoing.bytes, false)
    elseif outgoing.status == "complete" then
        self:close_peer_progress()
        notice(string.format(_("Sent %s to %s"), outgoing.name, outgoing.peer))
    elseif outgoing.status == "declined" then
        self:close_peer_progress()
        notice(_("The receiver declined the ZenFM transfer."), true)
    elseif outgoing.status == "error" then
        self:close_peer_progress()
        notice(peer_text(outgoing.error) and outgoing.error or _("ZenFM Send failed."), true)
    elseif outgoing.status == "canceled" then
        self:close_peer_progress()
    end
end

function ZenFM:show_peer_progress(id, name, current, total, incoming)
    local ButtonDialog = require("ui/widget/buttondialog")
    self:close_peer_progress()
    local percent = tonumber(total) and total > 0 and math.floor((tonumber(current) or 0) * 100 / total) or 100
    self.peer_progress_dialog = ButtonDialog:new{
        title = string.format(_("%s — %d%%"), name, percent),
        buttons = {{{
            text = _("Cancel transfer"),
            callback = function()
                self:peer_command("peer-cancel", { id }, { job_id = id })
                self:close_peer_progress()
            end,
        }}},
    }
    UIManager:show(self.peer_progress_dialog)
end

function ZenFM:close_peer_progress()
    if self.peer_progress_dialog then
        UIManager:close(self.peer_progress_dialog)
        self.peer_progress_dialog = nil
    end
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
            self:stop_peer_receive()
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
        self:stop_peer_receive()
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
    local image, width
    if status.url and self.daemon.settings.values.show_qr_code == true then
        local has_qr, QRWidget = pcall(require, "ui/widget/qrwidget")
        if has_qr then
            local Screen = require("device").screen
            local size = math.floor(math.min(Screen:getWidth(), Screen:getHeight()) * 0.35)
            image = QRWidget:new{ text = status.url, width = size, height = size }.image
            width = math.floor(Screen:getWidth() * 0.9)
        end
    end
    notice(table.concat(lines, "\n\n"), status.scheme == "http" or self.daemon.settings.values.advanced_root,
        true, image, width)
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

function ZenFM:show_device_name_dialog()
    input_dialog(self, _("Device name"), self.daemon:peer_name(), "text", function(raw)
        local name = Util.trim(raw)
        if #name > 256 or name:find("%c") then return false, _("Device name is invalid.") end
        return self.daemon.settings:set("device_name", name)
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

function ZenFM:settings_menu(include_status)
    local values = self.daemon.settings.values
    local function auto_stop_minutes()
        local minutes = self.daemon.settings.values.auto_stop_minutes
        return minutes > 0 and minutes
            or self.daemon.settings.values.auto_stop_last_minutes or 30
    end
    local items = {
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
            text_func = function() return _("Device name: ") .. self.daemon:peer_name() end,
            keep_menu_open = true,
            callback = function() self:show_device_name_dialog() end,
        },
        {
            text = _("Use device name as Browser tab title"),
            checked_func = function() return self.daemon.settings.values.use_device_name_as_title end,
            keep_menu_open = true,
            callback = function()
                local settings = self.daemon.settings
                if not settings:set("use_device_name_as_title", not settings.values.use_device_name_as_title) then
                    notice(_("Invalid value."), true)
                    return
                end
                self:restart_after_server_setting_change()
            end,
        },
        {
            text = _("Advanced"),
            sub_item_table = {
                {
                    text = _("Use unencrypted HTTP"),
                    checked_func = function() return self.daemon.settings.values.insecure_http end,
                    check_callback_updates_menu = true,
                    keep_menu_open = true,
                    callback = function(touchmenu_instance) self:confirm_http(touchmenu_instance) end,
                },
                {
                    text = _("Port: ") .. tostring(values.port),
                    keep_menu_open = true,
                    callback = function() self:show_port_dialog() end,
                },
                {
                    text = _("Root: expose /"),
                    checked_func = function() return self.daemon.settings.values.advanced_root end,
                    keep_menu_open = true,
                    callback = function(touchmenu_instance) self:confirm_advanced_root(touchmenu_instance) end,
                },
                {
                    text = _("Reset owner login"),
                    callback = function() self:confirm_reset_login() end,
                },
            },
        },
        {
            text = _("Show QR code"),
            checked_func = function() return self.daemon.settings.values.show_qr_code == true end,
            keep_menu_open = true,
            callback = function()
                self.daemon.settings:set("show_qr_code", not self.daemon.settings.values.show_qr_code)
            end,
        },
        {
            text = _("Receive with ZenFM"),
            callback = function() self:begin_peer_receive() end,
        },
        {
            text = _("Updates"),
            sub_item_table = {
                {
                    text_func = function()
                        return _("Version") .. ": " .. self.daemon:installed_backend_version()
                    end,
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
            },
        },
    }
    if include_status ~= false then
        table.insert(items, #items, {
            text = _("View address/QR code"),
            keep_menu_open = true,
            callback = function() self:onShowZenFMStatus() end,
        })
    end
    return items
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
                text = _("View address/QR code"),
                keep_menu_open = true,
                callback = function() self:onShowZenFMStatus() end,
            },
            { text = _("Settings"), sub_item_table = self:settings_menu(false) },
        },
    }
end

return ZenFM

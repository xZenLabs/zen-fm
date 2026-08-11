-- Keep ZenFM modules namespaced to avoid collisions in KOReader's package cache.
local Control = {}

local function luasocket_request(path, command, timeout)
    local ok, unix = pcall(require, "socket.unix")
    if not ok or type(unix) ~= "function" then return nil, "LuaSocket Unix support unavailable" end
    local client, err = unix()
    if not client then return nil, err end
    client:settimeout(timeout or 2)
    local connected, connect_err = client:connect(path)
    if not connected then client:close() return nil, connect_err end
    local sent, send_err = client:send(command .. "\n")
    if not sent then client:close() return nil, send_err end
    local response, receive_err = client:receive("*l")
    client:close()
    return response, receive_err
end

local function ffi_request(path, command, timeout)
    local ok, result, err = pcall(function()
        local bit = require("bit")
        local ffi = require("ffi")
        require("ffi/posix_h")
        if ffi.os ~= "Linux" then error("Unix socket FFI fallback supports Linux only") end
        if not pcall(ffi.typeof, "struct sockaddr_un") then
            ffi.cdef[[struct sockaddr_un { unsigned short sun_family; char sun_path[108]; };]]
        end
        local C = ffi.C
        local function declare(name, definition)
            if not pcall(function() return C[name] end) then ffi.cdef(definition) end
        end
        declare("connect", "int connect(int, const struct sockaddr *, unsigned int);")
        declare("send", "long send(int, const void *, unsigned long, int);")
        declare("recv", "long recv(int, void *, unsigned long, int);")
        local fd = C.socket(1, 1, 0)
        if fd < 0 then error("socket failed") end
        local function exchange()
            if #path + 1 > 108 then error("control socket path is too long") end
            local address = ffi.new("struct sockaddr_un")
            address.sun_family = 1
            ffi.copy(address.sun_path, path)
            if C.connect(fd, ffi.cast("const struct sockaddr *", address), ffi.sizeof(address)) ~= 0 then
                error("connect failed")
            end
            local request = command .. "\n"
            if C.send(fd, request, #request, 0x4000) ~= #request then error("send failed") end
            local pollfd = ffi.new("struct pollfd[1]")
            pollfd[0].fd = fd
            pollfd[0].events = C.POLLIN
            if C.poll(pollfd, 1, math.floor((timeout or 2) * 1000)) <= 0
                or bit.band(pollfd[0].revents, C.POLLERR) ~= 0 then error("control timeout") end
            local buffer = ffi.new("char[65537]")
            local count = tonumber(C.recv(fd, buffer, 65536, 0))
            if count <= 0 then error("empty control response") end
            return (ffi.string(buffer, count):match("^([^\r\n]+)"))
        end
        local exchanged, value = pcall(exchange)
        C.close(fd)
        if not exchanged then error(value) end
        return value
    end)
    if not ok then return nil, tostring(result) end
    return result, err
end

function Control.request(path, command, timeout)
    if command ~= "status" and command ~= "stop" then return nil, "unsupported control command" end
    local response, err = luasocket_request(path, command, timeout)
    if response then return response end
    local ffi_response, ffi_err = ffi_request(path, command, timeout)
    return ffi_response, ffi_err or err
end

return Control

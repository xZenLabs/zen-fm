-- Keep ZenFM modules namespaced to avoid collisions in KOReader's package cache.
local Signature = {}

local alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
local decode_map = {}
for index = 1, #alphabet do decode_map[alphabet:sub(index, index)] = index - 1 end

local function base64_decode(value)
    value = tostring(value or ""):gsub("%s+$", "")
    if value == "" or #value % 4 ~= 0 or value:find("[^A-Za-z0-9+/=]") then return nil end
    local output = {}
    for offset = 1, #value, 4 do
        local a, b = decode_map[value:sub(offset, offset)], decode_map[value:sub(offset + 1, offset + 1)]
        local c_char, d_char = value:sub(offset + 2, offset + 2), value:sub(offset + 3, offset + 3)
        local c, d = decode_map[c_char] or 0, decode_map[d_char] or 0
        if not a or not b or (c_char == "=" and d_char ~= "=") then return nil end
        local combined = a * 262144 + b * 4096 + c * 64 + d
        table.insert(output, string.char(math.floor(combined / 65536) % 256))
        if c_char ~= "=" then table.insert(output, string.char(math.floor(combined / 256) % 256)) end
        if d_char ~= "=" then table.insert(output, string.char(combined % 256)) end
        if (c_char == "=" or d_char == "=") and offset + 3 ~= #value then return nil end
    end
    return table.concat(output)
end

local function hex_decode(value)
    if type(value) ~= "string" or #value ~= 64 or value:find("[^0-9a-fA-F]") then return nil end
    return (value:gsub("..", function(pair) return string.char(tonumber(pair, 16)) end))
end

local function default_verify(message, signature, public_key)
    local ok, verified_or_error = pcall(function()
        local ffi = require("ffi")
        pcall(ffi.cdef, [[
            int ED25519_verify(const unsigned char *message, size_t message_len,
                const unsigned char signature[64], const unsigned char public_key[32]);
        ]])
        local crypto
        if ffi.loadlib then
            local loaded, library = pcall(ffi.loadlib, "crypto", "57")
            if loaded then crypto = library end
        end
        if not crypto then
            local loaded, library = pcall(ffi.load, "crypto")
            if loaded then crypto = library end
        end
        if not crypto then error("libcrypto is unavailable") end
        return crypto.ED25519_verify(message, #message, signature, public_key) == 1
    end)
    return ok and verified_or_error == true, ok and nil or tostring(verified_or_error)
end

function Signature.verify(message, encoded_signature, public_key_hex, verifier)
    if type(message) ~= "string" then return false, "manifest is invalid" end
    local signature = base64_decode(encoded_signature)
    if not signature or #signature ~= 64 then return false, "manifest signature is invalid" end
    local public_key = hex_decode(public_key_hex)
    if not public_key then return false, "release public key is not configured" end
    local ok, err = (verifier or default_verify)(message, signature, public_key)
    return ok == true, ok and nil or (err or "manifest signature did not verify")
end

Signature.base64_decode = base64_decode
Signature.hex_decode = hex_decode

return Signature

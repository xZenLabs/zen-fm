-- ZenFM translations stay private to this module. Do not inject them into
-- KOReader's process-wide gettext catalog: another plugin may use the same
-- English source string for a different meaning.
local I18n = {}

local directory = debug.getinfo(1, "S").source:match("^@(.*/)") or "./"
local active_language
local translations = {}

local supported_languages = {
    bg = true,
    cs = true,
    de = true,
    el = true,
    en = true,
    es = true,
    fr = true,
    it = true,
    ja = true,
    nl = true,
    pt_BR = true,
    pt_PT = true,
    ro = true,
    ru = true,
    uk = true,
    vi = true,
    zh_CN = true,
    zh_HK = true,
    zh_MO = true,
    zh_TW = true,
}

local aliases = {
    pt = "pt_PT",
    zh = "zh_CN",
    zh_HANS = "zh_CN",
    zh_HANT = "zh_TW",
}

local function unescape(value)
    return value:gsub("\\n", "\n")
        :gsub("\\t", "\t")
        :gsub('\\"', '"')
        :gsub("\\\\", "\\")
end

local function parse_po(path)
    local file = io.open(path, "r")
    if not file then return {} end

    local result = {}
    local id, value
    local reading_id, reading_value = false, false

    local function flush()
        if id and id ~= "" and value and value ~= "" then result[id] = value end
        id, value = nil, nil
        reading_id, reading_value = false, false
    end

    for raw_line in file:lines() do
        local line = raw_line:match("^%s*(.-)%s*$")
        if line == "" then
            flush()
        elseif line:match("^msgid%s+\"") then
            flush()
            id = unescape(line:match('^msgid%s+"(.*)"') or "")
            reading_id, reading_value = true, false
        elseif line:match("^msgstr%s+\"") then
            value = unescape(line:match('^msgstr%s+"(.*)"') or "")
            reading_id, reading_value = false, true
        elseif line:match('^"') then
            local continuation = unescape(line:match('^"(.*)"') or "")
            if reading_id and id then id = id .. continuation end
            if reading_value and value then value = value .. continuation end
        end
    end
    flush()
    file:close()
    return result
end

local function detected_language()
    local language = G_reader_settings and G_reader_settings:readSetting("language")
    if type(language) == "string" and language ~= "" then return language end
    return os.getenv("LANG") or os.getenv("LC_ALL") or os.getenv("LC_MESSAGES") or "en"
end

local function normalize_language(language)
    language = tostring(language or "en")
        :gsub("%..*$", "")
        :gsub("@.*$", "")
        :gsub("-", "_")
    if language == "C" or language == "POSIX" then return "en" end

    local base, region = language:match("^([A-Za-z]+)_([A-Za-z]+)$")
    if base and region then language = base:lower() .. "_" .. region:upper()
    else language = language:lower() end

    language = aliases[language] or language
    if supported_languages[language] then return language end
    base = language:match("^([a-z]+)")
    if base and supported_languages[base] then return base end
    return "en"
end

function I18n.refresh(language)
    active_language = normalize_language(language or detected_language())
    translations = active_language == "en"
        and {}
        or parse_po(directory .. "locales/" .. active_language .. ".po")
    return active_language
end

function I18n.translate(message, language)
    local expected = normalize_language(language or detected_language())
    if active_language ~= expected then I18n.refresh(expected) end
    return translations[message] or message
end

function I18n.get_language()
    local expected = normalize_language(detected_language())
    if active_language ~= expected then I18n.refresh(expected) end
    return active_language
end

I18n.supported_languages = supported_languages

return I18n

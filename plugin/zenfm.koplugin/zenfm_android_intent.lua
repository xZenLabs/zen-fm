-- Keep ZenFM modules namespaced to avoid collisions in KOReader's package cache.
local AndroidIntent = {
    ACTION = "android.intent.action.VIEW",
    PACKAGE = "org.zenlabs.zenfm",
    CLASS = "org.zenlabs.zenfm.ZenFMActivity",
}

local function bridge_available(android)
    return android and android.jni and type(android.jni.context) == "function"
        and android.app and android.app.activity
        and android.app.activity.vm ~= nil and android.app.activity.clazz ~= nil
end

function AndroidIntent.open(android, uri)
    if not bridge_available(android) or type(uri) ~= "string" or uri == "" then return false end
    local ok_ffi, ffi = pcall(require, "ffi")
    if not ok_ffi then return false end

    local called, opened = pcall(function()
        local one = ffi.new("jvalue[1]")
        local two = ffi.new("jvalue[2]")
        return android.jni:context(android.app.activity.vm, function(jni)
            local env, api
            local frame_pushed = false

            local function clear_exception()
                if env == nil or api == nil then return false end
                local exception = api.ExceptionOccurred(env)
                if exception == nil then return false end
                api.ExceptionClear(env)
                api.DeleteLocalRef(env, exception)
                return true
            end

            local function finish(result)
                if clear_exception() then result = false end
                local pop_frame = frame_pushed
                frame_pushed = false
                if pop_frame then api.PopLocalFrame(env, nil) end
                return result
            end

            local protected, result = pcall(function()
                env = jni.env
                api = env[0]
                if api.PushLocalFrame(env, 16) < 0 then
                    clear_exception()
                    return false
                end
                frame_pushed = true

                local uri_class = api.FindClass(env, "android/net/Uri")
                local failed = clear_exception()
                if uri_class == nil or failed then return finish(false) end
                local parse = api.GetStaticMethodID(env, uri_class, "parse",
                    "(Ljava/lang/String;)Landroid/net/Uri;")
                failed = clear_exception()
                if parse == nil or failed then return finish(false) end
                local uri_string = api.NewStringUTF(env, uri)
                failed = clear_exception()
                if uri_string == nil or failed then return finish(false) end
                one[0].l = uri_string
                local parsed_uri = api.CallStaticObjectMethodA(env, uri_class, parse, one)
                failed = clear_exception()
                if parsed_uri == nil or failed then return finish(false) end

                local intent_class = api.FindClass(env, "android/content/Intent")
                failed = clear_exception()
                if intent_class == nil or failed then return finish(false) end
                local constructor = api.GetMethodID(env, intent_class, "<init>",
                    "(Ljava/lang/String;Landroid/net/Uri;)V")
                failed = clear_exception()
                if constructor == nil or failed then return finish(false) end
                local action = api.NewStringUTF(env, AndroidIntent.ACTION)
                failed = clear_exception()
                if action == nil or failed then return finish(false) end
                two[0].l = action
                two[1].l = parsed_uri
                local intent = api.NewObjectA(env, intent_class, constructor, two)
                failed = clear_exception()
                if intent == nil or failed then return finish(false) end

                local set_class = api.GetMethodID(env, intent_class, "setClassName",
                    "(Ljava/lang/String;Ljava/lang/String;)Landroid/content/Intent;")
                failed = clear_exception()
                if set_class == nil or failed then return finish(false) end
                local package_name = api.NewStringUTF(env, AndroidIntent.PACKAGE)
                local class_name = api.NewStringUTF(env, AndroidIntent.CLASS)
                failed = clear_exception()
                if package_name == nil or class_name == nil or failed then return finish(false) end
                two[0].l = package_name
                two[1].l = class_name
                local explicit_intent = api.CallObjectMethodA(env, intent, set_class, two)
                failed = clear_exception()
                if explicit_intent == nil or failed then return finish(false) end

                local activity = android.app.activity.clazz
                local activity_class = api.GetObjectClass(env, activity)
                failed = clear_exception()
                if activity_class == nil or failed then return finish(false) end
                local start_activity = api.GetMethodID(env, activity_class, "startActivity",
                    "(Landroid/content/Intent;)V")
                failed = clear_exception()
                if start_activity == nil or failed then return finish(false) end
                one[0].l = intent
                api.CallVoidMethodA(env, activity, start_activity, one)
                return finish(true)
            end)
            if protected then return result == true end
            pcall(finish, false)
            return false
        end)
    end)
    return called and opened == true
end

return AndroidIntent

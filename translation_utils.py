#!/usr/bin/env python3
"""Maintain ZenFM's shared KOReader and frontend translation catalogs.

Usage:
    python3 translation_utils.py --sync [--locale LOCALE]

The utility extracts ``_("...")`` calls from the plugin plus the frontend
English catalogs. ``--sync`` adds, translates, removes, and sorts
entries, then regenerates the TypeScript frontend resources.
"""

import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
PLUGIN_DIR = os.path.join(SCRIPT_DIR, "plugin", "zenfm.koplugin")
LOCALES_DIR = os.path.join(PLUGIN_DIR, "locales")
FRONTEND_TRANSLATIONS_ENGLISH = os.path.join(
    SCRIPT_DIR, "frontend", "src", "locales", "translations.en.json")
FRONTEND_SETTINGS_ENGLISH = os.path.join(
    SCRIPT_DIR, "frontend", "src", "locales", "settings.en.json")
FRONTEND_GENERATED = os.path.join(
    SCRIPT_DIR, "frontend", "src", "locales", "translations.ts")

SUPPORTED_LOCALES = (
    "en", "bg", "cs", "de", "el", "es", "fr", "it", "ja", "nl",
    "pt_BR", "pt_PT", "ro", "ru", "uk", "vi", "zh_CN", "zh_HK",
    "zh_MO", "zh_TW",
)

GOOGLE_TRANSLATE_URL = "https://translate.googleapis.com/translate_a/single"
GOOGLE_LOCALES = {
    "pt_BR": "pt",
    "pt_PT": "pt-PT",
    "zh_CN": "zh-CN",
    "zh_HK": "zh-HK",
    "zh_MO": "zh-MO",
    "zh_TW": "zh-TW",
}
TRANSLATION_WORKERS = 6

LUA_PATTERNS = (
    re.compile(r'_\(\s*"((?:[^"\\]|\\.)*)"\s*\)', re.DOTALL),
    re.compile(r"_\(\s*'((?:[^'\\]|\\.)*)'\s*\)", re.DOTALL),
    re.compile(r"_\(\s*\[\[(.*?)\]\]\s*\)", re.DOTALL),
)
FORMAT_TOKEN_RE = re.compile(
    r"%(?:\d+\$)?[-+ #0]*\d*(?:\.\d+)?[A-Za-z%]|%\d+"
    r"|{{\s*[A-Za-z0-9_.-]+\s*}}|</?[A-Za-z][^>]*>"
)


def unescape_lua(value: str) -> str:
    return (
        value.replace("\\n", "\n")
        .replace("\\t", "\t")
        .replace('\\"', '"')
        .replace("\\'", "'")
        .replace("\\\\", "\\")
    )


def read_json(path: str) -> dict[str, object]:
    with open(path, encoding="utf-8") as file:
        return json.load(file)


def walk_strings(value: object, path: tuple[str, ...] = ()):
    if isinstance(value, str):
        yield path, value
    elif isinstance(value, dict):
        for key, child in value.items():
            yield from walk_strings(child, path + (key,))


def frontend_catalog() -> dict[str, object]:
    catalog = read_json(FRONTEND_TRANSLATIONS_ENGLISH)
    catalog["settings"] = read_json(FRONTEND_SETTINGS_ENGLISH)
    return catalog


def collect_messages() -> dict[str, list[str]]:
    messages: dict[str, list[str]] = {}
    for root, dirs, files in os.walk(PLUGIN_DIR):
        dirs[:] = [name for name in dirs if name not in {"backend", "data", "locales", "tests"}]
        for name in files:
            if not name.endswith(".lua"):
                continue
            path = os.path.join(root, name)
            with open(path, encoding="utf-8", errors="replace") as file:
                source = file.read()
            for pattern in LUA_PATTERNS:
                for match in pattern.finditer(source):
                    message = unescape_lua(match.group(1))
                    messages.setdefault(message, []).append(os.path.relpath(path, SCRIPT_DIR))

    for path in (FRONTEND_TRANSLATIONS_ENGLISH, FRONTEND_SETTINGS_ENGLISH):
        for _, message in walk_strings(read_json(path)):
            messages.setdefault(message, []).append(os.path.relpath(path, SCRIPT_DIR))
    return messages


def decode_po_string(value: str) -> str:
    try:
        return json.loads(value)
    except json.JSONDecodeError:
        return ""


def parse_po(path: str) -> dict[str, str]:
    if not os.path.exists(path):
        return {}
    with open(path, encoding="utf-8", errors="replace") as file:
        content = file.read()

    entries: dict[str, str] = {}
    for block in re.split(r"\n\s*\n", content.strip()):
        lines = block.splitlines()
        message_id: list[str] = []
        message_value: list[str] = []
        target: list[str] | None = None
        for line in lines:
            line = line.strip()
            if line.startswith("msgid "):
                target = message_id
                target.append(decode_po_string(line[6:]))
            elif line.startswith("msgstr "):
                target = message_value
                target.append(decode_po_string(line[7:]))
            elif line.startswith('"') and target is not None:
                target.append(decode_po_string(line))
        message = "".join(message_id)
        if message:
            entries[message] = "".join(message_value)
    return entries


def po_quote(value: str) -> str:
    return json.dumps(value, ensure_ascii=False)


def write_po(path: str, locale: str, entries: dict[str, str]) -> None:
    header = (
        'msgid ""\n'
        'msgstr ""\n'
        '"Project-Id-Version: ZenFM\\n"\n'
        '"MIME-Version: 1.0\\n"\n'
        '"Content-Type: text/plain; charset=UTF-8\\n"\n'
        '"Content-Transfer-Encoding: 8bit\\n"\n'
        f'"Language: {locale}\\n"\n'
    )
    blocks = [header.rstrip("\n")]
    for message in sorted(entries, key=str.casefold):
        blocks.append(f"msgid {po_quote(message)}\nmsgstr {po_quote(entries[message])}")
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as file:
        file.write("\n\n".join(blocks) + "\n")


def protect_format_tokens(message: str) -> tuple[str, list[str]]:
    tokens: list[str] = []

    def replace(match: re.Match[str]) -> str:
        tokens.append(match.group(0))
        return f"__ZENFM_FORMAT_{len(tokens) - 1}__"

    return FORMAT_TOKEN_RE.sub(replace, message), tokens


def restore_format_tokens(message: str, tokens: list[str]) -> str:
    for index, token in enumerate(tokens):
        marker = f"__ZENFM_FORMAT_{index}__"
        if message.count(marker) != 1:
            raise ValueError("translation changed a format placeholder marker")
        message = message.replace(marker, token)
    if sorted(FORMAT_TOKEN_RE.findall(message)) != sorted(tokens):
        raise ValueError("translation changed a format placeholder")
    return message


def google_translate(message: str, locale: str, timeout: int = 20) -> str:
    if locale == "en":
        return message
    protected, tokens = protect_format_tokens(message)
    query = urllib.parse.urlencode({
        "client": "gtx",
        "sl": "en",
        "tl": GOOGLE_LOCALES.get(locale, locale),
        "dt": "t",
        "q": protected,
    })
    request = urllib.request.Request(
        f"{GOOGLE_TRANSLATE_URL}?{query}",
        headers={"User-Agent": "Mozilla/5.0"},
    )
    last_error: Exception | None = None
    for attempt in range(3):
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                data = json.load(response)
            translated = "".join(part[0] for part in data[0] if part and part[0])
            if not translated:
                raise ValueError("Google returned an empty translation")
            return restore_format_tokens(translated, tokens)
        except (OSError, ValueError, KeyError, IndexError, TypeError, urllib.error.URLError) as error:
            last_error = error
            if attempt < 2:
                time.sleep(2**attempt)
    raise RuntimeError(f"Google translation failed for {locale}: {message!r}: {last_error}")


def translate_messages(locale: str, messages: list[str]) -> dict[str, str]:
    if not messages:
        return {}
    translated: dict[str, str] = {}
    with ThreadPoolExecutor(max_workers=min(TRANSLATION_WORKERS, len(messages))) as pool:
        jobs = {pool.submit(google_translate, message, locale): message for message in messages}
        for job in as_completed(jobs):
            translated[jobs[job]] = job.result()
    return translated


def selected_locales(locale: str | None) -> tuple[str, ...]:
    if locale:
        if locale not in SUPPORTED_LOCALES:
            raise ValueError(f"unsupported locale: {locale}")
        return (locale,)
    return SUPPORTED_LOCALES


def catalog_path(locale: str) -> str:
    return os.path.join(LOCALES_DIR, f"{locale}.po")


def read_frontend_translations() -> dict[str, object]:
    if not os.path.exists(FRONTEND_GENERATED):
        return {}
    with open(FRONTEND_GENERATED, encoding="utf-8", errors="replace") as file:
        source = file.read()
    match = re.search(
        r"export const fileBrowserTranslations\s*=\s*(\{.*\})\s+as const\s*$",
        source,
        re.DOTALL,
    )
    if not match:
        raise ValueError(f"could not parse {os.path.relpath(FRONTEND_GENERATED, SCRIPT_DIR)}")
    return json.loads(match.group(1))


def nested_value(value: object, path: tuple[str, ...]) -> object | None:
    for key in path:
        if not isinstance(value, dict):
            return None
        value = value.get(key)
    return value


def valid_translation(message: str, translation: object) -> bool:
    return (
        isinstance(translation, str)
        and bool(translation)
        and sorted(FORMAT_TOKEN_RE.findall(translation))
        == sorted(FORMAT_TOKEN_RE.findall(message))
    )


def frontend_translation_seeds(
    locale: str,
    english: dict[str, object],
    resources: dict[str, object],
) -> dict[str, str]:
    resource = resources.get(locale.replace("_", "-"), {})
    translation = resource.get("translation", {}) if isinstance(resource, dict) else {}
    seeds: dict[str, str] = {}
    for path, message in walk_strings(english):
        value = nested_value(translation, path)
        if valid_translation(message, value) and value != message:
            seeds.setdefault(message, value)
    return seeds


def sync_catalogs(locales: tuple[str, ...], source_messages: set[str]) -> None:
    english = frontend_catalog()
    frontend_resources = read_frontend_translations()
    plans: list[tuple[str, dict[str, str], int, int, int]] = []
    for locale in locales:
        existing = parse_po(catalog_path(locale))
        missing = source_messages - set(existing)
        dead = set(existing) - source_messages
        seeds = frontend_translation_seeds(locale, english, frontend_resources)
        synced = {
            message: (
                existing.get(message, "")
                if valid_translation(message, existing.get(message, ""))
                else seeds.get(message, "")
            )
            for message in source_messages
        }
        untranslated = sorted(message for message, value in synced.items() if not value)
        print(f"[{locale}] missing={len(missing)} dead={len(dead)} untranslated={len(untranslated)}")
        synced.update(translate_messages(locale, untranslated))
        plans.append((locale, synced, len(dead), len(missing), len(untranslated)))

    for locale, entries, dead, missing, untranslated in plans:
        write_po(catalog_path(locale), locale, entries)
        print(f"  -> {locale}.po: removed={dead} added={missing} translated={untranslated}")


def translated_tree(value: object, catalog: dict[str, str]) -> object:
    if isinstance(value, str):
        return catalog.get(value) or value
    if isinstance(value, dict):
        return {key: translated_tree(child, catalog) for key, child in value.items()}
    return value


def generate_frontend() -> None:
    english = frontend_catalog()
    generated = read_frontend_translations()
    for locale in SUPPORTED_LOCALES:
        if locale == "en":
            continue
        catalog = parse_po(catalog_path(locale))
        if not catalog:
            continue
        generated[locale.replace("_", "-")] = {
            "translation": translated_tree(english, catalog)
        }

    body = json.dumps(generated, ensure_ascii=False, indent=2)
    content = (
        "// Generated by translation_utils.py from plugin/zenfm.koplugin/locales/*.po.\n"
        "export const fileBrowserTranslations = "
        f"{body} as const\n"
    )
    with open(FRONTEND_GENERATED, "w", encoding="utf-8") as file:
        file.write(content)
    print(f"Generated {os.path.relpath(FRONTEND_GENERATED, SCRIPT_DIR)}")


def report(locales: tuple[str, ...], messages: dict[str, list[str]], args: argparse.Namespace) -> bool:
    source = set(messages)
    changed = False
    for locale in locales:
        path = catalog_path(locale)
        existing = parse_po(path)
        missing = sorted(source - set(existing))
        dead = sorted(set(existing) - source)
        untranslated = sorted(message for message, value in existing.items() if not value)

        if args.list_missing:
            print(f"[{locale}] {len(missing)} missing")
            for message in missing:
                print(f"  {message!r}")
            continue
        if args.list_untranslated:
            print(f"[{locale}] {len(untranslated)} untranslated")
            for message in untranslated:
                print(f"  {message!r}")
            continue

        print(f"[{locale}] missing={len(missing)} dead={len(dead) if args.show_dead or args.remove_dead else '?'}")
        for message in missing:
            files = messages[message]
            print(f"  MISSING {message!r} <- {', '.join(files[:2])}")
        if args.show_dead or args.remove_dead:
            for message in dead:
                print(f"  DEAD {message!r}")

        if args.update_po or args.remove_dead or args.alphabetize:
            entries = dict(existing)
            if args.update_po:
                entries.update({message: "" for message in missing})
            if args.remove_dead:
                entries = {message: value for message, value in entries.items() if message in source}
            write_po(path, locale, entries)
            changed = True
    return changed


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sync", action="store_true")
    parser.add_argument("--update-po", action="store_true")
    parser.add_argument("--remove-dead", action="store_true")
    parser.add_argument("--alphabetize", action="store_true")
    parser.add_argument("--list-missing", action="store_true")
    parser.add_argument("--list-untranslated", action="store_true")
    parser.add_argument("--show-dead", action="store_true")
    parser.add_argument("--generate-frontend", action="store_true")
    parser.add_argument("--locale")
    args = parser.parse_args()

    action_count = sum(bool(value) for value in (
        args.sync, args.update_po, args.remove_dead, args.alphabetize,
        args.list_missing, args.list_untranslated, args.show_dead,
        args.generate_frontend,
    ))
    if action_count == 0:
        parser.error("choose an action")
    if args.sync and action_count != 1:
        parser.error("--sync cannot be combined with other actions")

    try:
        locales = selected_locales(args.locale)
    except ValueError as error:
        parser.error(str(error))

    messages = collect_messages()
    print(f"Found {len(messages)} unique translatable strings")
    if args.sync:
        try:
            sync_catalogs(locales, set(messages))
        except RuntimeError as error:
            print(f"Error: {error}", file=sys.stderr)
            sys.exit(1)
        generate_frontend()
        return

    changed = report(locales, messages, args)
    if changed or args.generate_frontend:
        generate_frontend()


if __name__ == "__main__":
    main()

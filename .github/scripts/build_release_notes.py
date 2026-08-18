#!/usr/bin/env python3
import re
import sys
from pathlib import Path


CHANGELOG = (
    Path(__file__).resolve().parents[2]
    / "plugin"
    / "zenfm.koplugin"
    / "changelog.lua"
)


def find_version_block(content, version):
    key = '["{}"]'.format(version)
    key_pos = content.find(key)
    if key_pos < 0:
        return None

    eq_pos = content.find("=", key_pos + len(key))
    if eq_pos < 0:
        return None
    start = content.find("{", eq_pos)
    if start < 0:
        return None

    depth = 1
    quote = None
    escaped = False
    index = start + 1
    while index < len(content):
        char = content[index]
        if quote:
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == quote:
                quote = None
        else:
            if char == '"' or char == "'":
                quote = char
            elif char == "{":
                depth += 1
            elif char == "}":
                depth -= 1
                if depth == 0:
                    return content[start + 1:index]
        index += 1

    return None


def clean_lua_string(text):
    text = re.sub(r"\\u\{[0-9A-Fa-f]+\}\s*", "", text)
    escapes = {
        "n": "\n",
        "r": "\r",
        "t": "\t",
        "\\": "\\",
        '"': '"',
        "'": "'",
    }
    output = []
    index = 0
    while index < len(text):
        char = text[index]
        if char == "\\" and index + 1 < len(text):
            following = text[index + 1]
            output.append(escapes.get(following, char + following))
            index += 2
        else:
            output.append(char)
            index += 1
    return "".join(output).strip()


def extract_items(block):
    items = []
    quote = None
    escaped = False
    buffer = []

    for char in block:
        if quote:
            if escaped:
                buffer.append("\\" + char)
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == quote:
                item = clean_lua_string("".join(buffer))
                if item:
                    items.append(item)
                quote = None
                buffer = []
            else:
                buffer.append(char)
        elif char == '"' or char == "'":
            quote = char

    return items


def release_items(content, version):
    version = version[1:] if version.startswith("v") else version
    candidates = [version]
    stable_version = re.sub(r"-beta[0-9]+$", "", version)
    if stable_version != version:
        candidates.append(stable_version)

    for candidate in candidates:
        block = find_version_block(content, candidate)
        if block is not None:
            return extract_items(block)
    return []


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: build_release_notes.py VERSION")

    content = CHANGELOG.read_text(encoding="utf-8")
    items = release_items(content, sys.argv[1])

    print("## What's Changed\n")
    if items:
        for item in items:
            print("- {}".format(item))
    else:
        print("_No changelog entries for this version._")


if __name__ == "__main__":
    main()

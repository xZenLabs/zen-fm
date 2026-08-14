#!/usr/bin/env python3
import os
import sys
import unittest


ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)
sys.dont_write_bytecode = True

import translation_utils as translations


class TranslationUtilsTest(unittest.TestCase):
    def test_format_placeholders_survive_translation_markers(self):
        source = (
            "Move %s from <path>{{source}}</path> to "
            "<path>{{destination}}</path>."
        )
        protected, tokens = translations.protect_format_tokens(source)

        self.assertNotIn("%s", protected)
        self.assertNotIn("{{source}}", protected)
        self.assertEqual(translations.restore_format_tokens(protected, tokens), source)

    def test_changed_format_marker_is_rejected(self):
        protected, tokens = translations.protect_format_tokens("Result: %s")

        with self.assertRaisesRegex(ValueError, "changed a format placeholder marker"):
            translations.restore_format_tokens(protected.replace("FORMAT", "FORM AT"), tokens)

    def test_collects_both_frontend_english_catalogs(self):
        messages = translations.collect_messages()

        self.assertIn("Your files, easily accessible.", messages)
        self.assertIn("Settings saved", messages)

    def test_reuses_existing_frontend_translations_as_seeds(self):
        english = {"files": {"edit": "Edit"}}
        resources = {"de": {"translation": {"files": {"edit": "Bearbeiten"}}}}

        self.assertEqual(
            translations.frontend_translation_seeds("de", english, resources),
            {"Edit": "Bearbeiten"},
        )


if __name__ == "__main__":
    unittest.main()

import json
import unittest

from Tests.helpers.engine_go_shard import API, API_SHARDS, audit, select


class EngineGoShards(unittest.TestCase):
    def test_complete_partition_includes_new_tests_and_examples(self):
        names = ["TestKnown", "TestNew", "TestTiny", "Example", "FuzzParse"]
        listing = "\n".join([*names, "BenchmarkParse", "ok " + API])
        groups = [select(listing, {"TestKnown": 15.0}, i) for i in range(1, API_SHARDS + 1)]
        self.assertCountEqual((test for group in groups for test in group), names)

    def test_skips_and_missing_nested_completions_are_visible(self):
        def check(events, database=()):
            stream = [json.dumps(dict(Package=API, Action=action, Test=name)) for action, name in events]
            audit(["TestSQL"], stream, set(database))
        start = [("start", ""), ("run", "TestSQL")]
        end = [("pass", "TestSQL"), ("pass", "")]
        with self.assertRaises(ValueError):
            check(start + [("run", "TestSQL/nested")] + end)
        with self.assertRaises(ValueError):
            check(start + [("skip", "TestSQL"), ("pass", "")])
        check(start + [("skip", "TestSQL"), ("pass", "")], ["TestSQL"])

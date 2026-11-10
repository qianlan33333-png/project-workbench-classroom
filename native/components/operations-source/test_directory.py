import unittest

from directory import members, profile, profiles


class OriginalDirectoryTest(unittest.TestCase):
    def test_full_population_uses_original_cursor_and_count(self):
        def read(path, query):
            self.assertEqual(path, "archive/users")
            self.assertEqual(query, {"filter": "all", "scope": "all", "limit": 20, "q": "Avi", "cursor": "abc", "snapshot_id": "snapshot-1"})
            return {"total": 25142, "snapshot_id": "snapshot-1", "next_cursor": "def", "items": [
                {"safe_user_ref": "usr_1", "企微昵称": "Avi", "operator_contact": {"phone": "private"}},
            ]}

        out = profiles(read, {"q": "Avi", "cursor": "abc", "snapshot_id": "snapshot-1"})
        self.assertEqual(out["total"], 25142)
        self.assertEqual(out["next"], "def")
        self.assertEqual(out["records"], [{"id": "usr_1", "nickname": "Avi"}])

    def test_package_membership_stays_bound_to_the_original_revision(self):
        expected = {"action": "test_candidates", "package_id": "weekpkg-source", "week": "2026-W46"}

        def read(path, payload, timeout):
            self.assertEqual(path, "codex-copy-jobs")
            self.assertEqual(payload, expected)
            return {"items": [{"safe_user_ref": f"usr_{i}", "name": f"Person {i}"} for i in range(26)]}

        query = {"package": "weekpkg-source", "week": "2026-W46"}
        first = members(read, query)
        second = members(read, {**query, "after": first["next"], "revision": first["revision"]})
        self.assertEqual(first["total"], 26)
        self.assertEqual(len(first["records"]), 20)
        self.assertEqual(len(second["records"]), 6)
        self.assertEqual(members(read, {**query, "revision": "old"})["state"], "source_changed")

    def test_historical_merge_does_not_replace_published_fact(self):
        def read(path, query, timeout=18):
            if path == "archive/user":
                return {"resolved_ref": "usr_1", "snapshot_id": "snapshot-1", "user": {
                    "safe_user_ref": "usr_1", "企微昵称": "Avi", "行业": "当前行业",
                }}
            if path == "hxc-unified/user":
                return {"mapped": True, "merge_approved": True, "merge_rule_version": "hxc-profile-merge.v1", "context": {
                    "profile_facts": [{"field_key": "industry", "value": "历史行业", "source_ref": "evidence-1"}],
                }}
            return {}

        out = profile(read, {"id": "usr_1", "snapshot_id": "snapshot-1"})
        groups = {group["title"]: group["fields"] for group in out["groups"]}
        self.assertEqual(next(f["value"] for f in groups["基础信息"] if f["label"] == "行业"), "当前行业")
        self.assertEqual(groups["关系中心历史资料"][0]["value"], "历史行业")

import unittest
from unittest.mock import patch

from directory import delivery_label, members, package_binding, package_card, package_detail, package_edit, package_prepare, profile, profiles, reader


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

    def test_package_edit_binds_current_digest_and_revision(self):
        run = "copybatch-" + "1" * 32
        digest = "2" * 64
        params = {"package": "weekpkg-source", "week": "2026-W46", "run": run, "digest": digest,
                  "item": "usr_1", "revision": "2", "text": "修改的最终稿"}
        calls = []
        detail = {"run_id": run, "digest": digest, "is_sample": False, "items": [
            {"item_key": "usr_1", "safe_user_ref": "usr_1", "usable": True, "reviewable": True, "text": "原稿", "edit_revision": 2},
        ]}

        def read(path, payload, timeout):
            calls.append((path, payload))
            return {"revision": 3, "digest": "4" * 64, "automatic_send_allowed": False}

        with patch("directory.current_package", return_value={"run_id": run}) as latest, patch("directory.package_run", return_value=detail), patch("directory.formal_status", return_value=""):
            self.assertEqual(package_edit(read, {**params, "digest": "3" * 64})["state"], "source_changed")
            self.assertEqual(package_edit(read, {**params, "revision": "1"})["state"], "source_changed")
            self.assertEqual(package_edit(read, {**params, "text": "原稿"})["revision"], 2)
            self.assertEqual(calls, [])
            self.assertEqual(package_edit(read, params), {"state": "saved", "digest": "4" * 64, "revision": 3})
            latest.return_value = {"run_id": "copybatch-" + "5" * 32}
            self.assertEqual(package_edit(read, params)["state"], "source_changed")
        self.assertEqual(calls, [("copy-package-edit", {
            "run_id": run, "digest": digest, "item_key": "usr_1", "expected_revision": 2, "text": "修改的最终稿",
        })])

    def test_changed_membership_prepares_a_new_original_run(self):
        old, new = ("copybatch-" + digit * 32 for digit in ("1", "2"))
        params = {"package": "weekpkg-source", "week": "2026-W46", "revision": "current"}

        def read(path, payload, timeout):
            self.assertEqual(path, "codex-copy-jobs")
            self.assertEqual(payload, {"action": "prepare", "package_id": "weekpkg-source", "week": "2026-W46", "selected_user_refs": ["usr_2"]})
            return {"run_id": new, "count": 1, "automatic_send_allowed": False}

        with patch("directory.current_package", return_value={"run_id": old, "ready_for_generation": True}), \
             patch("directory.package_run", return_value={"items": [{"safe_user_ref": "usr_1"}]}), \
             patch("directory.candidate_records", return_value=([{"id": "usr_2"}], "current")):
            self.assertEqual(package_prepare(read, params), {"state": "ready", "run": new, "total": 1})

    def test_package_projection_exposes_only_the_selected_draft(self):
        params = {"run": "copybatch-" + "1" * 32}
        detail = {"run_id": params["run"], "digest": "2" * 64, "items": [
            {"item_key": "usr_1", "safe_user_ref": "usr_1", "usable": True, "reviewable": True, "text": "逐人正文",
             "judgment": {"goal": "原判断"}, "attachments": [{"title": "卡片", "type": "mini_program", "page": "pages/x", "verified": True}]},
            {"item_key": "usr_2", "safe_user_ref": "usr_2", "usable": True, "text": "另一人正文"},
        ]}
        with patch("directory.package_run", return_value=detail), patch("directory.formal_status", return_value=""):
            self.assertTrue(all("message" not in item for item in package_detail(None, params)["rows"]))
            result = package_detail(None, {**params, "item": "usr_1"})["rows"]
        self.assertEqual(len(result), 1)
        self.assertEqual(result[0]["message"], "逐人正文")
        self.assertEqual(result[0]["judgment"]["sections"][0]["lines"], ["原判断"])
        self.assertTrue(result[0]["attachments"][0]["verified"])

    def test_image_preview_is_bound_to_item_and_original_route(self):
        url = "/api/local/v3/personal-report-image?safe_user_ref=usr_1"
        detail = {"digest": "2" * 64, "items": [{"item_key": "usr_1", "attachments": [
            {"type": "image", "preview_url": url}, {"type": "image", "preview_url": "https://outside.example" + url},
        ]}]}
        params = {"digest": detail["digest"], "item": "usr_1", "card": "0"}
        with patch("directory.package_run", return_value=detail):
            with self.assertRaises(ValueError):
                package_card(None, {**params, "item": "usr_2"})
            with self.assertRaises(ValueError):
                package_card(None, {**params, "card": "1"})
            self.assertEqual(package_card(lambda route, q, image: (route, q, image), params),
                             ("personal-report-image", [("safe_user_ref", "usr_1")], True))

    def test_only_the_fixed_edit_route_uses_original_admin_authority(self):
        credentials = "DARU_AUTH_USER=operator\nDARU_AUTH_PASSWORD=demo\nDARU_ADMIN_USER=editor\nDARU_ADMIN_PASSWORD=other"
        with patch("directory.Path.read_text", return_value=credentials), patch("directory.urllib.request.build_opener") as opener:
            opener.return_value.open.return_value.__enter__.return_value.read.return_value = b'{"ok":true}'
            with self.assertRaises(ValueError):
                reader()("copy-package-edit", payload={})
            edit = reader(allow_edit=True)
            edit("copy-package-edit", payload={})
            self.assertEqual(opener.return_value.open.call_args.args[0].get_header("Authorization"), "Basic ZWRpdG9yOm90aGVy")
            edit("copy-package")
            self.assertEqual(opener.return_value.open.call_args.args[0].get_header("Authorization"), "Basic b3BlcmF0b3I6ZGVtbw==")

    def test_original_run_must_belong_to_the_exact_package(self):
        run = "copybatch-" + "1" * 32
        params = {"package": "weekpkg-source", "week": "2026-W46", "run": run}
        row = {"run_id": run, "parent_package_id": "weekpkg-other", "week": "2026-W46", "is_sample": False}
        read = lambda *_args, **_kwargs: {"items": [row]}
        with self.assertRaises(ValueError):
            package_binding(read, params)
        row["parent_package_id"] = params["package"]
        self.assertEqual(package_binding(read, params), run)

    def test_provider_acceptance_without_exact_receipt_is_pending(self):
        self.assertEqual(delivery_label({"provider_delivery_status": 1}, "mobile_pending")[0], "待发送")
        self.assertEqual(delivery_label({"local_execution_state": "final_failed"}, "provider_accepted")[0], "发送失败")
        receipt = {"provider_delivery_status": 1, "query_errcode": 0, "provider_sent_at": "2026-11-10", "msgid_sha256": "proof"}
        self.assertEqual(delivery_label(receipt, "provider_accepted"), ("已发送", "2026-11-10"))

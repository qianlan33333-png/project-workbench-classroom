import unittest
import json
import sqlite3
import tempfile
from datetime import datetime, timedelta
from pathlib import Path
from unittest.mock import patch

from directory import SHANGHAI, bind_daily_drafts, daily_scope, delivery_label, members, newcomer_deadline, package_binding, package_card, package_confirm, package_daily, package_detail, package_edit, package_prepare, package_review_page, profile, profiles, reader


class OriginalDirectoryTest(unittest.TestCase):
    def test_daily_detail_requests_full_original_judgment(self):
        params = {"package": "weekpkg-source", "week": "2026-W46", "run": "copybatch-" + "1"*32}
        detail = {"run_id": params["run"], "digest": "2"*64, "items": [
            {"item_key": "usr_1", "safe_user_ref": "usr_1", "usable": True, "text": "逐人正文", "judgment": {"goal": "原判断"}},
        ]}
        with patch("directory.package_run", return_value=detail) as source, \
             patch("directory.formal_status", return_value=""), patch("directory.candidate_records", return_value=([], "")), \
             patch("directory.daily_scope", return_value=[{"key": "usr_1"}]):
            self.assertNotIn("judgment", package_daily(None, params)["rows"][0])
            self.assertTrue(source.call_args.kwargs["preflight"])
            self.assertIsNotNone(package_daily(None, {**params, "item": "usr_1"})["rows"][0]["judgment"])
            self.assertFalse(source.call_args.kwargs["preflight"])

    def test_review_pages_keep_the_original_item_offsets_when_scope_shrinks(self):
        run = "copybatch-" + "a"*32
        items = [{"item_key": f"item_{i}", "safe_user_ref": f"usr_{i}",
                  "usable": True, "reviewable": True, "text": "完整原稿"} for i in range(40)]
        detail = {"run_id": run, "digest": "b"*64, "items": items}
        candidates = [{"id": item["safe_user_ref"], "nickname": "", "reason": "原策略"} for item in items]
        scope = [{"offset": i, "key": item["item_key"], "slot": "a"*64,
                  "reason": "原策略", "expiresAt": None} for i, item in enumerate(items)]
        params = {"package": "weekpkg-source", "week": "2026-W46", "run": run}
        with patch("directory.package_run", return_value=detail), \
             patch("directory.formal_status", return_value=""), \
             patch("directory.candidate_records", return_value=(candidates, "")), \
             patch("directory.daily_scope", side_effect=[scope, scope[1:]]), \
             patch("directory.review_archive", return_value=("c"*64,)), \
             patch("directory.review_recipients", return_value={}):
            first = package_review_page(None, params)
            second = package_review_page(None, {**params, "offset": str(first["next"])})
        self.assertEqual(first["next"], 20)
        self.assertEqual([row["key"] for row in second["rows"]], [f"item_{i}" for i in range(20, 40)])
        self.assertEqual(second["next"], 0)

    def test_same_business_node_reuses_older_original_draft(self):
        old, new = ("copybatch-" + digit*32 for digit in ("1", "2"))
        records = [{"id": "usr_1", "reason": "当前服务节点"}]
        scope = [{"id": "usr_1", "key": "new", "offset": 0, "reuseRun": old, "reuseItem": "old",
                  "reusePackage": "weekpkg-old", "reuseWeek": "2026-W45", "expiresAt": "2026-11-10T16:00:00+08:00"}]
        old_scope = [{"id": "usr_1", "key": "old", "offset": 1, "expiresAt": "2026-11-10T18:00:00+08:00"}]
        detail = {"items": [{"item_key": "irrelevant"}, {"item_key": "old", "usable": True, "reviewable": True, "text": "原正文"}]}
        with patch("directory.package_run", return_value=detail) as read_old, patch("directory.daily_scope", return_value=old_scope):
            result = bind_daily_drafts(None, {"week": "2026-W46", "package": "weekpkg-new"}, records, new, 1, scope)
            self.assertEqual(read_old.call_args.args[1]["week"], "2026-W45")
            self.assertEqual(read_old.call_args.args[1]["currentWeek"], "2026-W46")
        self.assertEqual(result, {"state": "ready", "run": old, "total": 2,
                                  "package": "weekpkg-old", "week": "2026-W45",
                                  "scope": [{"id": "usr_1", "key": "old", "offset": 1, "existing": True,
                                             "expiresAt": "2026-11-10T16:00:00+08:00"}]})
        with self.assertRaisesRegex(ValueError, "multiple original runs"):
            bind_daily_drafts(None, {}, records, new, 2, [*scope, {"id": "usr_2", "key": "new_2"}])

    def test_newcomer_window_is_inclusive_only_at_its_start(self):
        paid = datetime(2026, 11, 1, 9, tzinfo=SHANGHAI)
        for node, start, end in (("D2", 24, 96), ("D4", 96, 168), ("D7", 168, 192)):
            req = {"sku_pack_binding": {"primary_identity": "299", "paid_at": paid.isoformat(),
                                        "node": node, "skip_newcomer": False}}
            self.assertEqual(newcomer_deadline(req, paid+timedelta(hours=start)), paid+timedelta(hours=end))
            self.assertIsNone(newcomer_deadline(req, paid+timedelta(hours=end)))

    def test_daily_roster_excludes_expired_and_claimed_original_slots(self):
        now = datetime(2026, 11, 10, 9, tzinfo=SHANGHAI)
        run, old = ("copybatch-" + v*32 for v in ("a", "b"))
        params = {"day": "2026-11-10", "strategy": "hxc:onboarding", "run": run}
        items = [{"item_key": f"usr_{i}", "safe_user_ref": f"usr_{i}"} for i in range(3)]
        records = [{"id": item["safe_user_ref"], "reason": "原依据"} for item in items]
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "var").mkdir()
            with sqlite3.connect(root / "var/daru.sqlite") as conn:
                conn.execute("CREATE TABLE copy_batch_items (run_id,item_index,item_key,request_json)")
                conn.execute("CREATE TABLE sku299_generation_slots (slot_key,user_ref,cycle_key,node,run_id,item_key,state,handoff_id,sent_at)")
                for i, item in enumerate(items):
                    binding = {"primary_identity": "299", "node": "D7" if i == 2 else "D2",
                               "paid_at": (now - timedelta(hours=300 if i == 2 else 30)).isoformat(),
                               "skip_newcomer": False, "slot_key": f"slot_{i}", "cycle_key": "cycle"}
                    request = {"safe_user_ref": item["safe_user_ref"], "strategy_key": params["strategy"], "sku_pack_binding": binding}
                    conn.execute("INSERT INTO copy_batch_items VALUES (?,?,?,?)", (run, i, item["item_key"], json.dumps(request)))
                conn.execute("INSERT INTO sku299_generation_slots VALUES (?,?,?,?,?,?,?,?,?)",
                             ("slot_1", "usr_1", "cycle", "D2", old, "usr_1", "draft", None, None))
            def read(path, query, timeout):
                self.assertEqual((path, query["run_id"]), ("copy-package", old))
                return {"package": {"run_id": old, "items": [items[1]], "handoff": {"selected_item_keys": ["usr_1"]}}}

            with patch("directory.ORIGINAL", root), patch("directory.formal_status", return_value=""):
                scope = daily_scope(read, params, records, now=now, detail={"run_id": run, "items": items})
            self.assertEqual([row["key"] for row in scope], ["usr_0"])

    def test_original_confirmation_freezes_selection_and_requires_gate(self):
        run = "copybatch-" + "a"*32
        detail = {"run_id": run, "digest": "b"*64, "items": [
            {"item_key": "usr_1", "usable": True, "text": "正文", "attachments": [{"type": "mini_program"}]},
            {"item_key": "usr_2", "usable": True, "text": "另一条"},
        ]}
        params = {"package": "weekpkg-source", "week": "2026-W46", "run": run, "digest": detail["digest"],
                  "keys": '["usr_1"]', "staff": "123", "transport": "crm-test"}
        calls = []
        def read(path, payload, timeout):
            calls.append((path, payload))
            return {"ok": True, "automatic_send_allowed": False,
                    "review": {"decision": "approved", "selected_item_keys": ["usr_1"]},
                    "handoff": {"digest": detail["digest"], "selected_item_keys": ["usr_1"]}}
        with patch("directory.original_send_binding", return_value=None) as binding, \
             patch("directory.package_run", return_value=detail), patch("directory.formal_status", return_value=""), \
             patch("directory.candidate_records", return_value=([], "")), patch("directory.daily_scope", return_value=[{"key": "usr_1"}]):
            with self.assertRaisesRegex(ValueError, "disabled"):
                package_confirm(read, params)
            binding.return_value = {"staff": "123", "transport": "crm-test"}
            self.assertEqual(package_confirm(read, {**params, "digest": "c"*64}), {"state": "source_changed"})
            self.assertEqual(package_confirm(read, params), {"state": "recorded"})
            with patch("directory.formal_status", return_value="failed"):
                self.assertEqual(package_confirm(read, params), {"state": "needs_check"})
        self.assertEqual(calls, [("copy-package-review", {"run_id": run, "digest": detail["digest"],
            "decision": "approved", "excluded_item_keys": ["usr_2"], "handoff_on_approval": True})])

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

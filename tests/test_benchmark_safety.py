"""Offline regression tests for importing benchmark and paid-API harness modules."""
import builtins
import contextlib
import importlib.util
import io
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "benchmarks"))
sys.path.insert(0, str(ROOT / "scripts"))

ALL_13_MODULES = [
    ROOT / "benchmarks" / "common.py",
    ROOT / "benchmarks" / "2wiki_multihop_eval.py",
    ROOT / "benchmarks" / "agent_eval.py",
    ROOT / "benchmarks" / "cybersecurity_ad_eval.py",
    ROOT / "benchmarks" / "depth_scaling_eval.py",
    ROOT / "benchmarks" / "enterprise_audit_benchmark.py",
    ROOT / "benchmarks" / "metaqa_agent_eval.py",
    ROOT / "benchmarks" / "metaqa_colloquial_eval.py",
    ROOT / "benchmarks" / "vector_graph_vs_plain_rag.py",
    ROOT / "scripts" / "index_metaqa.py",
    ROOT / "scripts" / "setup_datasets.py",
    ROOT / "tests" / "test_mcp_vector_resolution.py",
    ROOT / "tests" / "test_vector_entity_linking.py",
]


def load_module(path):
    spec = importlib.util.spec_from_file_location("safety_" + path.stem, path)
    if spec is None or spec.loader is None:
        raise ImportError(f"Cannot load module from {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class BenchmarkImportSafety(unittest.TestCase):
    def test_all_13_modules_import_cleanly_without_side_effects(self):
        original_open = builtins.open

        def guarded_open(path, *args, **kwargs):
            self.assertNotEqual(Path(path).name, ".env", "import read credentials")
            return original_open(path, *args, **kwargs)

        self.assertEqual(len(ALL_13_MODULES), 13)
        for path in ALL_13_MODULES:
            self.assertTrue(path.exists(), f"Module {path} does not exist")
            with self.subTest(module=path.name), contextlib.ExitStack() as stack:
                stack.enter_context(mock.patch.dict(os.environ,
                    {"GOOGLE_API_KEY": "", "GEMINI_API_KEY": ""}))
                stack.enter_context(mock.patch("builtins.open", side_effect=guarded_open))
                for target in ("os.makedirs", "os.mkdir", "os.remove", "os.unlink",
                               "subprocess.Popen", "urllib.request.urlopen", "sqlite3.connect"):
                    stack.enter_context(mock.patch(target, side_effect=AssertionError(
                        f"import side effect in {path.name}: {target}")))
                if path.name != "common.py":
                    import common
                    stack.enter_context(mock.patch.object(common, "get_api_key",
                        side_effect=AssertionError(f"import in {path.name} looked up an API key")))
                output = io.StringIO()
                stack.enter_context(contextlib.redirect_stdout(output))
                load_module(path)
                self.assertEqual(output.getvalue(), "", f"import {path.name} printed output")

    def test_get_paths_is_a_pure_lookup(self):
        import common
        with mock.patch("os.makedirs", side_effect=AssertionError("created a directory")):
            paths = common.get_paths()
            self.assertEqual(len(paths), 4)
            repo_root, bin_path, data_dir, reports_dir = paths
            self.assertTrue(os.path.isabs(repo_root))

    def test_temp_location_honors_tmpdir(self):
        import common
        with tempfile.TemporaryDirectory() as custom_tmp:
            with mock.patch.dict(os.environ, {"TMPDIR": custom_tmp}):
                temp_dir = common.get_temp_dir()
                self.assertEqual(os.path.abspath(temp_dir), os.path.abspath(custom_tmp))
                temp_db = common.get_temp_db_path(prefix="test_", suffix=".db")
                self.assertTrue(str(temp_db).startswith(str(custom_tmp)))
                self.assertTrue(temp_db.endswith(".db"))

    def test_isolated_db_context_manager(self):
        import common
        with tempfile.TemporaryDirectory() as custom_tmp:
            with mock.patch.dict(os.environ, {"TMPDIR": custom_tmp}):
                # Test fresh isolated db
                db_path_captured = None
                with common.isolated_db(prefix="fresh_") as db_path:
                    db_path_captured = db_path
                    self.assertTrue(os.path.isabs(db_path))
                    self.assertTrue(str(db_path).startswith(str(custom_tmp)))
                    # write to db
                    con = sqlite3.connect(db_path)
                    con.execute("CREATE TABLE t (x INT);")
                    con.execute("INSERT INTO t VALUES (42);")
                    con.commit()
                    con.close()
                    self.assertTrue(os.path.exists(db_path))
                # After context exit, db is cleaned up
                self.assertFalse(os.path.exists(db_path_captured))

                # Test copy_from isolated db
                src_db = os.path.join(custom_tmp, "src.db")
                con = sqlite3.connect(src_db)
                con.execute("CREATE TABLE src_t (y INT);")
                con.execute("INSERT INTO src_t VALUES (100);")
                con.commit()
                con.close()

                with common.isolated_db(prefix="copy_", copy_from=src_db) as copied_db:
                    self.assertTrue(os.path.exists(copied_db))
                    con = sqlite3.connect(copied_db)
                    val = con.execute("SELECT y FROM src_t").fetchone()[0]
                    con.close()
                    self.assertEqual(val, 100)
                self.assertFalse(os.path.exists(copied_db))
                self.assertTrue(os.path.exists(src_db), "source db should remain untouched")

    def test_reap_process_terminate_and_kill(self):
        import common
        # Mock process that terminates cleanly
        mock_proc = mock.MagicMock()
        mock_proc.poll.return_value = None
        mock_proc.wait.return_value = 0
        common.reap_process(mock_proc, timeout=0.1)
        mock_proc.terminate.assert_called_once()
        mock_proc.wait.assert_called_once_with(timeout=0.1)
        mock_proc.kill.assert_not_called()

        # Mock process that times out on terminate and requires kill
        stubborn_proc = mock.MagicMock()
        stubborn_proc.poll.return_value = None
        stubborn_proc.wait.side_effect = [subprocess.TimeoutExpired(cmd="stubborn", timeout=0.1), 0]
        common.reap_process(stubborn_proc, timeout=0.1)
        stubborn_proc.terminate.assert_called_once()
        stubborn_proc.kill.assert_called_once()
        self.assertEqual(stubborn_proc.wait.call_count, 2)

    def test_isolated_copy_includes_committed_wal_data(self):
        import common
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "source.db"
            connection = sqlite3.connect(source)
            self.addCleanup(connection.close)
            try:
                connection.execute("PRAGMA journal_mode=WAL")
                connection.execute("PRAGMA wal_autocheckpoint=0")
                connection.execute("CREATE TABLE facts (value TEXT)")
                connection.execute("INSERT INTO facts VALUES ('committed')")
                connection.commit()
                with common.isolated_db(copy_from=source) as copied:
                    with contextlib.closing(sqlite3.connect(copied)) as reader:
                        try:
                            rows = reader.execute("SELECT value FROM facts").fetchall()
                        except sqlite3.Error as error:
                            self.fail(f"isolated copy lost committed WAL contents: {error}")
                        self.assertEqual(rows, [("committed",)])
            finally:
                connection.close()


if __name__ == "__main__":
    unittest.main()


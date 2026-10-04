"""Safety tests for dataset preparation; all databases are disposable fixtures."""

from contextlib import closing, contextmanager, redirect_stdout
import importlib.util
import inspect
import io
import os
from pathlib import Path
import sqlite3
import sys
import tempfile
import unittest
from unittest import mock


@contextmanager
def chdir(path):
    prev = os.getcwd()
    os.chdir(path)
    try:
        yield
    finally:
        os.chdir(prev)


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(ROOT / "benchmarks"))
sys.path.insert(0, str(ROOT / "scripts"))
SPEC = importlib.util.spec_from_file_location("dataset_index_metaqa", ROOT / "scripts" / "index_metaqa.py")
assert SPEC is not None and SPEC.loader is not None
index = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(index)

KB = b"Example Film|directed_by|Example Director\nExample Film|release_year|2001\n"


def graph_fixture(path):
    with closing(sqlite3.connect(path)) as con, con:
        con.executescript("""
            CREATE TABLE nodes (id TEXT PRIMARY KEY, kind TEXT NOT NULL, properties JSON NOT NULL);
            CREATE TABLE edges (from_id TEXT NOT NULL, to_id TEXT NOT NULL, kind TEXT NOT NULL, properties JSON NOT NULL);
            INSERT INTO nodes VALUES ('movie:Example Film', 'Movie', '{"name":"Example Film"}');
            INSERT INTO nodes VALUES ('person:Example Director', 'Person', '{"name":"Example Director"}');
            INSERT INTO edges VALUES ('movie:Example Film', 'person:Example Director', 'directed_by', '{}');
        """)


class DatasetSetupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir=os.environ.get("TMPDIR"))
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name).resolve()
        self.target = self.directory / "metaqa.db"

    def test_index_refuses_existing_database_before_network_access(self):
        original = b"unrelated database bytes"
        self.target.write_bytes(original)
        with mock.patch.object(index.urllib.request, "urlopen", return_value=io.BytesIO(KB)) as download:
            with self.assertRaises(FileExistsError):
                index.index_metaqa(str(self.target))
        download.assert_not_called()
        self.assertEqual(self.target.read_bytes(), original)

    def test_ensure_preserves_wrong_schema_database_and_raises(self):
        with closing(sqlite3.connect(self.target)) as con, con:
            con.execute("CREATE TABLE wrong (id TEXT);")
        original = self.target.read_bytes()
        with mock.patch.object(index.urllib.request, "urlopen", side_effect=AssertionError("download should not run")):
            with self.assertRaises(ValueError):
                index.ensure_metaqa(str(self.target))
        self.assertEqual(self.target.read_bytes(), original)

    def test_ensure_preserves_corrupt_database_and_raises(self):
        garbage = b"not a sqlite file"
        self.target.write_bytes(garbage)
        with mock.patch.object(index.urllib.request, "urlopen", side_effect=AssertionError("download should not run")):
            with self.assertRaises(ValueError):
                index.ensure_metaqa(str(self.target))
        self.assertEqual(self.target.read_bytes(), garbage)

    def test_index_preserves_target_created_during_build(self):
        real_validate = index.validate_metaqa
        original = b"concurrently created target"

        def create_competing_target(path):
            real_validate(path)
            self.target.write_bytes(original)

        with mock.patch.object(index.urllib.request, "urlopen", return_value=io.BytesIO(KB)):
            with mock.patch.object(index, "validate_metaqa", side_effect=create_competing_target):
                with self.assertRaises(FileExistsError):
                    index.index_metaqa(self.target)
        self.assertEqual(self.target.read_bytes(), original)
        self.assertEqual(list(self.directory.glob(".*.tmp")), [])

    def test_index_builds_in_temporary_sibling_and_installs_atomically(self):
        created_temp_paths = []
        real_connect = sqlite3.connect

        def tracking_connect(database, *args, **kwargs):
            if isinstance(database, (str, Path)) and not str(database).startswith("file:"):
                p = Path(database).resolve()
                if p != self.target.resolve():
                    created_temp_paths.append(p)
            return real_connect(database, *args, **kwargs)

        with mock.patch.object(index.urllib.request, "urlopen", return_value=io.BytesIO(KB)):
            with mock.patch.object(index.sqlite3, "connect", side_effect=tracking_connect):
                index.index_metaqa(str(self.target))

        self.assertTrue(self.target.exists())
        self.assertEqual(len(created_temp_paths), 1)
        temp_path = created_temp_paths[0]
        self.assertEqual(temp_path.resolve().parent, self.target.resolve().parent)
        self.assertFalse(temp_path.exists())
        index.validate_metaqa(str(self.target))

    def test_setup_datasets_passes_overwrite_flag(self):
        spec = importlib.util.spec_from_file_location("dataset_setup_datasets", ROOT / "scripts" / "setup_datasets.py")
        assert spec is not None and spec.loader is not None
        setup_mod = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(setup_mod)

        with mock.patch.object(setup_mod, "ensure_metaqa") as mock_ensure_metaqa:
            with mock.patch("cybersecurity_ad_eval.ensure_ad_dataset", create=True) as mock_ad, \
                 mock.patch("benchmarks.cybersecurity_ad_eval.ensure_ad_dataset", create=True):
                setup_mod.setup_all(overwrite=True)
                mock_ensure_metaqa.assert_called_once()
                self.assertEqual(mock_ensure_metaqa.call_args[1].get("overwrite"), True)

    def test_cli_index_metaqa_overwrite_flag(self):
        self.target.write_bytes(b"initial placeholder db")
        # Running without overwrite fails
        with mock.patch.object(sys, "argv", ["index_metaqa.py", str(self.target)]):
            with self.assertRaises(FileExistsError):
                index.main()

        # Running with --overwrite rebuilds
        with mock.patch.object(sys, "argv", ["index_metaqa.py", str(self.target), "--overwrite"]):
            with mock.patch.object(index.urllib.request, "urlopen", return_value=io.BytesIO(KB)):
                index.main()
        index.validate_metaqa(str(self.target))

    def test_setup_datasets_module_is_import_safe_without_api_key(self):
        # Ensure importing setup_datasets does not perform network calls or require API keys
        with mock.patch.dict(os.environ, {}, clear=True):
            with mock.patch.dict(sys.modules):
                for k in list(sys.modules):
                    if "cybersecurity" in k or "setup_datasets" in k or "common" in k:
                        sys.modules.pop(k, None)
                with mock.patch.object(index.urllib.request, "urlopen", side_effect=AssertionError("import should not access network")):
                    spec = importlib.util.spec_from_file_location("dataset_setup_datasets", ROOT / "scripts" / "setup_datasets.py")
                    assert spec is not None and spec.loader is not None
                    setup_mod = importlib.util.module_from_spec(spec)
                    spec.loader.exec_module(setup_mod)
        self.assertTrue(hasattr(setup_mod, "setup_all"))

    def test_filename_fixture_preserves_callers_working_directory(self):
        sentinel = self.directory / "custom_local_metaqa.db"
        sentinel.write_bytes(b"caller-owned database")
        nested = DatasetSetupTests("test_filename_only_path_resolves_and_builds")
        nested.setUp()
        try:
            with chdir(self.directory):
                nested.test_filename_only_path_resolves_and_builds()
            self.assertEqual(sentinel.read_bytes(), b"caller-owned database")
        finally:
            nested.doCleanups()

    def test_filename_only_path_resolves_and_builds(self):
        cwd_target = "custom_local_metaqa.db"
        with chdir(self.directory):
            cwd_path = Path.cwd() / cwd_target
            with mock.patch.object(index.urllib.request, "urlopen", return_value=io.BytesIO(KB)):
                res = index.index_metaqa(cwd_target)
            self.assertTrue(cwd_path.exists())
            self.assertEqual(Path(res), cwd_path)
            index.validate_metaqa(cwd_target)

    def test_refuse_overwriting_active_wal_database(self):
        wal_file = self.directory / (self.target.name + "-wal")
        wal_file.write_bytes(b"active wal journal contents")
        self.target.write_bytes(b"existing main database")
        with mock.patch.object(index.urllib.request, "urlopen", return_value=io.BytesIO(KB)):
            with self.assertRaises(RuntimeError) as cm:
                index.index_metaqa(str(self.target), overwrite=True)
            self.assertIn("WAL", str(cm.exception))
        self.assertEqual(self.target.read_bytes(), b"existing main database")
        self.assertEqual(wal_file.read_bytes(), b"active wal journal contents")

    def test_overwrite_failure_preserves_original_database_bytes(self):
        original = b"critical original data"
        self.target.write_bytes(original)
        with mock.patch.object(index.urllib.request, "urlopen", side_effect=RuntimeError("network drop")):
            with self.assertRaises(RuntimeError):
                index.index_metaqa(str(self.target), overwrite=True)
        self.assertEqual(self.target.read_bytes(), original)

    def test_ensure_metaqa_signature_accepts_keyword_only_overwrite(self):
        sig = inspect.signature(index.ensure_metaqa)
        params = list(sig.parameters.values())
        self.assertEqual(params[0].name, "target_db")
        self.assertIn(params[0].kind, (inspect.Parameter.POSITIONAL_OR_KEYWORD, inspect.Parameter.POSITIONAL_ONLY))
        self.assertEqual(params[1].name, "overwrite")
        self.assertEqual(params[1].kind, inspect.Parameter.KEYWORD_ONLY)
        self.assertIs(params[1].default, False)

    def test_index_metaqa_signature_accepts_keyword_only_overwrite(self):
        sig = inspect.signature(index.index_metaqa)
        params = list(sig.parameters.values())
        self.assertEqual(params[0].name, "target_db")
        self.assertEqual(params[1].name, "overwrite")
        self.assertEqual(params[1].kind, inspect.Parameter.KEYWORD_ONLY)
        self.assertIs(params[1].default, False)

    def test_ensure_reuses_valid_small_graph_without_download(self):
        graph_fixture(self.target)
        before = self.target.read_bytes()
        with mock.patch.object(index.urllib.request, "urlopen", side_effect=AssertionError("unexpected download")):
            self.assertEqual(index.ensure_metaqa(str(self.target)), str(self.target))
        self.assertEqual(self.target.read_bytes(), before)


if __name__ == "__main__":
    unittest.main()

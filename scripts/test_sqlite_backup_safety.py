"""Offline safety checks for consistent SQLite backups, including WAL."""
from contextlib import closing
from pathlib import Path
import sqlite3
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(ROOT / "scripts"))

from scripts.backup_sqlite import backup_database


class SQLiteBackupSafetyTests(unittest.TestCase):
    def backup_function(self):
        script = ROOT / "scripts" / "backup_sqlite.py"
        self.assertTrue(script.is_file(), "consistent backup/restore command is missing")
        return backup_database

    def test_backup_and_restore_include_committed_wal_data(self):
        backup = self.backup_function()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "live.db"
            snapshot = root / "backup.db"
            restored = root / "restored.db"
            with closing(sqlite3.connect(source)) as connection:
                connection.execute("PRAGMA journal_mode=WAL")
                connection.execute("PRAGMA wal_autocheckpoint=0")
                connection.execute("CREATE TABLE facts (id INTEGER PRIMARY KEY, value TEXT)")
                connection.execute("INSERT INTO facts VALUES (1, 'committed in WAL')")
                connection.commit()
                self.assertTrue(Path(str(source) + "-wal").is_file())
                backup(source, snapshot)
                backup(snapshot, restored)
                with closing(sqlite3.connect(restored)) as restored_connection:
                    row = restored_connection.execute("SELECT value FROM facts WHERE id=1").fetchone()
                    self.assertEqual(row, ("committed in WAL",))
                    self.assertEqual(restored_connection.execute("PRAGMA integrity_check").fetchone(), ("ok",))
                    self.assertEqual(restored_connection.execute("PRAGMA foreign_key_check").fetchall(), [])

    def test_refuses_target_with_existing_wal_sidecar(self):
        backup = self.backup_function()
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "source.db"
            target = Path(directory) / "target.db"
            sidecar = Path(str(target) + "-wal")
            with closing(sqlite3.connect(source)) as connection:
                connection.execute("CREATE TABLE facts (id INTEGER)")
                connection.commit()
            sidecar.write_bytes(b"existing WAL must remain untouched")
            with self.assertRaises(FileExistsError):
                backup(source, target)
            self.assertFalse(target.exists())
            self.assertEqual(sidecar.read_bytes(), b"existing WAL must remain untouched")


if __name__ == "__main__":
    unittest.main()

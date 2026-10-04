#!/usr/bin/env python3
"""Create a consistent SQLite snapshot, or restore it to a new file.

The SQLite backup API includes committed WAL contents. Existing targets are
never overwritten. Restore to a new path, verify it, then switch the application
while it is stopped; do not replace a live database or copy its .db file alone.
"""
import argparse
from contextlib import closing
import os
from pathlib import Path
import sqlite3
import tempfile
import time


def backup_database(source, target, *, timeout=30):
    """Copy a consistent, verified snapshot to a previously nonexistent target."""
    source = Path(source).resolve(strict=True)
    target = Path(target).resolve()
    if source == target:
        raise ValueError("source and target must differ")
    if target.exists():
        raise FileExistsError(f"target already exists: {target}")
    for suffix in ("-wal", "-shm", "-journal"):
        sidecar = Path(str(target) + suffix)
        if sidecar.exists():
            raise FileExistsError(f"target has an existing SQLite sidecar: {sidecar}")
    target.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary_name = tempfile.mkstemp(prefix=".sqlite-backup-", suffix=".db", dir=target.parent)
    os.close(descriptor)
    temporary = Path(temporary_name)
    deadline = time.monotonic() + timeout

    def check_deadline(status, remaining, total):
        if time.monotonic() > deadline:
            raise TimeoutError("SQLite backup exceeded its timeout")

    try:
        with closing(sqlite3.connect(source.as_uri() + "?mode=ro", uri=True, timeout=timeout)) as reader:
            with closing(sqlite3.connect(temporary, timeout=timeout)) as writer:
                reader.backup(writer, pages=256, progress=check_deadline, sleep=0.01)
                if writer.execute("PRAGMA integrity_check").fetchall() != [("ok",)]:
                    raise ValueError("backup failed SQLite integrity check")
                if writer.execute("PRAGMA foreign_key_check").fetchone() is not None:
                    raise ValueError("backup contains foreign key violations")
        with temporary.open("r+b") as snapshot:
            os.fsync(snapshot.fileno())
        # Atomic create-only publication: a concurrent target must not be clobbered.
        os.link(temporary, target)
    finally:
        temporary.unlink(missing_ok=True)
    return target


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", help="source SQLite database, including live WAL databases")
    parser.add_argument("target", help="new snapshot or restored database; must not exist")
    arguments = parser.parse_args(argv)
    try:
        target = backup_database(arguments.source, arguments.target)
    except (OSError, ValueError, sqlite3.Error) as error:
        parser.exit(1, f"Backup failed: {error}\n")
    print(f"Verified SQLite snapshot: {target}")


if __name__ == "__main__":
    main()

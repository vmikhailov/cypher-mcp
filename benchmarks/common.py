from contextlib import closing, contextmanager
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import time

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8")
if hasattr(sys.stderr, "reconfigure"):
    sys.stderr.reconfigure(encoding="utf-8")

def get_api_key():
    """Retrieve Google API key from environment variable or standard local .env files."""
    key = os.environ.get("GOOGLE_API_KEY") or os.environ.get("GEMINI_API_KEY")
    if key:
        return key
    candidate_paths = [
        os.environ.get("ENV_PATH"),
        os.path.join(os.getcwd(), ".env"),
        os.path.join(os.path.dirname(__file__), "..", ".env"),
        os.path.join(os.path.dirname(__file__), ".env"),
        os.path.expanduser("~/.env"),
    ]
    for p in candidate_paths:
        if p and os.path.exists(p):
            try:
                with open(p, "r", encoding="utf-8") as f:
                    for line in f:
                        line = line.strip()
                        if line.startswith("GOOGLE_API_KEY="):
                            return line.split("=", 1)[1].strip().strip("\"'")
                        if line.startswith("GEMINI_API_KEY="):
                            return line.split("=", 1)[1].strip().strip("\"'")
            except Exception:
                pass
    return None

def get_paths():
    """Retrieve canonical project paths dynamically from environment or script location."""
    repo_root = os.environ.get("CYPHER_MCP_ROOT") or os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
    bin_name = "cypher-mcp.exe" if os.name == "nt" else "cypher-mcp"
    default_bin = os.path.join(repo_root, "bin", bin_name)
    if not os.path.exists(default_bin) and os.path.exists(os.path.join(repo_root, bin_name)):
        default_bin = os.path.join(repo_root, bin_name)
    bin_path = os.environ.get("CYPHER_MCP_BIN") or default_bin
    data_dir = os.environ.get("CYPHER_MCP_DATA") or os.path.join(repo_root, "data")
    reports_dir = os.environ.get("CYPHER_MCP_REPORTS") or os.path.join(repo_root, "benchmarks", "reports")
    return repo_root, bin_path, data_dir, reports_dir

def get_temp_dir():
    """Get scratch / temporary directory honoring TMPDIR."""
    tmp_env = os.environ.get("TMPDIR")
    if tmp_env:
        os.makedirs(tmp_env, exist_ok=True)
        return os.path.abspath(tmp_env)
    return os.path.abspath(tempfile.gettempdir())

def get_temp_db_path(prefix="bench_", suffix=".db"):
    """Generate a unique disposable database file path within the temp directory."""
    temp_dir = get_temp_dir()
    fd, path = tempfile.mkstemp(prefix=prefix, suffix=suffix, dir=temp_dir)
    os.close(fd)
    if os.path.exists(path):
        os.remove(path)
    return path

@contextmanager
def isolated_db(prefix="bench_", suffix=".db", copy_from=None):
    """Provide a disposable database; snapshot SQLite sources including WAL."""
    with tempfile.TemporaryDirectory(prefix=prefix, dir=get_temp_dir()) as directory:
        path = str(Path(directory) / ("graph" + suffix))
        if copy_from is not None:
            source = Path(copy_from).resolve(strict=True)
            deadline = time.monotonic() + 30

            def check_deadline(status, remaining, total):
                if time.monotonic() > deadline:
                    raise TimeoutError("dataset snapshot exceeded its timeout")

            with closing(sqlite3.connect(source.as_uri() + "?mode=ro", uri=True)) as reader:
                with closing(sqlite3.connect(path)) as writer:
                    reader.backup(writer, pages=256, progress=check_deadline, sleep=0.01)
        yield path

def reap_process(proc, timeout=5.0):
    """Safely terminate child process with timeout and fallback kill."""
    if proc is None:
        return
    try:
        if proc.poll() is not None:
            return
    except Exception:
        pass
    try:
        proc.terminate()
        try:
            proc.wait(timeout=timeout)
        except (subprocess.TimeoutExpired, TimeoutError):
            proc.kill()
            proc.wait(timeout=timeout)
    except Exception:
        pass


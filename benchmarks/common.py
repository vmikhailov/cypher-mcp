import os
import sys

def get_api_key():
    """Retrieve Google API key from environment variable or standard local env files."""
    key = os.environ.get("GOOGLE_API_KEY") or os.environ.get("GEMINI_API_KEY")
    if key:
        return key
    candidate_paths = [
        os.environ.get("ENV_PATH"),
        os.environ.get("HERMES_ENV_PATH"),
        os.path.join(os.getcwd(), ".env"),
        os.path.join(os.path.dirname(__file__), "..", ".env"),
        os.path.join(os.path.dirname(__file__), ".env"),
        os.path.expanduser("~/.env"),
    ]
    localapp = os.environ.get("LOCALAPPDATA")
    if localapp:
        candidate_paths.append(os.path.join(localapp, "hermes", ".env"))
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
    bin_path = os.environ.get("CYPHER_MCP_BIN") or os.path.join(repo_root, "bin", bin_name)
    data_dir = os.environ.get("CYPHER_MCP_DATA") or os.path.join(repo_root, "data")
    reports_dir = os.environ.get("CYPHER_MCP_REPORTS") or os.path.join(repo_root, "benchmarks", "reports")
    os.makedirs(reports_dir, exist_ok=True)
    return repo_root, bin_path, data_dir, reports_dir

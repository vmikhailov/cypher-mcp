#!/usr/bin/env python3
"""
One-click dataset setup for cypher-mcp benchmarks.
Downloads and indexes external datasets (MetaQA, Active Directory) into data/.
"""

import os
import sys

sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "benchmarks"))

from common import get_paths
from index_metaqa import ensure_metaqa

def setup_all(*, overwrite=False):
    repo_root, bin_path, data_dir, reports_dir = get_paths()
    print(f"=== Setting up datasets in {data_dir} ===")
    
    print("\n[1/2] Ensuring MetaQA graph database...")
    metaqa_db = os.path.join(data_dir, "metaqa.db")
    ensure_metaqa(metaqa_db, overwrite=overwrite)

    print("\n[2/2] Ensuring Cybersecurity Active Directory dataset...")
    try:
        from cybersecurity_ad_eval import ensure_ad_dataset
        ensure_ad_dataset()
    except Exception as exc:
        print(f"Cybersecurity Active Directory dataset setup notice: {exc}")

    print("\nAll benchmark datasets are ready!")

def main(argv=None):
    import argparse
    parser = argparse.ArgumentParser(description="One-click dataset setup for cypher-mcp benchmarks.")
    parser.add_argument("--overwrite", action="store_true", help="Overwrite existing dataset databases")
    args = parser.parse_args(argv)
    setup_all(overwrite=args.overwrite)

if __name__ == "__main__":
    main()

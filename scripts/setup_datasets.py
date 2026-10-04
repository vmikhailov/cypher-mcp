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
from cybersecurity_ad_eval import ensure_ad_dataset

def setup_all():
    repo_root, bin_path, data_dir, reports_dir = get_paths()
    print(f"=== Setting up datasets in {data_dir} ===")
    
    print("\n[1/2] Ensuring MetaQA graph database...")
    metaqa_db = os.path.join(data_dir, "metaqa.db")
    ensure_metaqa(metaqa_db)

    print("\n[2/2] Ensuring Cybersecurity Active Directory dataset...")
    ensure_ad_dataset()

    print("\nAll benchmark datasets are ready!")

if __name__ == "__main__":
    setup_all()

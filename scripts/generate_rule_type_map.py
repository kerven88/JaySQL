#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
以 MySQL/Postgres 开发规范 md 文件为唯一数据源，
生成前端规则类型映射 JSON：rule_name -> rule_type（原样保留 md 取值）。
"""

import json
import re
import sys
from pathlib import Path


def parse_md(path, section_map):
    """解析 md，返回 [(category, name, rule_type), ...]"""
    rules = []
    current_cat = None
    with open(path, "r", encoding="utf-8") as f:
        for line in f:
            s = line.strip()
            if s in section_map:
                current_cat = section_map[s]
                continue
            if current_cat and line.startswith("|") and "规则名称" not in line and "---" not in line:
                parts = [p.strip() for p in line.split("|")]
                parts = [p for p in parts if p]
                if len(parts) >= 4:
                    rules.append((current_cat, parts[1], parts[2]))
    return rules


def main():
    if len(sys.argv) < 4:
        print("Usage: generate_rule_type_map.py <mysql.md> <postgres.md> <output.json>")
        sys.exit(1)

    mysql_path = Path(sys.argv[1])
    pg_path = Path(sys.argv[2])
    out_path = Path(sys.argv[3])

    mysql_rules = parse_md(
        mysql_path,
        {"### 表结构": "structure", "### 查询和更新": "query_update"},
    )
    pg_rules = parse_md(
        pg_path,
        {"### 表结构": "structure", "### SQL 窗口": "query_update"},
    )

    # 以规则名称为 key；同名规则在 MySQL/Postgres 中的规则类型一致（脚本会校验）
    rule_type_map = {}
    for cat, name, rt in mysql_rules + pg_rules:
        if name in rule_type_map and rule_type_map[name] != rt:
            print(f"WARN: 同名规则类型不一致 [{name}] mysql={rule_type_map[name]} postgres={rt}")
        rule_type_map[name] = rt

    out_path.parent.mkdir(parents=True, exist_ok=True)
    with open(out_path, "w", encoding="utf-8") as f:
        json.dump(rule_type_map, f, ensure_ascii=False, indent=2)

    print(f"generated {out_path} with {len(rule_type_map)} entries")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
clean_har.py — 从 HAR 文件中删除指定域名（含其子域名）的请求记录。

用法：
  python clean_har.py input.har
  python clean_har.py input.har -o clean.har
  python clean_har.py input.har -d facebook.com -d cdn.example.com
  python clean_har.py input.har --blocklist-file domains.txt
  python clean_har.py input.har --no-default-blocklist -d example.com
  python clean_har.py input.har --dry-run          # 只统计，不写文件
"""

import argparse
import json
import sys
from collections import Counter
from pathlib import Path
from urllib.parse import urlparse

# 默认黑名单：Google / Bing / 微软系。按需增删，或用 --no-default-blocklist 关闭。
DEFAULT_BLOCKLIST = {
    # Google
    "google.com", "google.cn", "googleapis.com", "gstatic.com",
    "googleusercontent.com", "googletagmanager.com", "googlesyndication.com",
    "google-analytics.com", "googleadservices.com", "doubleclick.net",
    "gvt1.com", "gvt2.com",
    # Bing / Microsoft
    "bing.com", "bing.net", "msn.com", "live.com",
    "microsoft.com", "microsoftonline.com", "office.com", "office.net",
    "clarity.ms", "msedge.net", "windowsupdate.com",
}


def normalize_domain(value: str) -> str:
    """把各种写法统一成纯域名：'https://*.Google.com:443/x' -> 'google.com'"""
    v = (value or "").strip().lower()
    if not v:
        return ""
    if "://" in v:
        try:
            v = urlparse(v).hostname or v
        except ValueError:
            pass
    v = v.split("/")[0].strip()
    if v.startswith("*."):
        v = v[2:]
    if not v.startswith("[") and ":" in v:  # 去端口（IPv6 保留）
        v = v.split(":")[0]
    return v.strip(".")


def host_matches(host: str, domain: str) -> bool:
    """host 等于 domain 或为其子域名（google.com 匹配 www.google.com，不匹配 google.com.cn）"""
    host = (host or "").lower().strip(".")
    domain = (domain or "").lower().strip(".")
    if not host or not domain:
        return False
    return host == domain or host.endswith("." + domain)


def entry_hosts(entry: dict) -> set:
    """取出一个 HAR entry 涉及的所有 hostname（URL + Host 头）。"""
    hosts = set()
    request = entry.get("request") or {}

    url = request.get("url") or ""
    if url.startswith("//"):
        url = "http:" + url
    if url:
        try:
            h = urlparse(url).hostname
            if h:
                hosts.add(h.lower())
        except ValueError:
            pass

    for header in request.get("headers") or []:
        if (header.get("name") or "").lower() == "host":
            raw = (header.get("value") or "").strip()
            if not raw:
                continue
            h = raw.split("//")[-1].split("/")[0]
            if h.startswith("["):  # IPv6，直接跳过
                continue
            h = h.split(":")[0]
            if h:
                hosts.add(h.lower())
    return hosts


def read_domains(path: str) -> set:
    domains = set()
    with open(path, "r", encoding="utf-8-sig") as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            d = normalize_domain(line)
            if d:
                domains.add(d)
    return domains


def default_output(input_path: str) -> str:
    p = Path(input_path)
    return str(p.with_name(f"{p.stem}.clean{p.suffix}"))


def main() -> int:
    parser = argparse.ArgumentParser(description="删除 HAR 中指定域名的请求记录")
    parser.add_argument("input", help="输入 HAR 文件")
    parser.add_argument("-o", "--output", help="输出 HAR 文件（默认 输入名.clean.har）")
    parser.add_argument("-d", "--drop", action="append", default=[], metavar="DOMAIN",
                        help="要删除的域名，可重复；自动包含其子域名")
    parser.add_argument("--blocklist-file", metavar="FILE",
                        help="域名列表文件，每行一个，# 开头为注释")
    parser.add_argument("--keep", action="append", default=[], metavar="DOMAIN",
                        help="白名单，命中者即使匹配黑名单也保留")
    parser.add_argument("--no-default-blocklist", action="store_true",
                        help="不使用内置的 Google/Bing 默认黑名单")
    parser.add_argument("--dry-run", action="store_true", help="只统计，不写输出文件")
    parser.add_argument("--compact", action="store_true", help="输出紧凑 JSON（不缩进）")
    parser.add_argument("-q", "--quiet", action="store_true")
    args = parser.parse_args()

    blocklist = set()
    if not args.no_default_blocklist:
        blocklist |= DEFAULT_BLOCKLIST
    blocklist |= {normalize_domain(d) for d in args.drop}
    if args.blocklist_file:
        blocklist |= read_domains(args.blocklist_file)
    blocklist = {d for d in blocklist if d}
    keep = {normalize_domain(d) for d in args.keep if d}

    if not blocklist:
        print("错误：黑名单为空，没有要删除的域名。", file=sys.stderr)
        return 1

    with open(args.input, "r", encoding="utf-8-sig") as f:
        har = json.load(f)

    log = har.get("log")
    if not isinstance(log, dict):
        print("错误：不是有效的 HAR 文件（缺少 log 对象）。", file=sys.stderr)
        return 2

    entries = log.get("entries") or []
    kept, removed = [], []
    removed_counter = Counter()

    for entry in entries:
        hosts = entry_hosts(entry)
        if not hosts:
            kept.append(entry)
            continue
        matched = [d for d in blocklist if any(host_matches(h, d) for h in hosts)]
        if not matched:
            kept.append(entry)
            continue
        if keep and any(host_matches(h, d) for h in hosts for d in keep):
            kept.append(entry)
            continue
        removed.append(entry)
        removed_counter.update(matched)

    log["entries"] = kept

    if not args.quiet:
        print(f"总条目: {len(entries)}  删除: {len(removed)}  保留: {len(kept)}")
        if removed_counter:
            print("按域名统计（一条请求可能命中多个域名，总和可能大于删除条数）：")
            for d, c in removed_counter.most_common():
                print(f"  {d}: {c}")

    if args.dry_run:
        print("--dry-run：未写出文件。")
        return 0

    output = args.output or default_output(args.input)
    with open(output, "w", encoding="utf-8") as f:
        if args.compact:
            json.dump(har, f, ensure_ascii=False, separators=(",", ":"))
        else:
            json.dump(har, f, ensure_ascii=False, indent=2)
    print(f"已写出: {output}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""从正方教务 HAR 生成 jwmock 配置。

数据来源（与 protocol/CourseMerge.go 的两个接口对应）：
  * zzxkyzb_cxZzxkYzbPartDisplay —— 课程列表（响应的 tmpList），教学班全集；
  * zzxkyzb_cxJxbWithKchZzxkYzb   —— 课程详情（JSON 数组），补充容量/教师/时间地点。

合并规则与 protocol/CourseMerge.go 一致：
  1. 列表按 jxb_id 去重：后出现的覆盖先出现的，输出顺序取首次出现的位置；
  2. 详情只更新列表中已存在的教学班（同 jxb_id 后出现覆盖先出现）；
     列表里没有的多余详情丢弃（和 MergeCourseDetail 一样，详情不是全集）；
  3. 字段保持教务原始格式：sksj 保留 <br/>、jsxx 保留“工号/姓名/职称”，
     不做 CourseMerge 里 <br/>→“,”、去掉“工号/”、去掉“校”等展示性转换，
     因为 engine 的 parseSksj/processLine 需要原始格式才能正确解析。

用法示例：
  python jwmock/tools/har2config.py
  python jwmock/tools/har2config.py --har "jwmock/real_data/096_xxx.har" --out jwmock/config.json
  python jwmock/tools/har2config.py --har a.har --har b.har --report jwmock/real_data/_work/har2config.txt
"""

from __future__ import annotations

import argparse
import base64
import json
import re
import sys
from pathlib import Path
from urllib.parse import parse_qsl

LIST_ENDPOINT = "zzxkyzb_cxZzxkYzbPartDisplay"
DETAIL_ENDPOINT = "zzxkyzb_cxJxbWithKchZzxkYzb"
ENDPOINTS = (LIST_ENDPOINT, DETAIL_ENDPOINT)

# 页签字段 ← 请求表单字段。xklc 只出现在详情请求表单里（列表请求不带）。
TAB_FORM_FIELDS = (
    ("kklxdm", "kklxdm"),
    ("xkkz_id", "xkkzId"),
    ("njdm_id", "njdmId"),
    ("zyh_id", "zyhId"),
)
DETAIL_FORM_FIELDS = (("xklc", "xklc"),)

ASCII_LETTERS = re.compile(r"[A-Za-z]")


class Report:
    def __init__(self) -> None:
        self.lines: list[str] = []
        self.warnings: list[str] = []

    def line(self, text: str = "") -> None:
        self.lines.append(text)

    def warn(self, text: str) -> None:
        self.warnings.append(text)

    def text(self) -> str:
        out = list(self.lines)
        if self.warnings:
            out.append("")
            out.append("警告:")
            out.extend("- " + w for w in self.warnings)
        else:
            out.append("")
            out.append("没有警告。")
        return "\n".join(out) + "\n"


def load_har(path: Path) -> dict:
    """读取 HAR（兼容 UTF-8 BOM）。"""
    raw = path.read_bytes()
    return json.loads(raw.decode("utf-8-sig", errors="replace"))


def endpoint_entries(har: dict, endpoint: str) -> list[dict]:
    entries = (har.get("log") or {}).get("entries") or []
    return [e for e in entries if endpoint in str((e.get("request") or {}).get("url") or "")]


def response_text(entry: dict) -> str:
    content = (entry.get("response") or {}).get("content") or {}
    text = content.get("text") or ""
    if content.get("encoding") == "base64":
        text = base64.b64decode(text).decode("utf-8", errors="replace")
    return text


def request_form(entry: dict) -> dict[str, str]:
    request = entry.get("request") or {}
    post = request.get("postData") or {}
    form: dict[str, str] = {}
    for param in post.get("params") or []:
        name = param.get("name")
        if name:
            form[str(name)] = str(param.get("value") or "")
    if not form and post.get("text"):
        form = dict(parse_qsl(post["text"], keep_blank_values=True))
    return form


def as_json(entry: dict):
    """解析响应体 JSON；非 JSON 返回 None。"""
    try:
        return json.loads(response_text(entry))
    except ValueError:
        return None


def clean(value) -> str:
    return str(value).strip() if value is not None else ""


def to_int(value) -> int | None:
    text = clean(value)
    if text == "":
        return None
    try:
        return int(float(text))
    except ValueError:
        return None


def load_rows(path: Path, report: Report) -> tuple[dict, dict]:
    """读一个 HAR，返回 (列表行, 详情行) 及统计信息。"""
    har = load_har(path)
    stats = {
        "path": path,
        "list_entries": 0,
        "list_ok": 0,
        "list_skipped": 0,
        "list_rows": 0,
        "list_forms": [],
        "detail_entries": 0,
        "detail_ok": 0,
        "detail_skipped": 0,
        "detail_rows": 0,
        "detail_forms": [],
        "translated_details": 0,
    }

    for entry in endpoint_entries(har, LIST_ENDPOINT):
        stats["list_entries"] += 1
        data = as_json(entry)
        rows = data.get("tmpList") if isinstance(data, dict) else None
        if isinstance(rows, list):
            stats["list_ok"] += 1
            stats["list_rows"] += len(rows)
            stats["list_forms"].append(request_form(entry))
        else:
            stats["list_skipped"] += 1

    for entry in endpoint_entries(har, DETAIL_ENDPOINT):
        stats["detail_entries"] += 1
        data = as_json(entry)
        if isinstance(data, list):
            stats["detail_ok"] += 1
            stats["detail_rows"] += len(data)
            stats["detail_forms"].append(request_form(entry))
            for row in data:
                if isinstance(row, dict) and ASCII_LETTERS.search(clean(row.get("sksj"))):
                    stats["translated_details"] += 1
        else:
            stats["detail_skipped"] += 1

    return har, stats


def merge_rows(
    merged_lists: dict[str, dict],
    order: list[str],
    detail_map: dict[str, dict],
    path: Path,
    report: Report,
) -> dict:
    """把单个 HAR 的列表/详情合并进全局结果（同 CourseMerge 语义）。"""
    har, st = load_rows(path, report)
    report.line(f"HAR: {path.name}")
    report.line(
        "  列表: 响应 {entries} 次（有效 {ok} / 跳过 {skipped}），原始 {rows} 行".format(
            entries=st["list_entries"], ok=st["list_ok"], skipped=st["list_skipped"], rows=st["list_rows"]
        )
    )
    report.line(
        "  详情: 响应 {entries} 次（有效 {ok} / 跳过 {skipped}），{rows} 行".format(
            entries=st["detail_entries"], ok=st["detail_ok"], skipped=st["detail_skipped"], rows=st["detail_rows"]
        )
    )

    if st["list_entries"] == 0:
        report.warn(f"{path.name}: 没找到列表接口 {LIST_ENDPOINT} 的请求")
    if st["detail_entries"] == 0:
        report.warn(f"{path.name}: 没找到详情接口 {DETAIL_ENDPOINT} 的请求")

    # 列表：按 jxb_id 去重，后出现覆盖先出现，顺序取首次出现。
    new_jxb = 0
    dup_rows = 0
    blank_jxb = 0
    file_list_jxbs: list[str] = []
    for entry in endpoint_entries(har, LIST_ENDPOINT):
        data = as_json(entry)
        rows = data.get("tmpList") if isinstance(data, dict) else None
        if not isinstance(rows, list):
            continue
        form = request_form(entry)
        for row in rows:
            if not isinstance(row, dict):
                continue
            jxb = clean(row.get("jxb_id"))
            if not jxb:
                blank_jxb += 1
                continue
            if jxb in merged_lists:
                dup_rows += 1
            else:
                order.append(jxb)
                new_jxb += 1
            if jxb not in file_list_jxbs:
                file_list_jxbs.append(jxb)
            merged_lists[jxb] = dict(row, _form=form)

    st["dup_rows"] = dup_rows
    st["blank_jxb"] = blank_jxb
    st["new_jxb"] = new_jxb
    report.line(f"  去重: 新增 {new_jxb} 门，重复覆盖 {dup_rows} 行，缺 jxb_id {blank_jxb} 行")

    # 详情：只更新列表里已有的教学班，后出现覆盖先出现。
    detail_extra_ids: list[str] = []
    detail_invalid = 0
    file_detail_jxbs: set[str] = set()
    for entry in endpoint_entries(har, DETAIL_ENDPOINT):
        data = as_json(entry)
        if not isinstance(data, list):
            continue
        form = request_form(entry)
        for row in data:
            if not isinstance(row, dict):
                detail_invalid += 1
                continue
            jxb = clean(row.get("jxb_id"))
            if not jxb:
                detail_invalid += 1
                continue
            file_detail_jxbs.add(jxb)
            if jxb in merged_lists:
                detail_map[jxb] = dict(row, _form=form)
            elif jxb not in detail_extra_ids:
                detail_extra_ids.append(jxb)

    matched_detail_jxbs = [jxb for jxb in file_detail_jxbs if jxb in merged_lists]
    missing_detail_ids = [jxb for jxb in file_list_jxbs if jxb not in file_detail_jxbs]
    st["detail_hit"] = len(matched_detail_jxbs)
    st["detail_missing"] = missing_detail_ids
    st["detail_extra"] = detail_extra_ids
    st["detail_invalid"] = detail_invalid
    report.line(
        "  详情去重: 唯一教学班 {unique} 个（详情行 {rows}），命中列表 {hit} 个；列表缺详情 {miss} 门；列表外多余详情 {extra} 个；非法详情行 {bad} 行".format(
            unique=len(file_detail_jxbs),
            rows=st["detail_rows"],
            hit=len(matched_detail_jxbs),
            miss=len(missing_detail_ids),
            extra=len(detail_extra_ids),
            bad=detail_invalid,
        )
    )
    if missing_detail_ids:
        report.warn(
            f"{path.name}: 列表中有 {len(missing_detail_ids)} 门课没有详情（详情请求可能漏抓），按列表字段生成: "
            + ", ".join(missing_detail_ids)
        )
    if detail_extra_ids:
        report.warn(
            f"{path.name}: 详情里有 {len(detail_extra_ids)} 个教学班不在列表中，按 CourseMerge 语义丢弃: "
            + ", ".join(detail_extra_ids)
        )
    if detail_invalid:
        report.warn(f"{path.name}: 跳过 {detail_invalid} 行缺少 jxb_id 的详情")

    # 参数一致性提醒。
    for form_list, label in ((st["list_forms"], "列表"), (st["detail_forms"], "详情")):
        for form_key, _tab_key in TAB_FORM_FIELDS + DETAIL_FORM_FIELDS:
            values = [clean(f.get(form_key)) for f in form_list if clean(f.get(form_key))]
            if len(set(values)) > 1:
                report.warn(f"{path.name}: {label}请求的 {form_key} 有多个取值: {sorted(set(values))}")

    # 浏览器翻译检测（093 那种详情被译成英文的 HAR 不适合当样本）。
    if st["detail_rows"] and st["translated_details"] > st["detail_rows"] * 0.3:
        report.warn(
            f"{path.name}: 详情疑似被浏览器自动翻译（{st['translated_details']}/{st['detail_rows']} 行 sksj 含 ASCII 字母），"
            "建议关闭网页翻译后重新抓包"
        )
    report.line()

    return st


def pick_tab_value(forms: list[dict], key: str) -> str:
    for form in forms:
        value = clean(form.get(key))
        if value:
            return value
    return ""


def distinct_form_values(forms: list[dict], key: str) -> list[str]:
    seen: list[str] = []
    for form in forms:
        value = clean(form.get(key))
        if value and value not in seen:
            seen.append(value)
    return seen


def build_courses(
    merged_lists: dict[str, dict],
    order: list[str],
    detail_map: dict[str, dict],
    report: Report,
) -> list[dict]:
    courses: list[dict] = []
    capacity_missing: list[str] = []
    kclxmc_fallback = 0
    enrolled_over_capacity = 0
    missing_fields: dict[str, int] = {}

    for jxb in order:
        row = merged_lists[jxb]
        detail = detail_map.get(jxb) or {}

        def field(*sources: str) -> str:
            for source in sources:
                value = clean(source)
                if value:
                    return value
            return ""

        course = {
            "jxbId": jxb,
            "jxbMc": field(row.get("jxbmc")),
            "kchId": field(row.get("kch_id")),
            "kch": field(row.get("kch")),
            "kcmc": field(row.get("kcmc")),
            "kklxdm": field(row.get("kklxdm")),
            "kclxmc": field(row.get("kclxmc"), detail.get("kclbmc")),
            "kzmc": field(row.get("kzmc")),
            "xf": field(row.get("xf"), detail.get("xf")),
            "sksj": field(detail.get("sksj")),
            "jxdd": field(detail.get("jxdd")),
            "jsxx": field(detail.get("jsxx")),
        }
        # 空字符串不输出，让 jwmock 用自身默认值。
        course = {key: value for key, value in course.items() if value != ""}

        capacity = to_int(detail.get("jxbrl"))
        if capacity is None or capacity <= 0:
            capacity = 0
            capacity_missing.append(jxb)
        enrolled = to_int(row.get("yxzrs")) or 0
        if enrolled < 0:
            enrolled = 0
        if capacity > 0 and enrolled > capacity:
            enrolled = capacity
            enrolled_over_capacity += 1
        course["capacity"] = capacity
        course["enrolled"] = enrolled

        if clean(row.get("kclxmc")) == "" and clean(detail.get("kclbmc")):
            kclxmc_fallback += 1
        for key in ("jxbMc", "kchId", "kch", "kcmc", "kklxdm", "kclxmc", "kzmc", "xf"):
            if not course.get(key):
                missing_fields[key] = missing_fields.get(key, 0) + 1
        if not course.get("sksj"):
            missing_fields["sksj"] = missing_fields.get("sksj", 0) + 1
        courses.append(course)

    if capacity_missing:
        report.warn(
            f"{len(capacity_missing)} 门课缺详情容量 jxbrl，容量按 0 写出，jwmock 会回退默认 30: "
            + ", ".join(capacity_missing)
        )
    if kclxmc_fallback:
        report.line(f"kclxmc 用详情 kclbmc 兜底: {kclxmc_fallback} 门")
    if enrolled_over_capacity:
        report.warn(f"{enrolled_over_capacity} 门课已选人数超过容量，已按容量截断")
    for key, count in sorted(missing_fields.items()):
        report.warn(f"{count} 门课的字段 {key} 为空，已省略（走 jwmock 默认值）")
    return courses


def build_tab(
    courses: list[dict],
    list_forms: list[dict],
    detail_forms: list[dict],
    tab_name: str,
    report: Report,
) -> dict:
    values = {tab_key: pick_tab_value(list_forms, form_key) for form_key, tab_key in TAB_FORM_FIELDS}
    xklc = pick_tab_value(detail_forms, "xklc")
    if not xklc and list_forms:
        xklc = pick_tab_value(list_forms, "xklc")
    # 列表行里也可能带 kklxdm（表单缺失时兜底）。
    if not values["kklxdm"] and courses:
        values["kklxdm"] = clean(courses[0].get("kklxdm"))

    if not values["kklxdm"]:
        report.warn("HAR 中没有 kklxdm，页签按 jwmock 默认值 10 处理")
    if not values["xkkzId"]:
        report.warn("HAR 中没有 xkkz_id，选课首页会使用 jwmock 生成的占位 xkkzId")
    if not xklc:
        report.warn("HAR 中没有 xklc，轮次按 jwmock 默认值 1 处理")

    # 多个 HAR 或同一 HAR 里页签参数不一致时给出告警（默认取第一个非空值）。
    for form_key, tab_key in TAB_FORM_FIELDS:
        options = distinct_form_values(list_forms, form_key)
        if len(options) > 1:
            report.warn(
                f"HAR 中页签参数 {form_key} 有 {len(options)} 个取值 {options}，已使用 {values[tab_key]}"
                "（不同页签请分别生成配置再合并 tabs）"
            )
    xklc_options = distinct_form_values(detail_forms, "xklc")
    if len(xklc_options) > 1:
        report.warn(f"HAR 中页签参数 xklc 有 {len(xklc_options)} 个取值 {xklc_options}，已使用 {xklc}")

    xklcmc = f"第{xklc}轮" if xklc else ""
    tab: dict = {
        "name": tab_name,
        "kklxdm": values["kklxdm"],
        "xkkzId": values["xkkzId"],
        "njdmId": values["njdmId"],
        "zyhId": values["zyhId"],
        "xklc": xklc,
        "xklcmc": xklcmc,
        "courses": courses,
    }
    return tab


def main(argv: list[str] | None = None) -> int:
    repo = Path(__file__).resolve().parents[2]
    parser = argparse.ArgumentParser(
        description="从正方教务 HAR 导出 jwmock 配置（合并 zzxkyzb_cxZzxkYzbPartDisplay 与 zzxkyzb_cxJxbWithKchZzxkYzb，按 jxb_id 合并）",
    )
    parser.add_argument(
        "--har",
        action="append",
        metavar="PATH",
        help="输入 HAR 路径，可重复；省略时自动选 jwmock/real_data 下最新的“通识选修课”HAR",
    )
    parser.add_argument("--out", default=str(repo / "jwmock" / "config.json"), help="输出配置路径（默认 jwmock/config.json）")
    parser.add_argument("--tab-name", default="通识选修课", help="页签名称（默认 通识选修课）")
    parser.add_argument("--base-path", default="/jwglxt", help="jwmock BasePath（默认 /jwglxt）")
    parser.add_argument("--username", default="20250001", help="模拟账号（默认 20250001）")
    parser.add_argument("--password", default="test123", help="模拟密码（默认 test123）")
    parser.add_argument("--report", help="把统计报告额外写入该文件（UTF-8）")
    args = parser.parse_args(argv)

    report = Report()
    har_paths = [Path(p).expanduser() for p in args.har] if args.har else []
    if not har_paths:
        real_dir = repo / "jwmock" / "real_data"
        candidates = sorted(real_dir.glob("*通识选修课*.har"))
        if not candidates:
            print(f"没有找到 HAR: {real_dir}/*通识选修课*.har，请用 --har 指定", file=sys.stderr)
            return 1
        har_paths = [candidates[-1]]
        report.line(f"未指定 --har，自动选择最新的通识选修课样本: {har_paths[0].name}")
    for path in har_paths:
        if not path.is_file():
            print(f"HAR 不存在: {path}", file=sys.stderr)
            return 1

    merged_lists: dict[str, dict] = {}
    order: list[str] = []
    detail_map: dict[str, dict] = {}
    list_forms: list[dict] = []
    detail_forms: list[dict] = []
    for path in har_paths:
        st = merge_rows(merged_lists, order, detail_map, path, report)
        list_forms.extend(st["list_forms"])
        detail_forms.extend(st["detail_forms"])

    if not order:
        print(f"两个接口一个教学班都没解析出来: {[str(p) for p in har_paths]}", file=sys.stderr)
        return 1

    report.line(f"合并结果: {len(order)} 门教学班（列表 jxb_id 去重后；详情已按 jxb_id 合并）")
    report.line()
    courses = build_courses(merged_lists, order, detail_map, report)
    tab = build_tab(courses, list_forms, detail_forms, args.tab_name, report)
    # withDefaults 会把空字符串替换成默认值；输出前去掉空值，避免覆盖默认值。
    tab = {key: value for key, value in tab.items() if value != "" or key in ("courses",)}

    config = {
        "basePath": args.base_path,
        "users": [{"username": args.username, "password": args.password}],
        "tabs": [tab],
    }
    out_path = Path(args.out).expanduser()
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(config, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    report.line(f"输出: {out_path}")
    report.line(
        "页签: {name} kklxdm={kklxdm} xkkzId={xkkz} njdmId={njdm} zyhId={zyh} xklc={xklc} xklcmc={xklcmc}".format(
            name=tab.get("name"),
            kklxdm=tab.get("kklxdm", ""),
            xkkz=tab.get("xkkzId", ""),
            njdm=tab.get("njdmId", ""),
            zyh=tab.get("zyhId", ""),
            xklc=tab.get("xklc", ""),
            xklcmc=tab.get("xklcmc", ""),
        )
    )
    text = report.text()
    print(text, end="")
    if args.report:
        report_path = Path(args.report).expanduser()
        report_path.parent.mkdir(parents=True, exist_ok=True)
        report_path.write_text(text, encoding="utf-8")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

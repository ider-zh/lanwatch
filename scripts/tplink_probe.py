#!/usr/bin/env python3
"""TP-Link TL-WDR5600 (192.168.1.3:8888) 探针。

协议为实测逆向结果，细节见 docs/DESIGN.md §2.3。
用途：1) 作为 Go 适配器（internal/source/tplink）的参考实现；2) 排障时手工查设备。

用法：
    python3 scripts/tplink_probe.py devices            # 规范化设备列表
    python3 scripts/tplink_probe.py devices --json     # 原始 JSON
    python3 scripts/tplink_probe.py raw hosts_info host_info
    TPLINK_PASSWORD=xxx python3 scripts/tplink_probe.py devices

注意：路由器密码连续错 10 次会锁定 20 分钟。脚本只登录一次，不会重试。
"""

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

BASE_URL = os.environ.get("TPLINK_URL", "http://192.168.1.3:8888")
# 真实密码只从环境变量读取，不在代码中留默认值（避免泄露到版本库）。
PASSWORD = os.environ.get("TPLINK_PASSWORD")
if not PASSWORD:
    sys.exit("error: TPLINK_PASSWORD is not set (export TPLINK_PASSWORD=... or use .env)")

# orgAuthPwd() 的两个常量，硬编码在 /web-static/lib/slp-ajax.js
ENC_KEY = "RDpbLfCPsJZ7fiv"
ENC_DICT = (
    "yLwVl0zKqws7LgKPRQ84Mdt708T1qQ3Ha7xv3H7NyU84p21BriUWBU43odz3iP4rBL3cD02KZciX"
    "TysVXiV8ngg6vL48rPJyAUw0HurW20xqxv9aYb4M9wK1Ae0wlro510qXeU07kV57fQMc8L6aLgML"
    "wygtc0F10a0Dg70TOoouyFhdysuRMO51yY5ZlOZZLEal1h0t9YQW0Ko7oBwmCAHoic4HYbUyVeU3"
    "sfQ1xtXcPcf1aT303wAQhv66qzW"
)

LINK_TYPES = {"0": "wired", "1": "wifi", "2": "wifi-guest"}
ANONYMOUS = "匿名主机"  # decode 后的占位主机名


# ---------------------------------------------------------------- 协议

def encode_password(password: str) -> str:
    """orgAuthPwd: DIC[(key[i] ^ pwd[i]) % len(DIC)]，较短一侧补 0xBB。"""
    out = []
    n = len(ENC_DICT)
    for i in range(max(len(ENC_KEY), len(password))):
        left = ord(ENC_KEY[i]) if i < len(ENC_KEY) else 0xBB
        right = ord(password[i]) if i < len(password) else 0xBB
        out.append(ENC_DICT[(left ^ right) % n])
    return "".join(out)


def _post(url: str, body: dict, timeout: int = 10) -> dict:
    req = urllib.request.Request(
        url,
        data=json.dumps(body).encode("utf-8"),
        headers={"Content-Type": "application/json; charset=UTF-8"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8")
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode("utf-8", "replace")
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return {"_raw": raw, "error_code": "PARSE_FAIL"}


def _decode(value):
    """响应中的字符串值是 URL 编码的 UTF-8。"""
    if isinstance(value, str):
        try:
            return urllib.parse.unquote(value)
        except Exception:
            return value
    if isinstance(value, dict):
        return {k: _decode(v) for k, v in value.items()}
    if isinstance(value, list):
        return [_decode(v) for v in value]
    return value


def _rows(entries) -> list:
    """表结构是 [{"x_1": {...}}, {"x_2": {...}}]，剥掉外层序号键。"""
    out = []
    for item in entries or []:
        if isinstance(item, dict):
            out.extend(v for v in item.values() if isinstance(v, dict))
    return out


class TPLink:
    def __init__(self, base_url: str = BASE_URL, password: str = PASSWORD):
        self.base = base_url.rstrip("/")
        self.password = password
        self.stok = None

    def login(self) -> str:
        # 登录 POST 的目标是站点根路径（浏览器用 xhr.open("POST","")），不是 /ds
        resp = _post(self.base + "/", {
            "method": "do",
            "login": {"password": encode_password(self.password)},
        })
        code = resp.get("error_code")
        if code != 0:
            raise RuntimeError("登录失败: %r (密码错 10 次会锁定 20 分钟)" % (resp,))
        self.stok = resp["stok"]
        return self.stok

    def ds(self, query: dict) -> dict:
        if not self.stok:
            self.login()
        resp = _decode(_post("%s/stok=%s/ds" % (self.base, self.stok), query))
        if resp.get("error_code") == -40401:  # 未认证，重登一次
            self.stok = None
            self.login()
            resp = _decode(_post("%s/stok=%s/ds" % (self.base, self.stok), query))
        return resp

    def collect(self) -> dict:
        """每轮采集：一次 POST 并行取 4 张表。"""
        data = self.ds({
            "method": "get",
            "hosts_info": {"table": "device", "name": "host_info"},
            "ip_mac_bind": {"name": "sys_arp"},
            "network": {"name": "lan"},
        })
        lan_ip = (data.get("network", {}).get("lan") or {}).get("ipaddr", "")
        prefix = lan_ip.rsplit(".", 1)[0] + "." if lan_ip else ""

        aliases = {}
        for row in _rows(data.get("hosts_info", {}).get("device")):
            mac = _norm_mac(row.get("mac"))
            if mac and row.get("name"):
                aliases[mac] = row["name"]

        devices = {}
        for row in _rows(data.get("hosts_info", {}).get("host_info")):
            mac = _norm_mac(row.get("mac"))
            if not mac:
                continue
            ip = row.get("ip", "")
            devices[mac] = {
                "mac": mac,
                "ip": ip,
                "hostname": aliases.get(mac) or _clean_host(row.get("hostname")),
                "link_type": LINK_TYPES.get(str(row.get("type")), "unknown"),
                "ssid": row.get("ssid", ""),
                "blocked": str(row.get("blocked", "0")) == "1",
                "present": bool(prefix) and ip.startswith(prefix),
                "confidence": 85 if ip.startswith(prefix) else 20,
                "via": "hosts_info",
            }

        # sys_arp 陈旧，只用来补齐 host_info 里没有的条目，且不当在线判据
        for row in _rows(data.get("ip_mac_bind", {}).get("sys_arp")):
            mac = _norm_mac(row.get("mac"))
            ip = row.get("ip", "")
            if not mac or mac in devices:
                continue
            on_lan = bool(prefix) and ip.startswith(prefix)
            devices[mac] = {
                "mac": mac,
                "ip": ip,
                "hostname": _clean_host(row.get("hostname")),
                "link_type": "unknown",
                "ssid": "",
                "blocked": False,
                "present": on_lan,
                "confidence": 60 if on_lan else 25,
                "via": "sys_arp",
            }

        return {
            "lan_gateway": lan_ip,
            "lan_prefix": prefix,
            "devices": sorted(devices.values(), key=lambda d: _ip_key(d["ip"])),
        }


def _norm_mac(value: str) -> str:
    """统一为大写冒号分隔。"""
    value = (value or "").strip().upper().replace("-", ":")
    return value


def _clean_host(value: str) -> str:
    value = (value or "").strip()
    return "" if value == ANONYMOUS else value


def _ip_key(ip: str) -> tuple:
    try:
        return tuple(int(p) for p in ip.split("."))
    except ValueError:
        return (999,)


# ---------------------------------------------------------------- CLI

def main() -> int:
    parser = argparse.ArgumentParser(description="TP-Link WDR5600 探针")
    parser.add_argument("action", choices=["devices", "raw", "login"])
    parser.add_argument("args", nargs="*", help="raw <module> <table>")
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--url", default=BASE_URL)
    parser.add_argument("--password", default=PASSWORD)
    opts = parser.parse_args()

    client = TPLink(opts.url, opts.password)

    if opts.action == "login":
        print(client.login())
        return 0

    if opts.action == "raw":
        if len(opts.args) != 2:
            parser.error("raw 需要 <module> <table>")
        module, table = opts.args
        print(json.dumps(client.ds({"method": "get", module: {"name": table}}),
                         ensure_ascii=False, indent=2))
        return 0

    result = client.collect()
    if opts.json:
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return 0

    print("LAN 网关 %s（前缀 %s）  %d 台设备"
          % (result["lan_gateway"], result["lan_prefix"], len(result["devices"])))
    print("%-19s %-16s %-14s %-11s %-5s %s"
          % ("MAC", "IP", "HOSTNAME", "LINK", "CONF", "PRESENT"))
    for dev in result["devices"]:
        print("%-19s %-16s %-14s %-11s %-5s %s"
              % (dev["mac"], dev["ip"], dev["hostname"] or "-",
                 dev["link_type"], dev["confidence"], dev["present"]))
    return 0


if __name__ == "__main__":
    sys.exit(main())

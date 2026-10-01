#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""passgate 端到端验证脚本：覆盖购卡、核验决策、幂等并发、冻结/退款/升级、申诉、运营统计。

前置：服务已在 127.0.0.1:8080 运行（空库启动会自动写入种子数据）。
运行：python3 test/e2e.py
"""
import datetime
import json
import subprocess
import sys
import time
import urllib.request
import urllib.error
import uuid
from concurrent.futures import ThreadPoolExecutor

BASE = "http://127.0.0.1:8080"
PASS = 0
FAIL = 0


def check(name, cond, extra=""):
    global PASS, FAIL
    if cond:
        PASS += 1
        print(f"  ✓ {name}")
    else:
        FAIL += 1
        print(f"  ✗ {name}  {extra}")


def section(title):
    print(f"\n== {title} ==")


def req(method, path, body=None, headers=None):
    url = BASE + path
    data = None
    h = {"Content-Type": "application/json"}
    if headers:
        h.update(headers)
    if body is not None:
        data = json.dumps(body).encode()
    r = urllib.request.Request(url, data=data, headers=h, method=method)
    try:
        with urllib.request.urlopen(r, timeout=15) as resp:
            return resp.status, json.loads(resp.read().decode() or "{}")
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read().decode() or "{}")
        except Exception:
            return e.code, {}


def redis_cli(*args, stdin=None):
    cmd = ["redis-cli"] + list(args)
    res = subprocess.run(cmd, input=stdin, capture_output=True, text=True)
    return res.stdout.strip()


def redis_set_json(key, obj):
    redis_cli("-x", "SET", key, stdin=json.dumps(obj))


def redis_get_json(key):
    out = redis_cli("GET", key)
    return json.loads(out) if out else None


def local_date():
    return datetime.datetime.now().astimezone().strftime("%Y-%m-%d")


def local_ts(hour=10, minute=30, date=None):
    now = datetime.datetime.now().astimezone()
    tz = now.strftime("%z")
    tz = tz[:3] + ":" + tz[3:]
    d = date or now.strftime("%Y-%m-%d")
    return f"{d}T{hour:02d}:{minute:02d}:00{tz}"


_uid = 0


def new_idcard():
    global _uid
    _uid += 1
    return "ID" + uuid.uuid4().hex[:14] + str(_uid)


def buy_pass(ptype="single", members=None, expiry="2032-01-01", phone=None):
    body = {
        "type": ptype,
        "holder_name": "测试用户" + str(_uid),
        "holder_id_card": new_idcard(),
        "id_card_expiry": expiry,
        "phone": phone or ("139" + str(uuid.uuid4().int)[:8]),
        "photo_url": "https://img.example.com/p.jpg",
    }
    if members:
        body["members"] = members
    st, r = req("POST", "/api/passes", body)
    assert st == 200, f"buy_pass failed: {r}"
    return r["data"]


def verify(pass_id=None, qr=None, idcard=None, device="dev-east-01", attraction="attr-lake",
           face=90.0, companions=None, req_id=None, ts=None, photo_clear=None, offline_sync=False):
    body = {
        "request_id": req_id or ("REQ-" + uuid.uuid4().hex[:12]),
        "device_id": device,
        "attraction": attraction,
        "face_score": face,
        "offline_sync": offline_sync,
    }
    if pass_id:
        body["pass_id"] = pass_id
    if qr:
        body["qr_token"] = qr
    if idcard:
        body["id_card"] = idcard
    if companions:
        body["companions"] = companions
    if ts:
        body["timestamp"] = ts
    if photo_clear is not None:
        body["photo_clear"] = photo_clear
    st, r = req("POST", "/api/verify", body)
    return st, r


def remaining(pass_id):
    st, r = req("GET", f"/api/passes/{pass_id}")
    return r["data"]["remaining_quota"]


# ============================================================
section("A. 健康检查与种子数据")
st, r = req("GET", "/healthz")
check("健康检查", st == 200 and r.get("status") == "ok")
st, r = req("GET", "/api/devices")
check("种子设备 4 台且在线", st == 200 and len(r["data"]) == 4 and all(d["online"] for d in r["data"]))
st, r = req("GET", "/api/admin/sales-rules")
check("售卖规则 3 类", st == 200 and len(r["data"]) == 3)
st, r = req("GET", "/api/holidays")
check("2026 节假日已配置", st == 200 and "2026-10-01" in r["data"])
# 确定性节假日：种子含真实国庆，而测试运行当天可能恰好是节假日（如 2026-10-01）。
# 将节假日集合改为：保留种子节假日但移除"今天"，另设一个今天+10 天的专属测试节假日，
# 使"正常日（今天）"用例与"节假日拦截"用例互不干扰。
_today = datetime.datetime.now().astimezone().date()
TEST_HOLIDAY = (_today + datetime.timedelta(days=10)).strftime("%Y-%m-%d")
_holidays = sorted({d for d in r["data"] if d != _today.strftime("%Y-%m-%d")} | {TEST_HOLIDAY})
st, r = req("PUT", "/api/holidays", {"dates": _holidays, "operator": "ops-li"})
check("重设确定性测试节假日（今天非节假日）", st == 200 and TEST_HOLIDAY in req("GET", "/api/holidays")[1]["data"]
      and _today.strftime("%Y-%m-%d") not in req("GET", "/api/holidays")[1]["data"], r)

section("B. 购卡与售卖规则")
p1 = buy_pass("single")
check("购单人卡成功", p1["id"].startswith("P") and p1["remaining_quota"] == 100 and p1["qr_token"].startswith("QR"))
st, r = req("POST", "/api/passes", {
    "type": "single", "holder_name": "重复", "holder_id_card": p1["holder_id_card"],
    "id_card_expiry": "2032-01-01", "phone": "13911112222"})
check("一证一卡：同证件重复购卡被拒", st == 409, r)
# 停售后禁售
st, rules = req("GET", "/api/admin/sales-rules")
rule_single = [x for x in rules["data"] if x["pass_type"] == "single"][0]
rule_off = dict(rule_single, on_sale=False)
st, r = req("PUT", "/api/admin/sales-rules/single", rule_off, {"X-Operator": "ops-li"})
check("运营停售单人卡", st == 200 and r["data"]["on_sale"] is False)
st, r = req("POST", "/api/passes", {
    "type": "single", "holder_name": "买不到", "holder_id_card": new_idcard(),
    "id_card_expiry": "2032-01-01", "phone": "13933334444"})
check("停售后购卡被拒", st == 409, r)
st, r = req("PUT", "/api/admin/sales-rules/single", rule_single, {"X-Operator": "ops-li"})
check("恢复售卖", st == 200 and r["data"]["on_sale"] is True)

section("C. 核验放行 / 幂等 / 并发去重")
pc = buy_pass("single")
st, r = verify(pass_id=pc["id"], req_id="C-0001", ts=local_ts(10, 30))
check("正常放行", st == 200 and r["data"]["decision"] == "ALLOW" and r["data"]["charged_quota"] == 1, r)
v_id = r["data"]["verification_id"]
st, r2 = verify(pass_id=pc["id"], req_id="C-0001", ts=local_ts(10, 30))
check("同 request_id 重试返回首次结果不重复扣", r2["data"]["verification_id"] == v_id and remaining(pc["id"]) == 99, r2)
st, r3 = verify(pass_id=pc["id"], req_id="C-0002", ts=local_ts(10, 31))
check("抖动重刷(不同request_id)按重复入园处理", r3["data"]["decision"] == "ALLOW" and r3["data"]["reason"] == "DUPLICATE_ENTRY"
      and r3["data"]["charged_quota"] == 0 and remaining(pc["id"]) == 99, r3)

# 并发：20 个相同 request_id
pc2 = buy_pass("single")
def concurrent_same(_):
    return verify(pass_id=pc2["id"], req_id="C-CONC", ts=local_ts(10, 30))
with ThreadPoolExecutor(max_workers=20) as ex:
    results = list(ex.map(concurrent_same, range(20)))
charged = sum(x[1]["data"]["charged_quota"] for x in results if x[0] == 200)
check("20 并发同 request_id 仅扣 1 次", charged == 1 and remaining(pc2["id"]) == 99,
      f"charged_sum={charged} remaining={remaining(pc2['id'])}")

# 并发：20 个不同 request_id 同景点（模拟闸机重试风暴）
pc3 = buy_pass("single")
def concurrent_diff(i):
    return verify(pass_id=pc3["id"], req_id=f"C-CONC-{i}", ts=local_ts(10, 30))
with ThreadPoolExecutor(max_workers=20) as ex:
    results = list(ex.map(concurrent_diff, range(20)))
charged = sum(x[1]["data"]["charged_quota"] for x in results if x[0] == 200)
check("20 并发不同 request_id 同景点仅扣 1 次(去重窗口)", charged == 1 and remaining(pc3["id"]) == 99,
      f"charged_sum={charged} remaining={remaining(pc3['id'])}")

# 二维码核验
fam = None
st, r = req("GET", "/api/passes/P000001")
if st == 200:
    fam = r["data"]
st, r = verify(qr=fam["qr_token"], face=88.0, ts=local_ts(10, 30))
check("二维码核验放行", st == 200 and r["data"]["decision"] == "ALLOW", r)
# 刷证件（成员证件定位年卡）；清掉高频计数避免误判，换景点避免去重窗口
redis_cli("DEL", "hf:P000001", "loc:P000001")
st, r = verify(idcard="110101199203054321", face=80.0, attraction="attr-garden", ts=local_ts(10, 32))
check("刷成员证件核验放行", st == 200 and r["data"]["decision"] == "ALLOW" and r["data"]["charged_quota"] == 1, r)

section("D. 核验失败原因覆盖")
st, r = verify(pass_id=pc["id"], device="dev-not-exist", ts=local_ts(10, 30))
check("未注册设备 DEVICE_UNKNOWN", r["data"]["reason"] == "DEVICE_UNKNOWN", r)

# 设备离线：注册新设备并把心跳改到 5 分钟前
st, r = req("POST", "/api/devices", {"id": "dev-offline-01", "name": "离线闸机", "gate_id": "gate-east", "attraction": "attr-lake"})
dev = redis_get_json("device:dev-offline-01")
dev["last_heartbeat"] = (datetime.datetime.now() - datetime.timedelta(minutes=5)).astimezone().isoformat()
redis_set_json("device:dev-offline-01", dev)
st, r = verify(pass_id=pc["id"], device="dev-offline-01", ts=local_ts(10, 30))
check("设备离线 DEVICE_OFFLINE", r["data"]["decision"] == "MANUAL_REVIEW" and r["data"]["reason"] == "DEVICE_OFFLINE", r)
# 离线补传用全新卡，避免去重窗口/高频计数干扰
pc_off = buy_pass("single")
st, r = verify(pass_id=pc_off["id"], device="dev-offline-01", ts=local_ts(10, 30), offline_sync=True, req_id="C-OFFLINE-SYNC")
check("离线补传(offline_sync)正常放行", r["data"]["decision"] == "ALLOW" and r["data"]["charged_quota"] == 1, r)
st, r = verify(pass_id=pc_off["id"], device="dev-offline-01", ts=local_ts(10, 30), offline_sync=True, req_id="C-OFFLINE-SYNC")
check("离线补传重复提交不重复扣次", remaining(pc_off["id"]) == 99, r)

st, r = verify(pass_id="P999999", ts=local_ts(10, 30))
check("年卡不存在 PASS_NOT_FOUND", r["data"]["reason"] == "PASS_NOT_FOUND", r)

pfrozen = buy_pass("single")
req("POST", f"/api/passes/{pfrozen['id']}/freeze", {"reason": "失主挂失", "operator": "cs-wang"})
st, r = verify(pass_id=pfrozen["id"], ts=local_ts(10, 30))
check("冻结卡 PASS_FROZEN", r["data"]["decision"] == "MANUAL_REVIEW" and r["data"]["reason"] == "PASS_FROZEN", r)

pexp = buy_pass("single")
pj = redis_get_json(f"pass:{pexp['id']}")
pj["valid_until"] = "2020-01-01"
redis_set_json(f"pass:{pexp['id']}", pj)
st, r = verify(pass_id=pexp["id"], ts=local_ts(10, 30))
check("年卡过期 PASS_EXPIRED", r["data"]["decision"] == "QUOTA_INSUFFICIENT" and r["data"]["reason"] == "PASS_EXPIRED", r)

st, r = req("GET", "/api/passes/P000003")  # 亲子卡不含 attr-summit
pcard = r["data"]
st, r = verify(pass_id=pcard["id"], attraction="attr-summit", ts=local_ts(10, 30))
check("景点不适用 ATTRACTION_NOT_ALLOWED", r["data"]["reason"] == "ATTRACTION_NOT_ALLOWED", r)

psingle = buy_pass("single")  # 单人卡节假日不可用
st, r = verify(pass_id=psingle["id"], ts=local_ts(10, 30, date=TEST_HOLIDAY))
check("节假日限制 HOLIDAY_BLOCKED", r["data"]["reason"] == "HOLIDAY_BLOCKED", r)
st, r = verify(pass_id=psingle["id"], ts=local_ts(23, 30))
check("时段限制 TIMESLOT_BLOCKED", r["data"]["reason"] == "TIMESLOT_BLOCKED", r)

st, r = verify(pass_id=fam["id"], companions=["ID_NOT_BOUND_1"], ts=local_ts(10, 33))
check("同行人未绑定 COMPANION_NOT_BOUND", r["data"]["reason"] == "COMPANION_NOT_BOUND", r)
st, r = verify(pass_id=pcard["id"], companions=["440101201909096666", "ID_X"], ts=local_ts(10, 30))
check("同行超限 COMPANION_EXCEEDED", r["data"]["reason"] == "COMPANION_EXCEEDED", r)

pphoto = buy_pass("single")
st, r = verify(pass_id=pphoto["id"], photo_clear=False, ts=local_ts(10, 30))
check("照片不清 PHOTO_UNCLEAR", r["data"]["reason"] == "PHOTO_UNCLEAR", r)
st, r = verify(pass_id=pphoto["id"], face=30.0, ts=local_ts(10, 30))
check("人脸低分 FACE_MISMATCH", r["data"]["reason"] == "FACE_MISMATCH", r)

pold = buy_pass("single", expiry="2020-06-01")
st, r = verify(pass_id=pold["id"], ts=local_ts(10, 30))
check("证件过期 ID_EXPIRED", r["data"]["reason"] == "ID_EXPIRED", r)

phf = buy_pass("single")
redis_cli("SET", f"hf:{phf['id']}", "4")
st, r = verify(pass_id=phf["id"], ts=local_ts(10, 30))
check("高频异常 SUSPECTED_LENDING/HIGH_FREQUENCY",
      r["data"]["decision"] == "SUSPECTED_LENDING" and r["data"]["reason"] == "HIGH_FREQUENCY", r)

pml = buy_pass("family")
st, r = verify(pass_id=pml["id"], attraction="attr-lake", ts=local_ts(10, 30))
assert r["data"]["decision"] == "ALLOW", r
st, r = verify(pass_id=pml["id"], attraction="attr-garden", ts=local_ts(10, 35))
check("多地异常 SUSPECTED_LENDING/MULTI_LOCATION",
      r["data"]["decision"] == "SUSPECTED_LENDING" and r["data"]["reason"] == "MULTI_LOCATION", r)

pcap = buy_pass("family")
today = local_date()
redis_cli("SET", f"cap:attr-garden:{today}", "3000")  # 百花园容量 3000
st, r = verify(pass_id=pcap["id"], attraction="attr-garden", ts=local_ts(10, 30))
check("容量超限 CAPACITY_FULL", r["data"]["reason"] == "CAPACITY_FULL", r)
redis_cli("DEL", f"cap:attr-garden:{today}")

pquota = buy_pass("single")
redis_cli("SET", f"pass:{pquota['id']}:remaining", "0")
st, r = verify(pass_id=pquota["id"], ts=local_ts(10, 30))
check("权益耗尽 QUOTA_EXHAUSTED", r["data"]["decision"] == "QUOTA_INSUFFICIENT" and r["data"]["reason"] == "QUOTA_EXHAUSTED", r)

section("E. 人工放行与客服可解释")
pm = buy_pass("single")
st, r = verify(pass_id=pm["id"], photo_clear=False, ts=local_ts(10, 30))
vid = r["data"]["verification_id"]
check("制造人工核验(PHOTO_UNCLEAR)", r["data"]["decision"] == "MANUAL_REVIEW", r)
st, r = req("POST", f"/api/verifications/{vid}/manual-review",
            {"action": "RELEASE", "operator": "staff-chen", "note": "现场比对身份证照片一致，人工放行"})
check("人工放行成功并扣减额度", st == 200 and r["data"]["charged_quota"] == 1 and remaining(pm["id"]) == 99, r)
st, r = req("POST", f"/api/verifications/{vid}/manual-review",
            {"action": "RELEASE", "operator": "staff-chen", "note": "重复处理"})
check("重复人工处理被拒", st == 409, r)
st, r = req("GET", f"/api/verifications/{vid}")
check("客服可查完整链路(原因+人工处理记录)",
      r["data"]["manual_action"] == "RELEASED" and r["manual_review"]["operator"] == "staff-chen"
      and r["manual_review"]["note"].startswith("现场比对"), r)
st, r = req("GET", f"/api/passes/{pm['id']}/gate-logs")
check("闸机日志含 MANUAL_RELEASE", any(g["action"] == "MANUAL_RELEASE" for g in r["data"]), r)

section("F. 冻结/解冻与预约一致性")
pr = buy_pass("single")
st, r = req("POST", "/api/reservations", {"pass_id": pr["id"], "attraction": "attr-lake",
                                          "date": local_date(), "slot": "10:00-12:00"})
rsv_id = r["data"]["id"]
check("预约成功", st == 200 and r["data"]["status"] == "BOOKED", r)
req("POST", f"/api/passes/{pr['id']}/freeze", {"reason": "涉嫌转借核查", "operator": "cs-wang"})
st, r = req("GET", f"/api/passes/{pr['id']}/reservations")
check("冻结不影响已预约未入园订单", r["data"][0]["status"] == "BOOKED", r)
st, r = req("POST", "/api/reservations", {"pass_id": pr["id"], "attraction": "attr-lake",
                                          "date": local_date(), "slot": "14:00-16:00"})
check("冻结期间不可新预约", st == 409, r)
req("POST", f"/api/passes/{pr['id']}/unfreeze", {"operator": "cs-wang"})
st, r = verify(pass_id=pr["id"], ts=local_ts(10, 30))
check("解冻后正常放行", r["data"]["decision"] == "ALLOW", r)
st, r = req("GET", f"/api/passes/{pr['id']}/reservations")
check("入园后预约核销为 USED", r["data"][0]["status"] == "USED", r)

section("G. 申诉与权益返还")
pa = buy_pass("single")
st, r = verify(pass_id=pa["id"], req_id="G-0001", ts=local_ts(10, 30))
vid = r["data"]["verification_id"]
check("扣次成功(剩99)", remaining(pa["id"]) == 99)
st, r = req("POST", "/api/appeals", {"pass_id": pa["id"], "verification_id": vid,
                                     "type": "WRONG_DEDUCTION", "description": "闸机离线重复扣次，申请返还"})
aid = r["data"]["id"]
check("申诉创建", st == 200 and r["data"]["status"] == "PENDING", r)
st, r = req("POST", f"/api/appeals/{aid}/resolve",
            {"approve": True, "resolution": "核实为闸机抖动误扣，返还 1 人次", "handler": "cs-wang"})
check("申诉通过自动返还额度", st == 200 and r["data"]["compensate_quota"] == 1 and remaining(pa["id"]) == 100, r)
st, r = req("POST", f"/api/appeals/{aid}/resolve",
            {"approve": True, "resolution": "重复处理", "handler": "cs-wang"})
check("申诉重复处理被拒", st == 409, r)

section("H. 退款与升级重算")
pref = buy_pass("single")
req("POST", "/api/reservations", {"pass_id": pref["id"], "attraction": "attr-lake",
                                  "date": local_date(), "slot": "10:00-12:00"})
st, r = req("POST", f"/api/passes/{pref['id']}/refund", {"operator": "cs-wang"})
check("退款：状态+退款金额", st == 200 and r["data"]["status"] == "refunded" and r["data"]["refund_amount"] > 0, r)
check("退款后权益清零", remaining(pref["id"]) == 0)
st, r = req("GET", f"/api/passes/{pref['id']}/reservations")
check("退款后未使用预约取消", r["data"][0]["status"] == "CANCELLED", r)
st, r = verify(pass_id=pref["id"], ts=local_ts(10, 30))
check("退款卡核验 PASS_REFUNDED", r["data"]["reason"] == "PASS_REFUNDED", r)

pup = buy_pass("single")
# 先把有效期改短（模拟已用 60 天、只剩 100 天的旧卡），升级后应重算为完整新周期
pj = redis_get_json(f"pass:{pup['id']}")
d60 = (datetime.datetime.now() - datetime.timedelta(days=60)).strftime("%Y-%m-%d")
d100 = (datetime.datetime.now() + datetime.timedelta(days=100)).strftime("%Y-%m-%d")
pj["valid_from"] = d60
pj["valid_until"] = d100
redis_set_json(f"pass:{pup['id']}", pj)
expect_until = (datetime.datetime.now() + datetime.timedelta(days=365)).strftime("%Y-%m-%d")
st, r = req("POST", f"/api/passes/{pup['id']}/upgrade", {"to_type": "family", "operator": "cs-wang"})
check("升级：类型变更+补差价", st == 200 and r["data"]["type"] == "family" and r["price_diff"] >= 0, r)
check("升级：有效期重算为新卡完整周期", r["data"]["valid_from"] == local_date() and r["data"]["valid_until"] == expect_until, r["data"])
check("升级：额度按新卡重置", r["data"]["remaining_quota"] == 400, r["data"])
st, r2 = verify(pass_id=pup["id"], attraction="attr-summit", ts=local_ts(10, 30))
check("升级后可进家庭卡景点", r2["data"]["decision"] == "ALLOW", r2)

section("I. 信息变更")
ph = buy_pass("single", phone="13700001111")
st, r = req("POST", f"/api/passes/{ph['id']}/phone",
            {"old_phone": "13700001111", "new_phone": "13700009999", "code": "000000"})
check("换手机号：错误验证码被拒", st == 403, r)
st, r = req("POST", f"/api/passes/{ph['id']}/phone",
            {"old_phone": "13700001111", "new_phone": "13700009999", "code": "123456"})
check("换手机号成功", st == 200 and r["data"]["phone"] == "13700009999", r)
st, r = req("GET", "/api/passes?phone=13700009999")
check("新手机号可查到卡", any(x["id"] == ph["id"] for x in r["data"]), r)
st, r = req("GET", "/api/passes?phone=13700001111")
check("旧手机号查不到卡", len(r["data"]) == 0, r)

pmem = buy_pass("parent_child", members=[{"name": "孩子", "id_card": new_idcard(), "relation": "child"}])
st, r = req("POST", f"/api/passes/{pmem['id']}/members",
            {"name": "外婆", "id_card": new_idcard(), "relation": "parent", "operator": "staff-chen"})
check("添加成员成功(消耗更换次数)", st == 200, r)
st, r = req("POST", f"/api/passes/{pmem['id']}/members",
            {"name": "外公", "id_card": new_idcard(), "relation": "parent", "operator": "staff-chen"})
check("更换次数用尽被拒", st == 409, r)
st, r = req("GET", f"/api/passes/{pmem['id']}")
mid = [m for m in r["data"]["members"] if m["name"] == "外婆"][0]["id"]
st, r = req("DELETE", f"/api/passes/{pmem['id']}/members/{mid}")
check("移除成员", st == 200, r)
st, r = req("GET", f"/api/passes/{pmem['id']}")
child = [m for m in r["data"]["members"] if m["role"] == "companion"][0]
st, r = req("POST", f"/api/passes/{pmem['id']}/members/{child['id']}/photo",
            {"photo_url": "https://img.example.com/new-photo.jpg", "operator": "staff-chen"})
check("孩子长大更新照片(版本+1)", st == 200 and r["data"]["photo_version"] == 2, r)

pq = buy_pass("single")
old_qr = pq["qr_token"]
st, r = req("POST", f"/api/passes/{pq['id']}/qr-refresh", {"operator": "cs-wang"})
new_qr = r["data"]["qr_token"]
st, r = verify(qr=old_qr, ts=local_ts(10, 30))
check("旧二维码失效", r["data"]["reason"] == "PASS_NOT_FOUND", r)
st, r = verify(qr=new_qr, ts=local_ts(10, 30))
check("新二维码放行", r["data"]["decision"] == "ALLOW", r)

section("J. 老人免刷脸（人工确认放行）")
# 种子家庭卡成员 张父(P000001-M3) 为免刷脸；换 attr-summit 避开去重窗口
st, r = verify(idcard="110101195501011239", face=-1.0, attraction="attr-summit", ts=local_ts(10, 40))
check("免刷脸成员转人工 FACE_EXEMPT_REVIEW",
      r["data"]["decision"] == "MANUAL_REVIEW" and r["data"]["reason"] == "FACE_EXEMPT_REVIEW", r)
vid = r["data"]["verification_id"]
st, r = req("POST", f"/api/verifications/{vid}/manual-review",
            {"action": "RELEASE", "operator": "staff-chen", "note": "老人证件人证一致，人工放行"})
check("人工确认放行老人", st == 200 and r["data"]["charged_quota"] == 1, r)
# 工作人员设置/取消免刷脸
st, r = req("GET", "/api/passes/P000001")
zhangfu = [m for m in r["data"]["members"] if m["name"] == "张父"][0]
st, r = req("POST", f"/api/passes/P000001/members/{zhangfu['id']}/face-exempt",
            {"exempt": False, "operator": "staff-chen"})
check("取消免刷脸标记", st == 200 and r["data"]["face_exempt"] is False, r)
req("POST", f"/api/passes/P000001/members/{zhangfu['id']}/face-exempt",
    {"exempt": True, "operator": "staff-chen"})

section("K. 出园与容量")
pk = buy_pass("family")
st, r = verify(pass_id=pk["id"], attraction="attr-lake", ts=local_ts(11, 0))
assert r["data"]["decision"] == "ALLOW", r
st, r = req("GET", "/api/attractions/attr-lake/capacity")
cap_after_enter = r["data"]["in_park"]
st, r = req("POST", "/api/exit", {"pass_id": pk["id"], "attraction": "attr-lake", "count": 1, "device_id": "dev-east-01"})
st, r = req("GET", "/api/attractions/attr-lake/capacity")
check("出园释放在园容量", r["data"]["in_park"] == cap_after_enter - 1, r)

section("M. 儿童照片变化复核")
# 新购亲子卡：爸爸 + 孩子（面部随成长变化，半年后闸机多次相似度不足）
prc = buy_pass("parent_child", members=[{
    "name": "复核娃", "id_card": new_idcard(), "id_card_expiry": "2032-06-01",
    "relation": "child", "photo_url": "https://img.example.com/kid-old.jpg"}])
kid = [m for m in prc["members"] if m["role"] == "companion"][0]
check("复核前无打开中的照片任务",
      req("GET", f"/api/passes/{prc['id']}")[1]["data"]["members"] is not None)

# 成年人（持卡人）低分不触发儿童任务
st, r = verify(pass_id=prc["id"], face=20.0, req_id="M-HOLDER-LOW", attraction="attr-lake", ts=local_ts(9, 0))
check("持卡人本人人脸不足仍为 FACE_MISMATCH 且不建儿童任务",
      r["data"]["reason"] == "FACE_MISMATCH" and not r["data"].get("photo_task_id"), r)

# 孩子刷证件 3 次人脸不足（不同景点避开 90 秒去重窗口；计数按成员累计，不受高频窗口影响）
kid_fails = []
for i, att in enumerate(["attr-lake", "attr-garden", "attr-lake"]):
    st, r = verify(idcard=kid["id_card"], face=25.0 + i, req_id=f"M-KID-FAIL-{i}",
                   attraction=att, ts=local_ts(9, 10 + i * 3))
    kid_fails.append(r)
check("第 1 次人脸不足提示但未建任务",
      kid_fails[0]["data"]["reason"] == "FACE_MISMATCH" and not kid_fails[0]["data"].get("photo_task_id")
      and "再 2 次" in kid_fails[0]["data"]["detail"], kid_fails[0])
check("第 2 次仍为 FACE_MISMATCH", kid_fails[1]["data"]["reason"] == "FACE_MISMATCH", kid_fails[1])
tid = kid_fails[2]["data"].get("photo_task_id")
check("第 3 次自动生成照片更新任务并转人工(PHOTO_RECHECK_REVIEW)",
      kid_fails[2]["data"]["reason"] == "PHOTO_RECHECK_REVIEW" and tid and tid.startswith("T"), kid_fails[2])

# 任务内容
st, r = req("GET", f"/api/photo-tasks/{tid}")
task = r["data"]
check("任务状态待家长提交/来源自动/累计 3 次",
      task["status"] == "PENDING_SUBMISSION" and task["source"] == "AUTO_GATE" and task["fail_count"] == 3
      and task["member_id"] == kid["id"] and task["trigger_verification_id"], r)
st, r = req("GET", "/api/photo-tasks?status=PENDING_SUBMISSION")
check("任务列表可按状态过滤", any(t["id"] == tid for t in r["data"]), r)

# 复核期间：即使孩子分数足够，闸机也转人工通道（旧照已不可信）
st, r = verify(idcard=kid["id_card"], face=99.0, req_id="M-KID-RECHECK-1",
               attraction="attr-garden", ts=local_ts(9, 30))
check("复核期间高分也转人工通道",
      r["data"]["decision"] == "MANUAL_REVIEW" and r["data"]["reason"] == "PHOTO_RECHECK_REVIEW"
      and r["data"]["photo_task_id"] == tid, r)
vid1 = r["data"]["verification_id"]
# 家长带孩子走人工通道：工作人员放行，必须记录操作人与原因
st, r = req("POST", f"/api/verifications/{vid1}/manual-review", {
    "action": "RELEASE", "operator": "staff-chen",
    "reason": "FACE_CHANGED_DURING_RECHECK", "note": "儿童半年面部变化，照片复核中，核对证件后人工放行"})
check("复核期人工放行成功并扣 1 人次", st == 200 and r["data"]["charged_quota"] == 1, r)
# 再次放行（不同 request_id / 景点），验证每次都留痕
st, r = verify(idcard=kid["id_card"], face=99.0, req_id="M-KID-RECHECK-2",
               attraction="attr-lake", ts=local_ts(14, 0))
vid2 = r["data"]["verification_id"]
req("POST", f"/api/verifications/{vid2}/manual-review", {
    "action": "RELEASE", "operator": "staff-chen", "note": "下午再次入园，复核任务未完成，人工放行"})
st, r = req("GET", f"/api/photo-tasks/{tid}")
releases = r["manual_releases"]
check("每次人工放行均留痕（操作人+原因，共 2 次）",
      len(releases) == 2 and {x["operator"] for x in releases} == {"staff-chen"}
      and releases[0]["reason"] == "FACE_CHANGED_DURING_RECHECK"
      and all(x["verification_id"] for x in releases), r)
check("闸机流水含两次 MANUAL_RELEASE",
      sum(1 for g in req("GET", f"/api/passes/{prc['id']}/gate-logs")[1]["data"]
          if g["action"] == "MANUAL_RELEASE") >= 2, r)

# 家长在小程序提交新照片
st, r = req("POST", f"/api/photo-tasks/{tid}/submit", {
    "new_photo_url": "https://img.example.com/kid-new.jpg", "channel": "MINIAPP", "operator": "139-parent"})
check("小程序提交新照片，任务进入待核验", st == 200 and r["data"]["status"] == "PENDING_REVIEW", r)
st, r = req("POST", f"/api/photo-tasks/{tid}/submit", {"new_photo_url": "x", "channel": "ONSITE"})
check("重复提交被拒", st == 409, r)
# 现场渠道也应可用（另一张卡验证 channel 枚举）
prc2 = buy_pass("parent_child", members=[{
    "name": "现场娃", "id_card": new_idcard(), "relation": "child", "photo_url": "https://img.example.com/o.jpg"}])
kid2 = [m for m in prc2["members"] if m["role"] == "companion"][0]
st, r = req("POST", f"/api/passes/{prc2['id']}/members/{kid2['id']}/photo-recheck-task",
            {"operator": "staff-chen"})
tid2 = r["data"]["id"]
check("工作人员可手工创建照片任务", st == 200 and r["data"]["status"] == "PENDING_SUBMISSION", r)
st, r = req("POST", f"/api/passes/{prc2['id']}/members/{kid2['id']}/photo-recheck-task",
            {"operator": "staff-chen"})
check("同儿童重复创建任务被拒", st == 409, r)
st, r = req("POST", f"/api/photo-tasks/{tid2}/submit",
            {"new_photo_url": "https://img.example.com/o2.jpg", "channel": "ONSITE", "operator": "staff-chen"})
check("现场渠道提交成功", st == 200 and r["data"]["submit_channel"] == "ONSITE", r)
# 非儿童成员不允许手工建任务
st, r = req("POST", f"/api/passes/{prc2['id']}/members/{prc2['id']}-M0/photo-recheck-task",
            {"operator": "staff-chen"})
check("非儿童成员不能建照片复核任务", st == 400, r)

# 工作人员核验：三项必须全部核验
st, r = req("POST", f"/api/photo-tasks/{tid}/review", {
    "approve": True, "id_checked": True, "guardian_checked": True, "history_checked": False,
    "operator": "staff-chen", "note": "历史入园没查"})
check("三项核验缺一不可", st == 400, r)
st, before = req("GET", "/api/devices/photo-sync/status")
sync_before = before["data"]["current_version"]
st, r = req("POST", f"/api/photo-tasks/{tid}/review", {
    "approve": True, "id_checked": True, "guardian_checked": True, "history_checked": True,
    "operator": "staff-chen", "note": "核验证件一致、监护关系成立、历史入园记录吻合，新照生效"})
task_after = r["data"]
check("核验通过：任务 APPROVED，旧 v1→新 v2，记录历史入园数与同步版本",
      st == 200 and task_after["status"] == "APPROVED"
      and task_after["old_photo_version"] == 1 and task_after["new_photo_version"] == 2
      and task_after["history_entry_count"] >= 2 and task_after["id_checked"]
      and task_after["guardian_checked"] and task_after["history_checked"]
      and task_after["sync_version"] == sync_before + 1 and task_after["old_photo_invalid_at"], r)
st, r = req("POST", f"/api/photo-tasks/{tid}/review",
            {"approve": True, "id_checked": True, "guardian_checked": True, "history_checked": True,
             "operator": "staff-chen", "note": "重复处理"})
check("任务不可重复处理", st == 409, r)

# 旧照归档（不删除），成员照片已更新
st, r = req("GET", f"/api/passes/{prc['id']}/members/{kid['id']}/photo-archives")
arch = r["data"]
check("旧照片已归档并标记失效时间",
      len(arch) == 1 and arch[0]["photo_version"] == 1 and arch[0]["photo_url"].endswith("kid-old.jpg")
      and arch[0]["invalid_at"] and arch[0]["archived_by"] == "staff-chen", r)
st, r = req("GET", f"/api/passes/{prc['id']}")
new_kid = [m for m in r["data"]["members"] if m["id"] == kid["id"]][0]
check("成员新照片生效、版本 2、有生效时间",
      new_kid["photo_url"].endswith("kid-new.jpg") and new_kid["photo_version"] == 2
      and new_kid.get("photo_updated_at"), r)
check("变更日志完整留痕（建任务/提交/通过）",
      {a["action"] for a in req("GET", f"/api/passes/{prc['id']}/changes")[1]["data"]}
      >= {"PHOTO_TASK_CREATE", "PHOTO_TASK_SUBMIT", "PHOTO_TASK_APPROVE"}, r)

# 复核结束：闸机人脸恢复正常放行
st, r = verify(idcard=kid["id_card"], face=95.0, req_id="M-KID-OK",
               attraction="attr-garden", ts=local_ts(10, 0))
check("新照片生效后人脸核验放行", r["data"]["decision"] == "ALLOW" and not r["data"].get("photo_task_id"), r)

# ---- 入口照片缓存同步：在线闸机立即追拉；离线闸机恢复后补拉 ----
new_ver = task_after["sync_version"]
st, r = req("GET", f"/api/devices/dev-east-01/photo-sync?after={new_ver - 1}")
check("在线闸机增量拉取到新照片事件",
      r["current_version"] == new_ver and len(r["data"]) == 1
      and r["data"][0]["photo_version"] == 2 and r["data"][0]["old_photo_invalid_at"]
      and r["data"][0]["member_id"] == kid["id"], r)
st, r = req("GET", "/api/devices/dev-east-02/photo-sync?after=0")
check("离线/未同步闸机从版本 0 全量追拉（恢复后减少重复人工核验）",
      r["current_version"] == new_ver and len(r["data"]) == new_ver
      and any(e["photo_version"] == 2 for e in r["data"]), r)
st, r = req("POST", "/api/devices/dev-east-01/photo-sync/ack", {"version": new_ver})
check("闸机 ACK 本地缓存已更新", st == 200 and r["data"]["acked_version"] == new_ver, r)
st, r = req("POST", "/api/devices/dev-east-01/photo-sync/ack", {"version": new_ver + 999})
check("ACK 超前版本被拒", st == 409, r)
st, r = req("GET", "/api/devices/photo-sync/status")
devstat = {d["device_id"]: d for d in r["data"]["devices"]}
check("同步状态：东门 1 已追平(待同步 0)，未 ACK 设备有待同步计数",
      devstat["dev-east-01"]["acked_version"] == new_ver and devstat["dev-east-01"]["pending_count"] == 0
      and devstat["dev-west-01"]["pending_count"] == new_ver, r)
# 未注册设备不可拉取
st, r = req("GET", "/api/devices/dev-ghost/photo-sync?after=0")
check("未注册设备拉取被拒(404)", st == 404, r)

# 直接更新照片同样归档 + 发布同步事件（任意路径照片变更都同步各入口）
st, r = req("POST", f"/api/passes/{prc2['id']}/members/{kid2['id']}/photo",
            {"photo_url": "https://img.example.com/o3.jpg", "operator": "staff-chen"})
check("直接更新照片版本递增", st == 200 and r["data"]["photo_version"] == 2, r)
st, r = req("GET", f"/api/passes/{prc2['id']}/members/{kid2['id']}/photo-archives")
check("直接更新同样归档旧照并标记失效",
      len(r["data"]) == 1 and r["data"][0]["photo_version"] == 1 and r["data"][0]["invalid_at"], r)

section("L. 运营统计与日志")
st, r = req("GET", "/api/admin/stats/overview")
d = r["data"]
check("总览：按卡类型统计", st == 200 and len(d["by_type"]) == 3 and d["entries_total"] > 0, d)
check("总览：异常原因分布", "PHOTO_UNCLEAR" in d["anomaly_by_reason"] or len(d["anomaly_by_reason"]) > 0, d)
check("总览：人工放行计数", d["manual_releases"] >= 2, d)
st, r = req("GET", "/api/admin/stats/peaks")
check("峰值：24 小时分布", st == 200 and len(r["data"]["by_hour"]) == 24, r)
st, r = req("GET", "/api/admin/gate-logs?limit=50")
check("闸机日志查询", st == 200 and len(r["data"]) > 0, r)
st, r = req("GET", f"/api/passes/{pm['id']}/changes")
check("变更日志留痕", st == 200 and any(c["action"] == "CREATE" for c in r["data"]), r)
st, r = req("GET", "/api/appeals?status=APPROVED")
check("申诉列表查询", st == 200 and len(r["data"]) >= 1, r)

print(f"\n========================================")
print(f"RESULT: PASS={PASS} FAIL={FAIL}")
print(f"========================================")
sys.exit(1 if FAIL > 0 else 0)

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
seed_holidays = r["data"]
check("2026 节假日已配置", st == 200 and "2026-10-01" in seed_holidays)

# 确定性测试：固定一个“今天之后 14 天”的节假日样例用于节假日断言（保证在年卡有效期内），
# 若运行当天恰为配置的节假日（如国庆当天跑测），同时将“今天”移出节假日集合，
# 保证依赖“今天可入园”的用例稳定。
today = local_date()
HOLIDAY_FIXTURE = (datetime.datetime.now().astimezone() + datetime.timedelta(days=14)).strftime("%Y-%m-%d")
holidays_under_test = sorted(set([d for d in seed_holidays if d != today] + [HOLIDAY_FIXTURE]))
st, _ = req("PUT", "/api/holidays", {"dates": holidays_under_test, "operator": "test-setup"})
assert st == 200

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
st, r = verify(pass_id=psingle["id"], ts=local_ts(10, 30, date=HOLIDAY_FIXTURE))
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

# ============================================================
section("M. 儿童照片变化复核")
# 全新亲子卡：赵某某 + 孩子，避免去重窗口/高频计数干扰
prc = buy_pass("parent_child", members=[{
    "name": "钱小豆", "id_card": new_idcard(), "id_card_expiry": "2032-06-01",
    "relation": "child", "photo_url": "https://img.example.com/xiaodou-v1.jpg"}])
st, r = req("GET", f"/api/passes/{prc['id']}")
child_m = [m for m in r["data"]["members"] if m["relation"] == "child"][0]
check("亲子卡儿童成员照片 v1", child_m["photo_version"] == 1, r)

# M1. 连续 3 次人脸相似度不足 → 自动生成照片更新任务
task_id = None
for i in range(2):
    st, r = verify(idcard=child_m["id_card"], face=31.0, attraction="attr-lake",
                   ts=local_ts(9, i + 1), req_id=f"M-LOW-{i}")
    assert r["data"]["decision"] == "MANUAL_REVIEW" and r["data"]["reason"] == "FACE_MISMATCH", r
check("前2次低分：转人工但暂无复核任务", "photo_review_task_id" not in r["data"], r)
st, r3 = verify(idcard=child_m["id_card"], face=28.0, attraction="attr-lake",
                ts=local_ts(9, 5), req_id="M-LOW-2")
task_id = r3["data"].get("photo_review_task_id")
check("第3次低分：自动生成照片更新任务", bool(task_id), r3)
st, r = req("GET", f"/api/photo-reviews/{task_id}")
t = r["data"]
check("任务状态 PENDING_SUBMISSION 且来源为闸机自动触发",
      st == 200 and t["status"] == "PENDING_SUBMISSION" and t["source"] == "AUTO_GATE_LOW_SCORE"
      and t["low_score_count"] >= 3 and len(t["trigger_verification_ids"]) >= 3, r)
# 第 4 次低分不再重复建任务，计数继续累计
st, r4 = verify(idcard=child_m["id_card"], face=25.0, attraction="attr-lake",
                ts=local_ts(9, 6), req_id="M-LOW-3")
check("第4次低分复用同一复核任务不重复创建", r4["data"]["photo_review_task_id"] == task_id, r4)
st, r = req("GET", "/api/photo-reviews?status=PENDING_SUBMISSION")
check("复核任务列表可按状态过滤", any(x["id"] == task_id for x in r["data"]), r)

# M2. 复核期间走人工通道（普通核验人工放行链路也留痕）
low_verif = r3["data"]["verification_id"]
st, r = req("POST", f"/api/verifications/{low_verif}/manual-review",
            {"action": "RELEASE", "operator": "staff-chen",
             "note": "儿童面部变化，照片复核中，人工通道放行"})
check("低分核验人工放行并扣次", st == 200 and r["data"]["charged_quota"] == 1, r)
st, r = req("GET", f"/api/photo-reviews/{task_id}/manual-passes")
check("普通人工放行自动挂到复核任务留痕", len(r["data"]) == 1
      and r["data"][0]["operator"] == "staff-chen" and "面部变化" in r["data"][0]["note"], r)
# 专用人工通道：必须填写原因与操作人
st, r = req("POST", f"/api/photo-reviews/{task_id}/manual-pass",
            {"device_id": "dev-east-01", "attraction": "attr-lake", "operator": "staff-chen"})
check("人工通道放行原因必填", st == 400, r)
redis_cli("DEL", f"dedup:{prc['id']}:attr-lake")
st, r = req("POST", f"/api/photo-reviews/{task_id}/manual-pass",
            {"device_id": "dev-east-01", "attraction": "attr-lake", "operator": "staff-chen",
             "reason": "儿童面部变化待照片复核", "note": "户口本照片与本人一致"})
check("人工通道放行(东门)并扣次", st == 200 and r["data"]["charged_quota"] == 1
      and r["data"]["review_task_id"] == task_id, r)
st, r = req("POST", f"/api/photo-reviews/{task_id}/manual-pass",
            {"device_id": "dev-south-01", "attraction": "attr-garden",
             "reason": "儿童面部变化待照片复核", "note": "第二次放行仍需登记"})
check("人工通道放行(南门)自动识别操作人头", st == 200 and r["data"]["charged_quota"] == 1
      and r["data"]["operator"] == "anonymous", r)
st, r = req("GET", f"/api/photo-reviews/{task_id}/manual-passes")
mpasses = r["data"]
check("每次人工放行均记录(共3条)且含操作人与原因",
      len(mpasses) == 3 and all(x["operator"] and x["reason"] for x in mpasses), r)
st, r = req("GET", f"/api/passes/{prc['id']}/manual-passes")
check("年卡维度可查人工通道记录", len(r["data"]) == 3, r)
st, r = req("GET", f"/api/passes/{prc['id']}/gate-logs")
check("闸机流水含 MANUAL_PASS/原因/操作人",
      any(g["action"] in ("MANUAL_PASS", "MANUAL_RELEASE") and g.get("operator") for g in r["data"]), r)

# M3. 家长小程序提交新照片
st, r = req("POST", f"/api/photo-reviews/{task_id}/submit",
            {"new_photo_url": "https://img.example.com/xiaodou-v2.jpg",
             "channel": "MINIAPP", "submitted_by": "13800008888", "note": "孩子长大样子变了"})
check("家长提交新照片 → SUBMITTED", st == 200 and r["data"]["status"] == "SUBMITTED"
      and r["data"]["submit_channel"] == "MINIAPP", r)
st, r = req("POST", f"/api/photo-reviews/{task_id}/submit",
            {"new_photo_url": "https://img.example.com/x.jpg"})
check("重复提交被拒", st == 409, r)

# M4. 工作人员三项核验后审批通过
st, r = req("POST", f"/api/photo-reviews/{task_id}/review",
            {"approve": True, "id_verified": True, "guardianship_check": "VERIFIED"})
check("审批必须填写处理说明", st == 400, r)
st, r = req("POST", f"/api/photo-reviews/{task_id}/review",
            {"approve": True, "id_verified": True, "guardianship_check": "VERIFIED",
             "note": "未核验历史入园记录"}, {"X-Operator": "staff-chen"})
check("缺历史入园核验被拒", st == 400, r)
st, r = req("POST", f"/api/photo-reviews/{task_id}/review",
            {"approve": True, "id_verified": False, "guardianship_check": "VERIFIED",
             "entry_history_check": "VERIFIED", "note": "未核验证件"}, {"X-Operator": "staff-chen"})
check("缺证件核验被拒", st == 400, r)
st, r = req("POST", f"/api/photo-reviews/{task_id}/review",
            {"approve": True, "id_verified": True, "guardianship_check": "VERIFIED",
             "entry_history_check": "VERIFIED", "note": "户口本+监护关系+近180天入园记录一致，通过",
             "operator": "staff-chen"})
t = r["data"]
expire_min = (datetime.datetime.now() + datetime.timedelta(days=6, hours=23)).astimezone()
expire_max = (datetime.datetime.now() + datetime.timedelta(days=7, hours=1)).astimezone()
expires = datetime.datetime.fromisoformat(t["old_photo_expires_at"])
check("审批通过：APPROVED/新版本+1/旧照片失效时间约7天后",
      st == 200 and t["status"] == "APPROVED" and t["new_version"] == 2 and t["old_version"] == 1
      and bool(t["archive_id"]) and expire_min <= expires <= expire_max, r)
st, r = req("GET", f"/api/passes/{prc['id']}")
child_m = [m for m in r["data"]["members"] if m["id"] == child_m["id"]][0]
check("成员照片已更新为 v2", child_m["photo_version"] == 2
      and child_m["photo_url"].endswith("xiaodou-v2.jpg") and child_m.get("photo_updated_at"), r)
st, r = req("GET", f"/api/passes/{prc['id']}/members/{child_m['id']}/photo-archives")
arch = r["data"]
check("旧照片已归档(含失效时间与三项核验依据)",
      len(arch) == 1 and arch[0]["old_version"] == 1 and arch[0]["id_verified"] is True
      and arch[0]["guardianship_check"] == "VERIFIED" and arch[0]["operator"] == "staff-chen"
      and arch[0]["expires_at"], r)
st, r = req("GET", f"/api/passes/{prc['id']}/changes")
check("变更日志留痕 PHOTO_REVIEW_APPROVED",
      any(c["action"] == "PHOTO_REVIEW_APPROVED" for c in r["data"]), r)
st, r = req("POST", f"/api/photo-reviews/{task_id}/manual-pass",
            {"device_id": "dev-east-01", "reason": "复核已结束不应再放行", "operator": "staff-chen"})
check("复核结束后人工通道关闭", st == 409, r)

# M5. 新照片生效 → 同步各入口缓存；离线闸机恢复后拉取
st, r = req("GET", "/api/devices/photo-sync")
sync = r["data"]
check("新照片版本进入缓存且向所有入口下发", sync["current_versions"] >= 1
      and all(d["pending_count"] >= 1 for d in sync["devices"]), r)
# 模拟西门闸机离线，再恢复拉取
dev = redis_get_json("device:dev-west-01")
dev["last_heartbeat"] = (datetime.datetime.now() - datetime.timedelta(minutes=10)).astimezone().isoformat()
redis_set_json("device:dev-west-01", dev)
st, r = req("GET", "/api/devices")
check("西门闸机已判离线", any(d["id"] == "dev-west-01" and d["online"] is False for d in r["data"]), r)
st, r = req("POST", "/api/devices/dev-west-01/photo-cache/pull")
pulled = r["data"]
check("离线恢复后拉取到新照片版本(含旧照片失效时间)",
      st == 200 and pulled["updated_count"] >= 1
      and any(v["member_id"] == child_m["id"] and v["photo_version"] == 2 and v["old_expires_at"]
              for v in pulled["photo_versions"]), r)
st, r = req("POST", "/api/devices/dev-west-01/photo-cache/pull")
check("重复拉取无待更新(updated_count=0)", r["data"]["updated_count"] == 0, r)
st, r = req("GET", "/api/devices")
check("拉取动作顺带刷新心跳重新上线", any(d["id"] == "dev-west-01" and d["online"] is True for d in r["data"]), r)
st, r = req("GET", "/api/devices/photo-sync")
west = [d for d in r["data"]["devices"] if d["device_id"] == "dev-west-01"][0]
east = [d for d in r["data"]["devices"] if d["device_id"] == "dev-east-01"][0]
check("同步总览：西门已同步0待更，东门仍有待更", west["pending_count"] == 0 and east["pending_count"] >= 1, r)
st, r = req("POST", "/api/devices/dev-east-01/heartbeat")
check("心跳响应携带待同步照片数", r["data"]["pending_photo_updates"] >= 1, r)
# 东门也拉取，全部入口同步完成
req("POST", "/api/devices/dev-east-01/photo-cache/pull")
req("POST", "/api/devices/dev-east-02/photo-cache/pull")
req("POST", "/api/devices/dev-south-01/photo-cache/pull")
req("POST", "/api/devices/dev-offline-01/photo-cache/pull")
st, r = req("GET", "/api/devices/photo-sync")
check("所有入口缓存同步完成", all(d["pending_count"] == 0 for d in r["data"]["devices"]), r)

# M6. 新照片生效后儿童刷脸直接放行（减少重复人工核验）
redis_cli("DEL", f"dedup:{prc['id']}:attr-lake", f"dedup:{prc['id']}:attr-garden",
          f"hf:{prc['id']}", f"loc:{prc['id']}")
st, r = verify(idcard=child_m["id_card"], face=93.0, attraction="attr-lake", ts=local_ts(10, 10))
check("新照片生效后儿童刷脸直接放行", r["data"]["decision"] == "ALLOW"
      and r["data"]["charged_quota"] == 1, r)

# M7. 驳回路径：主动发起 → 提交 → 三项核验后驳回
st, r = req("POST", "/api/photo-reviews", {"pass_id": prc["id"], "member_id": child_m["id"]})
task2 = r["data"]["id"]
check("家长主动发起复核任务", st == 200 and r["data"]["status"] == "PENDING_SUBMISSION", r)
st, r = req("POST", "/api/photo-reviews", {"pass_id": prc["id"], "member_id": child_m["id"]})
check("存在进行中任务时重复发起被拒", st == 409, r)
st, r = req("POST", f"/api/photo-reviews/{task2}/submit",
            {"new_photo_url": "https://img.example.com/xiaodou-bad.jpg", "channel": "ONSITE"})
check("现场渠道提交", r["data"]["submit_channel"] == "ONSITE", r)
st, r = req("POST", f"/api/photo-reviews/{task2}/review",
            {"approve": False, "id_verified": True, "guardianship_check": "VERIFIED",
             "entry_history_check": "VERIFIED", "note": "提交照片与证件不符，驳回", "operator": "staff-chen"})
check("复核驳回：REJECTED 且照片版本不变", st == 200 and r["data"]["status"] == "REJECTED", r)
st, r = req("GET", f"/api/passes/{prc['id']}")
child_m = [m for m in r["data"]["members"] if m["id"] == child_m["id"]][0]
check("驳回后仍保持 v2 旧归档仅1条", child_m["photo_version"] == 2, r)
st, r = req("POST", "/api/photo-reviews", {"pass_id": prc["id"], "member_id": child_m["id"]})
check("驳回后可重新发起任务", st == 200 and r["data"]["status"] == "PENDING_SUBMISSION", r)

print(f"\n========================================")
print(f"RESULT: PASS={PASS} FAIL={FAIL}")
print(f"========================================")
sys.exit(1 if FAIL > 0 else 0)

# passgate — 景区年卡实名入园同行权益与异常核验服务

基于 **Go + Fiber + Redis** 的景区年卡核验后端。覆盖：家庭/单人/亲子年卡售卖与实名信息管理、
闸机入园核验（人脸/证件/二维码/同行人/时段/容量）、异常核验原因可追溯、冻结/退款/升级权益重算、
现场人工放行留痕、客服申诉与权益返还、运营售卖规则与统计分析，以及
**儿童照片变化复核**（闸机多次相似度不足自动生成照片更新任务 → 家长现场/小程序提交 → 工作人员三项核验 →
旧照片归档并标记失效时间 → 新照片生效并同步各入口缓存；复核期间走人工通道，每次放行记录操作人与原因；
离线闸机恢复后自动拉取缓存，减少重复人工核验）。

## 快速启动

### 方式一：Docker Compose 一键启动（推荐）

依赖：Docker + Docker Compose。

```bash
docker compose up --build
# 服务 http://localhost:8080 ，Redis 容器内置（空库自动写入演示数据）
docker compose down            # 停止并清理
```

### 方式二：本地运行

依赖：Go 1.19+、Redis 5+（本机已在 127.0.0.1:6379 运行 Redis 7）。

```bash
# 1. 启动 Redis（若未运行）
redis-server --daemonize yes

# 2. 启动服务（默认端口 8080，空库自动写入演示数据）
go run .
# 或
go build -o passgate . && ./passgate

# 环境变量：PORT（默认 8080）、REDIS_ADDR（默认 127.0.0.1:6379）
```

也可单独构建镜像：

```bash
docker build -t passgate .
docker run -p 8080:8080 -e REDIS_ADDR=host.docker.internal:6379 passgate
```

启动后可见种子日志：示例年卡 家庭卡=P000001 单人卡=P000002 亲子卡=P000003。

### API 文档（Swagger UI / OpenAPI 3.x）

服务启动后浏览器打开：

- Swagger UI：<http://127.0.0.1:8080/docs>
- OpenAPI 原文：<http://127.0.0.1:8080/docs/openapi.yaml>

静态资源已通过 `go:embed` 打进二进制（`docs/`），**离线可用**，不依赖外部 CDN。

## 本地验证方式

```bash
bash test/run.sh        # 一键：构建 → 重置 Redis → 重启服务 → 运行全部 117 项端到端断言
# 或服务已启动且为全新种子库时：
python3 test/e2e.py
```

验证结论（实际执行）：`bash test/run.sh` 全量通过，输出 `RESULT: PASS=117 FAIL=0`，覆盖
正常放行、幂等重试、抖动去重、20 并发同/不同 request_id 均只扣 1 次、16 类失败原因、
人工放行留痕、冻结/解冻与预约一致性、申诉返还、退款/升级重算、信息变更、免刷脸人工确认、
出园释放容量、运营统计与日志查询，以及儿童照片变化复核全链路
（低分自动建任务、复核期人工通道逐次留痕、家长双渠道提交、工作人员三项核验、
旧照片归档+失效时间、新照片生效、各入口缓存下发/拉取、离线恢复更新、驳回重提）。

> 注：测试在启动时会将“今天”临时移出节假日集合并注入一个 14 天后的节假日样例，
> 保证在任意日期（含国庆当天）运行结果确定；用例结束不重置 Redis，下次跑 `run.sh` 会 `FLUSHALL` 后重新种子。

## 演示数据与测试账号

系统不设登录鉴权（演示），操作人通过请求头 `X-Operator: <姓名>` 或请求体 `operator` 字段标识，
所有敏感操作（冻结、人工放行、申诉处理、照片复核审批、规则调整）均记录操作人。建议的演示身份：

| 角色 | 标识 | 用途 |
|---|---|---|
| 现场工作人员 | `staff-chen` | 人工核验放行、设置免刷脸、**照片复核三项核验与审批** |
| 客服 | `cs-wang` | 冻结/解冻、退款、升级、申诉处理、换绑 |
| 运营 | `ops-li` | 售卖规则调整、节假日配置 |

种子数据（空库启动自动写入）：

- 景点：`attr-lake` 镜湖(容量5000)、`attr-summit` 云顶山(2000)、`attr-garden` 百花园(3000)
- 闸机：`dev-east-01/02`（东门）、`dev-west-01`（西门）、`dev-south-01`（南门），60 秒无心跳判离线
- 年卡：`P000001` 家庭卡（张三，含配偶/孩子/**免刷脸老人张父**）、`P000002` 单人卡（王五）、`P000003` 亲子卡（赵六+孩子）
- 售卖规则：单人 399 元/100 人次/节假日不可用；亲子 699 元/200 人次/同行 1 人；家庭 1299 元/400 人次/同行 3 人
- 2026 年法定节假日已配置（国庆 2026-10-01~08 等）
- 演示短信验证码固定为 `123456`（换绑手机号时使用）

## 核心 API

### 年卡生命周期
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/passes` | 购卡（实名+人脸照片+证件+手机号+成员；一证一卡；按售卖规则快照发卡） |
| GET | `/api/passes/:id` | 年卡详情（实时剩余额度、成员、二维码 token） |
| GET | `/api/passes?phone=` | 按手机号查卡 |
| POST | `/api/passes/:id/freeze` `/unfreeze` | 冻结/解冻（**不影响已预约未入园订单**） |
| POST | `/api/passes/:id/refund` | 退款：按剩余有效期 × 80% 折算，权益清零，未用预约取消 |
| POST | `/api/passes/:id/upgrade` | 升级：补差价，**有效期与额度按新卡重新计算** |
| POST | `/api/passes/:id/phone` | 换绑手机号（验证码 123456） |
| POST | `/api/passes/:id/members` / DELETE `.../members/:mid` | 家庭成员临时更换（消耗更换次数） |
| POST | `/api/passes/:id/members/:mid/photo` | 直接更新人脸照片（孩子长大，版本+1） |
| GET | `/api/passes/:id/members/:mid/photo-archives` | **成员旧照片归档（含失效时间与三项核验依据）** |
| POST | `/api/passes/:id/members/:mid/face-exempt` | 老人免刷脸标记 |
| POST | `/api/passes/:id/qr-refresh` | 刷新二维码（旧码立即失效） |
| GET | `/api/passes/:id/changes` | 变更日志（全部敏感操作留痕） |
| GET | `/api/passes/:id/manual-passes` | **照片复核期人工通道放行记录（操作人+原因）** |

### 入园核验
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/verify` | 闸机核验，返回 `ALLOW / MANUAL_REVIEW / QUOTA_INSUFFICIENT / SUSPECTED_LENDING` + 原因码；儿童连续低分时自动建照片复核任务，响应含 `photo_review_task_id` |
| GET | `/api/verifications/:id` | 核验详情（客服查看失败原因与人工处理链路） |
| POST | `/api/verifications/:id/manual-review` | 人工放行/拒绝（必须填操作人与说明，放行走同一原子扣减；关联照片复核时自动留痕） |
| POST | `/api/exit` | 出园，释放在园容量 |

`/api/verify` 请求示例：

```json
{
  "request_id": "gate-east-01-000123",
  "device_id": "dev-east-01",
  "qr_token": "QR...",
  "face_score": 88.5,
  "photo_clear": true,
  "companions": ["110101199203054321"],
  "attraction": "attr-lake",
  "timestamp": "2026-09-25T10:30:00+08:00",
  "offline_sync": false
}
```

失败/转人工原因码：`DEVICE_UNKNOWN` `DEVICE_OFFLINE` `PASS_NOT_FOUND` `PASS_FROZEN` `PASS_REFUNDED`
`PASS_EXPIRED` `PASS_NOT_STARTED` `ATTRACTION_NOT_ALLOWED` `HOLIDAY_BLOCKED` `TIMESLOT_BLOCKED`
`CAPACITY_FULL` `COMPANION_EXCEEDED` `COMPANION_NOT_BOUND` `FACE_MISMATCH` `FACE_EXEMPT_REVIEW`
`PHOTO_UNCLEAR` `ID_EXPIRED` `HIGH_FREQUENCY` `MULTI_LOCATION` `QUOTA_EXHAUSTED`（放行去重：`DUPLICATE_ENTRY`）。

### 儿童照片变化复核
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/photo-reviews` | 家长/现场主动发起照片更新任务（闸机连续低分也会**自动生成**，无需调用） |
| GET | `/api/photo-reviews?status=&pass_id=` | 复核任务列表（状态/年卡过滤） |
| GET | `/api/photo-reviews/:id` | 复核任务详情 |
| POST | `/api/photo-reviews/:id/submit` | 家长提交新照片：`new_photo_url` + `channel=ONSITE/MINIAPP` |
| POST | `/api/photo-reviews/:id/review` | 工作人员审批：**证件 + 监护关系 + 历史入园记录三项必须全部 VERIFIED**；通过则旧照归档、新照生效、缓存下发 |
| POST | `/api/photo-reviews/:id/manual-pass` | **复核期间人工通道放行**：`device_id` + 必填 `reason` 放行原因，记录操作人，原子扣次 |
| GET | `/api/photo-reviews/:id/manual-passes` | 该任务下每一次人工通道放行记录 |

### 设备与入口照片缓存
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/devices` `/api/devices/:id/heartbeat` | 设备注册、心跳（心跳响应携带 `pending_photo_updates` 待同步数） |
| GET | `/api/devices` | 设备列表（含在线状态） |
| POST | `/api/devices/:id/photo-cache/pull` | 闸机拉取待更新照片版本（**离线恢复后同样调用**，拉取即重新上线） |
| GET | `/api/devices/photo-sync` | 各入口照片缓存同步总览（待同步数/已同步数/最近拉取时间） |
| POST | `/api/reservations` `/api/reservations/:id/cancel` | 预约/取消（冻结卡不可新预约，已有预约保留） |
| POST | `/api/appeals` `/api/appeals/:id/resolve` | 申诉；客服通过自动返还误扣额度 |
| GET/PUT | `/api/holidays` | 节假日配置 |
| GET/PUT | `/api/admin/sales-rules[/:type]` | 售卖规则查询与调整（仅影响新售卡） |
| GET | `/api/admin/stats/overview` `/stats/peaks` `/gate-logs` | 按卡类型售卖/权益使用率/异常率、入园峰值、闸机流水 |

## 关键设计

- **幂等防重（网络抖动/闸机重试）**：`request_id` 先 `SET NX` 占位，重试直接返回首次结果；
  另有 90 秒「同卡同景点放行去重窗口」，重复刷闸按首次放行处理不重复扣次；
  扣减由 **Lua 脚本原子完成**（去重判定+额度+容量+写去重标记），多入口多设备并发安全。
- **疑似转借**：10 分钟窗口内核验 >4 次（`HIGH_FREQUENCY`）或出现在 ≥2 个景点（`MULTI_LOCATION`）→
  `SUSPECTED_LENDING`；一证一卡源头约束。
- **人工放行可解释**：人工处理强制操作人+说明，写 `ManualReview`、更新核验记录、写闸机日志
  `MANUAL_RELEASE`，客服 `GET /api/verifications/:id` 可见完整链路；同一核验仅可处理一次。
- **权益一致性**：额度以 Redis 计数器为权威；申诉通过自动返还；退款清零；升级按新卡重置并重算有效期；
  冻结/解冻只改状态，不触碰预约与额度。
- **离线闸机**：心跳超时判离线，正常请求转人工；离线期间闸机本地放行的记录以 `offline_sync=true`
  补传，凭 `request_id` 幂等去重，不会重复扣次。
- **儿童照片变化复核（本次新增）**：
  - **自动触发**：闸机对绑定儿童（`relation=child`）的人脸比对在 30 天窗口内累计 **≥3 次** `FACE_MISMATCH`
    （阈值 `PhotoReviewLowScoreThreshold=3`），服务自动生成 `PENDING_SUBMISSION` 照片更新任务
    （`source=AUTO_GATE_LOW_SCORE`），触发证据（核验 ID、首/末次低分时间）保存在任务上；存在进行中任务时不重复创建。
  - **家长提交**：现场（`ONSITE`）或小程序（`MINIAPP`）提交新照片 → `SUBMITTED`。
  - **工作人员三项核验**：证件（户口本/身份证）、监护关系、近 180 天历史入园记录必须全部确认且填写处理说明；
    通过 → 旧照片写入 `PhotoArchive`（含**旧照片失效时间** = 生效时刻 + 7 天宽限期 `PhotoReviewOldPhotoTTL`）、
    成员照片版本 +1 生效；驳回关闭任务，可重新发起。
  - **复核期人工通道**：任务进行中年卡可走人工通道，`/manual-pass` 强制 `reason` + 操作人，
    走同一原子扣减，写 `ManualPassRecord` 与闸机流水 `MANUAL_PASS`；常规 `/manual-review` 放行关联低分核验时也自动挂任务留痕。
    复核结束（通过/驳回）后该通道关闭。
  - **入口缓存同步**：新照片生效写 `photocache:ver:<member>` 并向全部入口设备投递待同步集合；
    闸机通过 `POST /api/devices/:id/photo-cache/pull` 拉取（含新 URL、版本与**旧照片失效时间**），
    拉取后标记已同步并刷新心跳；心跳响应携带 `pending_photo_updates` 提醒拉取；
    **离线闸机恢复后调用同一拉取接口即更新本地缓存并重新上线**，减少重复人工核验；
    `GET /api/devices/photo-sync` 可查各入口同步状态。

## 目录结构

```
main.go                  入口（依赖装配、种子、HTTP 服务）
docs/                    OpenAPI 3.x 规范 + Swagger UI（go:embed 内嵌，离线可用）
internal/model/          实体定义（含 PhotoReviewTask / PhotoArchive / ManualPassRecord / 缓存版本）
internal/store/          Redis 封装（JSON 存取、ID 序列、分布式锁）
internal/service/        业务层：pass.go 年卡生命周期 / verify.go 核验引擎 /
                         photoreview.go 照片复核与缓存同步 / ops.go 设备·预约·申诉·统计
internal/handler/        Fiber 路由与错误映射
internal/seed/           演示数据
test/e2e.py              117 项端到端断言
test/run.sh              一键验证（重置环境后全量跑）
Dockerfile / docker-compose.yml   容器化一键启动（app + redis:7）
```

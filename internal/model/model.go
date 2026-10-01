package model

import "time"

// PassType 年卡类型
type PassType string

const (
	PassTypeSingle      PassType = "single"       // 单人年卡
	PassTypeFamily      PassType = "family"       // 家庭年卡
	PassTypeParentChild PassType = "parent_child" // 亲子年卡
)

// PassStatus 年卡状态
type PassStatus string

const (
	PassStatusActive   PassStatus = "active"
	PassStatusFrozen   PassStatus = "frozen"
	PassStatusRefunded PassStatus = "refunded"
)

// Decision 核验结论
type Decision string

const (
	DecisionAllow   Decision = "ALLOW"              // 放行
	DecisionManual  Decision = "MANUAL_REVIEW"      // 人工核验
	DecisionQuota   Decision = "QUOTA_INSUFFICIENT" // 权益不足
	DecisionSuspect Decision = "SUSPECTED_LENDING"  // 疑似转借
)

// 核验失败 / 转人工原因码
const (
	ReasonNone              = ""
	ReasonDeviceUnknown     = "DEVICE_UNKNOWN"
	ReasonDeviceOffline     = "DEVICE_OFFLINE"
	ReasonPassNotFound      = "PASS_NOT_FOUND"
	ReasonPassFrozen        = "PASS_FROZEN"
	ReasonPassRefunded      = "PASS_REFUNDED"
	ReasonPassExpired       = "PASS_EXPIRED"
	ReasonPassNotStarted    = "PASS_NOT_STARTED"
	ReasonAttractionDenied  = "ATTRACTION_NOT_ALLOWED"
	ReasonHolidayBlocked    = "HOLIDAY_BLOCKED"
	ReasonTimeslotBlocked   = "TIMESLOT_BLOCKED"
	ReasonCapacityFull      = "CAPACITY_FULL"
	ReasonCompanionExceeded = "COMPANION_EXCEEDED"
	ReasonCompanionNotBound = "COMPANION_NOT_BOUND"
	ReasonFaceMismatch      = "FACE_MISMATCH"
	ReasonFaceExemptReview  = "FACE_EXEMPT_REVIEW"
	ReasonPhotoUnclear      = "PHOTO_UNCLEAR"
	ReasonIDExpired         = "ID_EXPIRED"
	ReasonHighFrequency     = "HIGH_FREQUENCY"
	ReasonMultiLocation     = "MULTI_LOCATION"
	ReasonQuotaExhausted    = "QUOTA_EXHAUSTED"
	ReasonDuplicateEntry    = "DUPLICATE_ENTRY"      // 网络抖动重复请求，视为同一次入园
	ReasonPhotoRecheck      = "PHOTO_RECHECK_REVIEW" // 儿童照片变化复核期间，转人工通道
)

// Member 年卡成员（持卡人 + 同行成员）
type Member struct {
	ID             string    `json:"id"`
	PassID         string    `json:"pass_id"`
	Name           string    `json:"name"`
	IDCard         string    `json:"id_card"`
	IDCardExpiry   string    `json:"id_card_expiry"` // YYYY-MM-DD
	Role           string    `json:"role"`           // holder | companion
	Relation       string    `json:"relation"`       // self | spouse | child | parent | other
	PhotoURL       string    `json:"photo_url"`
	PhotoVersion   int       `json:"photo_version"`
	PhotoUpdatedAt time.Time `json:"photo_updated_at,omitempty"` // 当前版本照片生效时间
	FaceExempt     bool      `json:"face_exempt"`                // 老人等无法刷脸成员
	CreatedAt      time.Time `json:"created_at"`
}

// Pass 年卡
type Pass struct {
	ID               string     `json:"id"`
	Type             PassType   `json:"type"`
	Status           PassStatus `json:"status"`
	HolderName       string     `json:"holder_name"`
	HolderIDCard     string     `json:"holder_id_card"`
	Phone            string     `json:"phone"`
	ValidFrom        string     `json:"valid_from"`  // YYYY-MM-DD
	ValidUntil       string     `json:"valid_until"` // YYYY-MM-DD
	TotalQuota       int        `json:"total_quota"`
	CompanionLimit   int        `json:"companion_limit"`
	Attractions      []string   `json:"attractions"`
	HolidayBlocked   bool       `json:"holiday_blocked"`
	AllowedSlots     []string   `json:"allowed_slots"` // ["08:00-18:00"]
	MemberChangeLeft int        `json:"member_change_left"`
	PaidAmount       float64    `json:"paid_amount"`
	FreezeReason     string     `json:"freeze_reason,omitempty"`
	FrozenAt         *time.Time `json:"frozen_at,omitempty"`
	RefundedAt       *time.Time `json:"refunded_at,omitempty"`
	RefundAmount     float64    `json:"refund_amount,omitempty"`
	UpgradedFrom     string     `json:"upgraded_from,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// PassView 年卡详情响应（合并 Redis 计数器中的实时额度）
type PassView struct {
	Pass
	RemainingQuota int      `json:"remaining_quota"`
	UsedQuota      int      `json:"used_quota"`
	Members        []Member `json:"members"`
	QRToken        string   `json:"qr_token"`
}

// Verification 一次闸机核验记录（无论成败均落库，客服可查）
type Verification struct {
	ID           string    `json:"id"`
	RequestID    string    `json:"request_id"` // 闸机侧幂等键
	PassID       string    `json:"pass_id"`
	MemberID     string    `json:"member_id,omitempty"` // 本次主入园成员（儿童复核时定位任务）
	PhotoTaskID  string    `json:"photo_task_id,omitempty"`
	PassType     string    `json:"pass_type"`
	HolderName   string    `json:"holder_name"`
	DeviceID     string    `json:"device_id"`
	GateID       string    `json:"gate_id"`
	Attraction   string    `json:"attraction"`
	Decision     Decision  `json:"decision"`
	Reason       string    `json:"reason"`
	Detail       string    `json:"detail"` // 人类可读说明，客服解释用
	FaceScore    float64   `json:"face_score"`
	Companions   []string  `json:"companions"`
	ChargedQuota int       `json:"charged_quota"`
	DuplicateOf  string    `json:"duplicate_of,omitempty"`
	OfflineSync  bool      `json:"offline_sync"`
	ManualAction string    `json:"manual_action,omitempty"` // RELEASED | REJECTED
	CreatedAt    time.Time `json:"created_at"`
}

// ManualReview 现场人工处理记录（人工放行必须留痕，客服可解释）
type ManualReview struct {
	ID             string    `json:"id"`
	VerificationID string    `json:"verification_id"`
	PassID         string    `json:"pass_id"`
	Action         string    `json:"action"` // RELEASE | REJECT
	Operator       string    `json:"operator"`
	Note           string    `json:"note"`
	ChargedQuota   int       `json:"charged_quota"`
	CreatedAt      time.Time `json:"created_at"`
}

// GateLog 闸机流水
type GateLog struct {
	ID             string    `json:"id"`
	VerificationID string    `json:"verification_id"`
	PassID         string    `json:"pass_id"`
	DeviceID       string    `json:"device_id"`
	GateID         string    `json:"gate_id"`
	Attraction     string    `json:"attraction"`
	Direction      string    `json:"direction"` // ENTRY | EXIT
	Action         string    `json:"action"`    // ALLOW | DENY | MANUAL_RELEASE | EXIT
	Reason         string    `json:"reason"`
	QuotaCharged   int       `json:"quota_charged"`
	Operator       string    `json:"operator,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// Device 闸机设备
type Device struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	GateID        string    `json:"gate_id"`
	Attraction    string    `json:"attraction"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	RegisteredAt  time.Time `json:"registered_at"`
}

// Reservation 预约单（冻结/解冻不影响已预约未入园订单）
type Reservation struct {
	ID         string    `json:"id"`
	PassID     string    `json:"pass_id"`
	Attraction string    `json:"attraction"`
	Date       string    `json:"date"` // YYYY-MM-DD
	Slot       string    `json:"slot"`
	Status     string    `json:"status"` // BOOKED | USED | CANCELLED
	CreatedAt  time.Time `json:"created_at"`
}

// Appeal 客服申诉单
type Appeal struct {
	ID              string     `json:"id"`
	PassID          string     `json:"pass_id"`
	VerificationID  string     `json:"verification_id,omitempty"`
	Type            string     `json:"type"` // WRONG_DEDUCTION | VERIFY_FAIL | FREEZE_DISPUTE | OTHER
	Description     string     `json:"description"`
	Status          string     `json:"status"` // PENDING | APPROVED | REJECTED
	Resolution      string     `json:"resolution,omitempty"`
	CompensateQuota int        `json:"compensate_quota,omitempty"`
	Handler         string     `json:"handler,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
}

// Attraction 景点（容量配置）
type Attraction struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
}

// SalesRule 售卖规则（运营可调，仅影响新售卡；已售卡为快照）
type SalesRule struct {
	PassType          PassType  `json:"pass_type"`
	Price             float64   `json:"price"`
	TotalQuota        int       `json:"total_quota"`
	CompanionLimit    int       `json:"companion_limit"`
	Attractions       []string  `json:"attractions"`
	HolidayBlocked    bool      `json:"holiday_blocked"`
	AllowedSlots      []string  `json:"allowed_slots"`
	ValidDays         int       `json:"valid_days"`
	MemberChangeLimit int       `json:"member_change_limit"`
	OnSale            bool      `json:"on_sale"`
	DailyLimit        int       `json:"daily_limit"` // 每日限售数量，0 表示不限
	UpdatedAt         time.Time `json:"updated_at"`
	UpdatedBy         string    `json:"updated_by"`
}

// ChangeLog 年卡变更日志（换手机号、换成员、冻结、退款、升级等均留痕）
type ChangeLog struct {
	ID        string    `json:"id"`
	PassID    string    `json:"pass_id"`
	Action    string    `json:"action"`
	Detail    string    `json:"detail"`
	Operator  string    `json:"operator"`
	CreatedAt time.Time `json:"created_at"`
}

// 照片复核任务状态
const (
	PhotoTaskPendingSubmission = "PENDING_SUBMISSION" // 服务已建任务，等待家长提交新照片（现场或小程序）
	PhotoTaskPendingReview     = "PENDING_REVIEW"     // 新照片已提交，等待工作人员核验
	PhotoTaskApproved          = "APPROVED"           // 核验通过：旧照归档、新照生效
	PhotoTaskRejected          = "REJECTED"           // 核验不通过（证件/监护关系/历史入园存疑）
	PhotoTaskCancelled         = "CANCELLED"          // 任务取消（重复任务合并/家长放弃）
)

// PhotoUpdateTask 儿童照片变化复核任务
//
// 触发：亲子年卡绑定儿童在 FaceRecheckWindow 内多次人脸比对不足（FACE_MISMATCH），
// 由服务自动生成；也允许客服/工作人员手工创建。
// 复核期间年卡可走人工通道，每次放行均由 PhotoRecheckRelease 留痕（操作人+原因）。
type PhotoUpdateTask struct {
	ID                    string    `json:"id"`
	PassID                string    `json:"pass_id"`
	MemberID              string    `json:"member_id"`
	MemberName            string    `json:"member_name"`
	PassType              PassType  `json:"pass_type"`
	Status                string    `json:"status"`
	Source                string    `json:"source"` // AUTO_GATE | MANUAL
	TriggerDeviceID       string    `json:"trigger_device_id,omitempty"`
	TriggerVerificationID string    `json:"trigger_verification_id,omitempty"`
	FailCount             int       `json:"fail_count"`        // 窗口内累计人脸不足次数
	WindowStartedAt       time.Time `json:"window_started_at"` // 首次人脸不足时间
	LastFailAt            time.Time `json:"last_fail_at"`
	// 家长提交
	NewPhotoURL    string     `json:"new_photo_url,omitempty"`
	SubmittedAt    *time.Time `json:"submitted_at,omitempty"`
	SubmitChannel  string     `json:"submit_channel,omitempty"` // ONSITE | MINIAPP
	SubmitOperator string     `json:"submit_operator,omitempty"`
	// 工作人员核验三项
	IDChecked         bool       `json:"id_checked"`                    // 证件核验
	GuardianChecked   bool       `json:"guardian_checked"`              // 监护关系核验
	HistoryChecked    bool       `json:"history_checked"`               // 历史入园记录核验
	HistoryEntryCount int        `json:"history_entry_count,omitempty"` // 核验时引用的历史入园次数
	ReviewOperator    string     `json:"review_operator,omitempty"`
	ReviewNote        string     `json:"review_note,omitempty"`
	ReviewedAt        *time.Time `json:"reviewed_at,omitempty"`
	// 归档/生效
	ArchivedPhotoID   string     `json:"archived_photo_id,omitempty"`
	OldPhotoVersion   int        `json:"old_photo_version,omitempty"`
	NewPhotoVersion   int        `json:"new_photo_version,omitempty"`
	ApprovedAt        *time.Time `json:"approved_at,omitempty"`
	OldPhotoInvalidAt time.Time  `json:"old_photo_invalid_at,omitempty"` // 旧照片失效时间
	SyncVersion       int64      `json:"sync_version,omitempty"`         // 对应的照片缓存同步事件版本
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// PhotoArchive 旧照片归档记录（只归档不删除，供稽核回溯）
type PhotoArchive struct {
	ID            string    `json:"id"`
	PassID        string    `json:"pass_id"`
	MemberID      string    `json:"member_id"`
	MemberName    string    `json:"member_name"`
	PhotoURL      string    `json:"photo_url"`
	PhotoVersion  int       `json:"photo_version"`
	EffectiveFrom time.Time `json:"effective_from"`
	InvalidAt     time.Time `json:"invalid_at"` // 旧照片失效时间
	TaskID        string    `json:"task_id"`
	ArchivedAt    time.Time `json:"archived_at"`
	ArchivedBy    string    `json:"archived_by"`
}

// PhotoRecheckRelease 复核期间人工通道放行留痕（每次放行记录操作人和原因）
type PhotoRecheckRelease struct {
	ID             string    `json:"id"`
	TaskID         string    `json:"task_id"`
	VerificationID string    `json:"verification_id"`
	PassID         string    `json:"pass_id"`
	MemberID       string    `json:"member_id"`
	DeviceID       string    `json:"device_id"`
	GateID         string    `json:"gate_id"`
	Attraction     string    `json:"attraction"`
	Operator       string    `json:"operator"`
	Reason         string    `json:"reason"` // FACE_CHANGED_DURING_RECHECK 等人工放行原因
	Note           string    `json:"note"`
	CreatedAt      time.Time `json:"created_at"`
}

// PhotoSyncEvent 照片变更缓存同步事件（各入口闸机按版本顺序追拉）
type PhotoSyncEvent struct {
	Version           int64     `json:"version"`
	PassID            string    `json:"pass_id"`
	MemberID          string    `json:"member_id"`
	MemberName        string    `json:"member_name"`
	PhotoURL          string    `json:"photo_url"`
	PhotoVersion      int       `json:"photo_version"`
	OldPhotoURL       string    `json:"old_photo_url,omitempty"`
	OldPhotoInvalidAt time.Time `json:"old_photo_invalid_at,omitempty"`
	TaskID            string    `json:"task_id,omitempty"`
	Type              string    `json:"type"` // PHOTO_UPDATED
	CreatedAt         time.Time `json:"created_at"`
}

// DeviceSyncView 设备同步状态
type DeviceSyncView struct {
	DeviceID     string     `json:"device_id"`
	Online       bool       `json:"online"`
	AckedVersion int64      `json:"acked_version"`
	PendingCount int        `json:"pending_count"` // 当前全局版本 - 已 ACK 版本
	LastAckAt    *time.Time `json:"last_ack_at,omitempty"`
	LastPullAt   *time.Time `json:"last_pull_at,omitempty"`
}

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
	ReasonDuplicateEntry    = "DUPLICATE_ENTRY" // 网络抖动重复请求，视为同一次入园
)

// Member 年卡成员（持卡人 + 同行成员）
type Member struct {
	ID             string     `json:"id"`
	PassID         string     `json:"pass_id"`
	Name           string     `json:"name"`
	IDCard         string     `json:"id_card"`
	IDCardExpiry   string     `json:"id_card_expiry"` // YYYY-MM-DD
	Role           string     `json:"role"`           // holder | companion
	Relation       string     `json:"relation"`       // self | spouse | child | parent | other
	PhotoURL       string     `json:"photo_url"`
	PhotoVersion   int        `json:"photo_version"`
	PhotoUpdatedAt *time.Time `json:"photo_updated_at,omitempty"` // 当前照片拍摄/生效时间
	FaceExempt     bool       `json:"face_exempt"`                // 老人等无法刷脸成员
	CreatedAt      time.Time  `json:"created_at"`
}

// PhotoArchive 旧照片归档（保留可追溯：照片地址、版本、归档原因、核验依据）
type PhotoArchive struct {
	ID           string    `json:"id"`
	MemberID     string    `json:"member_id"`
	PassID       string    `json:"pass_id"`
	Name         string    `json:"name"`
	OldPhotoURL  string    `json:"old_photo_url"`
	OldVersion   int       `json:"old_version"`
	Reason       string    `json:"reason"` // PHOTO_REVIEW_APPROVED | MANUAL_UPDATE
	ReviewTaskID string    `json:"review_task_id,omitempty"`
	ArchivedAt   time.Time `json:"archived_at"`
	ExpiresAt    time.Time `json:"expires_at"` // 旧照片失效时间（之后各入口不得再用于比对）
	// 工作人员复核三项依据
	IDVerified        bool   `json:"id_verified"`
	GuardianshipCheck string `json:"guardianship_check"`  // VERIFIED
	EntryHistoryCheck string `json:"entry_history_check"` // VERIFIED
	Operator          string `json:"operator"`
	Note              string `json:"note"`
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
	ID                string    `json:"id"`
	RequestID         string    `json:"request_id"` // 闸机侧幂等键
	PassID            string    `json:"pass_id"`
	PassType          string    `json:"pass_type"`
	HolderName        string    `json:"holder_name"`
	PrincipalMemberID string    `json:"principal_member_id,omitempty"` // 本次实际刷脸/刷证件的成员
	PrincipalName     string    `json:"principal_name,omitempty"`
	DeviceID          string    `json:"device_id"`
	GateID            string    `json:"gate_id"`
	Attraction        string    `json:"attraction"`
	Decision          Decision  `json:"decision"`
	Reason            string    `json:"reason"`
	Detail            string    `json:"detail"` // 人类可读说明，客服解释用
	FaceScore         float64   `json:"face_score"`
	Companions        []string  `json:"companions"`
	ChargedQuota      int       `json:"charged_quota"`
	DuplicateOf       string    `json:"duplicate_of,omitempty"`
	OfflineSync       bool      `json:"offline_sync"`
	ManualAction      string    `json:"manual_action,omitempty"`        // RELEASED | REJECTED
	PhotoReviewTaskID string    `json:"photo_review_task_id,omitempty"` // 低分触发/关联的照片复核任务
	CreatedAt         time.Time `json:"created_at"`
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
	MemberID       string    `json:"member_id,omitempty"`
	MemberName     string    `json:"member_name,omitempty"`
	DeviceID       string    `json:"device_id"`
	GateID         string    `json:"gate_id"`
	Attraction     string    `json:"attraction"`
	Direction      string    `json:"direction"` // ENTRY | EXIT
	Action         string    `json:"action"`    // ALLOW | DENY | MANUAL_RELEASE | MANUAL_PASS | EXIT
	Reason         string    `json:"reason"`
	Note           string    `json:"note,omitempty"` // 人工通道放行原因（照片复核期间必须填写）
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

// ---------- 儿童照片变化复核 ----------

// 照片复核任务状态
const (
	PhotoReviewPending   = "PENDING_SUBMISSION" // 服务自动生成，等待家长提交新照片
	PhotoReviewSubmitted = "SUBMITTED"          // 家长已提交，等待工作人员核验
	PhotoReviewApproved  = "APPROVED"           // 复核通过，新照片生效
	PhotoReviewRejected  = "REJECTED"           // 复核驳回（人证/监护关系不符等）
)

// 照片复核任务来源
const (
	PhotoReviewSourceAuto   = "AUTO_GATE_LOW_SCORE" // 闸机多次低分自动生成
	PhotoReviewSourceManual = "MANUAL_REQUEST"      // 家长/现场主动发起
)

// PhotoReviewTask 儿童照片变化复核任务
type PhotoReviewTask struct {
	ID          string `json:"id"`
	PassID      string `json:"pass_id"`
	MemberID    string `json:"member_id"`
	MemberName  string `json:"member_name"`
	ChildIDCard string `json:"child_id_card"`
	Status      string `json:"status"`
	Source      string `json:"source"`
	// 触发证据：闸机多次相似度不足
	LowScoreCount int       `json:"low_score_count"`
	TriggerVerifs []string  `json:"trigger_verification_ids"`
	FirstLowAt    time.Time `json:"first_low_score_at"`
	// 家长提交
	NewPhotoURL   string     `json:"new_photo_url,omitempty"`
	SubmitChannel string     `json:"submit_channel,omitempty"` // ONSITE | MINIAPP
	SubmitNote    string     `json:"submit_note,omitempty"`
	SubmittedBy   string     `json:"submitted_by,omitempty"`
	SubmittedAt   *time.Time `json:"submitted_at,omitempty"`
	// 工作人员复核：证件 + 监护关系 + 历史入园记录
	IDVerified          bool       `json:"id_verified"`
	GuardianshipCheck   string     `json:"guardianship_check,omitempty"` // VERIFIED
	EntryHistoryCheck   string     `json:"entry_history_check,omitempty"`
	EntryHistoryMatched int        `json:"entry_history_matched,omitempty"` // 近 180 天历史入园次数
	ReviewNote          string     `json:"review_note,omitempty"`
	ReviewedBy          string     `json:"reviewed_by,omitempty"`
	ReviewedAt          *time.Time `json:"reviewed_at,omitempty"`
	// 复核结果
	ArchiveID    string     `json:"archive_id,omitempty"`
	OldVersion   int        `json:"old_version,omitempty"`
	NewVersion   int        `json:"new_version,omitempty"`
	OldExpiresAt *time.Time `json:"old_photo_expires_at,omitempty"` // 旧照片失效时间
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// ManualPassRecord 照片复核期间人工通道放行记录（每次放行均记录操作人与原因）
type ManualPassRecord struct {
	ID             string    `json:"id"`
	PassID         string    `json:"pass_id"`
	MemberID       string    `json:"member_id"`
	MemberName     string    `json:"member_name"`
	ReviewTaskID   string    `json:"review_task_id"`
	DeviceID       string    `json:"device_id"`
	GateID         string    `json:"gate_id"`
	Attraction     string    `json:"attraction"`
	VerificationID string    `json:"verification_id,omitempty"`
	Reason         string    `json:"reason"` // 放行原因（必填）
	Operator       string    `json:"operator"`
	Note           string    `json:"note"`
	ChargedQuota   int       `json:"charged_quota"`
	CreatedAt      time.Time `json:"created_at"`
}

// PhotoCacheVersion 入口照片缓存中的成员照片版本（审批生效后下发）
type PhotoCacheVersion struct {
	MemberID     string    `json:"member_id"`
	PassID       string    `json:"pass_id"`
	Name         string    `json:"name"`
	PhotoURL     string    `json:"photo_url"`
	PhotoVersion int       `json:"photo_version"`
	UpdatedAt    time.Time `json:"updated_at"`
	OldExpiresAt time.Time `json:"old_expires_at"` // 旧照片失效时间，闸机据此丢弃本地旧版
}

// DevicePhotoSyncView 单台入口设备的照片缓存同步状态
type DevicePhotoSyncView struct {
	DeviceID       string     `json:"device_id"`
	Online         bool       `json:"online"`
	PendingCount   int64      `json:"pending_count"` // 待同步（已生效但该设备未确认）
	SyncedVersions int64      `json:"synced_versions"`
	LastPullAt     *time.Time `json:"last_pull_at,omitempty"`
}

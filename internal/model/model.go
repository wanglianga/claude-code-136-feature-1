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
	ID           string    `json:"id"`
	PassID       string    `json:"pass_id"`
	Name         string    `json:"name"`
	IDCard       string    `json:"id_card"`
	IDCardExpiry string    `json:"id_card_expiry"` // YYYY-MM-DD
	Role         string    `json:"role"`           // holder | companion
	Relation     string    `json:"relation"`       // self | spouse | child | parent | other
	PhotoURL     string    `json:"photo_url"`
	PhotoVersion int       `json:"photo_version"`
	FaceExempt   bool      `json:"face_exempt"` // 老人等无法刷脸成员
	CreatedAt    time.Time `json:"created_at"`
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

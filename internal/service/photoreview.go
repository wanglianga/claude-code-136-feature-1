package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
	"passgate/internal/model"
)

// ---------- 读取辅助 ----------

func (s *Service) getMember(ctx context.Context, memberID string) (*model.Member, error) {
	var m model.Member
	if err := s.St.GetJSON(ctx, keyMember(memberID), &m); err != nil {
		return nil, ErrNotFound
	}
	return &m, nil
}

func (s *Service) getPhotoReview(ctx context.Context, id string) (*model.PhotoReviewTask, error) {
	var t model.PhotoReviewTask
	if err := s.St.GetJSON(ctx, keyPhotoReview(id), &t); err != nil {
		return nil, ErrNotFound
	}
	return &t, nil
}

func (s *Service) savePhotoReview(ctx context.Context, t *model.PhotoReviewTask) {
	t.UpdatedAt = time.Now()
	_ = s.St.SetJSON(ctx, keyPhotoReview(t.ID), t)
}

// OpenPhotoReviewID 返回成员当前进行中的照片复核任务 ID（无则空串）
func (s *Service) OpenPhotoReviewID(ctx context.Context, memberID string) string {
	id, _ := s.St.R.Get(ctx, keyMemberOpenReview(memberID)).Result()
	return id
}

// ---------- 低分计数与自动建任务（在闸机核验 FACE_MISMATCH 时调用） ----------

// NoteFaceMismatch 记录一次该成员的人脸比对不达标；达到阈值且成员为儿童时自动生成照片更新任务。
// 返回本次关联的进行中复核任务 ID（可能为空）。
func (s *Service) NoteFaceMismatch(ctx context.Context, v *model.Verification, member *model.Member) string {
	if member == nil {
		return ""
	}
	now := v.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	lsKey := keyMemberLowScore(member.ID)
	s.St.R.ZAdd(ctx, lsKey, redis.Z{Score: float64(now.Unix()), Member: v.ID})
	s.St.R.Expire(ctx, lsKey, PhotoReviewWindow)
	// 丢弃窗口外的旧记录
	s.St.R.ZRemRangeByScore(ctx, lsKey, "0", fmt.Sprintf("(%d", now.Add(-PhotoReviewWindow).Unix()))
	cnt, _ := s.St.R.ZCard(ctx, lsKey).Result()

	openID := s.OpenPhotoReviewID(ctx, member.ID)
	if openID != "" {
		// 复核任务进行中：低分计数继续累计，但不再重复建任务
		if t, err := s.getPhotoReview(ctx, openID); err == nil && (t.Status == model.PhotoReviewPending || t.Status == model.PhotoReviewSubmitted) {
			t.LowScoreCount = int(cnt)
			if !contains(t.TriggerVerifs, v.ID) {
				t.TriggerVerifs = append(t.TriggerVerifs, v.ID)
			}
			s.savePhotoReview(ctx, t)
		}
		return openID
	}
	// 仅对绑定的儿童自动发起（亲子年卡儿童半年后面部变化场景）
	if member.Relation != "child" || cnt < int64(PhotoReviewLowScoreThreshold) {
		return ""
	}
	task := s.createPhotoReview(ctx, member, model.PhotoReviewSourceAuto, int(cnt), now)
	if task == nil {
		return ""
	}
	return task.ID
}

func (s *Service) createPhotoReview(ctx context.Context, m *model.Member, source string, lowCount int, now time.Time) *model.PhotoReviewTask {
	id, err := s.St.NextID(ctx, "preview", "PR")
	if err != nil {
		return nil
	}
	var verifs []string
	if lowCount > 0 {
		ids, _ := s.St.R.ZRange(ctx, keyMemberLowScore(m.ID), 0, -1).Result()
		verifs = ids
	}
	first := now
	if len(verifs) > 0 {
		if scores, err := s.St.R.ZRangeWithScores(ctx, keyMemberLowScore(m.ID), 0, 0).Result(); err == nil && len(scores) == 1 {
			first = time.Unix(int64(scores[0].Score), 0)
		}
	}
	t := &model.PhotoReviewTask{
		ID: id, PassID: m.PassID, MemberID: m.ID, MemberName: m.Name, ChildIDCard: m.IDCard,
		Status: model.PhotoReviewPending, Source: source, LowScoreCount: lowCount,
		TriggerVerifs: verifs, FirstLowAt: first, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.St.SetJSON(ctx, keyPhotoReview(id), t); err != nil {
		return nil
	}
	s.St.R.LPush(ctx, keyPhotoReviewList(), id)
	s.St.R.Set(ctx, keyMemberOpenReview(m.ID), id, 0)
	detail := fmt.Sprintf("成员 %s（%s）近 %d 次闸机人脸比对相似度不足，自动生成照片更新任务 %s",
		m.Name, m.IDCard, lowCount, id)
	if lowCount == 0 {
		detail = fmt.Sprintf("为成员 %s（%s）手动创建照片更新任务 %s", m.Name, m.IDCard, id)
	}
	s.addChangeLog(ctx, m.PassID, "PHOTO_REVIEW_CREATE", detail, "system")
	return t
}

// CreatePhotoReviewManual 家长/现场主动发起照片更新任务（不存在进行中任务时）
func (s *Service) CreatePhotoReviewManual(ctx context.Context, passID, memberID string) (*model.PhotoReviewTask, error) {
	m, err := s.getMember(ctx, memberID)
	if err != nil || m.PassID != passID {
		return nil, ErrNotFound
	}
	if id := s.OpenPhotoReviewID(ctx, m.ID); id != "" {
		if _, err := s.getPhotoReview(ctx, id); err == nil {
			return nil, fmt.Errorf("%w: %s", ErrConflict, id)
		}
	}
	if t := s.createPhotoReview(ctx, m, model.PhotoReviewSourceManual, 0, time.Now()); t != nil {
		return t, nil
	}
	return nil, ErrConflict
}

// ---------- 家长提交新照片（现场 / 小程序） ----------

// SubmitPhotoReviewInput 家长提交
type SubmitPhotoReviewInput struct {
	NewPhotoURL string `json:"new_photo_url"`
	Channel     string `json:"channel"`      // ONSITE | MINIAPP，默认 MINIAPP
	Note        string `json:"note"`         // 家长备注（可选）
	SubmittedBy string `json:"submitted_by"` // 家长标识/手机号（小程序）
}

func (s *Service) SubmitPhotoReview(ctx context.Context, taskID string, in SubmitPhotoReviewInput) (*model.PhotoReviewTask, error) {
	if in.NewPhotoURL == "" {
		return nil, fmt.Errorf("%w: new_photo_url required", ErrInvalidInput)
	}
	channel := in.Channel
	if channel == "" {
		channel = "MINIAPP"
	}
	if channel != "ONSITE" && channel != "MINIAPP" {
		return nil, fmt.Errorf("%w: channel must be ONSITE or MINIAPP", ErrInvalidInput)
	}
	token, ok, err := s.St.Lock(ctx, "preview:"+taskID, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrConflict
	}
	defer s.St.Unlock(ctx, "preview:"+taskID, token)

	t, err := s.getPhotoReview(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if t.Status != model.PhotoReviewPending {
		return nil, fmt.Errorf("%w: status=%s", ErrReviewNotPending, t.Status)
	}
	now := time.Now()
	t.Status = model.PhotoReviewSubmitted
	t.NewPhotoURL = in.NewPhotoURL
	t.SubmitChannel = channel
	t.SubmitNote = in.Note
	t.SubmittedBy = in.SubmittedBy
	t.SubmittedAt = &now
	s.savePhotoReview(ctx, t)
	s.addChangeLog(ctx, t.PassID, "PHOTO_REVIEW_SUBMIT",
		fmt.Sprintf("照片更新任务 %s：家长通过%s提交新照片，等待工作人员核验", taskID, channelName(channel)),
		"parent:"+in.SubmittedBy)
	return t, nil
}

func channelName(c string) string {
	if c == "ONSITE" {
		return "现场"
	}
	return "小程序"
}

// ---------- 工作人员复核（证件 / 监护关系 / 历史入园记录） ----------

// ReviewPhotoReviewInput 工作人员审批入参
type ReviewPhotoReviewInput struct {
	Approve           bool   `json:"approve"`
	IDVerified        bool   `json:"id_verified"`         // 已核验证件（户口本/身份证）
	GuardianshipCheck string `json:"guardianship_check"`  // VERIFIED 表示监护关系核验通过
	EntryHistoryCheck string `json:"entry_history_check"` // VERIFIED 表示历史入园记录核验通过
	Note              string `json:"note"`                // 处理说明（必填）
	Operator          string `json:"operator"`            // 工作人员
}

func (s *Service) ReviewPhotoReview(ctx context.Context, taskID string, in ReviewPhotoReviewInput) (*model.PhotoReviewTask, error) {
	if in.Operator == "" || in.Note == "" {
		return nil, fmt.Errorf("%w: operator / note required", ErrInvalidInput)
	}
	if !in.IDVerified || in.GuardianshipCheck != "VERIFIED" || in.EntryHistoryCheck != "VERIFIED" {
		return nil, ErrReviewCheckMissing
	}
	token, ok, err := s.St.Lock(ctx, "preview:"+taskID, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrConflict
	}
	defer s.St.Unlock(ctx, "preview:"+taskID, token)

	t, err := s.getPhotoReview(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if t.Status != model.PhotoReviewSubmitted {
		return nil, fmt.Errorf("%w: status=%s", ErrReviewNotSubmitted, t.Status)
	}
	m, err := s.getMember(ctx, t.MemberID)
	if err != nil || m.PassID != t.PassID {
		return nil, ErrNotFound
	}

	now := time.Now()
	t.IDVerified = true
	t.GuardianshipCheck = "VERIFIED"
	t.EntryHistoryCheck = "VERIFIED"
	t.EntryHistoryMatched = s.countRecentEntries(ctx, t.PassID, PhotoReviewHistoryDays)
	t.ReviewNote = in.Note
	t.ReviewedBy = in.Operator
	t.ReviewedAt = &now

	if !in.Approve {
		t.Status = model.PhotoReviewRejected
		s.savePhotoReview(ctx, t)
		s.St.R.Del(ctx, keyMemberOpenReview(t.MemberID))
		s.addChangeLog(ctx, t.PassID, "PHOTO_REVIEW_REJECTED",
			fmt.Sprintf("照片更新任务 %s 复核驳回：%s（已核验证件/监护关系/历史入园）", taskID, in.Note), in.Operator)
		return t, nil
	}

	// 1) 旧照片归档（含失效时间）
	oldExpires := now.Add(PhotoReviewOldPhotoTTL)
	archID, _ := s.St.NextID(ctx, "parchive", "PA")
	arch := model.PhotoArchive{
		ID: archID, MemberID: m.ID, PassID: m.PassID, Name: m.Name,
		OldPhotoURL: m.PhotoURL, OldVersion: m.PhotoVersion,
		Reason: "PHOTO_REVIEW_APPROVED", ReviewTaskID: taskID,
		ArchivedAt: now, ExpiresAt: oldExpires,
		IDVerified: true, GuardianshipCheck: "VERIFIED", EntryHistoryCheck: "VERIFIED",
		Operator: in.Operator, Note: in.Note,
	}
	if err := s.St.SetJSON(ctx, keyPhotoArchive(archID), &arch); err != nil {
		return nil, err
	}
	s.St.R.LPush(ctx, keyMemberPhotoArchives(m.ID), archID)

	// 2) 新照片生效：版本号 +1
	oldVersion := m.PhotoVersion
	m.PhotoURL = t.NewPhotoURL
	m.PhotoVersion++
	m.PhotoUpdatedAt = &now
	if err := s.St.SetJSON(ctx, keyMember(m.ID), m); err != nil {
		return nil, err
	}

	// 3) 下发到各入口照片缓存并标记旧照片失效时间
	s.dispatchPhotoCache(ctx, model.PhotoCacheVersion{
		MemberID: m.ID, PassID: m.PassID, Name: m.Name,
		PhotoURL: m.PhotoURL, PhotoVersion: m.PhotoVersion,
		UpdatedAt: now, OldExpiresAt: oldExpires,
	})

	t.Status = model.PhotoReviewApproved
	t.ArchiveID = archID
	t.OldVersion = oldVersion
	t.NewVersion = m.PhotoVersion
	t.OldExpiresAt = &oldExpires
	s.savePhotoReview(ctx, t)
	s.St.R.Del(ctx, keyMemberOpenReview(t.MemberID))

	s.addChangeLog(ctx, t.PassID, "PHOTO_REVIEW_APPROVED",
		fmt.Sprintf("照片更新任务 %s 通过：%s 照片 v%d 归档（旧照片失效时间 %s），新照片 v%d 生效并下发各入口缓存；近 %d 天历史入园 %d 次",
			taskID, m.Name, oldVersion, oldExpires.Format("2006-01-02 15:04"), m.PhotoVersion,
			PhotoReviewHistoryDays, t.EntryHistoryMatched),
		in.Operator)
	return t, nil
}

// countRecentEntries 统计近 N 天的成功入园次数（含人工放行），供工作人员核查历史入园记录
func (s *Service) countRecentEntries(ctx context.Context, passID string, days int) int {
	cutoff := time.Now().AddDate(0, 0, -days)
	ids, _ := s.St.R.LRange(ctx, keyPassVerifs(passID), 0, 499).Result()
	n := 0
	for _, id := range ids {
		var v model.Verification
		if err := s.St.GetJSON(ctx, keyVerif(id), &v); err != nil {
			continue
		}
		if v.CreatedAt.Before(cutoff) {
			continue
		}
		if v.Decision == model.DecisionAllow || v.ManualAction == "RELEASED" {
			n++
		}
	}
	return n
}

// ---------- 复核期间人工通道（每次放行记录操作人与原因） ----------

// ManualPassInput 照片复核期间人工通道放行入参
type ManualPassInput struct {
	DeviceID       string `json:"device_id"`
	Attraction     string `json:"attraction"`
	Reason         string `json:"reason"` // 放行原因（必填，如 儿童面部变化待复核）
	Note           string `json:"note"`
	Operator       string `json:"operator"`
	People         int    `json:"people"` // 放行人次（含儿童本人），默认 1
	VerificationID string `json:"verification_id"`
}

// ManualPassDuringReview 照片复核期间走人工通道：放行并全程留痕
func (s *Service) ManualPassDuringReview(ctx context.Context, taskID string, in ManualPassInput) (*model.ManualPassRecord, error) {
	if in.Operator == "" {
		return nil, fmt.Errorf("%w: operator required", ErrInvalidInput)
	}
	if in.Reason == "" {
		return nil, ErrReviewReasonEmpty
	}
	if in.DeviceID == "" {
		return nil, fmt.Errorf("%w: device_id required", ErrInvalidInput)
	}
	t, err := s.getPhotoReview(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if t.Status != model.PhotoReviewPending && t.Status != model.PhotoReviewSubmitted {
		return nil, fmt.Errorf("%w: review not in progress (status=%s)", ErrConflict, t.Status)
	}
	dev, err := s.GetDevice(ctx, in.DeviceID)
	if err != nil {
		return nil, err
	}
	attraction := in.Attraction
	if attraction == "" {
		attraction = dev.Attraction
	}
	people := in.People
	if people <= 0 {
		people = 1
	}
	p, err := s.getPass(ctx, t.PassID)
	if err != nil {
		return nil, err
	}
	if people > p.CompanionLimit+1 {
		return nil, fmt.Errorf("%w: people %d exceeds pass companion limit", ErrInvalidInput, people)
	}

	// 与闸机/人工放行同一原子扣减路径（去重窗口内不重复扣）
	date := dateOf(time.Now())
	code, charged, _, err := s.runCharge(ctx, t.PassID, attraction, date, people, 0, "MANUAL-PASS-"+taskID)
	if err != nil {
		return nil, err
	}
	if code != 1 && code != 2 {
		return nil, ErrQuotaInsufficient
	}

	id, _ := s.St.NextID(ctx, "mpass", "MP")
	rec := &model.ManualPassRecord{
		ID: id, PassID: t.PassID, MemberID: t.MemberID, MemberName: t.MemberName,
		ReviewTaskID: taskID, DeviceID: dev.ID, GateID: dev.GateID, Attraction: attraction,
		VerificationID: in.VerificationID, Reason: in.Reason, Operator: in.Operator, Note: in.Note,
		ChargedQuota: int(charged), CreatedAt: time.Now(),
	}
	if err := s.St.SetJSON(ctx, keyManualPass(id), rec); err != nil {
		return nil, err
	}
	s.St.R.LPush(ctx, keyReviewManualPasses(taskID), id)
	s.St.R.LPush(ctx, keyPassManualPasses(t.PassID), id)

	// 闸机流水：操作人 + 原因 + 说明
	glogID, _ := s.St.NextID(ctx, "glog", "G")
	gl := model.GateLog{
		ID: glogID, VerificationID: in.VerificationID, PassID: t.PassID,
		MemberID: t.MemberID, MemberName: t.MemberName,
		DeviceID: dev.ID, GateID: dev.GateID, Attraction: attraction,
		Direction: "ENTRY", Action: "MANUAL_PASS", Reason: "PHOTO_REVIEW_MANUAL",
		Note: in.Reason, QuotaCharged: int(charged), Operator: in.Operator, CreatedAt: time.Now(),
	}
	_ = s.St.SetJSON(ctx, keyGateLog(glogID), &gl)
	s.St.R.LPush(ctx, keyGateLogList(), glogID)
	s.St.R.LTrim(ctx, keyGateLogList(), 0, 9999)
	s.St.R.LPush(ctx, keyPassGateLogs(t.PassID), glogID)

	s.recordEntryStats(ctx, date, rec.CreatedAt.Hour(), p.Type, attraction, rec.ChargedQuota)
	s.statIncr(ctx, "stats:manual:"+date, 1)
	return rec, nil
}

// attachReviewManualPass 常规人工放行链路关联照片复核任务时，补写一条复核期人工通道留痕
// （闸机流水 MANUAL_RELEASE 已由核验链路写入，此处不再重复写闸机日志）
func (s *Service) attachReviewManualPass(ctx context.Context, v *model.Verification, taskID, operator, note string, charged int) {
	t, err := s.getPhotoReview(ctx, taskID)
	if err != nil || (t.Status != model.PhotoReviewPending && t.Status != model.PhotoReviewSubmitted) {
		return
	}
	id, _ := s.St.NextID(ctx, "mpass", "MP")
	rec := &model.ManualPassRecord{
		ID: id, PassID: v.PassID, MemberID: t.MemberID, MemberName: t.MemberName,
		ReviewTaskID: taskID, DeviceID: v.DeviceID, GateID: v.GateID, Attraction: v.Attraction,
		VerificationID: v.ID, Reason: v.Reason, Operator: operator, Note: note,
		ChargedQuota: charged, CreatedAt: time.Now(),
	}
	_ = s.St.SetJSON(ctx, keyManualPass(id), rec)
	s.St.R.LPush(ctx, keyReviewManualPasses(taskID), id)
	s.St.R.LPush(ctx, keyPassManualPasses(v.PassID), id)
}

// ListManualPasses 复核任务下的人工通道放行记录（每次放行可追溯）
func (s *Service) ListManualPasses(ctx context.Context, taskID string) ([]model.ManualPassRecord, error) {
	ids, err := s.St.R.LRange(ctx, keyReviewManualPasses(taskID), 0, 199).Result()
	if err != nil {
		return nil, err
	}
	out := make([]model.ManualPassRecord, 0, len(ids))
	for _, id := range ids {
		var r model.ManualPassRecord
		if err := s.St.GetJSON(ctx, keyManualPass(id), &r); err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// ListPassManualPasses 年卡维度的人工通道记录
func (s *Service) ListPassManualPasses(ctx context.Context, passID string) ([]model.ManualPassRecord, error) {
	ids, err := s.St.R.LRange(ctx, keyPassManualPasses(passID), 0, 199).Result()
	if err != nil {
		return nil, err
	}
	out := make([]model.ManualPassRecord, 0, len(ids))
	for _, id := range ids {
		var r model.ManualPassRecord
		if err := s.St.GetJSON(ctx, keyManualPass(id), &r); err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// ---------- 查询 ----------

func (s *Service) GetPhotoReview(ctx context.Context, id string) (*model.PhotoReviewTask, error) {
	return s.getPhotoReview(ctx, id)
}

// ListPhotoReviews 复核任务列表（可按状态/年卡过滤）
func (s *Service) ListPhotoReviews(ctx context.Context, status, passID string) ([]model.PhotoReviewTask, error) {
	ids, err := s.St.R.LRange(ctx, keyPhotoReviewList(), 0, 499).Result()
	if err != nil {
		return nil, err
	}
	out := make([]model.PhotoReviewTask, 0, len(ids))
	for _, id := range ids {
		var t model.PhotoReviewTask
		if err := s.St.GetJSON(ctx, keyPhotoReview(id), &t); err != nil {
			continue
		}
		if status != "" && t.Status != status {
			continue
		}
		if passID != "" && t.PassID != passID {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// ListMemberPhotoArchives 成员旧照片归档
func (s *Service) ListMemberPhotoArchives(ctx context.Context, passID, memberID string) ([]model.PhotoArchive, error) {
	m, err := s.getMember(ctx, memberID)
	if err != nil || m.PassID != passID {
		return nil, ErrNotFound
	}
	ids, err := s.St.R.LRange(ctx, keyMemberPhotoArchives(memberID), 0, 49).Result()
	if err != nil {
		return nil, err
	}
	out := make([]model.PhotoArchive, 0, len(ids))
	for _, id := range ids {
		var a model.PhotoArchive
		if err := s.St.GetJSON(ctx, keyPhotoArchive(id), &a); err == nil {
			out = append(out, a)
		}
	}
	return out, nil
}

// ---------- 入口照片缓存同步 ----------

// dispatchPhotoCache 新照片生效后写入当前缓存版本，并向所有入口设备下发待同步
func (s *Service) dispatchPhotoCache(ctx context.Context, pcv model.PhotoCacheVersion) {
	_ = s.St.SetJSON(ctx, keyPhotoCacheVersion(pcv.MemberID), &pcv)
	s.St.R.SAdd(ctx, keyPhotoCacheCurrent(), pcv.MemberID)
	deviceIDs, _ := s.St.R.SMembers(ctx, keyDeviceList()).Result()
	for _, d := range deviceIDs {
		s.St.R.SAdd(ctx, keyDevicePhotoPending(d), pcv.MemberID)
	}
}

// PullDevicePhotoCache 闸机（含离线恢复后）拉取待更新的照片版本；拉取后标记该设备已同步
func (s *Service) PullDevicePhotoCache(ctx context.Context, deviceID string) ([]model.PhotoCacheVersion, error) {
	if _, err := s.GetDevice(ctx, deviceID); err != nil {
		return nil, err
	}
	memberIDs, err := s.St.R.SMembers(ctx, keyDevicePhotoPending(deviceID)).Result()
	if err != nil {
		return nil, err
	}
	out := make([]model.PhotoCacheVersion, 0, len(memberIDs))
	for _, mid := range memberIDs {
		var pcv model.PhotoCacheVersion
		if err := s.St.GetJSON(ctx, keyPhotoCacheVersion(mid), &pcv); err == nil {
			out = append(out, pcv)
		}
		s.St.R.SRem(ctx, keyDevicePhotoPending(deviceID), mid)
		s.St.R.SAdd(ctx, keyDevicePhotoSynced(deviceID), mid)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MemberID < out[j].MemberID })
	s.St.R.Set(ctx, keyDeviceLastPull(deviceID), time.Now().Format(time.RFC3339), 0)
	// 拉取同时刷新心跳：离线恢复后主动拉取即视为重新上线
	_ = s.Heartbeat(ctx, deviceID)
	return out, nil
}

// DevicePhotoPendingCount 设备待同步照片版本数（心跳响应携带，提醒闸机拉取）
func (s *Service) DevicePhotoPendingCount(ctx context.Context, deviceID string) int64 {
	n, _ := s.St.R.SCard(ctx, keyDevicePhotoPending(deviceID)).Result()
	return n
}

// PhotoSyncOverview 各入口照片缓存同步总览
type PhotoSyncOverview struct {
	CurrentVersions int64                       `json:"current_versions"`
	Devices         []model.DevicePhotoSyncView `json:"devices"`
}

func (s *Service) PhotoSyncOverview(ctx context.Context) (*PhotoSyncOverview, error) {
	cur, _ := s.St.R.SCard(ctx, keyPhotoCacheCurrent()).Result()
	devices, err := s.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	o := &PhotoSyncOverview{CurrentVersions: cur, Devices: make([]model.DevicePhotoSyncView, 0, len(devices))}
	for _, d := range devices {
		pending, _ := s.St.R.SCard(ctx, keyDevicePhotoPending(d.ID)).Result()
		synced, _ := s.St.R.SCard(ctx, keyDevicePhotoSynced(d.ID)).Result()
		view := model.DevicePhotoSyncView{
			DeviceID: d.ID, Online: d.Online, PendingCount: pending, SyncedVersions: synced,
		}
		if raw, err := s.St.R.Get(ctx, keyDeviceLastPull(d.ID)).Result(); err == nil && raw != "" {
			if t, perr := time.Parse(time.RFC3339, raw); perr == nil {
				view.LastPullAt = &t
			}
		}
		o.Devices = append(o.Devices, view)
	}
	return o, nil
}

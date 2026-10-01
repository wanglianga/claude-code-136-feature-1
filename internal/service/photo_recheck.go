package service

import (
	"context"
	"fmt"
	"time"

	"passgate/internal/model"
)

// 儿童照片变化复核业务参数（演示环境取值，生产可配置化）
const (
	// FaceRecheckThreshold 窗口内人脸相似度不足达到此次数 → 自动生成照片更新任务
	FaceRecheckThreshold = 3
	// FaceRecheckWindow 累计人脸不足的滑动窗口
	FaceRecheckWindow = 30 * 24 * time.Hour
	// PhotoRecheckReason 复核期间人工通道放行的标准原因
	PhotoRecheckReason = "FACE_CHANGED_DURING_RECHECK"
)

// ---------- Redis key ----------
func keyFaceFail(memberID string) string       { return "facefail:" + memberID }
func keyPhotoTask(id string) string            { return "ptask:" + id }
func keyPhotoTaskList() string                 { return "ptask:list" }
func keyMemberOpenTask(memberID string) string { return "member:" + memberID + ":ptask" }
func keyPhotoTaskReleases(id string) string    { return "ptask:" + id + ":releases" }
func keyPhotoRelease(id string) string         { return "prelease:" + id }
func keyPhotoArchive(id string) string         { return "parchive:" + id }
func keyMemberArchives(memberID string) string { return "member:" + memberID + ":archives" }
func keyPhotoSyncVersion() string              { return "photosync:seq" }
func keyPhotoSyncEvent(v int64) string         { return fmt.Sprintf("photosync:%d", v) }
func keyDeviceSync(deviceID string) string     { return "devsync:" + deviceID }

// faceFailState 滑动窗口内的儿童人脸不足累计
type faceFailState struct {
	Count          int       `json:"count"`
	FirstAt        time.Time `json:"first_at"`
	LastAt         time.Time `json:"last_at"`
	DeviceID       string    `json:"device_id"`
	VerificationID string    `json:"verification_id"`
}

// registerChildFaceMismatch 儿童人脸比对不足：累计计数；达到阈值自动创建照片更新任务。
// 返回该儿童当前处于打开状态的复核任务（可能是本次新建，也可能是此前已建）。
func (s *Service) registerChildFaceMismatch(ctx context.Context, p *model.Pass, m *model.Member, deviceID, verifID string, ts time.Time) *model.PhotoUpdateTask {
	// 已有打开中的任务：直接返回，复核期间统一走人工通道
	if tid, err := s.St.R.Get(ctx, keyMemberOpenTask(m.ID)).Result(); err == nil && tid != "" {
		var task model.PhotoUpdateTask
		if err := s.St.GetJSON(ctx, keyPhotoTask(tid), &task); err == nil && isOpenPhotoTask(task.Status) {
			task.FailCount++
			task.LastFailAt = ts
			if deviceID != "" {
				task.TriggerDeviceID = deviceID
			}
			_ = s.St.SetJSON(ctx, keyPhotoTask(tid), &task)
			return &task
		}
		// 索引脏数据清理
		s.St.R.Del(ctx, keyMemberOpenTask(m.ID))
	}

	var st faceFailState
	_ = s.St.GetJSON(ctx, keyFaceFail(m.ID), &st)
	if st.Count == 0 || ts.Sub(st.FirstAt) > FaceRecheckWindow {
		st = faceFailState{Count: 0, FirstAt: ts}
	}
	st.Count++
	st.LastAt = ts
	st.DeviceID = deviceID
	st.VerificationID = verifID
	_ = s.St.SetJSON(ctx, keyFaceFail(m.ID), &st)
	s.St.R.Expire(ctx, keyFaceFail(m.ID), FaceRecheckWindow)
	if st.Count < FaceRecheckThreshold {
		return nil
	}
	task, err := s.createPhotoTask(ctx, p, m, "AUTO_GATE", deviceID, verifID, st, "system")
	if err != nil {
		return nil
	}
	return task
}

func isOpenPhotoTask(status string) bool {
	return status == model.PhotoTaskPendingSubmission || status == model.PhotoTaskPendingReview
}

// createPhotoTask 生成照片更新任务（自动触发 / 工作人员手工创建）
func (s *Service) createPhotoTask(ctx context.Context, p *model.Pass, m *model.Member, source, deviceID, verifID string, fails faceFailState, operator string) (*model.PhotoUpdateTask, error) {
	if existing, err := s.St.R.Get(ctx, keyMemberOpenTask(m.ID)).Result(); err == nil && existing != "" {
		var task model.PhotoUpdateTask
		if err := s.St.GetJSON(ctx, keyPhotoTask(existing), &task); err == nil && isOpenPhotoTask(task.Status) {
			return nil, ErrConflict
		}
		s.St.R.Del(ctx, keyMemberOpenTask(m.ID))
	}
	now := time.Now()
	id, err := s.St.NextID(ctx, "ptask", "T")
	if err != nil {
		return nil, err
	}
	first := fails.FirstAt
	if first.IsZero() {
		first = now
	}
	last := fails.LastAt
	if last.IsZero() {
		last = now
	}
	task := &model.PhotoUpdateTask{
		ID: id, PassID: p.ID, MemberID: m.ID, MemberName: m.Name, PassType: p.Type,
		Status: model.PhotoTaskPendingSubmission, Source: source,
		TriggerDeviceID: deviceID, TriggerVerificationID: verifID,
		FailCount: fails.Count, WindowStartedAt: first, LastFailAt: last,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.St.SetJSON(ctx, keyPhotoTask(id), task); err != nil {
		return nil, err
	}
	s.St.R.LPush(ctx, keyPhotoTaskList(), id)
	s.St.R.LTrim(ctx, keyPhotoTaskList(), 0, 999)
	s.St.R.Set(ctx, keyMemberOpenTask(m.ID), id, 0)
	s.statIncr(ctx, "stats:phototask:created", 1)
	detail := fmt.Sprintf("成员 %s（%s）%d 天内 %d 次人脸相似度不足，生成照片更新任务 %s，复核期间可走人工通道",
		m.Name, m.IDCard, int(FaceRecheckWindow.Hours()/24), task.FailCount, id)
	if source == "MANUAL" {
		detail = fmt.Sprintf("工作人员 %s 手工为成员 %s（%s）创建照片更新任务 %s", operator, m.Name, m.IDCard, id)
	}
	s.addChangeLog(ctx, p.ID, "PHOTO_TASK_CREATE", detail, operator)
	return task, nil
}

// CreatePhotoTaskManual 工作人员手工创建照片更新任务
func (s *Service) CreatePhotoTaskManual(ctx context.Context, passID, memberID, operator string) (*model.PhotoUpdateTask, error) {
	p, m, err := s.loadPassMember(ctx, passID, memberID)
	if err != nil {
		return nil, err
	}
	if m.Relation != "child" {
		return nil, fmt.Errorf("%w: photo recheck task only for child members", ErrInvalidInput)
	}
	return s.createPhotoTask(ctx, p, m, "MANUAL", "", "", faceFailState{}, operator)
}

func (s *Service) loadPassMember(ctx context.Context, passID, memberID string) (*model.Pass, *model.Member, error) {
	p, err := s.getPass(ctx, passID)
	if err != nil {
		return nil, nil, err
	}
	var m model.Member
	if err := s.St.GetJSON(ctx, keyMember(memberID), &m); err != nil || m.PassID != passID {
		return nil, nil, ErrNotFound
	}
	return p, &m, nil
}

// GetPhotoTask 任务详情（含复核期间人工放行记录）
func (s *Service) GetPhotoTask(ctx context.Context, id string) (*model.PhotoUpdateTask, []model.PhotoRecheckRelease, error) {
	var task model.PhotoUpdateTask
	if err := s.St.GetJSON(ctx, keyPhotoTask(id), &task); err != nil {
		return nil, nil, ErrNotFound
	}
	releases := s.listTaskReleases(ctx, id)
	return &task, releases, nil
}

func (s *Service) listTaskReleases(ctx context.Context, id string) []model.PhotoRecheckRelease {
	rids, _ := s.St.R.LRange(ctx, keyPhotoTaskReleases(id), 0, 199).Result()
	out := make([]model.PhotoRecheckRelease, 0, len(rids))
	for _, rid := range rids {
		var r model.PhotoRecheckRelease
		if err := s.St.GetJSON(ctx, keyPhotoRelease(rid), &r); err == nil {
			out = append(out, r)
		}
	}
	return out
}

// ListPhotoTasks 任务列表（可按状态 / 年卡过滤）
func (s *Service) ListPhotoTasks(ctx context.Context, status, passID string) ([]model.PhotoUpdateTask, error) {
	ids, err := s.St.R.LRange(ctx, keyPhotoTaskList(), 0, 499).Result()
	if err != nil {
		return nil, err
	}
	out := make([]model.PhotoUpdateTask, 0, len(ids))
	for _, id := range ids {
		var t model.PhotoUpdateTask
		if err := s.St.GetJSON(ctx, keyPhotoTask(id), &t); err == nil {
			if status != "" && t.Status != status {
				continue
			}
			if passID != "" && t.PassID != passID {
				continue
			}
			out = append(out, t)
		}
	}
	return out, nil
}

// SubmitPhotoInput 家长提交新照片（现场工作人员协助 或 小程序自助）
type SubmitPhotoInput struct {
	NewPhotoURL string `json:"new_photo_url"`
	Channel     string `json:"channel"`  // ONSITE | MINIAPP
	Operator    string `json:"operator"` // 现场为工作人员；小程序为家长标识
}

// SubmitTaskPhoto 家长提交新照片：任务进入待工作人员核验
func (s *Service) SubmitTaskPhoto(ctx context.Context, id string, in SubmitPhotoInput) (*model.PhotoUpdateTask, error) {
	if in.NewPhotoURL == "" {
		return nil, fmt.Errorf("%w: new_photo_url required", ErrInvalidInput)
	}
	if in.Channel != "ONSITE" && in.Channel != "MINIAPP" {
		return nil, fmt.Errorf("%w: channel must be ONSITE or MINIAPP", ErrInvalidInput)
	}
	token, ok, err := s.St.Lock(ctx, "ptask:"+id, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrConflict
	}
	defer s.St.Unlock(ctx, "ptask:"+id, token)

	var task model.PhotoUpdateTask
	if err := s.St.GetJSON(ctx, keyPhotoTask(id), &task); err != nil {
		return nil, ErrNotFound
	}
	if task.Status != model.PhotoTaskPendingSubmission {
		return nil, fmt.Errorf("%w: task not awaiting submission (status=%s)", ErrConflict, task.Status)
	}
	now := time.Now()
	task.NewPhotoURL = in.NewPhotoURL
	task.SubmitChannel = in.Channel
	task.SubmitOperator = in.Operator
	task.SubmittedAt = &now
	task.Status = model.PhotoTaskPendingReview
	task.UpdatedAt = now
	if err := s.St.SetJSON(ctx, keyPhotoTask(id), &task); err != nil {
		return nil, err
	}
	who := in.Operator
	if who == "" {
		who = "anonymous"
	}
	s.addChangeLog(ctx, task.PassID, "PHOTO_TASK_SUBMIT",
		fmt.Sprintf("照片更新任务 %s 通过%s提交新照片，待工作人员核验", id, map[string]string{"ONSITE": "现场", "MINIAPP": "小程序"}[in.Channel]), who)
	return &task, nil
}

// ReviewPhotoInput 工作人员核验入参：证件、监护关系、历史入园记录三项必须全部核验
type ReviewPhotoInput struct {
	Approve         bool   `json:"approve"`
	IDChecked       bool   `json:"id_checked"`
	GuardianChecked bool   `json:"guardian_checked"`
	HistoryChecked  bool   `json:"history_checked"`
	Note            string `json:"note"`
	Operator        string `json:"operator"`
}

// ReviewTaskPhoto 工作人员核验：三项通过后旧照归档、新照生效、发布入口缓存同步事件
func (s *Service) ReviewTaskPhoto(ctx context.Context, id string, in ReviewPhotoInput) (*model.PhotoUpdateTask, error) {
	if in.Operator == "" || in.Note == "" {
		return nil, fmt.Errorf("%w: operator / note required", ErrInvalidInput)
	}
	token, ok, err := s.St.Lock(ctx, "ptask:"+id, 10*time.Second)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrConflict
	}
	defer s.St.Unlock(ctx, "ptask:"+id, token)

	var task model.PhotoUpdateTask
	if err := s.St.GetJSON(ctx, keyPhotoTask(id), &task); err != nil {
		return nil, ErrNotFound
	}
	if !isOpenPhotoTask(task.Status) {
		return nil, ErrAlreadyProcessed
	}
	if in.Approve {
		if task.Status != model.PhotoTaskPendingReview {
			return nil, fmt.Errorf("%w: new photo not submitted yet", ErrConflict)
		}
		if !in.IDChecked || !in.GuardianChecked || !in.HistoryChecked {
			return nil, fmt.Errorf("%w: id / guardian-relation / entry-history must all be checked", ErrInvalidInput)
		}
		return s.approvePhotoTask(ctx, &task, in)
	}
	return s.rejectPhotoTask(ctx, &task, in)
}

func (s *Service) approvePhotoTask(ctx context.Context, task *model.PhotoUpdateTask, in ReviewPhotoInput) (*model.PhotoUpdateTask, error) {
	p, err := s.getPass(ctx, task.PassID)
	if err != nil {
		return nil, err
	}
	var m model.Member
	if err := s.St.GetJSON(ctx, keyMember(task.MemberID), &m); err != nil {
		return nil, ErrNotFound
	}
	now := time.Now()

	// 历史入园记录：以闸机流水为证据（ALLOW / MANUAL_RELEASE 均算真实入园）
	entryCount := s.countHistoryEntries(ctx, task.PassID, now)
	task.IDChecked, task.GuardianChecked, task.HistoryChecked = true, true, true
	task.HistoryEntryCount = entryCount
	task.ReviewOperator = in.Operator
	task.ReviewNote = in.Note
	task.ReviewedAt = &now

	// 1) 旧照片归档（不删除，供稽核回溯；标记失效时间）
	archID, _ := s.St.NextID(ctx, "parchive", "AR")
	arch := model.PhotoArchive{
		ID: archID, PassID: task.PassID, MemberID: task.MemberID, MemberName: task.MemberName,
		PhotoURL: m.PhotoURL, PhotoVersion: m.PhotoVersion, EffectiveFrom: m.CreatedAt,
		InvalidAt: now, TaskID: task.ID, ArchivedAt: now, ArchivedBy: in.Operator,
	}
	if err := s.St.SetJSON(ctx, keyPhotoArchive(archID), &arch); err != nil {
		return nil, err
	}
	s.St.R.LPush(ctx, keyMemberArchives(task.MemberID), archID)

	// 2) 新照片生效，版本 +1
	oldURL, oldVersion := m.PhotoURL, m.PhotoVersion
	m.PhotoURL = task.NewPhotoURL
	m.PhotoVersion++
	m.PhotoUpdatedAt = now
	if err := s.St.SetJSON(ctx, keyMember(task.MemberID), &m); err != nil {
		return nil, err
	}

	// 3) 发布照片缓存同步事件：各入口在线闸机立即追拉，离线闸机恢复后补拉
	ev := s.publishPhotoSync(ctx, p, &m, oldURL, now, task.ID)

	// 4) 任务收尾
	task.Status = model.PhotoTaskApproved
	task.ArchivedPhotoID = archID
	task.OldPhotoVersion = oldVersion
	task.NewPhotoVersion = m.PhotoVersion
	task.ApprovedAt = &now
	task.OldPhotoInvalidAt = now
	task.SyncVersion = ev.Version
	task.UpdatedAt = now
	if err := s.St.SetJSON(ctx, keyPhotoTask(task.ID), task); err != nil {
		return nil, err
	}
	s.St.R.Del(ctx, keyMemberOpenTask(task.MemberID))
	s.St.R.Del(ctx, keyFaceFail(task.MemberID))
	s.statIncr(ctx, "stats:phototask:approved", 1)
	s.addChangeLog(ctx, task.PassID, "PHOTO_TASK_APPROVE",
		fmt.Sprintf("照片更新任务 %s 核验通过（证件/监护关系/历史入园 %d 次），旧照 v%d 归档并于 %s 失效，新照 v%d 生效，同步事件 #%d 已发布",
			task.ID, entryCount, oldVersion, now.Format("2006-01-02 15:04:05"), m.PhotoVersion, ev.Version), in.Operator)
	return task, nil
}

// id0 占位：keyPhotoTask 只需 id，本身无转换；保留可读性
func id0(id string) string { return id }

func (s *Service) rejectPhotoTask(ctx context.Context, task *model.PhotoUpdateTask, in ReviewPhotoInput) (*model.PhotoUpdateTask, error) {
	now := time.Now()
	task.Status = model.PhotoTaskRejected
	task.IDChecked, task.GuardianChecked, task.HistoryChecked = in.IDChecked, in.GuardianChecked, in.HistoryChecked
	task.ReviewOperator = in.Operator
	task.ReviewNote = in.Note
	task.ReviewedAt = &now
	task.UpdatedAt = now
	if err := s.St.SetJSON(ctx, keyPhotoTask(task.ID), task); err != nil {
		return nil, err
	}
	s.St.R.Del(ctx, keyMemberOpenTask(task.MemberID))
	s.statIncr(ctx, "stats:phototask:rejected", 1)
	s.addChangeLog(ctx, task.PassID, "PHOTO_TASK_REJECT",
		fmt.Sprintf("照片更新任务 %s 核验未通过（证件=%v 监护=%v 历史入园=%v）：%s",
			task.ID, in.IDChecked, in.GuardianChecked, in.HistoryChecked, in.Note), in.Operator)
	return task, nil
}

// countHistoryEntries 统计年卡历史真实入园次数（闸机流水中的 ALLOW / MANUAL_RELEASE）
func (s *Service) countHistoryEntries(ctx context.Context, passID string, before time.Time) int {
	ids, _ := s.St.R.LRange(ctx, keyPassGateLogs(passID), 0, 499).Result()
	n := 0
	for _, id := range ids {
		var gl model.GateLog
		if err := s.St.GetJSON(ctx, keyGateLog(id), &gl); err != nil {
			continue
		}
		if gl.Direction == "ENTRY" && (gl.Action == "ALLOW" || gl.Action == "MANUAL_RELEASE") && !gl.CreatedAt.After(before) {
			n++
		}
	}
	return n
}

// publishPhotoSync 发布照片变更同步事件（全局递增版本）
func (s *Service) publishPhotoSync(ctx context.Context, p *model.Pass, m *model.Member, oldURL string, invalidAt time.Time, taskID string) model.PhotoSyncEvent {
	v, _ := s.St.R.Incr(ctx, keyPhotoSyncVersion()).Result()
	ev := model.PhotoSyncEvent{
		Version: v, PassID: p.ID, MemberID: m.ID, MemberName: m.Name,
		PhotoURL: m.PhotoURL, PhotoVersion: m.PhotoVersion,
		OldPhotoURL: oldURL, OldPhotoInvalidAt: invalidAt, TaskID: taskID,
		Type: "PHOTO_UPDATED", CreatedAt: time.Now(),
	}
	_ = s.St.SetJSON(ctx, keyPhotoSyncEvent(v), &ev)
	return ev
}

// recordRecheckRelease 复核期间人工通道每次放行留痕：操作人 + 原因
func (s *Service) recordRecheckRelease(ctx context.Context, task *model.PhotoUpdateTask, v *model.Verification, operator, reason, note string) {
	if reason == "" {
		reason = PhotoRecheckReason
	}
	id, _ := s.St.NextID(ctx, "prelease", "RL")
	gate := ""
	if dev, err := s.GetDevice(ctx, v.DeviceID); err == nil {
		gate = dev.GateID
	}
	rl := model.PhotoRecheckRelease{
		ID: id, TaskID: task.ID, VerificationID: v.ID, PassID: v.PassID, MemberID: task.MemberID,
		DeviceID: v.DeviceID, GateID: gate, Attraction: v.Attraction,
		Operator: operator, Reason: reason, Note: note, CreatedAt: time.Now(),
	}
	_ = s.St.SetJSON(ctx, keyPhotoRelease(id), &rl)
	s.St.R.LPush(ctx, keyPhotoTaskReleases(task.ID), id)
	s.statIncr(ctx, "stats:phototask:manual:"+dateOf(time.Now()), 1)
}

// ---------- 入口闸机照片缓存同步 ----------

// PullPhotoSync 闸机拉取 after 版本之后的照片变更事件（在线轮询 / 离线恢复后追拉）
func (s *Service) PullPhotoSync(ctx context.Context, deviceID string, after int64) (int64, []model.PhotoSyncEvent, error) {
	if _, err := s.GetDevice(ctx, deviceID); err != nil {
		return 0, nil, ErrNotFound
	}
	current, _ := s.St.R.Get(ctx, keyPhotoSyncVersion()).Int64()
	out := make([]model.PhotoSyncEvent, 0)
	for v := after + 1; v <= current; v++ {
		var ev model.PhotoSyncEvent
		if err := s.St.GetJSON(ctx, keyPhotoSyncEvent(v), &ev); err == nil {
			out = append(out, ev)
		}
	}
	s.St.R.HSet(ctx, keyDeviceSync(deviceID), "last_pull_at", time.Now().Format(time.RFC3339))
	return current, out, nil
}

// AckPhotoSync 闸机确认本地缓存已更新到 version
func (s *Service) AckPhotoSync(ctx context.Context, deviceID string, version int64) error {
	if _, err := s.GetDevice(ctx, deviceID); err != nil {
		return ErrNotFound
	}
	if version <= 0 {
		return fmt.Errorf("%w: version required", ErrInvalidInput)
	}
	current, _ := s.St.R.Get(ctx, keyPhotoSyncVersion()).Int64()
	if version > current {
		return fmt.Errorf("%w: version %d ahead of server %d", ErrConflict, version, current)
	}
	s.St.R.HSet(ctx, keyDeviceSync(deviceID),
		"acked_version", version, "last_ack_at", time.Now().Format(time.RFC3339))
	return nil
}

// ListDeviceSyncStatus 各入口设备照片缓存同步状态（在线/离线、待同步事件数）
func (s *Service) ListDeviceSyncStatus(ctx context.Context) ([]model.DeviceSyncView, error) {
	current, _ := s.St.R.Get(ctx, keyPhotoSyncVersion()).Int64()
	devices, err := s.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.DeviceSyncView, 0, len(devices))
	for _, d := range devices {
		view := model.DeviceSyncView{DeviceID: d.ID, Online: d.Online, AckedVersion: 0}
		acked, _ := s.St.R.HGet(ctx, keyDeviceSync(d.ID), "acked_version").Int64()
		view.AckedVersion = acked
		if current > acked {
			view.PendingCount = int(current - acked)
		}
		if raw, err := s.St.R.HGet(ctx, keyDeviceSync(d.ID), "last_ack_at").Result(); err == nil && raw != "" {
			if t, err := time.Parse(time.RFC3339, raw); err == nil {
				view.LastAckAt = &t
			}
		}
		if raw, err := s.St.R.HGet(ctx, keyDeviceSync(d.ID), "last_pull_at").Result(); err == nil && raw != "" {
			if t, err := time.Parse(time.RFC3339, raw); err == nil {
				view.LastPullAt = &t
			}
		}
		out = append(out, view)
	}
	return out, nil
}

// PhotoSyncCurrent 当前照片同步事件版本
func (s *Service) PhotoSyncCurrent(ctx context.Context) int64 {
	v, _ := s.St.R.Get(ctx, keyPhotoSyncVersion()).Int64()
	return v
}

// ListMemberPhotoArchives 成员历史照片归档
func (s *Service) ListMemberPhotoArchives(ctx context.Context, passID, memberID string) ([]model.PhotoArchive, error) {
	var m model.Member
	if err := s.St.GetJSON(ctx, keyMember(memberID), &m); err != nil || m.PassID != passID {
		return nil, ErrNotFound
	}
	ids, _ := s.St.R.LRange(ctx, keyMemberArchives(memberID), 0, 49).Result()
	out := make([]model.PhotoArchive, 0, len(ids))
	for _, id := range ids {
		var a model.PhotoArchive
		if err := s.St.GetJSON(ctx, keyPhotoArchive(id), &a); err == nil {
			out = append(out, a)
		}
	}
	return out, nil
}

package seed

import (
	"context"
	"log"
	"time"

	"passgate/internal/model"
	"passgate/internal/service"
)

// SeedIfEmpty 空库时写入演示数据：景点、闸机设备、售卖规则、2026 年节假日、示例年卡
func SeedIfEmpty(ctx context.Context, svc *service.Service) error {
	done, _ := svc.St.R.Get(ctx, "seed:done").Result()
	if done == "1" {
		return nil
	}
	log.Println("[seed] 写入演示数据...")

	// 景点
	attrs := []model.Attraction{
		{ID: "attr-lake", Name: "镜湖景区", Capacity: 5000},
		{ID: "attr-summit", Name: "云顶山", Capacity: 2000},
		{ID: "attr-garden", Name: "百花园", Capacity: 3000},
	}
	for _, a := range attrs {
		if _, err := svc.CreateAttraction(ctx, a); err != nil {
			return err
		}
	}

	// 闸机设备（多入口多设备）
	devices := []model.Device{
		{ID: "dev-east-01", Name: "东门 1 号闸机", GateID: "gate-east", Attraction: "attr-lake"},
		{ID: "dev-east-02", Name: "东门 2 号闸机", GateID: "gate-east", Attraction: "attr-lake"},
		{ID: "dev-west-01", Name: "西门 1 号闸机", GateID: "gate-west", Attraction: "attr-summit"},
		{ID: "dev-south-01", Name: "南门 1 号闸机", GateID: "gate-south", Attraction: "attr-garden"},
	}
	for _, d := range devices {
		if _, err := svc.RegisterDevice(ctx, d); err != nil {
			return err
		}
	}

	// 售卖规则
	rules := []model.SalesRule{
		{
			PassType: model.PassTypeSingle, Price: 399, TotalQuota: 100, CompanionLimit: 0,
			Attractions:    []string{"attr-lake", "attr-summit", "attr-garden"},
			HolidayBlocked: true, AllowedSlots: []string{"08:00-18:00"},
			ValidDays: 365, MemberChangeLimit: 0, OnSale: true, DailyLimit: 500,
			UpdatedAt: time.Now(), UpdatedBy: "seed",
		},
		{
			PassType: model.PassTypeParentChild, Price: 699, TotalQuota: 200, CompanionLimit: 1,
			Attractions:    []string{"attr-lake", "attr-garden"},
			HolidayBlocked: false, AllowedSlots: []string{"08:00-18:00"},
			ValidDays: 365, MemberChangeLimit: 1, OnSale: true, DailyLimit: 300,
			UpdatedAt: time.Now(), UpdatedBy: "seed",
		},
		{
			PassType: model.PassTypeFamily, Price: 1299, TotalQuota: 400, CompanionLimit: 3,
			Attractions:    []string{"attr-lake", "attr-summit", "attr-garden"},
			HolidayBlocked: false, AllowedSlots: []string{"06:00-21:00"},
			ValidDays: 365, MemberChangeLimit: 2, OnSale: true, DailyLimit: 200,
			UpdatedAt: time.Now(), UpdatedBy: "seed",
		},
	}
	for _, r := range rules {
		if err := svc.St.SetJSON(ctx, "salesrule:"+string(r.PassType), &r); err != nil {
			return err
		}
	}

	// 2026 年法定节假日（国务院口径近似）
	holidays := []string{
		"2026-01-01", "2026-01-02",
		"2026-02-16", "2026-02-17", "2026-02-18", "2026-02-19", "2026-02-20", "2026-02-21", "2026-02-22",
		"2026-04-04", "2026-04-05", "2026-04-06",
		"2026-05-01", "2026-05-02", "2026-05-03", "2026-05-04", "2026-05-05",
		"2026-06-19", "2026-06-20", "2026-06-21",
		"2026-10-01", "2026-10-02", "2026-10-03", "2026-10-04", "2026-10-05", "2026-10-06", "2026-10-07", "2026-10-08",
	}
	if err := svc.SetHolidays(ctx, holidays, "seed"); err != nil {
		return err
	}

	// 示例年卡
	// 1) 家庭年卡：张三一家（含免刷脸老人）
	family, err := svc.CreatePass(ctx, service.CreatePassInput{
		Type: model.PassTypeFamily, HolderName: "张三", HolderIDCard: "110101199001011234",
		IDCardExpiry: "2032-06-30", Phone: "13800000001", PhotoURL: "https://img.example.com/zhangsan.jpg",
		Members: []service.MemberInput{
			{Name: "李四", IDCard: "110101199203054321", IDCardExpiry: "2031-03-05", Relation: "spouse", PhotoURL: "https://img.example.com/lisi.jpg"},
			{Name: "张小明", IDCard: "110101201807121234", IDCardExpiry: "2030-07-12", Relation: "child", PhotoURL: "https://img.example.com/xiaoming.jpg"},
			{Name: "张父", IDCard: "110101195501011239", IDCardExpiry: "2035-01-01", Relation: "parent", PhotoURL: "https://img.example.com/zhangfu.jpg", FaceExempt: true},
		},
	})
	if err != nil {
		return err
	}
	// 2) 单人年卡：王五
	single, err := svc.CreatePass(ctx, service.CreatePassInput{
		Type: model.PassTypeSingle, HolderName: "王五", HolderIDCard: "310101198812124444",
		IDCardExpiry: "2029-12-12", Phone: "13800000002", PhotoURL: "https://img.example.com/wangwu.jpg",
	})
	if err != nil {
		return err
	}
	// 3) 亲子年卡：赵六 + 孩子
	pc, err := svc.CreatePass(ctx, service.CreatePassInput{
		Type: model.PassTypeParentChild, HolderName: "赵六", HolderIDCard: "440101199505058888",
		IDCardExpiry: "2033-05-05", Phone: "13800000003", PhotoURL: "https://img.example.com/zhaoliu.jpg",
		Members: []service.MemberInput{
			{Name: "赵小贝", IDCard: "440101201909096666", IDCardExpiry: "2031-09-09", Relation: "child", PhotoURL: "https://img.example.com/xiaobei.jpg"},
		},
	})
	if err != nil {
		return err
	}

	if err := svc.St.R.Set(ctx, "seed:done", "1", 0).Err(); err != nil {
		return err
	}
	log.Printf("[seed] 完成。示例年卡：家庭卡=%s 单人卡=%s 亲子卡=%s", family.ID, single.ID, pc.ID)
	log.Printf("[seed] 家庭卡二维码 token: %s", family.QRToken)
	return nil
}

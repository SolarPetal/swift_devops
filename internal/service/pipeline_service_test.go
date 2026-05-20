package service_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"swift-devops/internal/model"
	cryptopkg "swift-devops/internal/pkg/crypto"
	apperr "swift-devops/internal/pkg/errors"
	"swift-devops/internal/service"
)

// 准备 pipeline service + 它依赖的 host/artifact svc
func setupPipeSvc(t *testing.T) (*service.PipelineService, *service.HostService, *service.ArtifactService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Host{}, &model.Application{}, &model.Artifact{},
		&model.Deployment{}, &model.PipelineRun{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	aes, _ := cryptopkg.NewAESGCM(key)
	hostSvc := service.NewHostService(db, aes)
	artSvc := service.NewArtifactService(db)
	pipeSvc := service.NewPipelineService(db, hostSvc, artSvc, 0)
	return pipeSvc, hostSvc, artSvc, db
}

func seedAppForPipe(t *testing.T, db *gorm.DB) uint {
	t.Helper()
	a := &model.Application{
		AppCode: "demo-pipe", Name: "Demo", AppType: "jar",
		DeployPath: "/opt/apps/demo", Port: 8080,
		HealthCheckURL: "/actuator/health",
	}
	if err := db.Create(a).Error; err != nil {
		t.Fatalf("seed app: %v", err)
	}
	return a.ID
}

func registerJar(t *testing.T, svc *service.ArtifactService, appID uint, ver string) uint {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "x.jar")
	if err := os.WriteFile(p, []byte("xx"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a, err := svc.Register(service.ArtifactInput{
		AppID: appID, VersionTag: ver, FilePath: p,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return a.ID
}

func TestPipeline_Trigger_AppNotFound(t *testing.T) {
	svc, _, _, _ := setupPipeSvc(t)
	_, err := svc.Trigger(9999, 1, "tester", "single")
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "NOT_FOUND" {
		t.Fatalf("应 NOT_FOUND：%v", err)
	}
}

func TestPipeline_Trigger_ArtifactMismatch(t *testing.T) {
	svc, _, artSvc, db := setupPipeSvc(t)
	app1 := seedAppForPipe(t, db)
	app2 := &model.Application{AppCode: "other", Name: "X", DeployPath: "/o", Port: 9}
	db.Create(app2)
	artID := registerJar(t, artSvc, app2.ID, "v1") // 制品属于 app2

	_, err := svc.Trigger(app1, artID, "tester", "single")
	if err == nil {
		t.Fatal("应失败")
	}
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "BAD_REQUEST" {
		t.Fatalf("应 BAD_REQUEST：%v", err)
	}
}

func TestPipeline_Trigger_NoDeployments(t *testing.T) {
	svc, _, artSvc, db := setupPipeSvc(t)
	appID := seedAppForPipe(t, db)
	artID := registerJar(t, artSvc, appID, "v1")
	_, err := svc.Trigger(appID, artID, "tester", "single")
	if err == nil {
		t.Fatal("没绑主机应失败")
	}
	ae, ok := apperr.As(err)
	if !ok || ae.Code != "BAD_REQUEST" {
		t.Fatalf("应 BAD_REQUEST：%v", err)
	}
}

func TestPipeline_Trigger_StrategyValidation(t *testing.T) {
	svc, _, _, _ := setupPipeSvc(t)
	_, err := svc.Trigger(1, 1, "tester", "rolling")
	if err == nil {
		t.Fatal("rolling 应拒")
	}
	ae, _ := apperr.As(err)
	if ae == nil || ae.Code != "BAD_REQUEST" {
		t.Fatalf("应 BAD_REQUEST：%v", err)
	}
}

func TestPipeline_List_Empty(t *testing.T) {
	svc, _, _, _ := setupPipeSvc(t)
	list, err := svc.List(0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("应空")
	}
}

func TestPipeline_Get_NotFound(t *testing.T) {
	svc, _, _, _ := setupPipeSvc(t)
	_, err := svc.Get(9999)
	if err == nil {
		t.Fatal("应失败")
	}
}

// recPub 单测专用 publisher：记录所有事件，看到终态 status 就 close done。
type recPub struct {
	mu     sync.Mutex
	events []string
	done   chan struct{}
	once   sync.Once
}

func newRecPub() *recPub { return &recPub{done: make(chan struct{})} }

func (p *recPub) Publish(topic string, msg []byte) int {
	p.mu.Lock()
	p.events = append(p.events, string(msg))
	p.mu.Unlock()
	// 解析 type/status，终态触发 done
	var e map[string]any
	_ = json.Unmarshal(msg, &e)
	if e["type"] == "status" {
		st, _ := e["status"].(string)
		if st == "success" || st == "failed" {
			p.once.Do(func() { close(p.done) })
		}
	}
	return 1
}

func (p *recPub) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.events))
	copy(out, p.events)
	return out
}

func TestPipeline_Publisher_EmitsStatusAndStep(t *testing.T) {
	svc, hostSvc, artSvc, db := setupPipeSvc(t)
	pub := newRecPub()
	svc.SetPublisher(pub)

	appID := seedAppForPipe(t, db)
	artID := registerJar(t, artSvc, appID, "v1")
	// 绑一台肯定连不上的 host，让 pipeline 走完整路径但 dial 失败
	hv, err := hostSvc.Create(service.HostInput{
		Name: "ghost", IP: "127.0.0.1",
		Port: 1, // 端口 1 一般无人 listen
		AuthType: "password", Username: "x", Password: "x",
	})
	if err != nil {
		t.Fatalf("host create: %v", err)
	}
	depSvc := service.NewDeploymentService(db)
	if _, err := depSvc.Bind(appID, service.DeploymentInput{HostID: hv.ID}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	out, err := svc.Trigger(appID, artID, "tester", "single")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if out.Status != "running" {
		t.Fatalf("trigger 同步状态应 running，得 %s", out.Status)
	}

	// 等终态（dial 失败 ⇒ failed）
	select {
	case <-pub.done:
	case <-time.After(20 * time.Second):
		t.Fatalf("等了 20s 没等到终态。已收事件：%v", pub.snapshot())
	}

	events := pub.snapshot()
	if len(events) < 3 {
		t.Fatalf("至少应有 3 个事件（running + dial step + failed），实收 %d：%v", len(events), events)
	}
	// 第一帧必须是 status=running
	if !strings.Contains(events[0], `"type":"status"`) || !strings.Contains(events[0], `"status":"running"`) {
		t.Errorf("首帧应为 status=running，得 %s", events[0])
	}
	// 必须有至少一个 step 事件
	hasStep := false
	for _, e := range events {
		if strings.Contains(e, `"type":"step"`) {
			hasStep = true
			break
		}
	}
	if !hasStep {
		t.Error("应有 step 事件")
	}
	// 末帧必须是 status=failed（dial 失败）
	last := events[len(events)-1]
	if !strings.Contains(last, `"type":"status"`) || !strings.Contains(last, `"status":"failed"`) {
		t.Errorf("末帧应为 status=failed，得 %s", last)
	}
}

func TestPipeline_SnapshotEventBytes(t *testing.T) {
	svc, _, artSvc, db := setupPipeSvc(t)
	appID := seedAppForPipe(t, db)
	_ = registerJar(t, artSvc, appID, "v1")

	// 不存在
	if b := svc.SnapshotEventBytes(9999); b != nil {
		t.Fatalf("不存在的 run 应返回 nil，得 %s", b)
	}

	// 手造一条 run
	now := time.Now()
	r := &model.PipelineRun{
		AppID: appID, ArtifactID: 1, Strategy: "single",
		Status: "success", StateSnapshot: `{"steps":[]}`,
		TriggeredBy: "x", StartedAt: &now, FinishedAt: &now,
	}
	if err := db.Create(r).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	b := svc.SnapshotEventBytes(r.ID)
	if b == nil {
		t.Fatal("应有 snapshot 字节")
	}
	if !strings.Contains(string(b), `"type":"snapshot"`) {
		t.Fatalf("type 应为 snapshot：%s", b)
	}
}

// 编译期断言 service.PipelineService 接受 wspkg.Hub 作为 Publisher（接口契合）
var _ service.Publisher = (interface {
	Publish(topic string, msg []byte) int
})(nil)

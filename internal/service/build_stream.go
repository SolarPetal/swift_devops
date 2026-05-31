package service

import (
	"encoding/json"
	"fmt"
	"time"
)

// build_stream.go —— Sprint 5.5：构建日志实时 WS 推送。
//
// 设计与 pipeline 的实时步骤推送（pipeline_service.go）对齐，复用同一套
// ws.Hub / Publisher：
//   - building 中：builder 每写一块输出，hubLogWriter 就 Publish 一帧 "log"
//   - WS 连上：先收一帧 "snapshot"（截至此刻的全量日志 + 当前状态），晚到也能补全
//   - 构建结束：Publish 一帧 "status"（success/failed/cancelled）
//
// snapshot 与增量的边界可能重复极小一段日志——运维日志查看器（含 k8s logs -f）
// 都有这个边界问题，对内网平台完全可接受，故不做按字节去重的过度设计。

// BuildTopic 单次构建的 WS 主题命名约定。
func BuildTopic(buildID uint) string {
	return fmt.Sprintf("build:%d", buildID)
}

// BuildEvent 构建 WS 帧标准 schema（前端按 type 分发）。
type BuildEvent struct {
	Type    string `json:"type"`              // "snapshot" | "log" | "status"
	BuildID uint   `json:"build_id"`
	Chunk   string `json:"chunk,omitempty"`   // type=log：增量日志块，前端 append
	Log     string `json:"log,omitempty"`     // type=snapshot：截至此刻的全量日志
	Status  string `json:"status,omitempty"`  // type=snapshot / status：构建状态
	Ts      string `json:"ts"`
}

// hubLogWriter 是 io.Writer 适配器：每次 Write 把内容作为 "log" 帧推到 hub。
// 它总是返回 (len(p), nil)——推送失败绝不能中断真正写文件的 MultiWriter。
type hubLogWriter struct {
	pub     Publisher
	buildID uint
}

func (w *hubLogWriter) Write(p []byte) (int, error) {
	if w.pub != nil && len(p) > 0 {
		data, err := json.Marshal(BuildEvent{
			Type: "log", BuildID: w.buildID,
			Chunk: string(p), Ts: time.Now().Format(time.RFC3339),
		})
		if err == nil {
			w.pub.Publish(BuildTopic(w.buildID), data)
		}
	}
	return len(p), nil
}

// publishBuildStatus 推一帧构建终态。pub 为空时静默跳过。
func (s *BuildService) publishBuildStatus(buildID uint, status string) {
	if s.pub == nil {
		return
	}
	data, err := json.Marshal(BuildEvent{
		Type: "status", BuildID: buildID, Status: status,
		Ts: time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	s.pub.Publish(BuildTopic(buildID), data)
}

// SnapshotLogBytes 给 WS handler 用：连上时发截至此刻的全量日志 + 当前状态。
// 返回 nil 表示构建不存在（handler 据此放弃发首帧）。
func (s *BuildService) SnapshotLogBytes(buildID uint) []byte {
	v, err := s.Get(buildID)
	if err != nil {
		return nil
	}
	logTxt, _ := s.GetLog(buildID)
	data, err := json.Marshal(BuildEvent{
		Type: "snapshot", BuildID: buildID,
		Log: logTxt, Status: v.Status,
		Ts: time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return nil
	}
	return data
}

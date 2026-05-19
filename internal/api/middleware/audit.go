package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"swift-devops/internal/model"
)

const (
	auditPayloadMax = 4096
	auditTimeout    = 3 * time.Second
)

// resourceRE 从 /api/v1/<resource>[/<id>] 中提取资源类型与 ID。
var resourceRE = regexp.MustCompile(`^/api/v1/([^/]+)(?:/([^/?]+))?`)

// Audit 写操作审计中间件。仅对 POST/PUT/PATCH/DELETE 生效。
// 把 actor / action / resource / payload / IP 写入 audit_logs。
// 写库走 goroutine + 超时上下文，不阻塞主链。
func Audit(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		m := c.Request.Method
		if m != "POST" && m != "PUT" && m != "PATCH" && m != "DELETE" {
			c.Next()
			return
		}

		var bodySnippet []byte
		if c.Request.Body != nil {
			buf, _ := io.ReadAll(io.LimitReader(c.Request.Body, auditPayloadMax+1))
			c.Request.Body = io.NopCloser(bytes.NewReader(buf))
			if len(buf) > auditPayloadMax {
				bodySnippet = buf[:auditPayloadMax]
			} else {
				bodySnippet = buf
			}
		}

		c.Next()

		var resourceType, resourceID string
		if mm := resourceRE.FindStringSubmatch(c.Request.URL.Path); mm != nil {
			resourceType = mm[1]
			resourceID = mm[2]
		}
		actor, _ := c.Get("user")
		actorStr, _ := actor.(string)

		log := &model.AuditLog{
			Actor:        actorStr,
			Action:       m + " " + c.Request.URL.Path,
			ResourceType: resourceType,
			ResourceID:   resourceID,
			Payload:      sanitizePayload(bodySnippet),
			IP:           c.ClientIP(),
		}

		go func(l *model.AuditLog) {
			ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
			defer cancel()
			if err := db.WithContext(ctx).Create(l).Error; err != nil {
				slog.Warn("audit log write failed", "err", err, "action", l.Action)
			}
		}(log)
	}
}

// sanitizePayload 把 JSON 中疑似密码 / 私钥 / token 字段替换为 ***。
// 非 JSON 则原样返回（受 auditPayloadMax 限制）。
func sanitizePayload(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return string(b)
	}
	for k := range m {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "password") ||
			strings.Contains(lk, "secret") ||
			strings.Contains(lk, "token") ||
			strings.Contains(lk, "private_key") {
			m[k] = "***"
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return string(b)
	}
	return string(out)
}

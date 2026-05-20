package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"swift-devops/internal/config"
	apperr "swift-devops/internal/pkg/errors"
)

func JWT(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			apperr.Respond(c, apperr.New("UNAUTHORIZED", "missing token", http.StatusUnauthorized))
			return
		}
		tokenStr := strings.TrimPrefix(auth, "Bearer ")
		tok, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(cfg.Security.JWTSecret), nil
		})
		if err != nil || !tok.Valid {
			apperr.Respond(c, apperr.New("UNAUTHORIZED", "invalid token", http.StatusUnauthorized))
			return
		}
		if claims, ok := tok.Claims.(jwt.MapClaims); ok {
			c.Set("user", claims["sub"])
		}
		c.Next()
	}
}

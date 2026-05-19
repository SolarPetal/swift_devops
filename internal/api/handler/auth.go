package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"swift-devops/internal/config"
	"swift-devops/internal/pkg/crypto"
	apperr "swift-devops/internal/pkg/errors"
)

type AuthHandler struct {
	cfg *config.Config
}

func NewAuthHandler(cfg *config.Config) *AuthHandler {
	return &AuthHandler{cfg: cfg}
}

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apperr.Respond(c, apperr.Wrap(err, "BAD_REQUEST", err.Error(), http.StatusBadRequest))
		return
	}
	if req.Username != h.cfg.Admin.Username ||
		!crypto.CheckPassword(h.cfg.Admin.PasswordBcrypt, req.Password) {
		// 统一错误信息，避免账号枚举
		apperr.Respond(c, apperr.ErrInvalidCredentials)
		return
	}
	exp := time.Now().Add(h.cfg.Security.JWTTTL).Unix()
	claims := jwt.MapClaims{
		"sub": req.Username,
		"exp": exp,
		"iat": time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(h.cfg.Security.JWTSecret))
	if err != nil {
		apperr.Respond(c, apperr.Wrap(err, "INTERNAL", "sign failed", http.StatusInternalServerError))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":    signed,
		"username": req.Username,
		"expires":  exp,
	})
}

func (h *AuthHandler) Me(c *gin.Context) {
	user, _ := c.Get("user")
	c.JSON(200, gin.H{"user": user})
}

package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	cryptopkg "swift-devops/internal/pkg/crypto"
	apperr "swift-devops/internal/pkg/errors"
)

// GitCredentialInput 创建/更新 Git 凭证入参。
//   - Type=token: Username 是 GitHub 账号 / GitLab oauth2 等；Secret 是 PAT
//   - Type=ssh_key: Username 一般 "git"；Secret 是 PEM 私钥
type GitCredentialInput struct {
	Name     string `json:"name" binding:"required"`
	Type     string `json:"type" binding:"required,oneof=token ssh_key"`
	Username string `json:"username,omitempty"`
	Secret   string `json:"secret,omitempty"` // Update 时留空保留旧值
}

// GitCredentialView 凭证响应，secret 不返回（只透出 has_secret）。
type GitCredentialView struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Username  string `json:"username"`
	HasSecret bool   `json:"has_secret"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func toGitCredView(c *model.GitCredential) GitCredentialView {
	return GitCredentialView{
		ID: c.ID, Name: c.Name, Type: c.Type, Username: c.Username,
		HasSecret: c.Secret != "",
		CreatedAt: c.CreatedAt.Format(time.RFC3339),
		UpdatedAt: c.UpdatedAt.Format(time.RFC3339),
	}
}

// GitCredentialService Git 凭证管理（与 Host 凭证同一套 AES-GCM）。
type GitCredentialService struct {
	db    *gorm.DB
	crypt *cryptopkg.AESGCM
}

func NewGitCredentialService(db *gorm.DB, crypt *cryptopkg.AESGCM) *GitCredentialService {
	return &GitCredentialService{db: db, crypt: crypt}
}

// Create 录入新凭证。Secret 必填。
func (s *GitCredentialService) Create(in GitCredentialInput) (GitCredentialView, error) {
	if err := validateGitCred(in, true); err != nil {
		return GitCredentialView{}, err
	}
	ct, err := s.encryptSecret(in.Secret)
	if err != nil {
		return GitCredentialView{}, apperr.Wrap(err, "INTERNAL", "encrypt secret", 500)
	}
	c := &model.GitCredential{
		Name: strings.TrimSpace(in.Name), Type: in.Type,
		Username: strings.TrimSpace(in.Username), Secret: ct,
	}
	if err := s.db.Create(c).Error; err != nil {
		if isUniqueConstraint(err) {
			return GitCredentialView{}, apperr.New("CONFLICT",
				fmt.Sprintf("凭证名 %q 已存在", in.Name), 409)
		}
		return GitCredentialView{}, apperr.Wrap(err, "INTERNAL", "create git credential", 500)
	}
	return toGitCredView(c), nil
}

func (s *GitCredentialService) List() ([]GitCredentialView, error) {
	var cs []model.GitCredential
	if err := s.db.Order("id DESC").Find(&cs).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list git creds", 500)
	}
	vs := make([]GitCredentialView, len(cs))
	for i := range cs {
		vs[i] = toGitCredView(&cs[i])
	}
	return vs, nil
}

func (s *GitCredentialService) Get(id uint) (GitCredentialView, error) {
	c, err := s.findByID(id)
	if err != nil {
		return GitCredentialView{}, err
	}
	return toGitCredView(c), nil
}

// Update 更新；secret 留空时保留旧值。
func (s *GitCredentialService) Update(id uint, in GitCredentialInput) (GitCredentialView, error) {
	if err := validateGitCred(in, false); err != nil {
		return GitCredentialView{}, err
	}
	c, err := s.findByID(id)
	if err != nil {
		return GitCredentialView{}, err
	}
	c.Name = strings.TrimSpace(in.Name)
	c.Type = in.Type
	c.Username = strings.TrimSpace(in.Username)
	if in.Secret != "" {
		ct, encErr := s.encryptSecret(in.Secret)
		if encErr != nil {
			return GitCredentialView{}, apperr.Wrap(encErr, "INTERNAL", "encrypt secret", 500)
		}
		c.Secret = ct
	}
	if err := s.db.Save(c).Error; err != nil {
		if isUniqueConstraint(err) {
			return GitCredentialView{}, apperr.New("CONFLICT",
				fmt.Sprintf("凭证名 %q 已被占用", in.Name), 409)
		}
		return GitCredentialView{}, apperr.Wrap(err, "INTERNAL", "update git credential", 500)
	}
	return toGitCredView(c), nil
}

// Delete 删除凭证。BuildRun.CredID 仅是软引用，删凭证不级联到历史 build。
// 若需要强一致再加 CONFLICT 检查；当前简化处理。
func (s *GitCredentialService) Delete(id uint) error {
	res := s.db.Delete(&model.GitCredential{}, id)
	if res.Error != nil {
		return apperr.Wrap(res.Error, "INTERNAL", "delete git credential", 500)
	}
	if res.RowsAffected == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

// LoadSecret 解密返回明文 secret，供 builder 使用。
// 内部调用，不暴露给 handler。
func (s *GitCredentialService) LoadSecret(id uint) (typ, username, secret string, err error) {
	c, err := s.findByID(id)
	if err != nil {
		return "", "", "", err
	}
	plain, err := s.crypt.Decrypt(c.Secret)
	if err != nil {
		return "", "", "", apperr.Wrap(err, "INTERNAL", "decrypt secret", 500)
	}
	return c.Type, c.Username, plain, nil
}

func (s *GitCredentialService) findByID(id uint) (*model.GitCredential, error) {
	var c model.GitCredential
	if err := s.db.First(&c, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, apperr.Wrap(err, "INTERNAL", "get git credential", 500)
	}
	return &c, nil
}

// encryptSecret 把明文 secret 包成 blob 再加密。
// 保留 JSON wrapper 便于将来加更多字段（如 passphrase）。
func (s *GitCredentialService) encryptSecret(secret string) (string, error) {
	data, _ := json.Marshal(map[string]string{"v": secret})
	ct, err := s.crypt.Encrypt(string(data))
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}
	return ct, nil
}

// validateGitCred 创建 vs 更新规则不同：创建时 secret 必填，更新时可留空。
func validateGitCred(in GitCredentialInput, requireSecret bool) error {
	if strings.TrimSpace(in.Name) == "" {
		return apperr.New("BAD_REQUEST", "name 必填", 400)
	}
	switch in.Type {
	case "token", "ssh_key":
	default:
		return apperr.New("BAD_REQUEST", "type 必须为 token 或 ssh_key", 400)
	}
	if requireSecret && strings.TrimSpace(in.Secret) == "" {
		return apperr.New("BAD_REQUEST", "secret 必填（token 时是 PAT；ssh_key 时是 PEM 私钥）", 400)
	}
	if in.Type == "ssh_key" && in.Secret != "" {
		if !strings.Contains(in.Secret, "BEGIN") || !strings.Contains(in.Secret, "PRIVATE KEY") {
			return apperr.New("BAD_REQUEST",
				"type=ssh_key 时 secret 必须是 PEM 格式私钥（含 BEGIN ... PRIVATE KEY 行）", 400)
		}
	}
	return nil
}

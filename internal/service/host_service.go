// Package service 实现主机/应用/制品/流水线等业务编排。
//
// 设计原则：handler 薄，service 厚；事务边界与状态机在这一层。
package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"swift-devops/internal/model"
	cryptopkg "swift-devops/internal/pkg/crypto"
	apperr "swift-devops/internal/pkg/errors"
	sshpkg "swift-devops/internal/pkg/ssh"
)

// HostInput 创建/更新主机入参
type HostInput struct {
	Name       string `json:"name" binding:"required"`
	IP         string `json:"ip" binding:"required"`
	Port       int    `json:"port,omitempty"`
	AuthType   string `json:"auth_type" binding:"required,oneof=password key"`
	Username   string `json:"username" binding:"required"`
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
	GroupTag   string `json:"group_tag,omitempty"`
	Tags       string `json:"tags,omitempty"`
}

// HostView 主机响应，不含敏感字段
type HostView struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	IP        string `json:"ip"`
	Port      int    `json:"port"`
	AuthType  string `json:"auth_type"`
	Username  string `json:"username"`
	Status    string `json:"status"`
	GroupTag  string `json:"group_tag"`
	Tags      string `json:"tags"`
	HasSecret bool   `json:"has_secret"`
	HasHostKey bool  `json:"has_host_key"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func toView(h *model.Host) HostView {
	return HostView{
		ID: h.ID, Name: h.Name, IP: h.IP, Port: h.Port,
		AuthType: h.AuthType, Username: h.Username,
		Status: h.Status, GroupTag: h.GroupTag, Tags: h.Tags,
		HasSecret:  h.Secret != "",
		HasHostKey: h.HostKey != "",
		CreatedAt:  h.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  h.UpdatedAt.Format(time.RFC3339),
	}
}

// secretBlob 是 Host.Secret 解密后的内部结构
type secretBlob struct {
	Value      string `json:"v"`
	Passphrase string `json:"p,omitempty"`
}

// HostService Host 业务编排
type HostService struct {
	db    *gorm.DB
	crypt *cryptopkg.AESGCM
}

func NewHostService(db *gorm.DB, crypt *cryptopkg.AESGCM) *HostService {
	return &HostService{db: db, crypt: crypt}
}

// Create 录入新主机
func (s *HostService) Create(in HostInput) (HostView, error) {
	if err := validateHostInput(in); err != nil {
		return HostView{}, err
	}
	if in.Port == 0 {
		in.Port = 22
	}
	sec, err := s.encryptSecret(in)
	if err != nil {
		return HostView{}, apperr.Wrap(err, "INTERNAL", "encrypt secret", 500)
	}
	h := &model.Host{
		Name: in.Name, IP: in.IP, Port: in.Port,
		AuthType: in.AuthType, Username: in.Username,
		Secret: sec, GroupTag: in.GroupTag, Tags: in.Tags,
		Status: "unknown",
	}
	if err := s.db.Create(h).Error; err != nil {
		return HostView{}, apperr.Wrap(err, "INTERNAL", "create host", 500)
	}
	return toView(h), nil
}

// List 列出全部主机
func (s *HostService) List() ([]HostView, error) {
	var hs []model.Host
	if err := s.db.Order("id DESC").Find(&hs).Error; err != nil {
		return nil, apperr.Wrap(err, "INTERNAL", "list hosts", 500)
	}
	vs := make([]HostView, len(hs))
	for i := range hs {
		vs[i] = toView(&hs[i])
	}
	return vs, nil
}

// Get 取一个
func (s *HostService) Get(id uint) (HostView, error) {
	h, err := s.findByID(id)
	if err != nil {
		return HostView{}, err
	}
	return toView(h), nil
}

// Update 更新主机。Password / PrivateKey 字段为空时保留原值。
func (s *HostService) Update(id uint, in HostInput) (HostView, error) {
	// 注意：Update 不要求凭证非空（保留旧值）；AuthType 由 handler 的 binding tag 校验
	h, err := s.findByID(id)
	if err != nil {
		return HostView{}, err
	}
	h.Name = in.Name
	h.IP = in.IP
	if in.Port != 0 {
		h.Port = in.Port
	}
	h.AuthType = in.AuthType
	h.Username = in.Username
	h.GroupTag = in.GroupTag
	h.Tags = in.Tags
	if in.Password != "" || in.PrivateKey != "" {
		sec, encErr := s.encryptSecret(in)
		if encErr != nil {
			return HostView{}, apperr.Wrap(encErr, "INTERNAL", "encrypt secret", 500)
		}
		h.Secret = sec
		h.HostKey = "" // 凭证改了，重置 TOFU
	}
	if err := s.db.Save(h).Error; err != nil {
		return HostView{}, apperr.Wrap(err, "INTERNAL", "update host", 500)
	}
	return toView(h), nil
}

// Delete 删除主机
func (s *HostService) Delete(id uint) error {
	res := s.db.Delete(&model.Host{}, id)
	if res.Error != nil {
		return apperr.Wrap(res.Error, "INTERNAL", "delete host", 500)
	}
	if res.RowsAffected == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

// TestConnect 测试 SSH 连通性。
//   - 解密 secret
//   - 调 ssh.TestConnectivity
//   - 成功且 host key 是新学的：落库
//   - 成功：status=online；失败：status=offline
func (s *HostService) TestConnect(id uint) (sshpkg.TestResult, error) {
	h, err := s.findByID(id)
	if err != nil {
		return sshpkg.TestResult{}, err
	}
	blob, err := s.decryptSecret(h)
	if err != nil {
		return sshpkg.TestResult{}, apperr.Wrap(err, "INTERNAL", "decrypt secret", 500)
	}
	target := sshpkg.HostTarget{
		IP: h.IP, Port: h.Port, User: h.Username, KnownHostKey: h.HostKey,
	}
	auth := sshpkg.AuthMethod{
		Type: h.AuthType, Passphrase: blob.Passphrase,
	}
	if h.AuthType == "password" {
		auth.Password = blob.Value
	} else {
		auth.KeyPEM = blob.Value
	}
	result := sshpkg.TestConnectivity(target, auth, sshpkg.DialOptions{Timeout: 10 * time.Second})

	updates := map[string]any{}
	if result.OK {
		updates["status"] = "online"
		if h.HostKey == "" && result.HostKey != "" {
			updates["host_key"] = result.HostKey
		}
	} else {
		updates["status"] = "offline"
	}
	s.db.Model(&model.Host{}).Where("id = ?", id).Updates(updates)
	return result, nil
}

// --- 内部 ---

func (s *HostService) findByID(id uint) (*model.Host, error) {
	var h model.Host
	if err := s.db.First(&h, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.ErrNotFound
		}
		return nil, apperr.Wrap(err, "INTERNAL", "get host", 500)
	}
	return &h, nil
}

func validateHostInput(in HostInput) error {
	if in.AuthType == "password" && in.Password == "" {
		return apperr.New("BAD_REQUEST", "auth_type=password 时 password 必填", 400)
	}
	if in.AuthType == "key" && in.PrivateKey == "" {
		return apperr.New("BAD_REQUEST", "auth_type=key 时 private_key 必填", 400)
	}
	return nil
}

func (s *HostService) encryptSecret(in HostInput) (string, error) {
	blob := secretBlob{Passphrase: in.Passphrase}
	if in.AuthType == "password" {
		blob.Value = in.Password
	} else {
		blob.Value = in.PrivateKey
	}
	data, _ := json.Marshal(blob)
	ct, err := s.crypt.Encrypt(string(data))
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}
	return ct, nil
}

func (s *HostService) decryptSecret(h *model.Host) (secretBlob, error) {
	var blob secretBlob
	if h.Secret == "" {
		return blob, errors.New("empty secret")
	}
	plain, err := s.crypt.Decrypt(h.Secret)
	if err != nil {
		return blob, err
	}
	if err := json.Unmarshal([]byte(plain), &blob); err != nil {
		return blob, fmt.Errorf("unmarshal: %w", err)
	}
	return blob, nil
}

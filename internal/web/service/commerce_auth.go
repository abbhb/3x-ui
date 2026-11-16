package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func (s *CommerceService) Settings() (model.PortalSettings, error) {
	p := model.PortalSettings{ID: 1, AutoBlockEnabled: true, Mode: "legacy", TTLSeconds: 180, SourceMode: "any", CustomCIDRs: []string{}, LoginFailureLimit: 8, RegistrationFailureLimit: 5, LoginWindowSeconds: 900, RegistrationWindowSeconds: 600, AutoBlockSeconds: 86400}
	err := database.GetDB().First(&p, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return p, nil
	}
	return p, err
}

func (s *CommerceService) SaveSettings(p model.PortalSettings) error {
	p.ID = 1
	if !slices.Contains([]string{"legacy", "dual", "ephemeral"}, p.Mode) || (p.TTLSeconds != 0 && (p.TTLSeconds < 30 || p.TTLSeconds > 315360000)) || !slices.Contains([]string{"any", "china", "custom", "china_or_custom"}, p.SourceMode) || len(p.CustomCIDRs) > 512 || p.LoginFailureLimit < 2 || p.LoginFailureLimit > 100 || p.RegistrationFailureLimit < 2 || p.RegistrationFailureLimit > 100 || p.LoginWindowSeconds < 60 || p.LoginWindowSeconds > 86400 || p.RegistrationWindowSeconds < 60 || p.RegistrationWindowSeconds > 86400 || p.AutoBlockSeconds < 0 || p.AutoBlockSeconds > 2592000 {
		return ErrPortalInvalid
	}
	for i, value := range p.CustomCIDRs {
		network, err := portalNetwork(value)
		if err != nil {
			return ErrPortalInvalid
		}
		p.CustomCIDRs[i] = network
	}
	if p.SourceMode == "custom" && len(p.CustomCIDRs) == 0 {
		return ErrPortalInvalid
	}
	if p.PublicURL != "" {
		u, err := url.Parse(p.PublicURL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
			return ErrPortalInvalid
		}
		loopback := u.Hostname() == "localhost"
		if ip := net.ParseIP(u.Hostname()); ip != nil {
			loopback = ip.IsLoopback()
		}
		if u.Scheme != "https" && (u.Scheme != "http" || !loopback) {
			return ErrPortalInvalid
		}
		p.PublicURL = strings.TrimRight(p.PublicURL, "/")
	}
	if p.Enabled && (p.PublicURL == "" || len(os.Getenv("XUI_MGMA_TOKEN_PEPPER")) < 32) {
		return errors.New("portal activation requires a public URL and an installed MGMA pepper of at least 32 bytes")
	}
	return database.GetDB().Save(&p).Error
}

func portalPasswordValid(value string) bool {
	return utf8.RuneCountInString(value) >= 10 && len(value) <= 72
}

func (s *CommerceService) Register(username, password, invitation, sourceIP string) (*model.PortalAccount, error) {
	settings, err := s.Settings()
	if err != nil {
		return nil, err
	}
	if !settings.Enabled {
		return nil, ErrPortalUnavailable
	}
	username = strings.ToLower(strings.TrimSpace(username))
	if !portalUsername.MatchString(username) || !portalPasswordValid(password) || !portalBearerPattern.MatchString(invitation) {
		return nil, ErrPortalAuth
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	account := &model.PortalAccount{Username: username, PasswordHash: string(hash), IsActive: true, AuthVersion: 1}
	err = database.GetDB().Transaction(func(tx *gorm.DB) error {
		now := s.now()
		digest := portalHash(invitation)
		consume := tx.Model(&model.PortalInvitation{}).Where("digest = ? AND disabled_at = 0 AND starts_at <= ? AND (expires_at = 0 OR expires_at > ?) AND (max_uses = 0 OR use_count < max_uses)", digest, now, now).UpdateColumn("use_count", gorm.Expr("use_count + 1"))
		if consume.Error != nil {
			return consume.Error
		}
		if consume.RowsAffected != 1 {
			return ErrPortalAuth
		}
		// A valid invitation precedes the username lookup, including proxy names.
		pattern := "%." + strings.ReplaceAll(username, "_", "!_")
		var count int64
		if err := tx.Model(&model.ClientRecord{}).Where("LOWER(email) = ? OR LOWER(email) LIKE ? ESCAPE '!'", username, pattern).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrPortalAuth
		}
		if err := tx.Create(account).Error; err != nil {
			return ErrPortalAuth
		}
		var inv model.PortalInvitation
		if err := tx.Where("digest = ?", digest).First(&inv).Error; err != nil {
			return err
		}
		return tx.Create(&model.PortalInvitationUse{InvitationID: inv.ID, AccountID: account.ID, SourceIP: sourceIP, CreatedAt: now}).Error
	})
	return account, err
}

// Compare imported passlib bcrypt hashes with the historical 72-byte boundary.
func portalCheckPassword(hash, password string) bool {
	if len(password) > 512 {
		return false
	}
	bytes := []byte(password)
	if len(bytes) > 72 {
		bytes = bytes[:72]
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), bytes) == nil
}

// A fixed valid bcrypt hash keeps unknown-account checks on the password path.
const portalDummyHash = "$2a$10$7EqJtq98hPqEX7fNZaFWoO5d7wUV4yp.WG.M.lR7RC7DyD0SXMOC2"

func (s *CommerceService) Authenticate(username, password string) (*model.PortalAccount, error) {
	settings, err := s.Settings()
	if err != nil {
		return nil, err
	}
	if !settings.Enabled {
		return nil, ErrPortalAuth
	}
	var a model.PortalAccount
	err = database.GetDB().Where("username = ?", strings.ToLower(strings.TrimSpace(username))).First(&a).Error
	hash := a.PasswordHash
	if err != nil || !a.IsActive {
		hash = portalDummyHash
	}
	valid := portalCheckPassword(hash, password)
	if err != nil || !a.IsActive || !valid {
		return nil, ErrPortalAuth
	}
	return &a, nil
}

type portalClaims struct {
	Subject   string `json:"sub"`
	Audience  string `json:"aud"`
	Version   int64  `json:"ver"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

func portalSigningKey() ([]byte, error) {
	secret, err := (&SettingService{}).GetSecret()
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("3x-ui.portal.jwt.v1"))
	return mac.Sum(nil), nil
}

func (s *CommerceService) LoginToken(a model.PortalAccount) (string, error) {
	key, err := portalSigningKey()
	if err != nil {
		return "", err
	}
	now := s.now() / 1000
	claims := portalClaims{Subject: strconv.Itoa(a.ID), Audience: "portal", Version: a.AuthVersion, IssuedAt: now, ExpiresAt: now + 86400}
	encoded, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body := header + "." + base64.RawURLEncoding.EncodeToString(encoded)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *CommerceService) VerifyLoginToken(token string) (*model.PortalAccount, error) {
	if len(token) > 2048 {
		return nil, ErrPortalAuth
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrPortalAuth
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || string(header) != `{"alg":"HS256","typ":"JWT"}` {
		return nil, ErrPortalAuth
	}
	key, err := portalSigningKey()
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, ErrPortalAuth
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrPortalAuth
	}
	var claims portalClaims
	if json.Unmarshal(payload, &claims) != nil || claims.Audience != "portal" || claims.ExpiresAt <= s.now()/1000 || claims.IssuedAt > s.now()/1000+30 || claims.ExpiresAt-claims.IssuedAt > 86400 {
		return nil, ErrPortalAuth
	}
	id, err := strconv.Atoi(claims.Subject)
	if err != nil || id <= 0 {
		return nil, ErrPortalAuth
	}
	settings, err := s.Settings()
	if err != nil || !settings.Enabled {
		return nil, ErrPortalAuth
	}
	var a model.PortalAccount
	if database.GetDB().First(&a, id).Error != nil || !a.IsActive || a.AuthVersion != claims.Version {
		return nil, ErrPortalAuth
	}
	return &a, nil
}

func (s *CommerceService) ChangePassword(id int, current, next string, administrator bool) error {
	if !portalPasswordValid(next) {
		return ErrPortalInvalid
	}
	var a model.PortalAccount
	if database.GetDB().First(&a, id).Error != nil {
		return ErrPortalAuth
	}
	if !administrator && !portalCheckPassword(a.PasswordHash, current) {
		return ErrPortalAuth
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	result := database.GetDB().Model(&model.PortalAccount{}).Where("id = ? AND auth_version = ?", id, a.AuthVersion).Updates(map[string]any{"password_hash": string(hash), "auth_version": gorm.Expr("auth_version + 1")})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrPortalAuth
	}
	return nil
}

type PortalInvitationCreated struct {
	model.PortalInvitation
	Code string `json:"code"`
}

func (s *CommerceService) CreateInvitation(inv model.PortalInvitation) (PortalInvitationCreated, error) {
	var result PortalInvitationCreated
	if len(inv.Note) > 500 || inv.StartsAt < 0 || inv.ExpiresAt < 0 || inv.MaxUses < 0 || inv.MaxUses > 1_000_000 || (inv.ExpiresAt > 0 && inv.ExpiresAt <= max(inv.StartsAt, s.now())) || inv.ActorAdmin == "" {
		return result, ErrPortalInvalid
	}
	code, err := portalRandom()
	if err != nil {
		return result, err
	}
	inv.ID = 0
	inv.Digest = portalHash(code)
	inv.Prefix = code[:8]
	inv.UseCount = 0
	inv.DisabledAt = 0
	if inv.StartsAt == 0 {
		inv.StartsAt = s.now()
	}
	if err := database.GetDB().Create(&inv).Error; err != nil {
		return result, err
	}
	return PortalInvitationCreated{PortalInvitation: inv, Code: code}, nil
}

func NewPortalPassword() (string, error) { return portalRandom() }

// The one-time password is returned only after the entire grant commits. A lost
// response is recovered through password reset, never by storing plaintext.
func (s *CommerceService) CreateManagedAccount(username string, planID int, actor string) (map[string]any, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !portalUsername.MatchString(username) || planID <= 0 || actor == "" {
		return nil, ErrPortalInvalid
	}
	password, err := NewPortalPassword()
	if err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	account := model.PortalAccount{Username: username, PasswordHash: string(hash), IsActive: true, AuthVersion: 1}
	var result PortalMutationResult
	err = submitTrafficWrite(func() error {
		return database.GetDB().Transaction(func(tx *gorm.DB) error {
			var count int64
			pattern := "%." + strings.ReplaceAll(username, "_", "!_")
			if err := tx.Model(&model.ClientRecord{}).Where("LOWER(email) = ? OR LOWER(email) LIKE ? ESCAPE '!'", username, pattern).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return ErrPortalConflict
			}
			if err := tx.Create(&account).Error; err != nil {
				return ErrPortalConflict
			}
			return s.runPortalMutationTx(tx, account.ID, "admin-create", PortalMutation{Kind: "admin_grant", PlanID: planID, Actor: actor}, &result)
		})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"username": username, "password": password, "account": result.PortalAccountView}, nil
}

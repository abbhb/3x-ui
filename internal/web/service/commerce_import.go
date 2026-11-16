package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/subscriptioncompat"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

const portalBundleSchema = "marzban-portal-to-3x-ui/v1"

var (
	errPortalImport    = errors.New("portal import rejected; verify the frozen bundle, key fingerprints, empty offline target and grant mappings")
	portalBundleMember = regexp.MustCompile(`^(portal\.json|identities\.json|stage4-private\.json|allocations/[A-Za-z0-9_-]{1,64}\.sqlite3)$`)
	portalHexDigest    = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type portalImportedAccount struct {
	model.PortalAccount
	Hash string `json:"password_hash"`
}
type portalImportedPlan struct {
	model.PortalPlan
	Metadata string `json:"legacy_metadata"`
}
type portalImportedPurchase struct {
	model.PortalPurchase
	Key string `json:"idempotency_key"`
}
type portalImportedWallet struct {
	model.PortalWalletTransaction
	Key string `json:"idempotency_key"`
}
type portalImportedInvitation struct {
	model.PortalInvitation
	Hash string `json:"digest"`
}
type portalImportedAccess struct {
	ManuallyDisabled bool   `json:"manually_disabled"`
	ClientID         int    `json:"client_id"`
	Digest           string `json:"digest"`
	IssuedAt         int64  `json:"issued_at"`
	ExpiresAt        int64  `json:"expires_at"`
	ConsumedAt       int64  `json:"consumed_at"`
	Generation       int64  `json:"generation"`
}
type portalImportedLegacy struct {
	ClientID  int    `json:"client_id"`
	Username  string `json:"username"`
	CreatedAt int64  `json:"created_at"`
	RevokedAt int64  `json:"revoked_at"`
}
type portalImportedAttempt struct {
	ID           int    `json:"id"`
	SourceIP     string `json:"source_ip"`
	Kind         string `json:"kind"`
	FailureCount int    `json:"failure_count"`
	WindowStart  int64  `json:"window_start"`
	UpdatedAt    int64  `json:"updated_at"`
}
type portalImportBundle struct {
	SubscriptionsExtension *subscriptioncompat.ImportBundle `json:"subscriptions_extension,omitempty"`
	Schema                 string                           `json:"schema"`
	Clients                []ClientCreatePayload            `json:"clients"`
	ReservedIDs            []int64                          `json:"reserved_ids"`
	Accounts               []portalImportedAccount          `json:"accounts"`
	Plans                  []portalImportedPlan             `json:"plans"`
	Subscriptions          []model.PortalSubscription       `json:"subscriptions"`
	Purchases              []portalImportedPurchase         `json:"purchases"`
	Wallet                 []portalImportedWallet           `json:"wallet"`
	Invitations            []portalImportedInvitation       `json:"invitations"`
	InvitationUses         []model.PortalInvitationUse      `json:"invitation_uses"`
	Blocks                 []model.PortalIPBlock            `json:"ip_blocks"`
	Attempts               []portalImportedAttempt          `json:"security_attempts"`
	Settings               model.PortalSettings             `json:"settings"`
	Access                 []portalImportedAccess           `json:"access"`
	Legacy                 []portalImportedLegacy           `json:"legacy_users"`
	NextPlans              []model.PortalNextPlan           `json:"next_plans"`
	History                []model.PortalUsageReset         `json:"usage_history"`
	LegacyKeySHA256        string                           `json:"legacy_key_sha256"`
	PepperSHA256           string                           `json:"pepper_sha256"`
}
type PortalImportReport struct {
	BundleSHA256       string `json:"bundle_sha256"`
	Clients            int    `json:"clients"`
	Accounts           int    `json:"accounts"`
	Purchases          int    `json:"purchases"`
	WalletEntries      int    `json:"wallet_entries"`
	ReservedIdentities int    `json:"reserved_identities"`
	VerifiedRows       int    `json:"verified_rows"`
	ReadyForCutover    bool   `json:"ready_for_cutover"`
}

func portalBundleRead(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errPortalImport
	}
	return os.ReadFile(path)
}

// ImportPortalBundle requires an explicit offline acknowledgement and an empty
// canonical client/customer target. It never starts a core or contacts nodes.
// All rows are inserted and individually read back in one transaction.
func (s *CommerceService) ImportPortalBundle(directory string, offline, revokeLegacy, revokeGrants bool) (PortalImportReport, error) {
	var report PortalImportReport
	if !offline {
		return report, errPortalImport
	}
	root, err := filepath.Abs(directory)
	if err != nil {
		return report, errPortalImport
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return report, errPortalImport
	}
	manifestBytes, err := portalBundleRead(filepath.Join(root, "manifest.json"), 1<<20)
	if err != nil {
		return report, errPortalImport
	}
	var manifest struct {
		Schema       string            `json:"schema"`
		Ready        *bool             `json:"ready_for_cutover"`
		Files        map[string]string `json:"files"`
		ClientCount  int               `json:"client_count"`
		AccountCount int               `json:"account_count"`
	}
	if json.Unmarshal(manifestBytes, &manifest) != nil || manifest.Schema != portalBundleSchema || manifest.Ready == nil || *manifest.Ready || len(manifest.Files) < 3 {
		return report, errPortalImport
	}
	for _, name := range []string{"portal.json", "identities.json", "stage4-private.json"} {
		if manifest.Files[name] == "" {
			return report, errPortalImport
		}
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errPortalImport
		}
		relative, _ := filepath.Rel(root, path)
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return errPortalImport
		}
		if entry.IsDir() {
			if relative != "." && relative != "allocations" {
				return errPortalImport
			}
			return nil
		}
		if relative != "manifest.json" && !portalBundleMember.MatchString(relative) {
			return errPortalImport
		}
		if relative != "manifest.json" && manifest.Files[relative] == "" {
			return errPortalImport
		}
		return nil
	}); err != nil {
		return report, errPortalImport
	}
	for name, checksum := range manifest.Files {
		if !portalBundleMember.MatchString(name) || !portalHexDigest.MatchString(checksum) {
			return report, errPortalImport
		}
		blob, err := portalBundleRead(filepath.Join(root, filepath.FromSlash(name)), 512<<20)
		if err != nil || portalHash(string(blob)) != checksum {
			return report, errPortalImport
		}
	}
	blob, err := portalBundleRead(filepath.Join(root, "portal.json"), 64<<20)
	if err != nil {
		return report, errPortalImport
	}
	var bundle portalImportBundle
	decoder := json.NewDecoder(bytes.NewReader(blob))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&bundle) != nil || decoder.Decode(new(any)) != io.EOF || bundle.Schema != portalBundleSchema || len(bundle.Clients) != manifest.ClientCount || len(bundle.Accounts) != manifest.AccountCount {
		return report, errPortalImport
	}
	if !revokeLegacy && (len(os.Getenv("XUI_MARZBAN_SUBSCRIPTION_KEY")) < 32 || portalHash(os.Getenv("XUI_MARZBAN_SUBSCRIPTION_KEY")) != bundle.LegacyKeySHA256) {
		return report, errPortalImport
	}
	hasGrants := slices.ContainsFunc(bundle.Access, func(a portalImportedAccess) bool { return a.Digest != "" })
	if hasGrants && !revokeGrants && (len(os.Getenv("XUI_MGMA_TOKEN_PEPPER")) < 32 || portalHash(os.Getenv("XUI_MGMA_TOKEN_PEPPER")) != bundle.PepperSHA256) {
		return report, errPortalImport
	}
	if revokeLegacy {
		bundle.Settings.Mode = "ephemeral"
		bundle.Legacy = nil
	}
	if revokeGrants {
		for i := range bundle.Access {
			bundle.Access[i].Digest = ""
			bundle.Access[i].IssuedAt = 0
			bundle.Access[i].ConsumedAt = 0
			bundle.Access[i].ExpiresAt = 0
		}
	}
	report = PortalImportReport{BundleSHA256: portalHash(string(manifestBytes)), Clients: len(bundle.Clients), Accounts: len(bundle.Accounts), Purchases: len(bundle.Purchases), WalletEntries: len(bundle.Wallet), ReservedIdentities: len(bundle.ReservedIDs)}
	err = submitTrafficWrite(func() error {
		return database.GetDB().Transaction(func(tx *gorm.DB) error {
			// A stable row serializes two offline import processes before their reads.
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.PortalSettings{ID: 1}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.PortalSettings{}).Where("id = 1").UpdateColumn("enabled", gorm.Expr("enabled")).Error; err != nil {
				return err
			}
			var settings model.PortalSettings
			if tx.First(&settings, 1).Error != nil || settings.Enabled {
				return errPortalImport
			}
			models := append([]any{&model.ClientRecord{}}, model.PortalModels()...)
			for _, m := range models {
				if _, ok := m.(*model.PortalSettings); ok {
					continue
				}
				var n int64
				if tx.Model(m).Count(&n).Error != nil || n != 0 {
					return errPortalImport
				}
			}
			if err := s.importPortalRows(tx, &bundle, &report); err != nil {
				return err
			}
			if tx.Name() == "postgres" {
				for _, m := range append(models, &model.Host{}) {
					statement := &gorm.Statement{DB: tx}
					if statement.Parse(m) != nil {
						return errPortalImport
					}
					field := statement.Schema.LookUpField("ID")
					if field == nil {
						field = statement.Schema.LookUpField("Id")
					}
					if field == nil || !field.AutoIncrement {
						continue
					}
					table := statement.Schema.Table
					if err := tx.Exec(`SELECT setval(pg_get_serial_sequence(?, 'id'), COALESCE((SELECT MAX(id) FROM "`+table+`"),1),true) WHERE pg_get_serial_sequence(?, 'id') IS NOT NULL`, table, table).Error; err != nil {
						return err
					}
				}
			}
			return nil
		})
	})
	if err != nil {
		return PortalImportReport{}, errPortalImport
	}
	return report, nil
}

func portalImportReadback(tx *gorm.DB, row any, report *PortalImportReport, condition ...any) error {
	if err := tx.Create(row).Error; err != nil {
		return err
	}
	fresh := reflect.New(reflect.TypeOf(row).Elem()).Interface()
	if err := tx.First(fresh, condition...).Error; err != nil {
		return err
	}
	if !reflect.DeepEqual(reflect.ValueOf(row).Elem().Interface(), reflect.ValueOf(fresh).Elem().Interface()) {
		return fmt.Errorf("%w: readback of %T", errPortalImport, row)
	}
	report.VerifiedRows++
	return nil
}

func (s *CommerceService) importPortalRows(tx *gorm.DB, b *portalImportBundle, report *PortalImportReport) error {
	if len(b.Clients) == 0 || b.Settings.Enabled || b.Settings.PublicURL != "" || len(b.Attempts) > 10000 {
		return errPortalImport
	}
	// SaveSettings validation without an independent write or an activation.
	if !slices.Contains([]string{"legacy", "dual", "ephemeral"}, b.Settings.Mode) || !slices.Contains([]string{"any", "china", "custom", "china_or_custom"}, b.Settings.SourceMode) || (b.Settings.TTLSeconds != 0 && (b.Settings.TTLSeconds < 30 || b.Settings.TTLSeconds > 315360000)) || b.Settings.LoginFailureLimit < 2 || b.Settings.LoginFailureLimit > 100 || b.Settings.RegistrationFailureLimit < 2 || b.Settings.RegistrationFailureLimit > 100 || b.Settings.LoginWindowSeconds < 60 || b.Settings.LoginWindowSeconds > 86400 || b.Settings.RegistrationWindowSeconds < 60 || b.Settings.RegistrationWindowSeconds > 86400 || b.Settings.AutoBlockSeconds < 0 || b.Settings.AutoBlockSeconds > 2592000 {
		return errPortalImport
	}
	for _, network := range b.Settings.CustomCIDRs {
		if value, err := portalNetwork(network); err != nil || value != network {
			return errPortalImport
		}
	}
	if err := subscriptioncompat.ImportPrepareHosts(tx, b.SubscriptionsExtension, func(row any, condition ...any) error { return portalImportReadback(tx, row, report, condition...) }); err != nil {
		return err
	}
	clientIDs := map[int]bool{}
	subIDs := map[string]bool{}
	credentials := map[string]bool{}
	for _, payload := range b.Clients {
		client := payload.Client
		number, _, ok := strings.Cut(client.Email, ".")
		id, err := strconv.Atoi(number)
		if err != nil || !ok || id <= 0 || clientIDs[id] || subIDs[client.SubID] || !client.StrictHostBindings || !client.RequireMGMA || !portalBearerPattern.MatchString(client.SubID) || validateClientEmail(client.Email) != nil || client.TotalGB < 0 || client.ExpiryTime < 0 || client.Reset != 0 || client.ResetDay != 0 || client.ResetWeekday != 0 || client.TrafficReset != "never" || payload.Traffic == nil || payload.Traffic.Up < 0 || payload.Traffic.Down < 0 || payload.Traffic.ResetCount != 0 {
			return errPortalImport
		}
		for _, credential := range []string{client.ID, client.Auth} {
			if credential != "" {
				if credentials[credential] {
					return errPortalImport
				}
				credentials[credential] = true
			}
		}
		clientIDs[id] = true
		subIDs[client.SubID] = true
		if len(uniqueInts(payload.InboundIds)) != len(payload.InboundIds) {
			return errPortalImport
		}
		var inbounds []model.Inbound
		if len(payload.InboundIds) > 0 {
			if err := tx.Where("id IN ?", payload.InboundIds).Find(&inbounds).Error; err != nil || len(inbounds) != len(payload.InboundIds) {
				return errPortalImport
			}
		}
		var hosts []model.Host
		if len(client.ClientHostRuleIds) > 0 {
			if err := tx.Where("id IN ?", client.ClientHostRuleIds).Find(&hosts).Error; err != nil || len(hosts) != len(client.ClientHostRuleIds) {
				return errPortalImport
			}
		}
		for _, host := range hosts {
			if (host.IsDisabled && b.SubscriptionsExtension == nil) || !slices.Contains(payload.InboundIds, host.InboundId) {
				return errPortalImport
			}
		}
		for _, ib := range inbounds {
			if !ib.Enable || (ib.Protocol != model.VLESS && ib.Protocol != model.Hysteria) || (ib.Protocol == model.VLESS && client.ID == "") || (ib.Protocol == model.Hysteria && client.Auth == "") {
				return errPortalImport
			}
			if ib.Protocol == model.VLESS && !ib.DisableFlow && client.Flow != "" && !inboundCanEnableTlsFlow(string(ib.Protocol), ib.StreamSettings, ib.Settings) {
				return errPortalImport
			}
		}
		record := client.ToRecord()
		record.Id = id
		record.LimitHwid = payload.LimitHwid
		if err := tx.Create(record).Error; err != nil {
			return err
		}
		if err := tx.Model(record).Update("enable", client.Enable).Error; err != nil {
			return err
		}
		record.Enable = client.Enable
		var read model.ClientRecord
		if tx.First(&read, id).Error != nil || read.Email != record.Email || read.UUID != record.UUID || read.Auth != record.Auth || read.SubID != record.SubID || read.Enable != client.Enable || read.TotalGB != client.TotalGB || read.ExpiryTime != client.ExpiryTime || !read.StrictHostBindings || !read.RequireMGMA || !slices.Equal(read.ClientHostRuleIds, client.ClientHostRuleIds) {
			return errPortalImport
		}
		report.VerifiedRows++
		for _, ib := range inbounds {
			flow := ""
			if ib.Protocol == model.VLESS && !ib.DisableFlow {
				flow = client.Flow
			}
			link := model.ClientInbound{ClientId: id, InboundId: ib.Id, FlowOverride: flow}
			if err := portalImportReadback(tx, &link, report, "client_id = ? AND inbound_id = ?", id, ib.Id); err != nil {
				return err
			}
		}
		traffic := xray.ClientTraffic{Email: record.Email, Enable: client.Enable, Up: payload.Traffic.Up, Down: payload.Traffic.Down, Total: client.TotalGB, ExpiryTime: client.ExpiryTime}
		if err := tx.Create(&traffic).Error; err != nil {
			return err
		}
		if err := tx.Model(&traffic).Update("enable", client.Enable).Error; err != nil {
			return err
		}
		var check xray.ClientTraffic
		if tx.Where("email = ?", record.Email).First(&check).Error != nil || check.Up != traffic.Up || check.Down != traffic.Down || check.Total != traffic.Total || check.Enable != client.Enable || check.ExpiryTime != traffic.ExpiryTime {
			return errPortalImport
		}
		report.VerifiedRows++
	}
	reserved := map[int64]bool{}
	for _, id := range b.ReservedIDs {
		if id <= 0 || id == int64(^uint64(0)>>1) || reserved[id] {
			return errPortalImport
		}
		reserved[id] = true
		identity := model.PortalIdentity{ID: id}
		if clientIDs[int(id)] {
			value := int(id)
			identity.ClientID = &value
		}
		if err := portalImportReadback(tx, &identity, report, id); err != nil {
			return err
		}
	}
	for id := range clientIDs {
		if !reserved[int64(id)] {
			return errPortalImport
		}
	}
	accounts := map[int]*model.PortalAccount{}
	names := map[string]bool{}
	for _, wire := range b.Accounts {
		a := wire.PortalAccount
		a.PasswordHash = wire.Hash
		a.AuthVersion = 1
		a.ProxyClient = nil
		if a.ID <= 0 || accounts[a.ID] != nil || !portalUsername.MatchString(a.Username) || a.Username != strings.ToLower(a.Username) || names[a.Username] || a.WalletBalanceMinor < 0 || a.WalletBalanceMinor > portalMaxBalance || (a.ClientID != nil && !clientIDs[*a.ClientID]) {
			return errPortalImport
		}
		if _, err := bcrypt.Cost([]byte(a.PasswordHash)); err != nil {
			return errPortalImport
		}
		names[a.Username] = true
		if err := portalImportReadback(tx, &a, report, a.ID); err != nil {
			return err
		}
		accounts[a.ID] = &a
	}
	if err := subscriptioncompat.ImportRows(tx, b.SubscriptionsExtension, func(row any, condition ...any) error { return portalImportReadback(tx, row, report, condition...) }); err != nil {
		return err
	}
	plans := map[int]*model.PortalPlan{}
	for _, wire := range b.Plans {
		p := wire.PortalPlan
		p.LegacyMetadata = wire.Metadata
		if b.SubscriptionsExtension != nil {
			if err := subscriptioncompat.CaptureHostSignatures(tx, &p); err != nil {
				return err
			}
		}
		if p.ID <= 0 || plans[p.ID] != nil || p.Currency != "CNY" || p.PriceMinor < 0 || p.PriceMinor > portalMaxAmount || p.DurationDays < 1 || p.DurationDays > 3650 || p.DataLimit < 0 || p.DataLimit > 1_000_000_000_000_000_000 || len(p.Name) == 0 || !json.Valid([]byte(p.LegacyMetadata)) {
			return errPortalImport
		}
		if !p.CompatibilityPending {
			if _, err := portalValidatePlan(tx, &p); err != nil {
				return err
			}
		}
		if p.CompatibilityPending && p.IsVisible {
			return errPortalImport
		}
		if err := portalImportReadback(tx, &p, report, p.ID); err != nil {
			return err
		}
		plans[p.ID] = &p
	}
	if b.SubscriptionsExtension != nil {
		if err := subscriptioncompat.ValidateImportedPlans(tx); err != nil {
			return err
		}
	}
	for _, sub := range b.Subscriptions {
		if accounts[sub.AccountID] == nil || accounts[sub.AccountID].ClientID == nil || plans[sub.PlanID] == nil || sub.Currency != "CNY" || sub.PricePaidMinor < 0 || sub.ExpiresAt < 0 {
			return errPortalImport
		}
		if err := portalImportReadback(tx, &sub, report, "account_id = ?", sub.AccountID); err != nil {
			return err
		}
	}
	purchases := map[int]*model.PortalPurchase{}
	for _, wire := range b.Purchases {
		p := wire.PortalPurchase
		p.IdempotencyKey = wire.Key
		if p.ID <= 0 || purchases[p.ID] != nil || accounts[p.AccountID] == nil || plans[p.PlanID] == nil || !slices.Contains([]string{"self_purchase", "admin_grant", "admin_renewal"}, p.Kind) || p.AmountMinor < 0 || p.BalanceBeforeMinor-p.AmountMinor != p.BalanceAfterMinor || p.BalanceAfterMinor < 0 || len(p.IdempotencyKey) == 0 || len(p.IdempotencyKey) > 128 || p.Currency != "CNY" {
			return errPortalImport
		}
		if err := portalImportReadback(tx, &p, report, p.ID); err != nil {
			return err
		}
		purchases[p.ID] = &p
	}
	balances := map[int]int64{}
	debits := map[int]int{}
	for _, wire := range b.Wallet {
		row := wire.PortalWalletTransaction
		row.IdempotencyKey = wire.Key
		if accounts[row.AccountID] == nil || row.AmountMinor == 0 || row.BalanceAfterMinor < 0 || row.BalanceAfterMinor > portalMaxBalance || balances[row.AccountID]+row.AmountMinor != row.BalanceAfterMinor || len(row.IdempotencyKey) == 0 || len(row.IdempotencyKey) > 128 {
			return errPortalImport
		}
		if row.Kind == "purchase_debit" {
			if row.PurchaseID == nil {
				return errPortalImport
			}
			p := purchases[*row.PurchaseID]
			if p == nil || p.AccountID != row.AccountID || row.AmountMinor != -p.AmountMinor || p.AmountMinor == 0 || row.BalanceAfterMinor != p.BalanceAfterMinor {
				return errPortalImport
			}
			debits[p.ID]++
		} else if row.Kind != "admin_credit" || row.AmountMinor <= 0 || row.PurchaseID != nil {
			return errPortalImport
		}
		balances[row.AccountID] = row.BalanceAfterMinor
		if err := portalImportReadback(tx, &row, report, row.ID); err != nil {
			return err
		}
	}
	for _, a := range accounts {
		if balances[a.ID] != a.WalletBalanceMinor {
			return errPortalImport
		}
	}
	for _, p := range purchases {
		if p.AmountMinor > 0 && debits[p.ID] != 1 {
			return errPortalImport
		}
	}
	// Imported idempotency records prevent retries of old purchases or credits
	// from charging again. Old first-response payloads were not retained by Marzban.
	for _, p := range purchases {
		request := PortalMutation{Kind: p.Kind, PlanID: p.PlanID, Actor: p.ActorAdmin}
		if p.Kind == "admin_renewal" {
			request.PlanID = 0
			request.Days = p.DurationDays
		}
		if p.Kind == "self_purchase" {
			request.Actor = ""
		}
		if err := s.importPortalAction(tx, accounts[p.AccountID], "subscription", p.IdempotencyKey, request, p.ID, p.BalanceAfterMinor, report); err != nil {
			return err
		}
	}
	for _, wire := range b.Wallet {
		r := wire.PortalWalletTransaction
		if r.Kind == "admin_credit" {
			if err := s.importPortalAction(tx, accounts[r.AccountID], "wallet_credit", wire.Key, PortalMutation{Kind: r.Kind, AmountMinor: r.AmountMinor, Actor: r.ActorAdmin, Note: r.Note}, 0, r.BalanceAfterMinor, report); err != nil {
				return err
			}
		}
	}
	invitations := map[int]bool{}
	for _, wire := range b.Invitations {
		r := wire.PortalInvitation
		r.Digest = wire.Hash
		if r.ID <= 0 || !portalHexDigest.MatchString(r.Digest) || r.UseCount < 0 || r.MaxUses < 0 || (r.MaxUses > 0 && r.UseCount > r.MaxUses) {
			return errPortalImport
		}
		invitations[r.ID] = true
		if err := portalImportReadback(tx, &r, report, r.ID); err != nil {
			return err
		}
	}
	for _, r := range b.InvitationUses {
		if !invitations[r.InvitationID] || accounts[r.AccountID] == nil {
			return errPortalImport
		}
		if err := portalImportReadback(tx, &r, report, r.ID); err != nil {
			return err
		}
	}
	for _, r := range b.Blocks {
		if n, err := portalNetwork(r.Network); err != nil || n != r.Network {
			return errPortalImport
		}
		if err := portalImportReadback(tx, &r, report, r.ID); err != nil {
			return err
		}
	}
	for _, r := range b.Attempts {
		if _, err := portalNetwork(r.SourceIP); err != nil || r.FailureCount < 1 || !slices.Contains([]string{"portal_login", "portal_registration", "admin_login"}, r.Kind) {
			return errPortalImport
		}
		row := model.PortalSecurityAttempt{ID: r.ID, SourceIP: r.SourceIP, Kind: r.Kind, FailureCount: r.FailureCount, WindowStart: r.WindowStart, UpdatedAt: r.UpdatedAt}
		if err := portalImportReadback(tx, &row, report, row.ID); err != nil {
			return err
		}
	}
	accessIDs := map[int]bool{}
	for _, r := range b.Access {
		if !clientIDs[r.ClientID] || accessIDs[r.ClientID] || (r.Digest != "" && !portalHexDigest.MatchString(r.Digest)) || r.Generation < 1 || r.IssuedAt < 0 || r.ExpiresAt < 0 || r.ConsumedAt < 0 {
			return errPortalImport
		}
		accessIDs[r.ClientID] = true
		row := model.PortalAccess{ManuallyDisabled: r.ManuallyDisabled, ClientID: r.ClientID, Digest: r.Digest, IssuedAt: r.IssuedAt, ExpiresAt: r.ExpiresAt, ConsumedAt: r.ConsumedAt, Generation: r.Generation}
		if err := portalImportReadback(tx, &row, report, "client_id = ?", r.ClientID); err != nil {
			return err
		}
	}
	if len(accessIDs) != len(clientIDs) {
		return errPortalImport
	}
	for _, r := range b.Legacy {
		if !clientIDs[r.ClientID] || r.CreatedAt <= 0 || r.RevokedAt < 0 {
			return errPortalImport
		}
		row := model.PortalLegacyUser{ClientID: r.ClientID, Username: r.Username, CreatedAt: r.CreatedAt, RevokedAt: r.RevokedAt}
		if err := portalImportReadback(tx, &row, report, "client_id = ?", r.ClientID); err != nil {
			return err
		}
	}
	for _, r := range b.NextPlans {
		if !clientIDs[r.ClientID] || r.DataLimit < 0 || r.DataLimit > 1_000_000_000_000_000_000 || r.Expire < 0 || r.Expire > int64(^uint64(0)>>1)/1000 {
			return errPortalImport
		}
		if err := portalImportReadback(tx, &r, report, "client_id = ?", r.ClientID); err != nil {
			return err
		}
	}
	for _, r := range b.History {
		if !clientIDs[r.ClientID] || r.UsedTraffic < 0 || r.ResetAt <= 0 {
			return errPortalImport
		}
		if err := portalImportReadback(tx, &r, report, r.ID); err != nil {
			return err
		}
	}
	b.Settings.ID = 1
	if err := tx.Save(&b.Settings).Error; err != nil {
		return err
	}
	var settings model.PortalSettings
	if tx.First(&settings, 1).Error != nil || !reflect.DeepEqual(settings, b.Settings) {
		return errPortalImport
	}
	report.VerifiedRows++
	return nil
}

func (s *CommerceService) importPortalAction(tx *gorm.DB, a *model.PortalAccount, scope, key string, request PortalMutation, purchaseID int, balance int64, report *PortalImportReport) error {
	view, err := s.accountView(tx, *a)
	if err != nil {
		return err
	}
	view.WalletBalanceMinor = balance
	result := PortalMutationResult{PortalAccountView: view, PurchaseID: purchaseID}
	input, _ := json.Marshal(request)
	output, _ := json.Marshal(result)
	row := model.PortalAction{AccountID: a.ID, Scope: scope, Key: key, RequestHash: portalHash(string(input)), Response: string(output), CreatedAt: s.now()}
	return portalImportReadback(tx, &row, report, "account_id = ? AND scope = ? AND key = ?", a.ID, scope, key)
}

func RunPortalImportCLI(args []string) error {
	// Keep parsing here small and deliberately separate from normal server startup.
	directory := ""
	offline, revokeLegacy, revokeGrants := false, false, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--bundle":
			i++
			if i >= len(args) {
				return errPortalImport
			}
			directory = args[i]
		case "--offline":
			offline = true
		case "--revoke-legacy":
			revokeLegacy = true
		case "--revoke-grants":
			revokeGrants = true
		default:
			return errPortalImport
		}
	}
	if directory == "" || !offline {
		return errPortalImport
	}
	if err := database.InitDB(config.GetDBPath()); err != nil {
		return errors.New("offline target initialization failed")
	}
	defer func() { _ = database.CloseDB() }()
	report, err := (&CommerceService{}).ImportPortalBundle(directory, offline, revokeLegacy, revokeGrants)
	if err != nil {
		return err
	}
	encoded, _ := json.Marshal(report)
	fmt.Println(string(encoded))
	return nil
}

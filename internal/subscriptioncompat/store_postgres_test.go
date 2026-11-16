package subscriptioncompat

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// Separate database sessions must serialize snapshots across different sources,
// not just concurrent refreshes of one source. Each candidate fits on its own;
// publishing both would overrun the same plan's 5000-node limit.
func TestSubscriptionsPostgresBudgetRace(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("XUI_DB_DSN"))
	if os.Getenv("XUI_DB_TYPE") != "postgres" || dsn == "" {
		t.Skip("set a dedicated PostgreSQL test DSN")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlAdmin, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("subscription_budget_%d", time.Now().UnixNano())
	if err = admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error; _ = sqlAdmin.Close() })
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, parseErr := url.Parse(dsn)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		query := u.Query()
		query.Set("search_path", schema)
		u.RawQuery = query.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema
	}
	t.Setenv("XUI_DB_DSN", dsn)
	s := compatDB(t)
	snapshot := func(count int) []byte {
		var text strings.Builder
		text.WriteString("proxies:\n")
		for i := range count {
			fmt.Fprintf(&text, "- {name: node-%d, type: socks5, server: synthetic.example.test, port: 1080}\n", i)
		}
		return []byte(text.String())
	}
	selections := []model.ExternalSelection{}
	sourceIDs := []int{}
	for i := range 6 {
		source, createErr := s.CreateSource(nil, SourceInput{Name: fmt.Sprintf("Source %d", i), Enabled: true, RefreshMinutes: 15})
		if createErr != nil {
			t.Fatal(createErr)
		}
		count := 1000
		if i >= 4 {
			count = 1
		}
		if _, err = s.ImportSnapshot(source.ID, nil, snapshot(count)); err != nil {
			t.Fatal(err)
		}
		sourceIDs = append(sourceIDs, source.ID)
		selections = append(selections, model.ExternalSelection{SourceID: source.ID, Mode: "all"})
	}
	if err = s.DB.Create(&model.PortalPlan{Name: "Budget race", ExternalSelections: selections}).Error; err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, id := range sourceIDs[4:] {
		workers.Go(func() {
			<-start
			_, updateErr := s.ImportSnapshot(id, nil, snapshot(600))
			results <- updateErr
		})
	}
	close(start)
	workers.Wait()
	close(results)
	committed, rejected := 0, 0
	for updateErr := range results {
		if updateErr == nil {
			committed++
		} else if errors.Is(updateErr, ErrBudget) {
			rejected++
		} else {
			t.Fatal(updateErr)
		}
	}
	if committed != 1 || rejected != 1 {
		t.Fatalf("snapshot commits=%d budget rejections=%d", committed, rejected)
	}
	var count int64
	if err = s.DB.Model(&model.SubscriptionNode{}).Where("is_active = ?", true).Count(&count).Error; err != nil || count != 4601 {
		t.Fatalf("atomic last-good readback: count=%d err=%v", count, err)
	}
	if err = ValidateSelections(s.DB, selections); err != nil {
		t.Fatalf("committed snapshots violate plan budget: %v", err)
	}
}

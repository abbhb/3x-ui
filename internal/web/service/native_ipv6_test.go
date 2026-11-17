package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func nativeIPv6Fixture(t *testing.T) (*model.ClientRecord, *model.Inbound, map[string]bool, *bool) {
	t.Helper()
	setupBulkDB(t)
	db := database.GetDB()
	client := &model.ClientRecord{Email: "7.sample", Enable: true}
	if err := db.Create(client).Error; err != nil {
		t.Fatal(err)
	}
	ib := mkInbound(t, 28177, model.VLESS, `{ "clients": [] }`)
	if err := db.Create(&model.ClientInbound{ClientId: client.Id, InboundId: ib.Id}).Error; err != nil {
		t.Fatal(err)
	}
	addresses, fail := map[string]bool{"2001:db8:1::a": true}, false
	previous := nativeIPv6IP
	t.Cleanup(func() { nativeIPv6IP = previous })
	nativeIPv6IP = func(_ context.Context, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "-j -6 address show" {
			info := []map[string]any{}
			for address := range addresses {
				info = append(info, map[string]any{"local": address})
			}
			return json.Marshal([]map[string]any{{"ifname": "eth0", "addr_info": info}})
		}
		if len(args) != 6 || strings.Join(args[:3], " ") != "-6 address add" || args[4] != "dev" || args[5] != "eth0" {
			t.Fatalf("unexpected host mutation: %v", args)
		}
		if fail {
			return nil, errors.New("synthetic address preparation failure")
		}
		addresses[strings.TrimSuffix(args[3], "/128")] = true
		return nil, nil
	}
	if err := ConfigureNativeIPv6Pool(context.Background(), NativeIPv6Pool{Interface: "eth0", Prefix: "2001:db8:1::/64"}); err != nil {
		t.Fatal(err)
	}
	return client, ib, addresses, &fail
}

func TestNativeIPv6LeaseSurvivesIdentityChangesAndRestore(t *testing.T) {
	client, _, addresses, _ := nativeIPv6Fixture(t)
	ctx := context.Background()
	first, err := AllocateNativeIPv6(ctx, []int{client.Id})
	if err != nil || len(first) != 1 || !first[0].Ready {
		t.Fatalf("allocation=%v err=%v", first, err)
	}
	if err := database.GetDB().Model(client).Updates(map[string]any{"email": "renamed", "uuid": "new-credential"}).Error; err != nil {
		t.Fatal(err)
	}
	second, err := AllocateNativeIPv6(ctx, []int{client.Id})
	if err != nil || first[0].Address != second[0].Address {
		t.Fatalf("lease changed: %v %v", second, err)
	}
	delete(addresses, first[0].Address)
	template := `{"outbounds":[{"tag":"ordinary-native","protocol":"freedom","sendThrough":"` + first[0].Address + `"}]}`
	if err := prepareNativeIPv6Bindings(ctx, template); err != nil || !addresses[first[0].Address] {
		t.Fatalf("restore failed: %v", err)
	}
	if err := ConfigureNativeIPv6Pool(ctx, NativeIPv6Pool{Interface: "eth0", Prefix: "2001:db8:2::/64"}); err == nil {
		t.Fatal("changed pool while permanent leases exist")
	}
	report, err := NativeIPv6PoolReport(ctx)
	if err != nil || len(report.Leases) != 1 || !report.Leases[0].Ready || report.Clients[0].Email != "renamed" {
		t.Fatalf("readback: %+v %v", report, err)
	}
}

func TestNativeIPv6RejectsForeignClientsAndKeepsPendingLease(t *testing.T) {
	client, _, _, fail := nativeIPv6Fixture(t)
	foreign := model.ClientRecord{Email: "foreign"}
	if err := database.GetDB().Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := AllocateNativeIPv6(context.Background(), []int{foreign.Id}); err == nil {
		t.Fatal("allocated an ungranted client")
	}
	*fail = true
	if _, err := AllocateNativeIPv6(context.Background(), []int{client.Id}); err == nil {
		t.Fatal("claimed successful preparation")
	}
	report, err := NativeIPv6PoolReport(context.Background())
	if err != nil || len(report.Leases) != 1 || report.Leases[0].Ready {
		t.Fatalf("lost pending lease: %+v %v", report, err)
	}
	address := report.Leases[0].Address
	*fail = false
	ready, err := AllocateNativeIPv6(context.Background(), []int{client.Id})
	if err != nil || ready[0].Address != address {
		t.Fatalf("retry changed allocation: %+v %v", ready, err)
	}
}

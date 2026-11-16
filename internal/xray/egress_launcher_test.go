package xray

import (
	"context"
	"reflect"
	"testing"
)

func TestCoreStartCommandUsesExplicitRealBinaryAndNativeConfig(t *testing.T) {
	t.Setenv("XUI_XRAY_LAUNCHER", "/private/launch.py")
	command := coreStartCommand(context.Background(), "/private/xray", "/private/core.json")
	if !reflect.DeepEqual(command.Args, []string{"/private/launch.py", "/private/xray", "-c", "/private/core.json"}) {
		t.Fatalf("launcher args=%v", command.Args)
	}
	t.Setenv("XUI_XRAY_LAUNCHER", "")
	command = coreStartCommand(context.Background(), "/private/xray", "/private/core.json")
	if !reflect.DeepEqual(command.Args, []string{"/private/xray", "-c", "/private/core.json"}) {
		t.Fatalf("native args=%v", command.Args)
	}
}

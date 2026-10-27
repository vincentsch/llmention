//go:build !windows

package notify

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommandTimeoutBoundsInheritedPipes(t *testing.T) {
	for _, script := range []string{"#!/bin/sh\nsleep 2 &\nwait\n", "#!/bin/sh\nsleep 2 &\nexit 0\n"} {
		t.Run(script, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "hook")
			if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			rows := Deliver(context.Background(), Config{Command: path}, notificationCard(), Options{CommandTimeout: 100 * time.Millisecond})
			if rows[0].Status != StatusFailed || rows[0].ReasonCode != ReasonCommandTimeout {
				t.Fatalf("command summary = %#v", rows[0])
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("command timeout waited for descendant's inherited pipes: %s", elapsed)
			}
		})
	}
}

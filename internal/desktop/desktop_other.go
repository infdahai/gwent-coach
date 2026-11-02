//go:build !windows

package desktop

import (
	"context"
	"fmt"
)

func Take(context.Context) (Capture, error) {
	return Capture{}, fmt.Errorf("desktop capture requires Windows interactive desktop")
}

func Click(context.Context, Capture, int, int, string) (Receipt, error) {
	return Receipt{Outcome: "not_sent"}, fmt.Errorf("desktop input requires Windows interactive desktop")
}

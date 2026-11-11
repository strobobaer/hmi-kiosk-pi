//go:build !linux

package platform

import "context"

// WatchCorner: Ohne Linux gibt es keine Eingabegeräte; blockiert bis ctx endet.
func WatchCorner(ctx context.Context, devDir string, cfg CornerCfg, hit func()) error {
	<-ctx.Done()
	return nil
}

//go:build !windows

package winstart

import "os"

// openNoWriteShare: Außerhalb von Windows (nur für Tests) ein einfaches Öffnen.
func openNoWriteShare(path string) (*os.File, error) { return os.Open(path) }

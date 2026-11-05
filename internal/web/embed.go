package web

import "embed"

// assets enthält die Kachelseite, die Leiste und unter assets/admin die Admin-Seite.
//
//go:embed assets/*
var assets embed.FS
